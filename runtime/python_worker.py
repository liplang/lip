#!/usr/bin/env python3
"""Small, dependency-free JSONL worker used by LIP's Python adapter.

Only stdout protocol frames are emitted. Diagnostics belong on stderr. The
worker accepts dotted call names (for example numpy.linalg.solve) and a few
dependency-free numerical operations used by smoke tests and examples.
"""

import importlib
import importlib.util
import json
import hashlib
import math
import mmap
import numbers
import os
import platform
import stat
import sys
import time
import uuid

PROTOCOL = 1
NOT_BUILTIN = object()


def module_rules(name):
    return [item.strip() for item in name.split(",") if item.strip()]


ALLOW_ANY_MODULE = os.environ.get("LIP_PYTHON_ALLOW_ANY", "false").lower() == "true"
ALLOWED_MODULES = module_rules(os.environ.get("LIP_PYTHON_ALLOWED_MODULES", ""))
DENIED_MODULES = set(module_rules(os.environ.get("LIP_PYTHON_DENIED_MODULES", "")))
DATA_DIR = os.path.realpath(os.environ.get("LIP_PYTHON_DATA_DIR", "")) if os.environ.get("LIP_PYTHON_DATA_DIR") else ""
MAX_BLOB_BYTES = int(os.environ.get("LIP_PYTHON_MAX_BLOB_BYTES", str(512 * 1024 * 1024)))
MAX_OPEN_BLOBS = int(os.environ.get("LIP_PYTHON_MAX_OPEN_BLOBS", "64"))
MAX_HANDLES = int(os.environ.get("LIP_PYTHON_MAX_HANDLES", "10000"))
OBJECTS = {}
NEXT_HANDLE = 0
WORKER_ID = uuid.uuid4().hex
HANDLE_COUNT = 0
BLOBS = {}
HANDLE_BLOBS = {}


def matches_rule(name, rule):
    if rule.endswith(".*"):
        base = rule[:-2]
        return name == base or name.startswith(base + ".")
    if rule.endswith("*"):
        return name.startswith(rule[:-1])
    return name == rule or name.startswith(rule + ".")


def module_allowed(name):
    if ALLOW_ANY_MODULE:
        return True
    if any(matches_rule(name, rule) for rule in DENIED_MODULES):
        return False
    if ALLOWED_MODULES:
        return any(matches_rule(name, rule) for rule in ALLOWED_MODULES)
    return True


def session_objects(session):
    return OBJECTS.setdefault(session or "default", {})


def blob_key(blob, session):
    return ((session or "default"), blob.get("$lip_blob"))


def blob_path(blob):
    if not DATA_DIR:
        raise ValueError("Python blob data plane is not configured")
    name = blob.get("$lip_blob")
    if not isinstance(name, str) or not name or os.path.basename(name) != name or name in (".", ".."):
        raise ValueError("invalid Python blob name")
    path = os.path.realpath(os.path.join(DATA_DIR, name))
    try:
        if os.path.commonpath((DATA_DIR, path)) != DATA_DIR:
            raise ValueError("Python blob escapes data directory")
    except ValueError:
        raise ValueError("Python blob escapes data directory")
    return path


def validate_blob(blob):
    if not isinstance(blob, dict) or "$lip_blob" not in blob:
        raise ValueError("expected a Python blob descriptor")
    path = blob_path(blob)
    size = blob.get("size")
    digest = blob.get("sha256")
    if not isinstance(size, int) or size < 0 or size > MAX_BLOB_BYTES:
        raise ValueError("invalid Python blob size")
    if not isinstance(digest, str) or len(digest) != 64:
        raise ValueError("invalid Python blob SHA-256")
    try:
        int(digest, 16)
    except ValueError:
        raise ValueError("invalid Python blob SHA-256")
    info = os.stat(path)
    if not stat_is_regular(info):
        raise ValueError("Python blob is not a regular file")
    if info.st_size != size:
        raise ValueError("Python blob size does not match descriptor")
    fmt = blob.get("format") or "raw"
    if fmt not in ("raw", "npy"):
        raise ValueError("unsupported Python blob format %s" % fmt)
    if fmt == "raw" and ((blob.get("dtype") is None) != (blob.get("shape") is None)):
        raise ValueError("raw typed blobs require both dtype and shape")
    if fmt == "npy" and (blob.get("dtype") is not None or blob.get("shape") is not None):
        raise ValueError("npy blobs do not accept raw dtype or shape metadata")
    actual = hashlib.sha256()
    with open(path, "rb") as source:
        for chunk in iter(lambda: source.read(1024 * 1024), b""):
            actual.update(chunk)
    if actual.hexdigest() != digest:
        raise ValueError("Python blob SHA-256 does not match descriptor")
    return path


def stat_is_regular(info):
    return stat.S_ISREG(info.st_mode)


def blob_numpy_value(blob, path, mapping):
    import numpy as np

    fmt = blob.get("format") or "raw"
    if fmt == "npy":
        mapping.close()
        return np.load(path, mmap_mode="r", allow_pickle=False)
    if fmt != "raw":
        raise ValueError("unsupported Python blob format %s" % fmt)
    dtype_name = blob.get("dtype")
    shape = blob.get("shape")
    order = blob.get("order") or "C"
    if dtype_name is None and shape is None:
        return np.frombuffer(mapping, dtype=np.uint8)
    if not isinstance(dtype_name, str) or not isinstance(shape, list):
        raise ValueError("typed raw blobs require dtype and shape")
    if order not in ("C", "F") or any(not isinstance(item, int) or item < 0 for item in shape):
        raise ValueError("invalid Python blob shape or order")
    dtype = np.dtype(dtype_name)
    if dtype.hasobject:
        raise ValueError("object dtype is not allowed in a raw Python blob")
    expected = dtype.itemsize
    for item in shape:
        expected *= item
    if expected != blob["size"]:
        raise ValueError("typed raw blob size does not match dtype and shape")
    return np.ndarray(tuple(shape), dtype=dtype, buffer=mapping, order=order)


def open_blob(blob, session):
    key = blob_key(blob, session)
    existing = BLOBS.get(key)
    if existing is not None:
        if existing.get("descriptor") != blob:
            raise ValueError("Python blob descriptor changed for an open blob")
        validate_blob(blob)
        return existing["value"]
    if MAX_OPEN_BLOBS > 0 and len(BLOBS) >= MAX_OPEN_BLOBS:
        raise RuntimeError("Python blob mapping limit exceeded")
    path = validate_blob(blob)
    fmt = blob.get("format") or "raw"
    if fmt == "npy":
        try:
            import numpy as np
        except ImportError:
            raise RuntimeError("numpy is required to open a .npy Python blob")
        value = np.load(path, mmap_mode="r", allow_pickle=False)
        BLOBS[key] = {"value": value, "mapping": None, "file": None, "descriptor": dict(blob)}
        return value
    if fmt != "raw":
        raise ValueError("unsupported Python blob format %s" % fmt)
    source = open(path, "rb")
    mapping = None
    try:
        mapping = mmap.mmap(source.fileno(), 0, access=mmap.ACCESS_READ) if blob["size"] else None
        if mapping is None:
            if blob.get("dtype") is not None or blob.get("shape") is not None:
                try:
                    import numpy as np
                except ImportError:
                    raise RuntimeError("numpy is required to open a typed empty Python blob")
                dtype = np.dtype(blob.get("dtype"))
                shape = blob.get("shape")
                if dtype.hasobject or not isinstance(shape, list):
                    raise ValueError("invalid typed empty Python blob")
                value = np.empty(tuple(shape), dtype=dtype)
                value.setflags(write=False)
            else:
                value = b""
        else:
            try:
                value = blob_numpy_value(blob, path, mapping)
            except ImportError:
                value = memoryview(mapping)
        BLOBS[key] = {"value": value, "mapping": mapping, "file": source, "descriptor": dict(blob)}
        return value
    except Exception:
        if mapping is not None:
            mapping.close()
        source.close()
        raise


def release_blob(blob, session):
    global HANDLE_COUNT
    validation_error = None
    try:
        path = validate_blob(blob)
    except Exception as error:
        validation_error = error
        path = blob_path(blob)
    key = blob_key(blob, session)
    entry = BLOBS.pop(key, None)
    for handle_key, blob_key_value in list(HANDLE_BLOBS.items()):
        if handle_key[0] == (session or "default") and blob_key_value == key:
            session_objects(session).pop(handle_key[1], None)
            del HANDLE_BLOBS[handle_key]
            HANDLE_COUNT -= 1
    if entry is not None:
        value = entry.get("value")
        mapping = entry.get("mapping")
        source = entry.get("file")
        entry.clear()
        del value
        del entry
        if mapping is not None:
            mapping.close()
        if source is not None:
            source.close()
    try:
        os.unlink(path)
    except FileNotFoundError:
        pass
    if validation_error is not None:
        raise validation_error
    return True


def handle_object(value, session):
    global HANDLE_COUNT, NEXT_HANDLE
    if MAX_HANDLES > 0 and HANDLE_COUNT >= MAX_HANDLES:
        raise RuntimeError("Python object handle limit exceeded")
    handle = "%s:%d" % (WORKER_ID, NEXT_HANDLE)
    NEXT_HANDLE += 1
    HANDLE_COUNT += 1
    session_objects(session)[handle] = value
    for key, entry in BLOBS.items():
        if entry.get("value") is value:
            HANDLE_BLOBS[((session or "default"), handle)] = key
            break
    return {
        "$python_handle": handle,
        "type": "%s.%s" % (type(value).__module__, type(value).__name__),
    }


def resolve_value(value, session):
    if isinstance(value, dict):
        if "$lip_blob" in value:
            return open_blob(value, session)
        if "$python_handle" in value:
            handle = value["$python_handle"]
            try:
                return session_objects(session)[handle]
            except KeyError:
                raise ValueError("unknown Python handle %s" % handle)
        return {key: resolve_value(item, session) for key, item in value.items()}
    if isinstance(value, list):
        return [resolve_value(item, session) for item in value]
    return value


def release_value(value, session):
    global HANDLE_COUNT
    if not isinstance(value, dict) or "$python_handle" not in value:
        raise ValueError("python.release expects a Python handle")
    handle = value["$python_handle"]
    objects = session_objects(session)
    if handle not in objects:
        raise ValueError("unknown Python handle %s" % handle)
    del objects[handle]
    HANDLE_BLOBS.pop(((session or "default"), handle), None)
    HANDLE_COUNT -= 1
    return True


def json_value(value, session, force=False):
    """Turn common scientific Python values into JSON-safe values."""
    if value is None or isinstance(value, (str, bool, int, float)):
        if isinstance(value, float) and (math.isnan(value) or math.isinf(value)):
            raise ValueError("Python result must be finite; NaN/Inf cannot become LIP null")
        return value
    if isinstance(value, numbers.Number):
        if hasattr(value, "item"):
            return json_value(value.item(), session, force)
        if force:
            return str(value)
        return handle_object(value, session)
    if isinstance(value, dict):
        result = {}
        for key, item in value.items():
            name = str(key)
            if name in result:
                raise ValueError("Python dictionary keys collide as LIP object key %r" % name)
            result[name] = json_value(item, session, force)
        return result
    if isinstance(value, (set, frozenset)):
        if force:
            raise ValueError("Python set has no LIP list order; use sorted before conversion")
        return handle_object(value, session)
    if isinstance(value, (list, tuple)):
        return [json_value(item, session, force) for item in value]
    if force:
        if hasattr(value, "tolist"):
            return json_value(value.tolist(), session, True)
        if hasattr(value, "to_dict"):
            try:
                return json_value(value.to_dict(orient="records"), session, True)
            except TypeError:
                return json_value(value.to_dict(), session, True)
        return str(value)
    return handle_object(value, session)


def encode_result(value, session, force=False):
    """A failed conversion must not leave unreachable object handles behind."""
    global HANDLE_COUNT
    objects = session_objects(session)
    before = set(objects)
    try:
        return json_value(value, session, force)
    except Exception:
        for handle in set(objects) - before:
            del objects[handle]
            HANDLE_BLOBS.pop(((session or "default"), handle), None)
            HANDLE_COUNT -= 1
        raise


def builtin(name, args, session):
    if name in ("module_available", "python.module_available"):
        if len(args) != 1 or not isinstance(args[0], str):
            raise ValueError("module_available expects one module name")
        return importlib.util.find_spec(args[0]) is not None
    if name == "python.call":
        if len(args) != 3 or not isinstance(args[1], str) or not isinstance(args[2], list):
            raise ValueError("python.call expects handle, method name, and argument list")
        target = getattr(args[0], args[1])
        if not callable(target):
            raise TypeError("Python object attribute %s is not callable" % args[1])
        return target(*args[2])
    if name == "python.open_blob":
        if len(args) != 1:
            raise ValueError("python.open_blob expects one blob descriptor")
        return open_blob(args[0], session)
    if name == "echo":
        if len(args) != 1:
            raise ValueError("echo expects one argument")
        return args[0]
    if name == "sum":
        if len(args) != 1:
            raise ValueError("sum expects one argument")
        return sum(args[0])
    if name == "mean":
        if len(args) != 1 or not args[0]:
            raise ValueError("mean expects one non-empty sequence")
        return sum(args[0]) / len(args[0])
    if name == "dot":
        if len(args) != 2 or len(args[0]) != len(args[1]):
            raise ValueError("dot expects two vectors of equal length")
        return sum(left * right for left, right in zip(args[0], args[1]))
    if name == "matrix_multiply":
        if len(args) != 2:
            raise ValueError("matrix_multiply expects two matrices")
        left, right = args
        if not left or not right or len(left[0]) != len(right):
            raise ValueError("matrix dimensions do not agree")
        return [
            [sum(left[i][k] * right[k][j] for k in range(len(right))) for j in range(len(right[0]))]
            for i in range(len(left))
        ]
    if name == "sleep":
        if len(args) != 1:
            raise ValueError("sleep expects seconds")
        time.sleep(float(args[0]))
        return None
    if name == "fail":
        raise RuntimeError(str(args[0]) if args else "requested failure")
    return NOT_BUILTIN


def call_operation(name, args, session):
    # Worker control operations are always available.  Module policy governs
    # imported Python packages, not handle/session bookkeeping.  Keeping this
    # distinction matters when a restricted worker still needs to call
    # python.call or inspect a value with python.to_json.
    if name in ("python.call", "python.module_available"):
        value = builtin(name, args, session)
        if value is not NOT_BUILTIN:
            return value
    module_name = name.rsplit(".", 1)[0] if "." in name else ""
    if module_name and not module_allowed(module_name):
        raise PermissionError("module %s is not enabled by this worker" % module_name)
    if "." not in name:
        value = builtin(name, args, session)
        if value is not NOT_BUILTIN:
            return value
    # Import the longest module prefix, then traverse real attributes. This
    # also handles class/static methods such as datetime.datetime.fromisoformat.
    parts = name.split(".")
    target = None
    for split in range(len(parts) - 1, 0, -1):
        prefix = ".".join(parts[:split])
        try:
            target = importlib.import_module(prefix)
        except ModuleNotFoundError as error:
            if error.name != prefix and not prefix.startswith(error.name + "."):
                raise
            continue
        for part in parts[split:]:
            target = getattr(target, part)
        break
    if target is None:
        raise ModuleNotFoundError("no module prefix found for operation %s" % name)
    if not callable(target):
        raise TypeError("operation %s is not callable" % name)
    return target(*args)


def respond(payload):
    sys.stdout.write(json.dumps(payload, separators=(",", ":"), allow_nan=False) + "\n")
    sys.stdout.flush()


capabilities = [
    "echo", "sum", "mean", "dot", "matrix_multiply", "sleep", "fail",
    "module_available", "dotted-call", "dynamic-import", "python.call",
    "python.to_json", "python.release", "python.open_blob", "python.release_blob",
    "blob-v1", "math.*", "statistics.*",
]

respond({
    "type": "ready",
    "protocol": PROTOCOL,
    "python": platform.python_version(),
    "capabilities": capabilities,
})

for line in sys.stdin:
    if not line.strip():
        continue
    request = None
    try:
        request = json.loads(line)
        if request.get("type") == "cancel":
            # Requests are processed serially. The Go side kills the worker
            # when cancellation cannot be observed at a safe Python point.
            continue
        if request.get("type") != "request":
            raise ValueError("unknown frame type")
        operation = request["op"]
        session = request.get("session") or "default"
        raw_args = request.get("args") or []
        # Drop references from the previous request before releasing a mapped
        # object. NumPy arrays keep exported pointers to their mmap buffer.
        args = None
        result = None
        if operation == "python.release":
            result = release_value(raw_args[0] if len(raw_args) == 1 else raw_args, session)
            force_json = False
        elif operation == "python.release_blob":
            result = release_blob(raw_args[0] if len(raw_args) == 1 else raw_args, session)
            force_json = False
        elif operation == "python.open_blob":
            if len(raw_args) != 1:
                raise ValueError("python.open_blob expects one blob descriptor")
            result = handle_object(open_blob(raw_args[0], session), session)
            force_json = False
        else:
            args = resolve_value(raw_args, session)
            force_json = operation == "python.to_json"
            if force_json:
                if len(args) != 1:
                    raise ValueError("python.to_json expects one value")
                result = args[0]
            else:
                result = call_operation(operation, args, session)
        respond({"id": request["id"], "ok": True, "value": encode_result(result, session, force_json)})
    except Exception as error:
        if request is None or "id" not in request:
            print("invalid request: %s" % error, file=sys.stderr)
            continue
        respond({
            "id": request["id"],
            "ok": False,
            "error": {"type": type(error).__name__, "message": str(error)},
        })
