package script

// Both Python adapters use the fixed c-hex Data profile. Protocol metadata is
// ordinary JSON; no business object shape is reserved by the transport.
const pythonCanonicalCodec = `import math, struct
def c_hex(value):
    def enc(x):
        if x is None: return b'\x00'
        if type(x) is bool: return b'\x02' if x else b'\x01'
        if type(x) in (int, float):
            n = float(x)
            bits = bytes.fromhex("7ff8000000000000") if math.isnan(n) else struct.pack(">d", 0.0 if n == 0 else n)
            return b'\x03' + bits
        if type(x) is str:
            data = x.encode("utf-8")
            return b'\x04' + struct.pack(">Q", len(data)) + data
        if type(x) is list:
            return b'\x05' + struct.pack(">Q", len(x)) + b''.join(enc(v) for v in x)
        if type(x) is dict and all(type(k) is str for k in x):
            return b'\x06' + struct.pack(">Q", len(x)) + b''.join(enc(k)+enc(x[k]) for k in sorted(x))
        raise ValueError("canonical encoding requires Data")
    return enc(value).hex()
def from_c_hex(text):
    if type(text) is not str or len(text)%2 or any(c not in "0123456789abcdef" for c in text): raise ValueError("invalid c-hex")
    data, offset = bytes.fromhex(text), 0
    def take(n):
        nonlocal offset
        if n > len(data)-offset: raise ValueError("truncated canonical input")
        result = data[offset:offset+n]
        offset += n
        return result
    def size(): return struct.unpack(">Q", take(8))[0]
    def dec(depth=0):
        if depth > 512: raise ValueError("canonical nesting budget exceeded")
        tag = take(1)[0]
        if tag == 0: return None
        if tag in (1,2): return tag == 2
        if tag == 3:
            bits = take(8)
            n = struct.unpack(">d", bits)[0]
            if bits.hex() == "8000000000000000" or math.isnan(n) and bits.hex() != "7ff8000000000000": raise ValueError("noncanonical number")
            return n
        if tag == 4: return take(size()).decode("utf-8")
        if tag in (5,6):
            n = size()
            if n > (len(data)-offset)//(10 if tag == 6 else 1): raise ValueError("invalid canonical length")
            if tag == 5: return [dec(depth+1) for _ in range(n)]
            result, previous = {}, None
            for _ in range(n):
                key = dec(depth+1)
                if type(key) is not str or previous is not None and key <= previous: raise ValueError("unsorted canonical keys")
                previous = key
                result[key] = dec(depth+1)
            return result
        raise ValueError("unknown canonical tag")
    value = dec()
    if offset != len(data): raise ValueError("trailing canonical input")
    return value
`

// Protocol responses use a dedicated descriptor. Library stdout/stderr and
// native writes never corrupt the JSON protocol or disappear into a buffer.
const nativePythonWorker = pythonCanonicalCodec + `import importlib, inspect, json, math, numbers, os, pydoc, sys, types
protocol = os.fdopen(3, "w", encoding="utf-8")
token = os.environ["LIP_NATIVE_TOKEN"]
objects, identities = {}, {}
owners = {}
active_scope, active_owner, active_run = "", "", ""
next_handle = 0
class BoundaryError(Exception): pass
class BoundaryArity(BoundaryError): pass
class BoundaryResource(BoundaryError): pass
class Prepared:
    def __init__(self, value): self.value=value
def reference(value, temporary=False):
    global next_handle
    next_handle += 1
    key = str(next_handle)
    objects[key] = value
    owners[key] = (active_scope, active_owner, active_run)
    if not temporary: identities[id(value)] = key
    return {"$lip.native":{"language":"python", "token":token, "generation":token, "scope":active_scope, "owner":active_owner, "id":key, "type":type(value).__module__+"."+type(value).__qualname__, "temporary":temporary}}
def release_handle(key):
    value = objects.pop(key, None)
    owners.pop(key, None)
    if identities.get(id(value)) == key: identities.pop(id(value), None)
def encode(x, seen=None):
    if isinstance(x, Prepared): return reference(x.value, temporary=True)
    if x is None or type(x) in (str, bool): return x
    if isinstance(x, numbers.Integral):
        n = int(x)
        if abs(n) > 9007199254740991: raise BoundaryError("integer outside safe range")
        return n
    if isinstance(x, numbers.Real):
        n = float(x)
        return 0.0 if n == 0 else n
    if type(x) in (list, tuple, dict):
        seen = set() if seen is None else seen
        if id(x) not in seen and (type(x) is not dict or all(type(k) is str for k in x)):
            seen.add(id(x))
            try:
                if type(x) is dict: return {k:encode(v, seen) for k,v in x.items()}
                return [encode(v, seen) for v in x]
            finally: seen.remove(id(x))
    key = identities.get(id(x))
    if key is not None and owners[key][1] != active_owner: key = None
    if key is None:
        return reference(x)
    return {"$lip.native":{"language":"python", "token":token, "generation":token, "scope":owners[key][0], "owner":owners[key][1], "id":key, "type":type(x).__module__+"."+type(x).__qualname__}}
def decode(x):
    if type(x) is float and x.is_integer() and abs(x) <= 9007199254740991: return int(x)
    if type(x) is list: return [decode(v) for v in x]
    if type(x) is dict:
        if "$lip.native" in x:
            r = x["$lip.native"]
            if not isinstance(r, dict) or r.get("language") != "python" or r.get("token") != token or r.get("id") not in objects:
                raise BoundaryResource("foreign, forged or expired native resource reference")
            if owners[r["id"]][1] != active_owner: raise BoundaryResource("native resource belongs to another owner")
            return objects[r["id"]]
        return {k:decode(v) for k,v in x.items()}
    return x
def attributes(value, member):
    for name in member.split(".") if member else []:
        try: value = getattr(value, name)
        except AttributeError:
            if not isinstance(value, types.ModuleType): raise
            value = importlib.import_module(value.__name__+"."+name)
    return value
def resolve(module, member):
    return attributes(importlib.import_module(module), member)
def snapshot(value, seen=None):
    seen = set() if seen is None else seen
    if id(value) in seen: raise BoundaryError("cyclic native object")
    seen.add(id(value))
    try:
        if type(value) in (list, tuple): return [snapshot(v, seen) for v in value]
        if type(value) is dict and all(type(k) is str for k in value): return {k:snapshot(v, seen) for k,v in value.items()}
        if type(value) in (set, frozenset): return [snapshot(v, seen) for v in value]
        converter = getattr(value, "tolist", None)
        if callable(converter): return snapshot(converter(), seen)
        converted = encode(value)
        if isinstance(converted, dict) and "$lip.native" in converted: raise BoundaryError("object needs an explicit Data conversion")
        return converted
    finally: seen.remove(id(value))
def execute(request):
    global active_scope, active_owner, active_run
    if request["module"] == "lip.cleanup":
        for key, owner in list(owners.items()):
            if owner[2] == request["run"] and key not in request["keep"]: release_handle(key)
        return True
    active_scope, active_owner, active_run = request["scope"], request["owner"], request["run"]
    args, kwargs = decode(request["args"]), decode(request["kwargs"])
    action, member = request["action"], request["member"]
    if request["module"] == "lip.native":
        value = args.pop(0)
        if action == "release":
            key = request["args"][0]["$lip.native"]["id"]
            release_handle(key)
            return True
        if action == "snapshot": return snapshot(value)
        if action == "length": return len(value)
        if action == "string": return str(value)
        if action == "index": return value[args[0]]
        if action == "slice": return value[slice(*args)]
        if action == "get": return attributes(value, member.removeprefix("get."))
        if action in ("call", "prepare"):
            if member != action: value = attributes(value, member.removeprefix(action+"."))
    else:
        value = resolve(request["module"], member)
        if action == "get": return value
    if action == "help": return pydoc.render_doc(value, renderer=pydoc.plaintext)
    if not callable(value): raise BoundaryError("callee is not callable")
    try:
        signature = inspect.signature(value)
    except (ValueError, TypeError): pass
    else:
        try: signature.bind(*args, **kwargs)
        except TypeError as error: raise BoundaryArity(str(error)) from error
    if action == "prepare": return Prepared(value)
    return value(*args, **kwargs)
for line in sys.stdin:
    completed = False
    before = set(objects)
    request = None
    try:
        request = json.loads(line, parse_int=float, parse_float=float)
        if request["module"] != "lip.cleanup":
            request["args"] = [from_c_hex(x) for x in request["args"]]
            request["kwargs"] = {k:from_c_hex(v) for k,v in request["kwargs"].items()}
        result = execute(request)
        completed = True
        response = {"status":"result", "value":c_hex(encode(result))}
        json.dumps(response, ensure_ascii=False, allow_nan=False).encode("utf-8")
    except BaseException as error:
        for key in set(objects) - before:
            release_handle(key)
        uncertain = isinstance(error, (TimeoutError, ConnectionError, KeyboardInterrupt, SystemExit))
        response = {"status":"known_fault" if completed else "uncertain" if uncertain else "rejected", "message":type(error).__name__+": "+str(error), "code":"E-RESOURCE" if isinstance(error, BoundaryResource) else "E-ARITY" if isinstance(error, BoundaryArity) else "E-SCHEMA" if isinstance(error, BoundaryError) else "E-EFFECT-REJECTED"}
    finally:
        if request and request["module"] == "lip.native" and request["action"] == "call":
            ref = request["args"][0].get("$lip.native", {}) if isinstance(request["args"][0], dict) else {}
            if ref.get("temporary"): release_handle(ref["id"])
    protocol.write(json.dumps(response, ensure_ascii=False, allow_nan=False)+"\n")
    protocol.flush()
`
