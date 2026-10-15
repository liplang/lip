package script

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"plugin"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"
)

// GoAdapter is the common reflection and resource bridge for Go packages and
// application exports. Its values stay native; only Data or scoped references
// cross into LIP. Calls remain ordinary Host operations.
type GoAdapter struct {
	mu         sync.Mutex
	token      string
	objects    map[string]reflect.Value
	libraries  map[string]map[string]any
	registered map[string]bool
	seq        uint64
	owners     map[string]nativeRun
	active     nativeRun
}
type goNamespace string
type nativeGoGeneric struct{ instances []any }

// Match the same homogeneous Data types used by Go's build-time inference.
// Resource arguments use their actual registered Go types.
func (a *GoAdapter) argumentType(x any) reflect.Type {
	switch v := x.(type) {
	case nil:
		return reflect.TypeFor[any]()
	case []any:
		t := reflect.TypeFor[any]()
		for i, x := range v {
			u := a.argumentType(x)
			if i == 0 {
				t = u
			} else if t != u {
				t = reflect.TypeFor[any]()
				break
			}
		}
		return reflect.SliceOf(t)
	case map[string]any:
		if _, ok := v[nativeReferenceKey]; ok {
			return a.decoded(x).Type()
		}
		t := reflect.TypeFor[any]()
		first := true
		for _, x := range v {
			u := a.argumentType(x)
			if first {
				t = u
				first = false
			} else if t != u {
				t = reflect.TypeFor[any]()
				break
			}
		}
		return reflect.MapOf(reflect.TypeFor[string](), t)
	default:
		return reflect.TypeOf(x)
	}
}
func (a *GoAdapter) genericInstance(g nativeGoGeneric, args []any) any {
	for _, fn := range g.instances {
		t := reflect.TypeOf(fn)
		fixed := t.NumIn()
		if t.IsVariadic() {
			fixed--
		}
		if len(args) < fixed || !t.IsVariadic() && len(args) != fixed {
			continue
		}
		match := true
		for i, x := range args {
			var want reflect.Type
			if t.IsVariadic() && i >= fixed {
				want = t.In(fixed).Elem()
			} else {
				want = t.In(i)
			}
			if a.argumentType(x) != want {
				match = false
				break
			}
		}
		if match {
			return fn
		}
	}
	fail("E-TYPE", "generic Go argument types have no static instance; register concrete instances in HostSource")
	return nil
}

func NewGoAdapter() *GoAdapter {
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		panic(err)
	}
	return &GoAdapter{token: hex.EncodeToString(id[:]), objects: map[string]reflect.Value{}, owners: map[string]nativeRun{}, libraries: map[string]map[string]any{}, registered: map[string]bool{}}
}

// RegisterPackage exposes supplied exports through import go. It is useful for
// embedded libraries, instantiated generic types and platforms without plugins.
// Register before creating programs/sessions; reflection copies the export map.
func (a *GoAdapter) RegisterPackage(path string, exports map[string]any) {
	a.mu.Lock()
	defer a.mu.Unlock()
	copy := map[string]any{}
	for name, value := range exports {
		copy[name] = value
	}
	a.libraries[path] = copy
	a.registered[path] = true
}

var nativeGoPlugins = struct {
	sync.Mutex
	exports map[string]map[string]any
}{exports: map[string]map[string]any{}}

func goPluginLoaded(key string) bool {
	nativeGoPlugins.Lock()
	defer nativeGoPlugins.Unlock()
	return nativeGoPlugins.exports[key] != nil
}

func (a *GoAdapter) packageMembers(path string) map[string]string {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.registered[path] {
		return nil
	}
	lib := a.libraries[path]
	if lib == nil {
		return nil
	}
	names := map[string]string{}
	for name, value := range lib {
		names[name] = fmt.Sprintf("%s: %T", name, value)
	}
	return names
}

// Registration metadata can be checked without executing package code.
func (a *GoAdapter) checkPackageCall(path, member string, count int, named bool) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	value := a.libraries[path][member]
	if v, ok := value.(reflect.Value); ok {
		if !v.IsValid() || !v.CanInterface() {
			return diagnostic("E-TYPE", "callee is not callable")
		}
		value = v.Interface()
	}
	if _, ok := value.(reflect.Type); ok {
		if named || count > 1 {
			return diagnostic("E-ARITY", "type constructor accepts zero or one positional argument")
		}
		return nil
	}
	t := reflect.TypeOf(value)
	if t == nil || t.Kind() != reflect.Func || reflect.ValueOf(value).IsNil() {
		return diagnostic("E-TYPE", "callee is not callable")
	}
	fixed := t.NumIn()
	if t.IsVariadic() {
		fixed--
	}
	if named || count < fixed || !t.IsVariadic() && count != fixed {
		return diagnostic("E-ARITY", "Go signature argument count mismatch; registered exports use positional arguments")
	}
	return nil
}
func (a *GoAdapter) Close() {
	a.mu.Lock()
	defer a.mu.Unlock()
	fresh := NewGoAdapter()
	a.token = fresh.token
	a.objects = fresh.objects
	a.owners = fresh.owners
	// Keep immutable registration metadata and loaded libraries. Go plugins cannot
	// be unloaded; package initialization is a process lifetime event.
}
func (a *GoAdapter) invoke(r nativeGoRequest) Response {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.active = r.Run
	response := a.respond(r)
	raw, err := json.Marshal(response)
	if err != nil {
		return Response{Outcome: RejectedOutcome, Error: diagnostic("E-VALUE", err.Error()), KnownCompleted: true}
	}
	return nativeResponse(raw)
}

type nativeGoRequest struct {
	Run                                      nativeRun
	Module, Member, Action, Plugin, PluginID string
	Args                                     []any
	Kwargs                                   map[string]any
}
type nativeGoFault struct{ code, message string }

func fail(code, message string) { panic(nativeGoFault{code, message}) }
func (a *GoAdapter) reference(v reflect.Value) any {
	a.seq++
	id := strconv.FormatUint(a.seq, 10)
	a.objects[id] = v
	a.owners[id] = a.active
	return map[string]any{"$lip.native": map[string]any{"language": "go", "token": a.token, "scope": a.active.scope, "owner": a.active.owner, "generation": a.token, "id": id, "type": v.Type().String(), "go_type": a.typeInfo(v.Type())}}
}
func (a *GoAdapter) typeInfo(t reflect.Type) any {
	if t.Name() != "" {
		return map[string]any{"kind": "named", "name": t.Name(), "path": t.PkgPath()}
	}
	switch t.Kind() {
	case reflect.Slice:
		return map[string]any{"kind": "slice", "elem": a.typeInfo(t.Elem())}
	case reflect.Pointer:
		return map[string]any{"kind": "pointer", "elem": a.typeInfo(t.Elem())}
	case reflect.Array:
		return map[string]any{"kind": "array", "length": t.Len(), "elem": a.typeInfo(t.Elem())}
	case reflect.Map:
		return map[string]any{"kind": "map", "key": a.typeInfo(t.Key()), "elem": a.typeInfo(t.Elem())}
	}
	return map[string]any{"kind": "unsupported"}
}

func (a *GoAdapter) referenceTypeInfo(x any, run nativeRun) (info map[string]any, err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	previous := a.active
	a.active = run
	defer func() { a.active = previous }()
	defer func() {
		if p := recover(); p != nil {
			if fault, ok := p.(nativeGoFault); ok {
				err = diagnostic(fault.code, fault.message)
			} else {
				err = diagnostic("E-RESOURCE", fmt.Sprint(p))
			}
		}
	}()
	v := a.decoded(x)
	return a.typeInfo(v.Type()).(map[string]any), nil
}
func (a *GoAdapter) encode(v reflect.Value, seen map[uintptr]bool) any {
	if !v.IsValid() {
		return nil
	}
	if v.Kind() == reflect.Interface {
		if v.IsNil() {
			return nil
		}
		return a.encode(v.Elem(), seen)
	}
	if v.Type() == reflect.TypeFor[Value]() {
		value := v.Interface().(Value)
		native, err := value.Native()
		if err != nil {
			fail("E-VALUE", err.Error())
		}
		return native
	}
	switch v.Kind() {
	case reflect.Pointer, reflect.Map, reflect.Slice, reflect.Func, reflect.Chan:
		if v.IsNil() {
			return nil
		}
	}
	if v.Type() == reflect.TypeFor[json.Number]() {
		s := string(v.Interface().(json.Number))
		n, err := strconv.ParseFloat(s, 64)
		if err != nil {
			fail("E-VALUE", "constant outside numeric range")
		}
		if !strings.ContainsAny(s, ".eE") && math.Abs(n) > 9007199254740991 {
			fail("E-VALUE", "integer outside safe range")
		}
		return a.encode(reflect.ValueOf(n), seen)
	}
	switch v.Kind() {
	case reflect.Bool:
		return v.Bool()
	case reflect.String:
		if !utf8.ValidString(v.String()) {
			fail("E-VALUE", "native string is not valid UTF-8; use a byte resource or explicit encoding")
		}
		return v.String()
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		n := v.Int()
		if n < -9007199254740991 || n > 9007199254740991 {
			fail("E-VALUE", "integer outside safe range")
		}
		return n
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		n := v.Uint()
		if n > 9007199254740991 {
			fail("E-VALUE", "integer outside safe range")
		}
		return n
	case reflect.Float32, reflect.Float64:
		n := v.Float()
		if math.IsNaN(n) || math.IsInf(n, 0) {
			fail("E-VALUE", "non-finite number")
		}
		return n
	}
	// Preserve native container types, methods, pointer identity and resources.
	if v.Kind() == reflect.Struct && !v.CanAddr() {
		copy := reflect.New(v.Type()).Elem()
		copy.Set(v)
		v = copy
	}
	return a.reference(v)
}
func (a *GoAdapter) decoded(x any) reflect.Value {
	if x == nil {
		return reflect.Value{}
	}
	if m, ok := x.(map[string]any); ok {
		if raw, ok := m["$lip.native"]; ok {
			r, ok := raw.(map[string]any)
			if !ok {
				fail("E-RESOURCE", "invalid resource reference")
			}
			if r["token"] != a.token || r["language"] != "go" {
				fail("E-RESOURCE", "foreign, forged or expired native resource reference")
			}
			id, _ := r["id"].(string)
			v, ok := a.objects[id]
			if !ok {
				fail("E-RESOURCE", "expired native resource reference")
			}
			if a.active.owner != "" && a.owners[id].owner != a.active.owner {
				fail("E-RESOURCE", "native resource belongs to another owner")
			}
			return v
		}
		m2 := map[string]any{}
		for k, item := range m {
			v := a.decoded(item)
			if v.IsValid() {
				m2[k] = v.Interface()
			} else {
				m2[k] = nil
			}
		}
		return reflect.ValueOf(m2)
	}
	if xs, ok := x.([]any); ok {
		out := make([]any, len(xs))
		for i, item := range xs {
			v := a.decoded(item)
			if v.IsValid() {
				out[i] = v.Interface()
			}
		}
		return reflect.ValueOf(out)
	}
	return reflect.ValueOf(x)
}
func (a *GoAdapter) convert(x any, t reflect.Type) reflect.Value {
	v := a.decoded(x)
	if !v.IsValid() {
		switch t.Kind() {
		case reflect.Interface, reflect.Pointer, reflect.Map, reflect.Slice, reflect.Func, reflect.Chan:
			return reflect.Zero(t)
		}
		fail("E-SCHEMA", "null cannot map to "+t.String())
	}
	if v.Type().AssignableTo(t) {
		return v
	}
	if t.Kind() == reflect.Interface && v.Type().Implements(t) {
		return v
	}
	out := reflect.New(t).Elem()
	if n, ok := x.(float64); ok {
		switch t.Kind() {
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			if math.Trunc(n) == n && math.Abs(n) <= 9007199254740991 && !out.OverflowInt(int64(n)) {
				out.SetInt(int64(n))
				return out
			}
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
			if math.Trunc(n) == n && n >= 0 && n <= 9007199254740991 && !out.OverflowUint(uint64(n)) {
				out.SetUint(uint64(n))
				return out
			}
		case reflect.Float32, reflect.Float64:
			if !out.OverflowFloat(n) && (t.Kind() != reflect.Float32 || float64(float32(n)) == n) {
				out.SetFloat(n)
				return out
			}
		}
	}
	switch t.Kind() {
	case reflect.String:
		if s, ok := x.(string); ok {
			out.SetString(s)
			return out
		}
	case reflect.Bool:
		if b, ok := x.(bool); ok {
			out.SetBool(b)
			return out
		}
	case reflect.Pointer:
		out.Set(reflect.New(t.Elem()))
		out.Elem().Set(a.convert(x, t.Elem()))
		return out
	case reflect.Slice, reflect.Array:
		if s, ok := x.(string); ok && t.Kind() == reflect.Slice && t.Elem().Kind() == reflect.Uint8 {
			out.SetBytes([]byte(s))
			return out
		}
		if xs, ok := x.([]any); ok {
			if t.Kind() == reflect.Array {
				if len(xs) != t.Len() {
					fail("E-SCHEMA", "array length mismatch")
				}
			} else {
				out.Set(reflect.MakeSlice(t, len(xs), len(xs)))
			}
			for i, item := range xs {
				out.Index(i).Set(a.convert(item, t.Elem()))
			}
			return out
		}
	case reflect.Map:
		if m, ok := x.(map[string]any); ok && t.Key().Kind() == reflect.String {
			out.Set(reflect.MakeMap(t))
			for k, item := range m {
				key := reflect.New(t.Key()).Elem()
				key.SetString(k)
				out.SetMapIndex(key, a.convert(item, t.Elem()))
			}
			return out
		}
	case reflect.Struct:
		if m, ok := x.(map[string]any); ok {
			for k, item := range m {
				f := out.FieldByName(k)
				if !f.IsValid() || !f.CanSet() {
					fail("E-SCHEMA", "unknown or private struct field "+k)
				}
				f.Set(a.convert(item, f.Type()))
			}
			return out
		}
	}
	if v.Type().ConvertibleTo(t) && v.Kind() == t.Kind() {
		return v.Convert(t)
	}
	fail("E-SCHEMA", "cannot map value to "+t.String())
	return out
}
func (a *GoAdapter) member(v reflect.Value, name string) reflect.Value {
	if v.IsValid() && v.Type() == reflect.TypeFor[goNamespace]() {
		path := string(v.Interface().(goNamespace))
		value, ok := a.libraries[path][name]
		if !ok {
			fail("E-OPERATION", "no exported member "+name)
		}
		if v, ok := value.(reflect.Value); ok {
			return v
		}
		return reflect.ValueOf(value)
	}
	if v.Kind() == reflect.Interface {
		v = v.Elem()
	}
	if m := v.MethodByName(name); m.IsValid() {
		return m
	}
	if v.Kind() != reflect.Pointer && v.CanAddr() {
		if m := v.Addr().MethodByName(name); m.IsValid() {
			return m
		}
	}
	for v.Kind() == reflect.Pointer {
		if v.IsNil() {
			fail("E-TYPE", "nil receiver")
		}
		v = v.Elem()
	}
	if v.Kind() == reflect.Struct {
		f := v.FieldByName(name)
		if f.IsValid() && f.CanInterface() {
			return f
		}
	}
	fail("E-OPERATION", "no exported member "+name)
	return reflect.Value{}
}
func (a *GoAdapter) snapshot(v reflect.Value, seen map[uintptr]bool) any {
	if !v.IsValid() {
		return nil
	}
	if v.Kind() == reflect.Interface || v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return nil
		}
		if v.Kind() == reflect.Pointer {
			p := v.Pointer()
			if seen[p] {
				fail("E-VALUE", "cyclic native object")
			}
			seen[p] = true
			defer delete(seen, p)
		}
		return a.snapshot(v.Elem(), seen)
	}
	switch v.Kind() {
	case reflect.Slice, reflect.Array:
		if v.Kind() == reflect.Slice {
			p := v.Pointer()
			if seen[p] {
				fail("E-VALUE", "cyclic native list")
			}
			seen[p] = true
			defer delete(seen, p)
		}
		xs := make([]any, v.Len())
		for i := range xs {
			xs[i] = a.snapshot(v.Index(i), seen)
		}
		return xs
	case reflect.Map:
		if v.Type().Key().Kind() != reflect.String {
			fail("E-VALUE", "Data map requires string keys")
		}
		p := uintptr(v.UnsafePointer())
		if seen[p] {
			fail("E-VALUE", "cyclic native map")
		}
		seen[p] = true
		defer delete(seen, p)
		m := map[string]any{}
		iter := v.MapRange()
		for iter.Next() {
			if !utf8.ValidString(iter.Key().String()) {
				fail("E-VALUE", "Data map key is not valid UTF-8")
			}
			m[iter.Key().String()] = a.snapshot(iter.Value(), seen)
		}
		return m
	case reflect.Struct:
		m := map[string]any{}
		for i := 0; i < v.NumField(); i++ {
			if v.Type().Field(i).IsExported() {
				m[v.Type().Field(i).Name] = a.snapshot(v.Field(i), seen)
			}
		}
		return m
	}
	encoded := a.encode(v, seen)
	if ref, ok := encoded.(map[string]any); ok && ref[nativeReferenceKey] != nil {
		fail("E-VALUE", "object needs an explicit Data conversion")
	}
	return encoded
}
func (a *GoAdapter) execute(r nativeGoRequest) any {
	// Validate every reference before plugin.Open can run package initializers.
	for _, x := range r.Args {
		a.decoded(x)
	}
	for _, x := range r.Kwargs {
		a.decoded(x)
	}
	name := r.Member
	if r.Module == "lip.native" && (r.Action == "get" || r.Action == "prepare" || r.Action == "call") {
		name = strings.TrimPrefix(name, r.Action+".")
	}
	var value any
	if r.Module == "lip.native" {
		if len(r.Args) == 0 {
			fail("E-ARITY", "missing receiver")
		}
		v := a.decoded(r.Args[0])
		ref, _ := r.Args[0].(map[string]any)
		if meta, ok := ref[nativeReferenceKey].(map[string]any); ok {
			if id, ok := meta["id"].(string); ok {
				if r.Action == "release" {
					delete(a.objects, id)
					delete(a.owners, id)
					return true
				}
				if temporary, _ := meta["temporary"].(bool); temporary && r.Action == "call" {
					defer func() { delete(a.objects, id); delete(a.owners, id) }()
				}
			}
		}
		r.Args = r.Args[1:]
		if r.Action == "index" {
			if len(r.Args) != 1 {
				fail("E-ARITY", "index needs one key")
			}
			for v.Kind() == reflect.Pointer || v.Kind() == reflect.Interface {
				v = v.Elem()
			}
			if v.Kind() == reflect.Map {
				return a.encode(v.MapIndex(a.convert(r.Args[0], v.Type().Key())), map[uintptr]bool{})
			}
			if v.Kind() != reflect.Slice && v.Kind() != reflect.Array && v.Kind() != reflect.String {
				fail("E-TYPE", "native value cannot be indexed")
			}
			i, ok := r.Args[0].(float64)
			if !ok || math.Trunc(i) != i || i < 0 || i >= float64(v.Len()) {
				fail("E-INDEX", "index out of range")
			}
			return a.encode(v.Index(int(i)), map[uintptr]bool{})
		}
		if r.Action == "snapshot" {
			return a.snapshot(v, map[uintptr]bool{})
		}
		if r.Action == "length" {
			switch v.Kind() {
			case reflect.Array, reflect.Slice, reflect.Map, reflect.String, reflect.Chan:
			default:
				fail("E-TYPE", "native value has no length")
			}
			return v.Len()
		}
		if r.Action == "string" {
			return fmt.Sprint(v.Interface())
		}
		if r.Member != "call" && r.Member != "prepare" {
			for _, part := range strings.Split(name, ".") {
				v = a.member(v, part)
			}
		}
		value = v
	} else {
		lib := a.libraries[r.Module]
		if lib == nil {
			lib = map[string]any{}
			a.libraries[r.Module] = lib
		}
		if r.PluginID != "" {
			nativeGoPlugins.Lock()
			func() {
				defer nativeGoPlugins.Unlock()
				exports := nativeGoPlugins.exports[r.PluginID]
				if exports == nil {
					p, err := plugin.Open(r.Plugin)
					if err != nil {
						fail("E-IMPORT", err.Error())
					}
					symbol, err := p.Lookup("Library")
					if err != nil {
						fail("E-IMPORT", err.Error())
					}
					exports = *symbol.(*map[string]any)
					nativeGoPlugins.exports[r.PluginID] = exports
				}
				for name, value := range exports {
					lib[name] = value
				}
			}()
		}
		if name == "" {
			return a.reference(reflect.ValueOf(goNamespace(r.Module)))
		}
		parts := strings.Split(name, ".")
		var ok bool
		value, ok = lib[parts[0]]
		if !ok {
			fail("E-OPERATION", "no exported symbol "+name)
		}
		for _, part := range parts[1:] {
			v, ok := value.(reflect.Value)
			if !ok {
				v = reflect.ValueOf(value)
			}
			value = a.member(v, part)
		}
	}
	if v, ok := value.(reflect.Value); ok && v.IsValid() {
		if typ, ok := v.Interface().(reflect.Type); ok {
			value = typ
		}
		if generic, ok := v.Interface().(nativeGoGeneric); ok {
			value = generic
		}
	}
	if generic, ok := value.(nativeGoGeneric); ok {
		if r.Action == "get" {
			return a.reference(reflect.ValueOf(generic))
		}
		if r.Action == "prepare" {
			return a.temporary(reflect.ValueOf(generic))
		}
		value = a.genericInstance(generic, r.Args)
	}
	if typ, ok := value.(reflect.Type); ok {
		if r.Action == "get" {
			return a.reference(reflect.ValueOf(typ))
		}
		if len(r.Kwargs) != 0 || len(r.Args) > 1 {
			fail("E-ARITY", "type constructor accepts zero or one argument")
		}
		if r.Action == "prepare" {
			return a.temporary(reflect.ValueOf(typ))
		}
		v := reflect.New(typ).Elem()
		if len(r.Args) == 1 {
			v = a.convert(r.Args[0], typ)
		}
		return a.encode(v, map[uintptr]bool{})
	}
	v, ok := value.(reflect.Value)
	if !ok {
		v = reflect.ValueOf(value)
	}
	if r.Action == "get" {
		return a.encode(v, map[uintptr]bool{})
	}
	if v.Kind() != reflect.Func || v.IsNil() {
		if s, ok := value.(string); ok && strings.HasPrefix(s, "generic ") {
			fail("E-TYPE", s)
		}
		fail("E-TYPE", "callee is not callable")
	}
	if len(r.Kwargs) != 0 {
		fail("E-ARITY", "Go calls use positional arguments")
	}
	t := v.Type()
	fixed := t.NumIn()
	if t.IsVariadic() {
		fixed--
	}
	if len(r.Args) < fixed || !t.IsVariadic() && len(r.Args) != fixed {
		fail("E-ARITY", "Go signature argument count mismatch")
	}
	if r.Action == "prepare" {
		return a.temporary(v)
	}
	args := make([]reflect.Value, len(r.Args))
	for i, x := range r.Args {
		var typ reflect.Type
		if t.IsVariadic() && i >= fixed {
			typ = t.In(fixed).Elem()
		} else {
			typ = t.In(i)
		}
		args[i] = a.convert(x, typ)
	}
	out := v.Call(args)
	// A sole error is also a useful Go value (errors.New, fmt.Errorf). Preserve
	// it as a resource. In a multi-result signature, a trailing error is the
	// conventional success/failure channel; applications can use explicit
	// contracts when they need a different result mapping.
	if len(out) > 1 && t.Out(len(out)-1) == reflect.TypeFor[error]() {
		last := out[len(out)-1]
		out = out[:len(out)-1]
		if !last.IsNil() {
			fail("E-EFFECT-REJECTED", last.Interface().(error).Error())
		}
	}
	if len(out) == 0 {
		return nil
	}
	if len(out) == 1 {
		return a.encode(out[0], map[uintptr]bool{})
	}
	xs := make([]any, len(out))
	for i, x := range out {
		xs[i] = a.encode(x, map[uintptr]bool{})
	}
	return xs
}

func (a *GoAdapter) temporary(v reflect.Value) any {
	ref := a.reference(v).(map[string]any)
	ref[nativeReferenceKey].(map[string]any)["temporary"] = true
	return ref
}
func (a *GoAdapter) respond(r nativeGoRequest) (response map[string]any) {
	response = map[string]any{}
	before := a.seq
	defer func() {
		if p := recover(); p != nil {
			for id := range a.objects {
				seq, _ := strconv.ParseUint(id, 10, 64)
				if seq > before {
					delete(a.objects, id)
					delete(a.owners, id)
				}
			}
			code := "E-EFFECT-REJECTED"
			message := fmt.Sprint(p)
			status := "uncertain"
			if f, ok := p.(nativeGoFault); ok {
				code, message = f.code, f.message
				status = "rejected"
			}
			if code == "E-VALUE" {
				status = "known_fault"
			}
			response = map[string]any{"status": status, "code": code, "message": message}
		}
	}()
	result := a.execute(r)
	return map[string]any{"status": "result", "value": result}
}
