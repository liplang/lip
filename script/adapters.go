package script

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"unicode/utf8"
)

// FromNative copies supported Go data into immutable language values. Byte
// buffers, pointers, functions and resources require an explicit adapter.
func FromNative(native any) (Value, error) {
	return fromReflect(reflect.ValueOf(native), map[visitKey]bool{})
}

type visitKey struct {
	typ    reflect.Type
	ptr    uintptr
	length int // Different slice views may share a start address without a cycle.
}

func fromReflect(v reflect.Value, seen map[visitKey]bool) (Value, error) {
	if !v.IsValid() {
		return Value{}, nil
	}
	if v.Kind() == reflect.Interface {
		if v.IsNil() {
			return Value{}, nil
		}
		return fromReflect(v.Elem(), seen)
	}
	if v.Type() == reflect.TypeFor[Value]() {
		x := v.Interface().(Value)
		if !x.IsData() {
			return Value{}, diagnostic("E-VALUE", "native value is not Data")
		}
		return x, nil
	}
	switch v.Kind() {
	case reflect.Bool:
		return BoolValue(v.Bool()), nil
	case reflect.String:
		return StringValue(v.String())
	case reflect.Float32, reflect.Float64:
		return NumberValue(v.Float())
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		n := v.Int()
		if n < -9007199254740991 || n > 9007199254740991 {
			return Value{}, diagnostic("E-VALUE", "native integer outside safe range")
		}
		return number(float64(n)), nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		n := v.Uint()
		if n > 9007199254740991 {
			return Value{}, diagnostic("E-VALUE", "native integer outside safe range")
		}
		return number(float64(n)), nil
	case reflect.Slice, reflect.Array:
		if v.Type().Elem().Kind() == reflect.Uint8 {
			return Value{}, diagnostic("E-VALUE", "bytes require explicit encoding")
		}
		if v.Kind() == reflect.Slice {
			key := visitKey{typ: v.Type(), ptr: v.Pointer(), length: v.Len()}
			if seen[key] {
				return Value{}, diagnostic("E-VALUE", "cyclic native list")
			}
			seen[key] = true
			defer delete(seen, key)
		}
		xs := make([]Value, v.Len())
		for i := range xs {
			x, err := fromReflect(v.Index(i), seen)
			if err != nil {
				return Value{}, err
			}
			xs[i] = x
		}
		return ListValue(xs...), nil
	case reflect.Map:
		if v.Type().Key().Kind() != reflect.String {
			return Value{}, diagnostic("E-VALUE", "native map requires string keys")
		}
		key := visitKey{typ: v.Type(), ptr: uintptr(v.UnsafePointer())}
		if seen[key] {
			return Value{}, diagnostic("E-VALUE", "cyclic native object")
		}
		seen[key] = true
		defer delete(seen, key)
		o := map[string]Value{}
		iter := v.MapRange()
		for iter.Next() {
			k := iter.Key().String()
			if !utf8.ValidString(k) {
				return Value{}, diagnostic("E-VALUE", "invalid native object key")
			}
			x, err := fromReflect(iter.Value(), seen)
			if err != nil {
				return Value{}, err
			}
			o[k] = x
		}
		return ObjectValue(o)
	case reflect.Struct:
		o := map[string]Value{}
		for i := 0; i < v.NumField(); i++ {
			f := v.Type().Field(i)
			if !f.IsExported() {
				continue
			}
			name := strings.Split(f.Tag.Get("json"), ",")[0]
			if name == "-" {
				continue
			}
			if name == "" {
				name = f.Name
			}
			if _, ok := o[name]; ok {
				return Value{}, diagnostic("E-VALUE", "duplicate struct field mapping")
			}
			x, err := fromReflect(v.Field(i), seen)
			if err != nil {
				return Value{}, err
			}
			o[name] = x
		}
		return ObjectValue(o)
	}
	return Value{}, diagnostic("E-VALUE", "native value requires an explicit Data adapter")
}

// GoHandler maps a registered fixed signature. A leading context.Context and
// trailing error are supported. Native integers require schema Type=number
// plus the safe integer check; bytes and custom resources use a wrapper.
func GoHandler(contract Contract, fn any) (func(context.Context, Request) Response, error) {
	if !validSchema(contract.Result, map[visitKey]bool{}) {
		return nil, diagnostic("E-OPERATION", "invalid result schema")
	}
	for _, p := range contract.Params {
		if !validSchema(p.Schema, map[visitKey]bool{}) {
			return nil, diagnostic("E-OPERATION", "invalid parameter schema")
		}
	}
	contract = cloneContract(contract)
	f := reflect.ValueOf(fn)
	if !f.IsValid() || f.Kind() != reflect.Func || f.IsNil() || f.Type().IsVariadic() {
		return nil, diagnostic("E-OPERATION", "Go operation needs a nonvariadic function")
	}
	typ := f.Type()
	hasContext := typ.NumIn() > 0 && typ.In(0) == reflect.TypeFor[context.Context]()
	offset := 0
	if hasContext {
		offset = 1
	}
	if typ.NumIn()-offset != len(contract.Params) {
		return nil, diagnostic("E-ARITY", "Go signature/schema arity mismatch")
	}
	return func(ctx context.Context, r Request) Response {
		vs, err := bindContract(contract, r.Positional, r.Named)
		if err != nil {
			response := Rejected("E-SCHEMA", err.Error())
			response.boundaryFault = true
			return response
		}
		args := make([]reflect.Value, typ.NumIn())
		if hasContext {
			args[0] = reflect.ValueOf(ctx)
		}
		for i, v := range vs {
			converted, err := toReflect(v, typ.In(i+offset))
			if err != nil {
				response := Rejected("E-SCHEMA", err.Error())
				response.boundaryFault = true
				return response
			}
			args[i+offset] = converted
		}
		out := f.Call(args)
		if len(out) > 0 && typ.Out(len(out)-1) == reflect.TypeFor[error]() {
			last := out[len(out)-1]
			out = out[:len(out)-1]
			if !last.IsNil() {
				err := last.Interface().(error)
				var timeout interface{ Timeout() bool }
				timed := errors.As(err, &timeout)
				if contract.Effect != ReadOnlyEffect && (errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || timed && timeout.Timeout()) {
					return Uncertain(err.Error())
				}
				return Rejected("E-EFFECT-REJECTED", err.Error())
			}
		}
		values := make([]Value, len(out))
		for i, x := range out {
			value, err := FromNative(x.Interface())
			if err != nil {
				return Response{Outcome: RejectedOutcome, Error: diagnostic("E-VALUE", err.Error()), KnownCompleted: true}
			}
			values[i] = value
		}
		result := Value{}
		if len(values) == 1 {
			result = values[0]
		} else if len(values) > 1 {
			result = ListValue(values...)
		}
		return Result(result)
	}, nil
}
func toReflect(v Value, typ reflect.Type) (reflect.Value, error) {
	if typ == reflect.TypeFor[Value]() {
		return reflect.ValueOf(v), nil
	}
	if typ.Kind() == reflect.Interface && typ.NumMethod() == 0 {
		x, err := v.Native()
		if err != nil {
			return reflect.Value{}, err
		}
		if x == nil {
			return reflect.Zero(typ), nil
		}
		return reflect.ValueOf(x), nil
	}
	out := reflect.New(typ).Elem()
	switch typ.Kind() {
	case reflect.Float32, reflect.Float64:
		if v.k == Number {
			if typ.Kind() == reflect.Float32 && !math.IsNaN(v.n) && float64(float32(v.n)) != v.n {
				return reflect.Value{}, diagnostic("E-SCHEMA", "lossy float32 conversion")
			}
			out.SetFloat(v.n)
			return out, nil
		}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		if v.k == Number && math.Trunc(v.n) == v.n && math.Abs(v.n) <= 9007199254740991 && !out.OverflowInt(int64(v.n)) {
			out.SetInt(int64(v.n))
			return out, nil
		}
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		if v.k == Number && v.n >= 0 && math.Trunc(v.n) == v.n && v.n <= 9007199254740991 && !out.OverflowUint(uint64(v.n)) {
			out.SetUint(uint64(v.n))
			return out, nil
		}
	case reflect.String:
		if v.k == String {
			out.SetString(v.s)
			return out, nil
		}
	case reflect.Bool:
		if v.k == Bool {
			out.SetBool(v.b)
			return out, nil
		}
	case reflect.Slice:
		if v.k == List && typ.Elem().Kind() != reflect.Uint8 {
			out = reflect.MakeSlice(typ, len(v.l), len(v.l))
			for i, x := range v.l {
				z, e := toReflect(x, typ.Elem())
				if e != nil {
					return reflect.Value{}, e
				}
				out.Index(i).Set(z)
			}
			return out, nil
		}
	case reflect.Array:
		if v.k == List && len(v.l) == typ.Len() && typ.Elem().Kind() != reflect.Uint8 {
			for i, x := range v.l {
				z, e := toReflect(x, typ.Elem())
				if e != nil {
					return reflect.Value{}, e
				}
				out.Index(i).Set(z)
			}
			return out, nil
		}
	case reflect.Map:
		if v.k == Object && typ.Key().Kind() == reflect.String {
			out = reflect.MakeMapWithSize(typ, len(v.o))
			for k, x := range v.o {
				z, e := toReflect(x, typ.Elem())
				if e != nil {
					return reflect.Value{}, e
				}
				key := reflect.New(typ.Key()).Elem()
				key.SetString(k)
				out.SetMapIndex(key, z)
			}
			return out, nil
		}
	case reflect.Struct:
		if v.k == Object {
			for i := 0; i < typ.NumField(); i++ {
				f := typ.Field(i)
				if !f.IsExported() {
					continue
				}
				name := strings.Split(f.Tag.Get("json"), ",")[0]
				if name == "-" {
					continue
				}
				if name == "" {
					name = f.Name
				}
				x, ok := v.o[name]
				if !ok {
					continue
				}
				z, e := toReflect(x, f.Type)
				if e != nil {
					return reflect.Value{}, e
				}
				out.Field(i).Set(z)
			}
			return out, nil
		}
	}
	return reflect.Value{}, diagnostic("E-SCHEMA", "cannot map Data to "+typ.String())
}

// Pack Data arguments individually, leaving protocol metadata as ordinary JSON.
func nativeArgumentWire(positional []Value, named map[string]Value) ([]string, map[string]string, error) {
	xs := make([]string, len(positional))
	for i, v := range positional {
		x, err := canonicalHex(v)
		if err != nil {
			return nil, nil, err
		}
		xs[i] = x.s
	}
	o := make(map[string]string, len(named))
	for k, v := range named {
		x, err := canonicalHex(v)
		if err != nil {
			return nil, nil, err
		}
		o[k] = x.s
	}
	return xs, o, nil
}

// PythonHandler invokes a registered member in a single-call worker using
// c-hex Data transport. Module output goes to stderr.
func PythonHandler(executable string, contract Contract) func(context.Context, Request) Response {
	if executable == "" {
		executable = "python3"
	}
	return func(ctx context.Context, r Request) Response {
		args, kwargs, err := nativeArgumentWire(r.Positional, r.Named)
		if err != nil {
			return Rejected("E-SCHEMA", err.Error())
		}
		id, _ := r.CallID.Wire().Native()
		payload, err := json.Marshal(map[string]any{"module": contract.Operation.Path, "member": contract.Operation.Member, "args": args, "kwargs": kwargs, "call_id": id, "write": contract.Effect != ReadOnlyEffect})
		if err != nil {
			return Rejected("E-SCHEMA", err.Error())
		}
		cmd := exec.CommandContext(ctx, executable, "-c", pythonWorker)
		cmd.Stdin = strings.NewReader(string(payload))
		output, err := cmd.Output()
		if err != nil {
			return Uncertain("Python worker terminated: " + err.Error())
		}
		value, err := DecodeData(output)
		if err != nil {
			return Uncertain("invalid Python worker protocol: " + err.Error())
		}
		fields, ok := value.Fields()
		if !ok {
			return Uncertain("invalid Python response")
		}
		status, _ := fields["status"].Text()
		switch status {
		case "result":
			result, err := decodeCanonicalHex(fields["value"])
			if err != nil {
				return Response{Outcome: RejectedOutcome, Error: diagnostic("E-VALUE", err.Error()), KnownCompleted: true}
			}
			return Result(result)
		case "known_fault":
			message, _ := fields["message"].Text()
			return Response{Outcome: RejectedOutcome, Error: diagnostic("E-VALUE", message), KnownCompleted: true}
		case "rejected":
			message, _ := fields["message"].Text()
			return Rejected("E-EFFECT-REJECTED", message)
		case "uncertain":
			message, _ := fields["message"].Text()
			return Uncertain(message)
		}
		return Uncertain("unknown Python response")
	}
}

const pythonWorker = pythonCanonicalCodec + `import contextlib, importlib, json, math, sys
request = json.load(sys.stdin, parse_int=float, parse_float=float)
def data(x):
    if x is None or isinstance(x, (str, bool)):
        return x
    if isinstance(x, int):
        if abs(x) > 9007199254740991:
            raise ValueError("integer outside safe range")
        return float(x)
    if isinstance(x, float):
        return 0.0 if x == 0 else x
    if isinstance(x, (list, tuple)):
        return [data(item) for item in x]
    if isinstance(x, dict) and all(isinstance(k, str) for k in x):
        return {k: data(v) for k,v in x.items()}
    raise ValueError("native result requires explicit Data conversion")
try:
    with contextlib.redirect_stdout(sys.stderr):
        fn = importlib.import_module(request["module"])
        for name in request["member"].split("."):
            fn = getattr(fn, name)
        positional = [from_c_hex(x) for x in request["args"]]
        named = {k:from_c_hex(v) for k,v in request["kwargs"].items()}
        result = fn(*positional, **named)
except Exception as error:
    uncertain = request["write"] and isinstance(error, (TimeoutError, ConnectionError))
    response = {"status":"uncertain" if uncertain else "rejected", "message":str(error)}
else:
    try:
        response = {"status":"result", "value":c_hex(data(result))}
        json.dumps(response, ensure_ascii=False, allow_nan=False).encode("utf-8")
    except Exception as error:
        response = {"status":"known_fault", "message":str(error)}
sys.stdout.write(json.dumps(response, ensure_ascii=False, allow_nan=False))
`

// DemoHost registers four read operations and one console write. Python operations
// need Python 3 and only use its standard library. Console writes use stderr,
// leaving stdout for the Flow's final JSON output.
func DemoHost() (Host, error) {
	numberList := Schema{Type: "list", Items: &Schema{Type: "number"}}
	operations := []Contract{
		{
			Operation: Operation{Language: "python", Path: "statistics", Member: "mean"},
			Version:   "1", Params: []Parameter{{Name: "data", Schema: numberList}},
			Result: Schema{Type: "number"}, Effect: ReadOnlyEffect, Concurrent: true,
		},
		{
			Operation: Operation{Language: "python", Path: "builtins", Member: "sum"},
			Version:   "1", Params: []Parameter{{Name: "iterable", Schema: numberList}},
			Result: Schema{Type: "number"}, Effect: ReadOnlyEffect, Concurrent: true,
		},
		{
			// Native sha256 returns bytes. The explicit wrapper produces a Data string.
			Operation: Operation{Language: "go", Path: "crypto/sha256", Member: "Sum256"},
			Version:   "1", Params: []Parameter{{Name: "text", Schema: Schema{Type: "string"}}},
			Result: Schema{Type: "string"}, Effect: ReadOnlyEffect, Concurrent: true,
		},
		{
			Operation: Operation{Language: "go", Path: "strings", Member: "ToUpper"},
			Version:   "1", Params: []Parameter{{Name: "text", Schema: Schema{Type: "string"}}},
			Result: Schema{Type: "string"}, Effect: ReadOnlyEffect, Concurrent: true,
		},
		{
			Operation: Operation{Language: "host", Path: "console", Member: "write"},
			Version:   "1", Params: []Parameter{{Name: "message", Schema: Schema{Type: "string"}}},
			Result: Schema{Type: "null"}, Effect: WriteEffect,
		},
	}
	for i := range operations {
		operation := &operations[i]
		var err error
		switch i {
		case 0, 1:
			operation.Invoke = PythonHandler("python3", *operation)
		case 2:
			operation.Invoke, err = GoHandler(*operation, func(text string) string {
				digest := sha256.Sum256([]byte(text))
				return hex.EncodeToString(digest[:])
			})
		case 3:
			operation.Invoke, err = GoHandler(*operation, strings.ToUpper)
		case 4:
			operation.Invoke = func(_ context.Context, request Request) Response {
				message := ""
				if len(request.Positional) > 0 {
					message, _ = request.Positional[0].Text()
				} else {
					message, _ = request.Named["message"].Text()
				}
				if _, err := fmt.Fprintln(os.Stderr, message); err != nil {
					return Uncertain(err.Error())
				}
				return Result(NullValue())
			}
		}
		if err != nil {
			return Host{}, err
		}
	}
	return Host{Contracts: operations}, nil
}
