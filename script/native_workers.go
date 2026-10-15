package script

// Protocol responses use a dedicated descriptor. Library stdout/stderr and
// native writes never corrupt the JSON protocol or disappear into a buffer.
const nativePythonWorker = `import importlib, inspect, json, math, numbers, os, pydoc, sys, types
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
        if not math.isfinite(n): raise BoundaryError("non-finite number")
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
        result = execute(request)
        completed = True
        response = {"status":"result", "value":encode(result)}
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
