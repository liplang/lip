package script

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/constant"
	"go/importer"
	goparser "go/parser"
	gotoken "go/token"
	"go/types"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// NativeLibraries owns persistent Python and Go runtimes. Only Invoke starts
// native code; discovery, lowering, completion and proposal creation do not.
// Native objects remain in their runtime and cross the Data boundary as scoped
// resource references. Close (or Session.Reset) invalidates those references.
type NativeLibraries struct {
	mu       sync.Mutex
	dir      string
	workers  map[string]*nativeProcess
	packages map[string]*nativePackage
	Python   string
	Go       *GoAdapter
	// Static forbids runtime package discovery/building in generated programs.
	static bool
}
type nativeProcess struct {
	cmd     *exec.Cmd
	in      io.WriteCloser
	out     *os.File
	decoder *json.Decoder
	token   string
	dead    bool
}
type nativePackage struct {
	source     string
	members    map[string]string
	signatures map[string]*types.Signature
	pkg        *types.Package
	plugin     string
	manual     bool
	parameters map[string]nativeGoParameters
}
type nativeGoParameters struct {
	names    []string
	variadic bool
}
type nativeStaticValue struct{ typ types.Type }

// Native syntax provenance is independent of the normative type/effect system.
// Functions retain their result shape with their frozen lexical captures.
type nativeShape struct {
	native       bool
	fields       map[string]nativeShape
	items        []nativeShape
	item, result *nativeShape
	source       *expression
}

func (n *NativeLibraries) freezeStatic() { n.mu.Lock(); defer n.mu.Unlock(); n.static = true }

func (n *NativeLibraries) registerStatic(path string, exports map[string]any, parameters map[string]nativeGoParameters) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.Go == nil {
		n.Go = NewGoAdapter()
	}
	n.Go.RegisterPackage(path, exports)
	if n.packages == nil {
		n.packages = map[string]*nativePackage{}
	}
	n.packages[path] = &nativePackage{manual: true, parameters: parameters}
}

func NewNativeHost(base Host) Host {
	if base.Native == nil {
		base.Native = &NativeLibraries{Python: "python3", Go: NewGoAdapter()}
	}
	return base
}
func (h Host) Close() error {
	if h.Native != nil {
		return h.Native.Close()
	}
	return nil
}
func (n *NativeLibraries) Close() error {
	n.mu.Lock()
	defer n.mu.Unlock()
	for _, w := range n.workers {
		w.in.Close()
		w.cmd.Process.Kill()
		w.cmd.Wait()
		w.out.Close()
	}
	if n.Go != nil {
		n.Go.Close()
	}
	n.workers = nil
	n.packages = nil
	if n.dir != "" {
		os.RemoveAll(n.dir)
		n.dir = ""
	}
	return nil
}

const nativeReferenceKey = "$lip.native"

type nativeRunKey struct{}
type nativeSessionKey struct{}
type nativeRun struct{ scope, owner, id string }

func newNativeOwner() (string, error) {
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(id[:]), nil
}
func newNativeRun(ctx context.Context, runID string, session bool) (nativeRun, error) {
	id, err := newNativeOwner()
	if err != nil {
		return nativeRun{}, err
	}
	r := nativeRun{scope: "run", owner: runID + ":" + id, id: id}
	if session {
		r.scope = "session"
		r.owner, _ = ctx.Value(nativeSessionKey{}).(string)
	}
	return r, nil
}

// New handles are retained only when reachable from a successful cell. Existing
// session handles belong to that session until release/reset/close. A file run
// revokes every handle it created, even if its output contains a reference.
func (n *NativeLibraries) endRun(run nativeRun, values []Value) error {
	keep := map[string]map[string]bool{"go": {}, "python": {}}
	functions := map[*function]bool{}
	environments := map[*environment]bool{}
	var visit func(Value)
	var env func(*environment)
	env = func(e *environment) {
		if e == nil || environments[e] {
			return
		}
		environments[e] = true
		for _, v := range e.values {
			visit(v)
		}
		env(e.parent)
	}
	visit = func(v Value) {
		if meta, ok := v.o[nativeReferenceKey]; ok && meta.k == Object {
			language, _ := meta.o["language"].Text()
			id, _ := meta.o["id"].Text()
			if ids := keep[language]; ids != nil {
				ids[id] = true
			}
		}
		for _, x := range v.l {
			visit(x)
		}
		for _, x := range v.o {
			visit(x)
		}
		if v.f != nil && !functions[v.f] {
			functions[v.f] = true
			env(v.f.captures)
		}
		if v.p != nil {
			for _, x := range v.p.positional {
				visit(x)
			}
			for _, x := range v.p.named {
				visit(x)
			}
		}
	}
	for _, v := range values {
		visit(v)
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.Go != nil {
		n.Go.mu.Lock()
		for id, owner := range n.Go.owners {
			if owner.id == run.id && !keep["go"][id] {
				delete(n.Go.objects, id)
				delete(n.Go.owners, id)
			}
		}
		n.Go.mu.Unlock()
	}
	if w := n.workers["python"]; w != nil && !w.dead {
		payload := map[string]any{"module": "lip.cleanup", "action": "cleanup", "args": []any{}, "run": run.id, "keep": keep["python"]}
		if err := json.NewEncoder(w.in).Encode(payload); err != nil {
			w.dead = true
			return diagnostic("E-RESOURCE", "native cleanup: "+err.Error())
		}
		var raw json.RawMessage
		if err := w.decoder.Decode(&raw); err != nil {
			w.dead = true
			return diagnostic("E-RESOURCE", "native cleanup: "+err.Error())
		}
		if response := nativeResponse(raw); response.Error != nil {
			return response.Error
		}
	}
	return nil
}

// NativeDescription does not inspect or call the referenced object.
func NativeDescription(v Value) (string, bool) {
	if v.k != Object {
		return "", false
	}
	r, ok := v.o[nativeReferenceKey]
	if !ok || r.k != Object {
		return "", false
	}
	language, _ := r.o["language"].Text()
	typ, _ := r.o["type"].Text()
	return "<" + language + " " + typ + ">", true
}
func containsNative(v Value) bool {
	if _, ok := NativeDescription(v); ok {
		return true
	}
	for _, x := range v.l {
		if containsNative(x) {
			return true
		}
	}
	for _, x := range v.o {
		if containsNative(x) {
			return true
		}
	}
	return false
}

func (n *NativeLibraries) discover(ctx context.Context, path string) (*nativePackage, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if p := n.packages[path]; p != nil {
		return p, nil
	}
	if n.Go != nil {
		if members := n.Go.packageMembers(path); members != nil {
			return &nativePackage{members: members, manual: true}, nil
		}
	}
	if n.static {
		return nil, diagnostic("E-IMPORT", "Go package was not statically registered: "+path)
	}
	// Export data is produced by the user's Go toolchain and module resolver.
	// No per-package registry and no package initializers are involved here.
	cmd := exec.CommandContext(ctx, "go", "list", "-export", "-deps", "-json", path)
	output, err := cmd.Output()
	if err != nil {
		return nil, nativeGoImportError(path, err)
	}
	exports := map[string]string{}
	decoder := json.NewDecoder(strings.NewReader(string(output)))
	for {
		var info struct{ ImportPath, Export string }
		if err := decoder.Decode(&info); err == io.EOF {
			break
		} else if err != nil {
			return nil, err
		}
		exports[info.ImportPath] = info.Export
	}
	imp := importer.ForCompiler(gotoken.NewFileSet(), "gc", func(path string) (io.ReadCloser, error) { return os.Open(exports[path]) })
	pkg, err := imp.Import(path)
	if err != nil {
		return nil, diagnostic("E-IMPORT", err.Error())
	}
	p := &nativePackage{members: map[string]string{}, signatures: map[string]*types.Signature{}, pkg: pkg}
	var body strings.Builder
	fmt.Fprintf(&body, "package main\nimport(\"reflect\";\"encoding/json\";p %s)\nvar Library = map[string]any{\n", strconv.Quote(path))
	for _, name := range pkg.Scope().Names() {
		obj := pkg.Scope().Lookup(name)
		if !obj.Exported() {
			continue
		}
		p.members[name] = types.ObjectString(obj, func(p *types.Package) string { return p.Name() })
		switch o := obj.(type) {
		case *types.Func:
			sig := o.Type().(*types.Signature)
			p.signatures[name] = sig
			if sig.TypeParams().Len() != 0 {
				fmt.Fprintf(&body, "%q: %q,\n", name, "generic function requires concrete Go type arguments: "+name)
			} else {
				fmt.Fprintf(&body, "%q: p.%s,\n", name, name)
			}
		case *types.Var:
			if sig, ok := o.Type().Underlying().(*types.Signature); ok {
				p.signatures[name] = sig
			}
			fmt.Fprintf(&body, "%q: reflect.ValueOf(&p.%s).Elem(),\n", name, name)
		case *types.TypeName:
			t := types.Unalias(o.Type())
			if named, ok := t.(*types.Named); ok && named.TypeParams().Len() != 0 {
				fmt.Fprintf(&body, "%q: %q,\n", name, "generic type requires concrete Go type arguments: "+name)
			} else {
				fmt.Fprintf(&body, "%q: reflect.TypeOf((*p.%s)(nil)).Elem(),\n", name, name)
			}
		case *types.Const:
			switch o.Val().Kind() {
			case constant.String:
				fmt.Fprintf(&body, "%q: %s,\n", name, strconv.Quote(constant.StringVal(o.Val())))
			case constant.Bool:
				fmt.Fprintf(&body, "%q: %s,\n", name, o.Val().ExactString())
			case constant.Int:
				fmt.Fprintf(&body, "%q: json.Number(%q),\n", name, o.Val().ExactString())
			case constant.Float:
				n, _ := constant.Float64Val(o.Val())
				fmt.Fprintf(&body, "%q: json.Number(%q),\n", name, strconv.FormatFloat(n, 'g', -1, 64))
			case constant.Complex:
				fmt.Fprintf(&body, "%q: complex128(p.%s),\n", name, name)
			}
		}
	}
	body.WriteString("}\nvar _ = reflect.TypeOf\nvar _ = json.Number(\"0\")\nvar _ = p")
	// Every real package has at least one exported symbol referenced above.
	source := strings.TrimSuffix(body.String(), "\nvar _ = p")
	// Packages with no exports still need their initialization side effects.
	if len(p.members) == 0 {
		source = strings.Replace(source, "p "+strconv.Quote(path), "_ "+strconv.Quote(path), 1)
	}
	p.source = source
	if n.packages == nil {
		n.packages = map[string]*nativePackage{}
	}
	n.packages[path] = p
	return p, nil
}

func nativeGoImportError(path string, err error) error {
	message := err.Error()
	if e, ok := err.(*exec.ExitError); ok && len(e.Stderr) != 0 {
		message = strings.TrimSpace(string(e.Stderr))
	}
	if strings.Contains(message, "is not in std") && !strings.Contains(path, "/") {
		message += "\nGo imports use case-sensitive package paths, not library project names. Use the library's full package path with import go \"<package-path>\" as alias. Configure third-party dependencies with go get in your Go module, then start lipc from that module's directory."
	} else if strings.Contains(message, "no required module provides package") || strings.Contains(message, "cannot find module providing package") || strings.Contains(message, "missing go.sum entry") {
		message += "\nConfigure this dependency with go get in your Go module, then start lipc from that module's directory. If there is no go.mod, initialize your own module with go mod init first. Installing lipc does not install imported libraries; import does not add dependencies automatically."
	}
	return diagnostic("E-IMPORT", fmt.Sprintf("Go import %q failed: %s", path, message))
}

func nativeAction(op Operation) string {
	if op.Language == "host" {
		action := strings.SplitN(op.Member, ".", 2)[0]
		if action == "get" || action == "prepare" || action == "call" {
			return action
		}
		return op.Member
	}
	if op.Member == "perform.get" || strings.HasPrefix(op.Member, "perform.get.") {
		return "get"
	}
	if strings.HasPrefix(op.Member, "perform.prepare.") {
		return "prepare"
	}
	return "call"
}

func (n *NativeLibraries) contract(op Operation, action string) Contract {
	c := Contract{Operation: op, Version: "lip-native-1", Params: []Parameter{{Name: "args", Schema: Schema{Type: "list"}}, {Name: "kwargs", Schema: Schema{Type: "object"}}}, Result: Schema{Type: "any"}, Effect: UnknownEffect}
	c.Invoke = func(ctx context.Context, r Request) Response { return n.invoke(ctx, op, action, r) }
	return c
}

func (n *NativeLibraries) start(ctx context.Context, language string) (*nativeProcess, error) {
	if n.workers == nil {
		n.workers = map[string]*nativeProcess{}
	}
	if w := n.workers[language]; w != nil {
		if w.dead {
			return nil, fmt.Errorf("%s runtime was terminated; reset the session before continuing", language)
		}
		return w, nil
	}
	if n.dir == "" {
		dir, err := os.MkdirTemp("", "lip-native-")
		if err != nil {
			return nil, err
		}
		n.dir = dir
	}
	python := n.Python
	if python == "" {
		python = "python3"
	}
	cmd := exec.Command(python, "-u", "-c", nativePythonWorker)
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return nil, err
	}
	refToken := hex.EncodeToString(id[:])
	cmd.Env = append(os.Environ(), "LIP_NATIVE_TOKEN="+refToken, "PYTHONDONTWRITEBYTECODE=1")
	reader, writer, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	cmd.ExtraFiles = []*os.File{writer}
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	in, err := cmd.StdinPipe()
	if err != nil {
		reader.Close()
		writer.Close()
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		reader.Close()
		writer.Close()
		in.Close()
		return nil, err
	}
	writer.Close()
	w := &nativeProcess{cmd: cmd, in: in, out: reader, decoder: json.NewDecoder(reader), token: refToken}
	n.workers[language] = w
	return w, nil
}

func (n *NativeLibraries) invoke(ctx context.Context, op Operation, action string, r Request) Response {
	// Discover before taking the process lock. Native code is still not running.
	var pkg *nativePackage
	if op.Language == "go" {
		var err error
		pkg, err = n.discover(ctx, op.Path)
		if err != nil {
			return Rejected("E-IMPORT", err.Error())
		}
	}
	args, _ := r.Positional[0].Native()
	kwargs, _ := r.Positional[1].Native()
	if pkg != nil && action == "call" {
		if params, ok := pkg.parameters[op.Member]; ok {
			xs, err := nativeOrderedGoArguments(params.names, params.variadic, args.([]any), kwargs.(map[string]any))
			if err != nil {
				return Rejected("E-ARITY", err.Error())
			}
			args, kwargs = xs, map[string]any{}
		}
		if sig := pkg.signatures[op.Member]; sig != nil {
			xs, err := nativeGoArguments(sig, args.([]any), kwargs.(map[string]any))
			if err != nil {
				return Rejected("E-ARITY", err.Error())
			}
			args, kwargs = xs, map[string]any{}
			if sig.TypeParams().Len() != 0 {
				source, err := n.specialize(ctx, op, xs)
				if err != nil {
					if d, ok := err.(*Diagnostic); ok {
						return Rejected(d.Code, d.Message)
					}
					return Rejected("E-TYPE", err.Error())
				}
				pkg = &nativePackage{source: source}
			}
		}
	}
	language := op.Language
	// Object operations carry the receiver as their first packed argument.
	if language == "host" {
		xs := args.([]any)
		if len(xs) == 0 {
			return Rejected("E-SCHEMA", "missing native receiver")
		}
		for {
			ref, ok := xs[0].(map[string]any)
			if !ok {
				return n.dataAccess(ctx, action, op.Member, xs)
			}
			if meta, ok := ref[nativeReferenceKey].(map[string]any); ok {
				language, _ = meta["language"].(string)
				if language != "python" && language != "go" {
					return Rejected("E-SCHEMA", "invalid native language")
				}
				break
			}
			// Immutable Data wrappers may hold native resources or callables.
			// Resolve their field prefix here, at the authorized perform boundary,
			// before delegating the remaining path to the owning native runtime.
			if (action == "call" || action == "prepare") && strings.HasPrefix(op.Member, action+".") {
				parts := strings.SplitN(strings.TrimPrefix(op.Member, action+"."), ".", 2)
				if receiver, ok := ref[parts[0]]; ok {
					xs[0] = receiver
					op.Member = action
					if len(parts) == 2 {
						op.Member += "." + parts[1]
					}
					continue
				}
			}
			return n.dataAccess(ctx, action, op.Member, xs)
		}
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	pluginPath := ""
	pluginID := ""
	if pkg != nil && pkg.source != "" {
		pluginID = digest(textValue(pkg.source))
		if n.dir == "" {
			dir, err := os.MkdirTemp("", "lip-native-")
			if err != nil {
				return Rejected("E-IMPORT", err.Error())
			}
			n.dir = dir
		}
		if pkg.plugin == "" && !goPluginLoaded(pluginID) {
			file := filepath.Join(n.dir, "pkg-"+digest(textValue(pkg.source))+".go")
			pluginPath = strings.TrimSuffix(file, ".go") + ".so"
			if err := os.WriteFile(file, []byte(pkg.source), 0600); err != nil {
				return Rejected("E-IMPORT", err.Error())
			}
			if _, err := os.Stat(pluginPath); err != nil {
				buildArgs := []string{"build", "-buildmode=plugin", "-o", pluginPath, file}
				if os.Getenv("LIP_NATIVE_RACE") == "1" {
					buildArgs = append([]string{"build", "-race"}, buildArgs[1:]...)
				}
				build := exec.CommandContext(ctx, "go", buildArgs...)
				if output, err := build.CombinedOutput(); err != nil {
					return Rejected("E-IMPORT", fmt.Sprintf("Go package build: %s: %v", output, err))
				}
			}
			pkg.plugin = pluginPath
		}
		pluginPath = pkg.plugin
	}
	member := op.Member
	if op.Language != "host" && (action == "get" || action == "prepare") {
		prefix := "perform." + action
		if member == prefix {
			member = ""
		} else {
			member = strings.TrimPrefix(member, prefix+".")
		}
	}
	if language == "go" {
		if n.Go == nil {
			n.Go = NewGoAdapter()
		}
		run, _ := ctx.Value(nativeRunKey{}).(nativeRun)
		return n.Go.invoke(nativeGoRequest{Run: run, Module: op.Path, Member: member, Action: action, Plugin: pluginPath, PluginID: pluginID, Args: args.([]any), Kwargs: kwargs.(map[string]any)})
	}
	w, err := n.start(ctx, language)
	if err != nil {
		return Rejected("E-IMPORT", err.Error())
	}
	run, _ := ctx.Value(nativeRunKey{}).(nativeRun)
	payload := map[string]any{"scope": run.scope, "owner": run.owner, "run": run.id, "language": language, "module": op.Path, "member": member, "action": action, "args": args, "kwargs": kwargs, "plugin": pluginPath}
	// No automatic restart/retry: termination might follow a completed write.
	done := make(chan struct{})
	watchDone := make(chan struct{})
	go func() {
		defer close(watchDone)
		select {
		case <-ctx.Done():
			w.cmd.Process.Kill()
		case <-done:
		}
	}()
	defer func() { close(done); <-watchDone }()
	if err := json.NewEncoder(w.in).Encode(payload); err != nil {
		w.dead = true
		return Uncertain("native runtime terminated: " + err.Error())
	}
	var raw json.RawMessage
	if err := w.decoder.Decode(&raw); err != nil {
		w.dead = true
		return Uncertain("native runtime terminated: " + err.Error())
	}
	return nativeResponse(raw)
}

func nativeResponse(raw []byte) Response {
	value, err := DecodeData(raw)
	if err != nil {
		return Uncertain("invalid native response: " + err.Error())
	}
	f := value.o
	status, _ := f["status"].Text()
	message, _ := f["message"].Text()
	code, _ := f["code"].Text()
	if code == "" {
		code = "E-EFFECT-REJECTED"
	}
	switch status {
	case "result":
		return Result(f["value"])
	case "known_fault":
		return Response{Outcome: RejectedOutcome, Error: diagnostic("E-VALUE", message), KnownCompleted: true}
	case "rejected":
		response := Rejected(code, message)
		response.boundaryFault = code == "E-RESOURCE" || code == "E-SCHEMA" || code == "E-ARITY" || code == "E-TYPE" || code == "E-IMPORT" || code == "E-OPERATION" || code == "E-INDEX"
		return response
	case "uncertain":
		return Uncertain(message)
	}
	return Uncertain("unknown native response")
}

type nativeTypeImporter struct {
	ctx       context.Context
	libraries *NativeLibraries
}

func (i nativeTypeImporter) Import(path string) (*types.Package, error) {
	p, err := i.libraries.discover(i.ctx, path)
	if err != nil {
		return nil, err
	}
	return p.pkg, nil
}

// Let go/types infer generic type arguments from the actual Data/native types.
// The resulting function is compiled by Go itself, including its constraints.
func (n *NativeLibraries) specialize(ctx context.Context, op Operation, args []any) (string, error) {
	imports := map[string]string{op.Path: "p"}
	qualify := func(path string) string {
		if name := imports[path]; name != "" {
			return name
		}
		name := fmt.Sprintf("lib%d", len(imports))
		imports[path] = name
		return name
	}
	var resourceType func(map[string]any) (string, error)
	resourceType = func(meta map[string]any) (string, error) {
		kind, _ := meta["kind"].(string)
		switch kind {
		case "named":
			path, _ := meta["path"].(string)
			name, _ := meta["name"].(string)
			if path == "" {
				return name, nil
			}
			if !ast.IsExported(name) {
				return "", fmt.Errorf("generic argument type %s is private", name)
			}
			return qualify(path) + "." + name, nil
		case "slice", "pointer", "array":
			elem, ok := meta["elem"].(map[string]any)
			if !ok {
				return "", fmt.Errorf("invalid native type")
			}
			typ, err := resourceType(elem)
			prefix := "[]"
			if kind == "pointer" {
				prefix = "*"
			}
			if kind == "array" {
				prefix = fmt.Sprintf("[%v]", meta["length"])
			}
			return prefix + typ, err
		case "map":
			key, err := resourceType(meta["key"].(map[string]any))
			if err != nil {
				return "", err
			}
			value, err := resourceType(meta["elem"].(map[string]any))
			return "map[" + key + "]" + value, err
		}
		return "", fmt.Errorf("native type cannot be used for generic inference")
	}
	var dataType func(any) (string, error)
	dataType = func(x any) (string, error) {
		switch v := x.(type) {
		case nativeStaticValue:
			return types.TypeString(v.typ, func(p *types.Package) string { return qualify(p.Path()) }), nil
		case string:
			return "string", nil
		case bool:
			return "bool", nil
		case float64:
			return "float64", nil
		case []any:
			elem := "any"
			for i, x := range v {
				t, err := dataType(x)
				if err != nil {
					return "", err
				}
				if i == 0 {
					elem = t
				} else if t != elem {
					elem = "any"
					break
				}
			}
			return "[]" + elem, nil
		case map[string]any:
			if _, ok := v[nativeReferenceKey].(map[string]any); ok {
				if n.Go == nil {
					return "", fmt.Errorf("generic Go arguments require Go native resources")
				}
				run, _ := ctx.Value(nativeRunKey{}).(nativeRun)
				meta, err := n.Go.referenceTypeInfo(v, run)
				if err != nil {
					return "", err
				}
				return resourceType(meta)
			}
			elem := "any"
			first := true
			for _, x := range v {
				t, err := dataType(x)
				if err != nil {
					return "", err
				}
				if first {
					elem = t
					first = false
				} else if t != elem {
					elem = "any"
					break
				}
			}
			return "map[string]" + elem, nil
		}
		return "any", nil
	}
	var body strings.Builder
	body.WriteString("func infer(){\n")
	for i, x := range args {
		typ, err := dataType(x)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(&body, "var a%d %s\n", i, typ)
	}
	fmt.Fprintf(&body, "p.%s(", op.Member)
	for i := range args {
		if i != 0 {
			body.WriteString(",")
		}
		fmt.Fprintf(&body, "a%d", i)
	}
	body.WriteString(")}\n")
	header := func() string {
		var b strings.Builder
		b.WriteString("package main\nimport(\n")
		var paths []string
		for path := range imports {
			paths = append(paths, path)
		}
		sort.Strings(paths)
		for _, path := range paths {
			fmt.Fprintf(&b, "%s %q\n", imports[path], path)
		}
		b.WriteString(")\n")
		return b.String()
	}
	fset := gotoken.NewFileSet()
	file, err := goparser.ParseFile(fset, "infer.go", header()+body.String(), 0)
	if err != nil {
		return "", err
	}
	info := &types.Info{Instances: map[*ast.Ident]types.Instance{}}
	config := types.Config{Importer: nativeTypeImporter{ctx, n}}
	if _, err := config.Check("lip.native.infer", fset, []*ast.File{file}, info); err != nil {
		return "", fmt.Errorf("Go generic inference: %w", err)
	}
	var instance types.Instance
	found := false
	for id, value := range info.Instances {
		if id.Name == op.Member {
			instance = value
			found = true
			break
		}
	}
	if !found {
		return "", fmt.Errorf("Go generic type arguments could not be inferred")
	}
	var typeArgs []string
	for i := 0; i < instance.TypeArgs.Len(); i++ {
		typeArgs = append(typeArgs, types.TypeString(instance.TypeArgs.At(i), func(p *types.Package) string { return qualify(p.Path()) }))
	}
	return header() + fmt.Sprintf("var Library=map[string]any{%q:p.%s[%s]}\n", op.Member, op.Member, strings.Join(typeArgs, ",")), nil
}

func (n *NativeLibraries) dataAccess(ctx context.Context, action, member string, args []any) Response {
	// A native result may contain ordinary immutable Data. Projection remains
	// useful when its type was not known during lowering.
	v, err := FromNative(args[0])
	if err != nil {
		return Rejected("E-SCHEMA", err.Error())
	}
	if action == "snapshot" {
		return n.snapshotData(ctx, v)
	}
	if action == "string" {
		if v.k == String {
			return Result(v)
		}
		data, err := EncodeJSON(v)
		if err != nil {
			return Rejected("E-VALUE", err.Error())
		}
		return Result(textValue(string(data)))
	}
	if action == "length" {
		switch v.k {
		case Object:
			return Result(number(float64(len(v.o))))
		case List:
			return Result(number(float64(len(v.l))))
		case String:
			return Result(number(float64(len([]rune(v.s)))))
		}
	}
	key := strings.TrimPrefix(member, "get.")
	if action == "get" && v.k == Object {
		if x, ok := v.o[key]; ok {
			return Result(x)
		}
	}
	if action == "index" && len(args) == 2 {
		if v.k == Object {
			if key, ok := args[1].(string); ok {
				if x, ok := v.o[key]; ok {
					return Result(x)
				}
			}
		}
		if v.k == List {
			if i, ok := args[1].(float64); ok && i >= 0 && i < float64(len(v.l)) && i == float64(int(i)) {
				return Result(v.l[int(i)])
			}
		}
		if v.k == String {
			rs := []rune(v.s)
			if i, ok := args[1].(float64); ok && i >= 0 && i < float64(len(rs)) && i == float64(int(i)) {
				return Result(textValue(string(rs[int(i)])))
			}
		}
	}
	return Rejected("E-TYPE", "value has no native member "+key)
}

func (n *NativeLibraries) snapshotData(ctx context.Context, v Value) Response {
	if _, ok := NativeDescription(v); ok {
		return n.invoke(ctx, Operation{"host", "lip.native", "snapshot"}, "snapshot", Request{Positional: []Value{ListValue(v), object(nil)}})
	}
	if v.k == List {
		xs := make([]Value, len(v.l))
		for i, x := range v.l {
			r := n.snapshotData(ctx, x)
			if r.Outcome != ResultOutcome {
				return r
			}
			xs[i] = r.Value
		}
		return Result(ListValue(xs...))
	}
	if v.k == Object {
		m := map[string]Value{}
		for _, key := range sortedKeys(v.o) {
			r := n.snapshotData(ctx, v.o[key])
			if r.Outcome != ResultOutcome {
				return r
			}
			m[key] = r.Value
		}
		return Result(object(m))
	}
	return Result(v)
}

func nativeGoArguments(sig *types.Signature, pos []any, named map[string]any) ([]any, error) {
	names := make([]string, sig.Params().Len())
	for i := range names {
		names[i] = sig.Params().At(i).Name()
	}
	return nativeOrderedGoArguments(names, sig.Variadic(), pos, named)
}
func nativeOrderedGoArguments(names []string, variadic bool, pos []any, named map[string]any) ([]any, error) {
	fixed := len(names)
	if variadic {
		fixed--
	}
	if !variadic && len(pos) > fixed {
		return nil, fmt.Errorf("too many Go arguments")
	}
	args := make([]any, fixed)
	set := make([]bool, fixed)
	for i := 0; i < len(pos) && i < fixed; i++ {
		args[i] = pos[i]
		set[i] = true
	}
	var tail []any
	if len(pos) > fixed {
		tail = append(tail, pos[fixed:]...)
	}
	for name, value := range named {
		index := -1
		for i := range names {
			if names[i] == name {
				index = i
				break
			}
		}
		if index < 0 {
			return nil, fmt.Errorf("unknown Go argument %s", name)
		}
		if variadic && index == fixed {
			if len(pos) > fixed {
				return nil, fmt.Errorf("duplicate variadic argument %s", name)
			}
			xs, ok := value.([]any)
			if !ok {
				return nil, fmt.Errorf("named variadic argument %s must be a list", name)
			}
			tail = xs
			continue
		}
		if set[index] {
			return nil, fmt.Errorf("duplicate Go argument %s", name)
		}
		args[index] = value
		set[index] = true
	}
	for i, ok := range set {
		if !ok {
			return nil, fmt.Errorf("missing Go argument %s", names[i])
		}
	}
	return append(args, tail...), nil
}

func (n *NativeLibraries) completions(path string) []string {
	n.mu.Lock()
	defer n.mu.Unlock()
	var names []string
	if n.Go != nil {
		for name := range n.Go.packageMembers(path) {
			names = append(names, name)
		}
	}
	if p := n.packages[path]; p != nil {
		for name := range p.members {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}
