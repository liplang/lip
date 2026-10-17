package script

import (
	"bytes"
	"context"
	"embed"
	"fmt"
	"go/ast"
	"go/format"
	goparser "go/parser"
	gotoken "go/token"
	"go/types"
	"math"
	"sort"
	"strconv"
	"strings"
)

// Runtime sources are bundled with the compiler. The runtime uses the standard
// library; selected Go libraries are linked from the application's module.
// Executables need neither the compiler nor a source checkout.
//
//go:embed compiler.go checker.go values.go evaluator.go scheduler.go host.go trace.go adapters.go native.go native_workers.go go_adapter.go
var runtimeSources embed.FS

// GoOptions supplies trusted native integration code, never LIP program text.
// HostSource is a Go file in package main with func generatedHost() (Host,error).
// Its imports resolve in the application's Go module. Contracts must match
// the Host used by Compile. With no operations, HostSource may be empty.
type GoOptions struct{ HostSource string }

// DemoHostGoSource exports the same explicit teaching Host used by DemoHost.
// Read the already bundled adapter source, so installed lipc needs no checkout
// and operation contracts have a single definition across execution paths.
func DemoHostGoSource() (string, error) {
	source, err := runtimeSources.ReadFile("adapters.go")
	if err != nil {
		return "", err
	}
	file, err := goparser.ParseFile(gotoken.NewFileSet(), "adapters.go", source, 0)
	if err != nil {
		return "", err
	}
	for _, declaration := range file.Decls {
		fn, ok := declaration.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "DemoHost" {
			continue
		}
		fn.Name.Name = "generatedHost"
		var output strings.Builder
		output.WriteString("package main\nimport (\n")
		for _, im := range file.Imports {
			output.WriteString(printGo(im))
			output.WriteByte('\n')
		}
		output.WriteString(")\n")
		output.WriteString(printGo(fn))
		return output.String(), nil
	}
	return "", fmt.Errorf("bundled adapters lack DemoHost")
}

// EmitGo compiles a checked program to self-contained Go source. Every LIP
// expression and local statement becomes a specialized Go function. Static
// region metadata is retained for the shared scheduler, not reparsed at run time.
func (p *Program) EmitGo(options GoOptions) (source []byte, err error) {
	defer func() { err = diagnosticPhase(err, "static") }()
	needsHostSource := false
	for _, c := range p.host.Contracts {
		needsHostSource = needsHostSource || c.Version != "lip-native-1"
	}
	if p.host.Native != nil && p.host.Native.Go != nil {
		for _, im := range p.syntax.imports {
			if im.language == "go" && p.host.Native.Go.packageMembers(im.path) != nil {
				needsHostSource = true
			}
		}
	}
	if needsHostSource && options.HostSource == "" {
		return nil, diagnostic("E-OPERATION", "Go emission requires native Host registration source")
	}
	g := &goEmitter{program: p, exprID: map[*expression]int{}, stmtID: map[*statement]int{}, blockID: map[*block]int{}}
	for _, name := range sortedFunctionNames(p.syntax.functions) {
		g.expression(p.syntax.functions[name])
	}
	g.block(p.syntax.body)
	for _, name := range sortedFlowNames(p.syntax.flows) {
		g.block(p.syntax.flows[name].body)
	}
	declarations, imports, templates, err := compiledRuntime()
	if err != nil {
		return nil, err
	}
	var body strings.Builder
	body.WriteString(declarations)
	if options.HostSource != "" {
		file, err := goparser.ParseFile(gotoken.NewFileSet(), "host.go", options.HostSource, 0)
		if err != nil {
			return nil, fmt.Errorf("Host source: %w", err)
		}
		for _, im := range file.Imports {
			path, _ := strconv.Unquote(im.Path.Value)
			name := importName(im, path)
			if old, ok := imports[name]; ok && old != path {
				return nil, fmt.Errorf("conflicting Host import %s", name)
			}
			imports[name] = path
		}
		for _, d := range file.Decls {
			if x, ok := d.(*ast.GenDecl); ok && x.Tok == gotoken.IMPORT {
				continue
			}
			body.WriteString(printGo(d))
			body.WriteByte('\n')
		}
	} else {
		body.WriteString("func generatedHost()(Host,error){return Host{},nil}\n")
	}
	for i := range g.exprs {
		fmt.Fprintf(&body, "var e%d = new(expression)\n", i)
	}
	for i := range g.stmts {
		fmt.Fprintf(&body, "var s%d = new(statement)\n", i)
	}
	for i := range g.blocks {
		fmt.Fprintf(&body, "var b%d = new(block)\n", i)
	}
	body.WriteString("var generatedMetadataOnce sync.Once\nvar generatedStaticData map[string]dataDecl\nvar generatedFlows map[string]*flowDecl\nvar generatedBuild Value\n")
	body.WriteString("func generatedProgram(host Host)(*Program,error){\n")
	body.WriteString("if host.Resources==nil{host.Resources=NewResourcePool(nil)}\n")
	if p.host.Native != nil {
		body.WriteString("host=NewNativeHost(host)\n")
		body.WriteString("host.Contracts=append([]Contract(nil),host.Contracts...)\n")
		registration, err := g.staticGoLibraries(imports)
		if err != nil {
			return nil, err
		}
		body.WriteString(registration)
		body.WriteString("host.Native.freezeStatic()\n")
		for _, c := range p.host.Contracts {
			if c.Version != "lip-native-1" {
				continue
			}
			action := nativeAction(c.Operation)
			fmt.Fprintf(&body, "host.Contracts=append(host.Contracts,host.Native.contract(Operation{%s,%s,%s},%s))\n", quote(c.Operation.Language), quote(c.Operation.Path), quote(c.Operation.Member), quote(action))
		}
	}
	body.WriteString(`contracts:=make(map[string]Contract)
 for _,c:=range host.Contracts { if err:=validateContract(c);err!=nil{return nil,err};key:=identityKey(c.Operation.Wire());if _,ok:=contracts[key];ok{return nil,diagnostic("E-OPERATION","duplicate Host operation")};contracts[key]=cloneContract(c) }
`)
	for _, c := range p.host.Contracts {
		fmt.Fprintf(&body, "if c,ok:=contracts[%s];!ok || contractFingerprint(c)!=%s{return nil,diagnostic(\"E-OPERATION\",\"compiled operation contract changed\")}\n", quote(identityKey(c.Operation.Wire())), quote(contractFingerprint(c)))
	}
	// Build all nodes before evaluating. Initializers only link static metadata
	// and native function pointers; frozen source text is build metadata only.
	// Programs share immutable metadata; concurrent factories never rewrite it.
	body.WriteString("generatedMetadataOnce.Do(func(){\n")
	for i, e := range g.exprs {
		fmt.Fprintf(&body, "*e%d = %s\n", i, g.exprLiteral(e, i))
	}
	for i, s := range g.stmts {
		fmt.Fprintf(&body, "*s%d = %s\n", i, g.stmtLiteral(s, i))
	}
	for i, b := range g.blocks {
		fmt.Fprintf(&body, "*b%d = %s\n", i, g.blockLiteral(b))
	}
	body.WriteString("generatedStaticData=map[string]dataDecl{")
	namesData := make([]string, 0, len(p.syntax.data))
	for name := range p.syntax.data {
		namesData = append(namesData, name)
	}
	sort.Strings(namesData)
	for _, name := range namesData {
		d := p.syntax.data[name]
		fmt.Fprintf(&body, "%s:{typ:%s,value:%s,tok:%s},", quote(name), quote(d.typ), valueLiteral(d.value), tokenLiteral(d.tok))
	}
	body.WriteString("}\ngeneratedFlows=map[string]*flowDecl{")
	for _, name := range sortedFlowNames(p.syntax.flows) {
		f := p.syntax.flows[name]
		fmt.Fprintf(&body, "%s:{name:%s,result:%s,params:%s,body:%s,tok:%s,summary:analysis{typ:%s,effect:EffectClass(%d),concurrent:%t}},", quote(name), quote(f.name), quote(f.result), g.params(f.params), g.blockRef(f.body), tokenLiteral(f.tok), quote(f.result), f.summary.effect, f.summary.concurrent)
	}
	fmt.Fprintf(&body, "}\ngeneratedBuild=%s\n})\n", valueLiteral(p.build))
	fmt.Fprintf(&body, "syntax:=&syntax{flowName:%s,result:%s,params:%s,body:%s,data:generatedStaticData,flows:generatedFlows,functions:map[string]*expression{", quote(p.syntax.flowName), quote(p.syntax.result), g.params(p.syntax.params), g.blockRef(p.syntax.body))
	for _, name := range sortedFunctionNames(p.syntax.functions) {
		fmt.Fprintf(&body, "%s:%s,", quote(name), g.exprRef(p.syntax.functions[name]))
	}
	body.WriteString("}}\n")
	body.WriteString("check:=&checker{syntax:syntax,contracts:contracts,aliases:map[string]importDecl{},stores:map[string]*statement{},writers:map[string]int{}}\n")
	for _, d := range p.syntax.imports {
		fmt.Fprintf(&body, "check.aliases[%s]=importDecl{language:%s,path:%s,alias:%s}\n", quote(d.alias), quote(d.language), quote(d.path), quote(d.alias))
	}
	names := make([]string, 0, len(p.check.stores))
	for name := range p.check.stores {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		fmt.Fprintf(&body, "check.stores[%s]=%s\n", quote(name), g.stmtRef(p.check.stores[name]))
	}
	names = names[:0]
	for name := range p.check.writers {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		fmt.Fprintf(&body, "check.writers[%s]=%d\n", quote(name), p.check.writers[name])
	}
	body.WriteString("host.Contracts=nil;for _,c:=range contracts{host.Contracts=append(host.Contracts,c)};capabilities:=make(map[string]bool,len(host.Capabilities));for k,v:=range host.Capabilities{capabilities[k]=v};host.Capabilities=capabilities;return &Program{syntax:syntax,check:check,host:host,build:generatedBuild},nil\n}\n")
	for i, e := range g.exprs {
		code, err := specialize(templates["eval"], e.tag, fmt.Sprintf("nativeExpr%d", i), fmt.Sprintf("e%d", i), true)
		if err != nil {
			return nil, err
		}
		body.WriteString(code)
	}
	for i, s := range g.stmts {
		// Flow-only statements execute through the graph scheduler, never locally.
		switch s.tag {
		case "bind", "eval", "if", "match", "for", "break", "continue":
			code, err := specialize(templates["localStatement"], s.tag, fmt.Sprintf("nativeStmt%d", i), fmt.Sprintf("s%d", i), false)
			if err != nil {
				return nil, err
			}
			body.WriteString(code)
		}
	}
	body.WriteString(generatedMain)
	for name, path := range map[string]string{"flag": "flag", "os": "os", "signal": "os/signal"} {
		imports[name] = path
	}
	used := map[string]bool{}
	tree, err := goparser.ParseFile(gotoken.NewFileSet(), "body.go", "package main\n"+body.String(), 0)
	if err != nil {
		return nil, fmt.Errorf("generated runtime: %w", err)
	}
	ast.Inspect(tree, func(n ast.Node) bool {
		if s, ok := n.(*ast.SelectorExpr); ok {
			if id, ok := s.X.(*ast.Ident); ok {
				used[id.Name] = true
			}
		}
		return true
	})
	var output strings.Builder
	output.WriteString("// Code generated by LIP; DO NOT EDIT.\n// Standalone Go target: static graph + native computation + Host runtime.\npackage main\nimport (\n")
	keys := make([]string, 0, len(imports))
	for k := range imports {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, name := range keys {
		if strings.HasPrefix(name, "_lipinit") {
			fmt.Fprintf(&output, "_ %s\n", quote(imports[name]))
			continue
		}
		if used[name] {
			fmt.Fprintf(&output, "%s %s\n", name, quote(imports[name]))
		}
	}
	output.WriteString(")\n")
	output.WriteString(body.String())
	formatted, err := format.Source([]byte(output.String()))
	if err != nil {
		return nil, fmt.Errorf("format generated Go: %w", err)
	}
	return formatted, nil
}

// Link automatic exports and generic instances into the executable. Discovery
// and Go type inference happen here, never in a deployed operation.
func (g *goEmitter) staticGoLibraries(imports map[string]string) (string, error) {
	n := g.program.host.Native
	ctx := context.Background()
	aliases := g.program.check.aliases
	instances := map[Operation]map[string]string{}
	visiting := map[*expression]bool{}
	var example func(*expression) (any, error)
	var proposalExample func(*expression) (any, error)
	resolve := func(e *expression) (Operation, *nativePackage, *types.Signature, error) {
		parts := strings.SplitN(e.name, ".", 2)
		if len(parts) != 2 {
			return Operation{}, nil, nil, fmt.Errorf("not a native Go proposal")
		}
		im := aliases[parts[0]]
		if im.language != "go" {
			return Operation{}, nil, nil, fmt.Errorf("not a native Go proposal")
		}
		op := Operation{"go", im.path, parts[1]}
		pkg, err := n.discover(ctx, im.path)
		if err != nil {
			return op, nil, nil, err
		}
		return op, pkg, pkg.signatures[op.Member], nil
	}
	// Rename only package selectors. This retains full Go syntax for named,
	// nested and third-party type arguments without textual substitutions.
	library := func(source string) (map[string]string, *ast.File, error) {
		file, err := goparser.ParseFile(gotoken.NewFileSet(), "library.go", source, 0)
		if err != nil {
			return nil, nil, err
		}
		rename := map[string]string{}
		for _, im := range file.Imports {
			path, _ := strconv.Unquote(im.Path.Value)
			old := importName(im, path)
			if old == "_" {
				continue
			}
			name := ""
			for alias, p := range imports {
				if p == path && alias != "_" {
					name = alias
					break
				}
			}
			if name == "" {
				for i := 0; ; i++ {
					candidate := fmt.Sprintf("nativepkg%d", i)
					if _, ok := imports[candidate]; !ok {
						name = candidate
						break
					}
				}
				imports[name] = path
			}
			rename[old] = name
		}
		entries := map[string]string{}
		ast.Inspect(file, func(node ast.Node) bool {
			if s, ok := node.(*ast.SelectorExpr); ok {
				if id, ok := s.X.(*ast.Ident); ok && rename[id.Name] != "" {
					id.Name = rename[id.Name]
				}
			}
			return true
		})
		for _, d := range file.Decls {
			gd, ok := d.(*ast.GenDecl)
			if !ok || gd.Tok != gotoken.VAR {
				continue
			}
			for _, s := range gd.Specs {
				v, ok := s.(*ast.ValueSpec)
				if !ok || len(v.Names) != 1 || v.Names[0].Name != "Library" {
					continue
				}
				c := v.Values[0].(*ast.CompositeLit)
				for _, entry := range c.Elts {
					kv := entry.(*ast.KeyValueExpr)
					key, _ := strconv.Unquote(kv.Key.(*ast.BasicLit).Value)
					entries[key] = printGo(kv.Value)
				}
			}
		}
		return entries, file, nil
	}
	proposalExample = func(e *expression) (any, error) {
		op, _, sig, err := resolve(e)
		if err != nil || sig == nil {
			return nil, fmt.Errorf("native result type is not known at build time")
		}
		if sig.TypeParams().Len() > 0 {
			if len(e.args) != 2 {
				return nil, fmt.Errorf("invalid packed native arguments")
			}
			pos, err := example(e.args[0].expr)
			if err != nil {
				return nil, err
			}
			named, err := example(e.args[1].expr)
			if err != nil {
				return nil, err
			}
			args, err := nativeGoArguments(sig, pos.([]any), named.(map[string]any))
			if err != nil {
				return nil, err
			}
			source, err := n.specialize(ctx, op, args)
			if err != nil {
				return nil, err
			}
			// Read the instantiated signature using the same Go type checker.
			fset := gotoken.NewFileSet()
			file, err := goparser.ParseFile(fset, "instance.go", source, 0)
			if err != nil {
				return nil, err
			}
			info := &types.Info{Types: map[ast.Expr]types.TypeAndValue{}}
			config := types.Config{Importer: nativeTypeImporter{ctx, n}}
			if _, err := config.Check("lip.native.static", fset, []*ast.File{file}, info); err != nil {
				return nil, err
			}
			ast.Inspect(file, func(node ast.Node) bool {
				if kv, ok := node.(*ast.KeyValueExpr); ok {
					if key, ok := kv.Key.(*ast.BasicLit); ok && key.Value == strconv.Quote(op.Member) {
						if s, ok := info.TypeOf(kv.Value).(*types.Signature); ok {
							sig = s
						}
					}
				}
				return true
			})
			entries, _, err := library(source)
			if err != nil {
				return nil, err
			}
			if instances[op] == nil {
				instances[op] = map[string]string{}
			}
			instances[op][entries[op.Member]] = entries[op.Member]
		}
		if sig.Results().Len() == 0 {
			return nil, nil
		}
		if sig.Results().Len() != 1 && !(sig.Results().Len() == 2 && types.Identical(sig.Results().At(1).Type(), types.Universe.Lookup("error").Type())) {
			return nil, fmt.Errorf("multiple native results require explicit static registration for generic inference")
		}
		result := sig.Results().At(0).Type()
		// Scalars cross the common Data boundary before another native call:
		// Go int/float aliases become LIP binary64, and named text/bool values
		// become their Data primitives. Resources keep their Go type.
		if basic, ok := result.Underlying().(*types.Basic); ok {
			switch {
			case basic.Info()&(types.IsInteger|types.IsFloat) != 0:
				result = types.Typ[types.Float64]
			case basic.Info()&types.IsString != 0:
				result = types.Typ[types.String]
			case basic.Info()&types.IsBoolean != 0:
				result = types.Typ[types.Bool]
			}
		}
		if _, unknown := result.Underlying().(*types.Interface); unknown {
			return nil, fmt.Errorf("generic argument has a dynamic Go interface result; register concrete instances in HostSource")
		}
		return nativeStaticValue{result}, nil
	}
	example = func(e *expression) (any, error) {
		if e == nil || visiting[e] {
			return nil, fmt.Errorf("generic argument type is not known at build time; register concrete instances in HostSource")
		}
		visiting[e] = true
		defer delete(visiting, e)
		switch e.tag {
		case "lit":
			return e.value.Native()
		case "list":
			xs := make([]any, len(e.elems))
			for i, x := range e.elems {
				v, err := example(x)
				if err != nil {
					return nil, err
				}
				xs[i] = v
			}
			return xs, nil
		case "object":
			xs := map[string]any{}
			for _, f := range e.fields {
				v, err := example(f.expr)
				if err != nil {
					return nil, err
				}
				xs[f.name] = v
			}
			return xs, nil
		case "id":
			if x := g.program.syntax.nativeOrigins[e]; x != nil {
				return example(x)
			}
			if d, ok := g.program.syntax.data[e.name]; ok {
				return d.value.Native()
			}
		case "block":
			return example(e.body.tail)
		case "perform":
			if e.x.tag == "proposal" {
				return proposalExample(e.x)
			}
		}
		switch e.info.typ {
		case "number":
			return float64(0), nil
		case "string":
			return "", nil
		case "bool":
			return false, nil
		}
		return nil, fmt.Errorf("generic argument type is not known at build time; register concrete instances in HostSource")
	}
	for _, e := range g.exprs {
		if e.tag != "proposal" {
			continue
		}
		_, pkg, sig, err := resolve(e)
		if err != nil || pkg.manual || sig == nil || sig.TypeParams().Len() == 0 {
			continue
		}
		if _, err := proposalExample(e); err != nil {
			return "", at(diagnostic("E-TYPE", err.Error()), e.tok)
		}
	}
	var out strings.Builder
	paths := map[string]bool{}
	for _, im := range g.program.syntax.imports {
		if im.language == "go" {
			paths[im.path] = true
		}
	}
	ordered := make([]string, 0, len(paths))
	for path := range paths {
		ordered = append(ordered, path)
	}
	sort.Strings(ordered)
	for _, path := range ordered {
		pkg, err := n.discover(ctx, path)
		if err != nil {
			continue
		} // Explicit contracts may describe virtual paths.
		if pkg.manual {
			continue
		}
		entries, _, err := library(pkg.source)
		if err != nil {
			return "", err
		}
		if len(entries) == 0 {
			imports[fmt.Sprintf("_lipinit%d", len(imports))] = path
		}
		for name, sig := range pkg.signatures {
			if sig.TypeParams().Len() == 0 {
				continue
			}
			choices := instances[Operation{"go", path, name}]
			values := make([]string, 0, len(choices))
			for v := range choices {
				values = append(values, v)
			}
			sort.Strings(values)
			entries[name] = "nativeGoGeneric{instances:[]any{" + strings.Join(values, ",") + "}}"
		}
		fmt.Fprintf(&out, "host.Native.registerStatic(%q,map[string]any{", path)
		names := make([]string, 0, len(entries))
		for name := range entries {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			fmt.Fprintf(&out, "%q:%s,", name, entries[name])
		}
		out.WriteString("},map[string]nativeGoParameters{")
		for _, name := range names {
			if sig := pkg.signatures[name]; sig != nil {
				fmt.Fprintf(&out, "%q:{names:[]string{", name)
				for i := 0; i < sig.Params().Len(); i++ {
					fmt.Fprintf(&out, "%q,", sig.Params().At(i).Name())
				}
				fmt.Fprintf(&out, "},variadic:%t},", sig.Variadic())
			}
		}
		out.WriteString("})\n")
	}
	return out.String(), nil
}

type goEmitter struct {
	program *Program
	exprID  map[*expression]int
	stmtID  map[*statement]int
	blockID map[*block]int
	exprs   []*expression
	stmts   []*statement
	blocks  []*block
}

func (g *goEmitter) expression(e *expression) {
	if e == nil {
		return
	}
	if _, ok := g.exprID[e]; ok {
		return
	}
	g.exprID[e] = len(g.exprs)
	g.exprs = append(g.exprs, e)
	g.expression(e.x)
	g.expression(e.y)
	g.expression(e.z)
	for _, a := range e.args {
		g.expression(a.expr)
	}
	for _, x := range e.elems {
		g.expression(x)
	}
	for _, f := range e.fields {
		g.expression(f.expr)
	}
	g.block(e.body)
	g.arms(e.arms)
}
func (g *goEmitter) statement(s *statement) {
	if s == nil {
		return
	}
	if _, ok := g.stmtID[s]; ok {
		return
	}
	g.stmtID[s] = len(g.stmts)
	g.stmts = append(g.stmts, s)
	g.expression(s.expr)
	g.block(s.body)
	g.block(s.otherwise)
	g.statement(s.other)
	g.arms(s.arms)
}
func (g *goEmitter) block(b *block) {
	if b == nil {
		return
	}
	if _, ok := g.blockID[b]; ok {
		return
	}
	g.blockID[b] = len(g.blocks)
	g.blocks = append(g.blocks, b)
	for _, s := range b.items {
		g.statement(s)
	}
	g.expression(b.tail)
	g.statement(b.exit)
}
func (g *goEmitter) arms(as []arm) {
	for _, a := range as {
		g.expression(a.guard)
		g.expression(a.result)
		g.block(a.body)
	}
}
func (g *goEmitter) exprRef(e *expression) string {
	if e == nil {
		return "nil"
	}
	return fmt.Sprintf("e%d", g.exprID[e])
}
func (g *goEmitter) stmtRef(s *statement) string {
	if s == nil {
		return "nil"
	}
	return fmt.Sprintf("s%d", g.stmtID[s])
}
func (g *goEmitter) blockRef(b *block) string {
	if b == nil {
		return "nil"
	}
	return fmt.Sprintf("b%d", g.blockID[b])
}
func quote(s string) string { return strconv.Quote(s) }
func tokenLiteral(t token) string {
	return fmt.Sprintf("token{kind:%s,text:%s,site:%d,line:%d,column:%d,source:%s}", quote(t.kind), quote(t.text), t.site, t.line, t.column, quote(t.source))
}
func valueLiteral(v Value) string {
	switch v.k {
	case Null:
		return "NullValue()"
	case Bool:
		return fmt.Sprintf("BoolValue(%t)", v.b)
	case Number:
		if math.IsNaN(v.n) {
			return "nanValue()"
		}
		if math.IsInf(v.n, 1) {
			return "infValue()"
		}
		if math.IsInf(v.n, -1) {
			return "number(-infValue().n)"
		}
		return "number(" + strconv.FormatFloat(v.n, 'g', -1, 64) + ")"
	case String:
		return "textValue(" + quote(v.s) + ")"
	case List:
		items := make([]string, len(v.l))
		for i, value := range v.l {
			items[i] = valueLiteral(value)
		}
		return "ListValue(" + strings.Join(items, ",") + ")"
	case Object:
		items := make([]string, 0, len(v.o))
		for _, key := range sortedKeys(v.o) {
			items = append(items, quote(key)+":"+valueLiteral(v.o[key]))
		}
		return "object(map[string]Value{" + strings.Join(items, ",") + "})"
	}
	panic("non-literal in checked syntax")
}
func (g *goEmitter) params(ps []parameter) string {
	var b strings.Builder
	b.WriteString("[]parameter{")
	for _, p := range ps {
		fmt.Fprintf(&b, "{name:%s,typ:%s,tok:%s},", quote(p.name), quote(p.typ), tokenLiteral(p.tok))
	}
	b.WriteString("}")
	return b.String()
}
func (g *goEmitter) armLiteral(as []arm) string {
	var b strings.Builder
	b.WriteString("[]arm{")
	for _, a := range as {
		b.WriteString("{patterns:[]pattern{")
		for _, p := range a.patterns {
			fmt.Fprintf(&b, "{typ:%s,value:%s,wildcard:%t,tok:%s},", quote(p.typ), valueLiteral(p.value), p.wildcard, tokenLiteral(p.tok))
		}
		fmt.Fprintf(&b, "},guard:%s,result:%s,body:%s,tok:%s},", g.exprRef(a.guard), g.exprRef(a.result), g.blockRef(a.body), tokenLiteral(a.tok))
	}
	b.WriteString("}")
	return b.String()
}
func (g *goEmitter) exprLiteral(e *expression, i int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "expression{native:nativeExpr%d,trustedNative:%t,tag:%s,name:%s,typ:%s,state:%s,tok:%s,value:%s,x:%s,y:%s,z:%s,params:%s,body:%s,arms:%s,info:analysis{typ:%s,effect:EffectClass(%d),concurrent:%t,hasFlow:%t},", i, e.trustedNative, quote(e.tag), quote(e.name), quote(e.typ), quote(e.state), tokenLiteral(e.tok), valueLiteral(e.value), g.exprRef(e.x), g.exprRef(e.y), g.exprRef(e.z), g.params(e.params), g.blockRef(e.body), g.armLiteral(e.arms), quote(e.info.typ), e.info.effect, e.info.concurrent, e.info.hasFlow)
	b.WriteString("args:[]argument{")
	for _, a := range e.args {
		fmt.Fprintf(&b, "{name:%s,expr:%s},", quote(a.name), g.exprRef(a.expr))
	}
	b.WriteString("},elems:[]*expression{")
	for _, x := range e.elems {
		b.WriteString(g.exprRef(x) + ",")
	}
	b.WriteString("},fields:[]field{")
	for _, f := range e.fields {
		fmt.Fprintf(&b, "{name:%s,expr:%s,tok:%s},", quote(f.name), g.exprRef(f.expr), tokenLiteral(f.tok))
	}
	b.WriteString("}}")
	return b.String()
}
func (g *goEmitter) stmtLiteral(s *statement, i int) string {
	native := "nil"
	switch s.tag {
	case "bind", "eval", "if", "match", "for", "break", "continue":
		native = fmt.Sprintf("nativeStmt%d", i)
	}
	return fmt.Sprintf("statement{native:%s,tag:%s,name:%s,tok:%s,expr:%s,body:%s,otherwise:%s,other:%s,arms:%s,parallel:%t}", native, quote(s.tag), quote(s.name), tokenLiteral(s.tok), g.exprRef(s.expr), g.blockRef(s.body), g.blockRef(s.otherwise), g.stmtRef(s.other), g.armLiteral(s.arms), s.parallel)
}
func (g *goEmitter) blockLiteral(b *block) string {
	var out strings.Builder
	fmt.Fprintf(&out, "block{tok:%s,tail:%s,exit:%s,items:[]*statement{", tokenLiteral(b.tok), g.exprRef(b.tail), g.stmtRef(b.exit))
	for _, s := range b.items {
		out.WriteString(g.stmtRef(s) + ",")
	}
	out.WriteString("}}")
	return out.String()
}
func sortedFunctionNames(fs map[string]*expression) []string {
	names := make([]string, 0, len(fs))
	for n := range fs {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}
func printGo(n ast.Node) string {
	var b bytes.Buffer
	_ = format.Node(&b, gotoken.NewFileSet(), n)
	return b.String()
}
func importName(im *ast.ImportSpec, path string) string {
	if im.Name != nil {
		return im.Name.Name
	}
	parts := strings.Split(path, "/")
	return parts[len(parts)-1]
}

// Select only runtime declarations. Generated artifacts contain no lexer,
// parser, static checker, interpreter dispatch or embedded LIP source.
func compiledRuntime() (string, map[string]string, map[string]*ast.FuncDecl, error) {
	var b strings.Builder
	imports := map[string]string{}
	templates := map[string]*ast.FuncDecl{}
	allowed := map[string]bool{}
	for _, n := range strings.Fields("token parameter argument field pattern arm expression statement block importDecl flowDecl dataDecl syntax analysis operationSummary function proposal bindingInfo asciiDigit asciiLetter validID checker builtinArity error resolve") {
		allowed[n] = true
	}
	for _, name := range []string{"compiler.go", "checker.go", "values.go", "evaluator.go", "scheduler.go", "host.go", "trace.go", "adapters.go", "native.go", "native_workers.go", "go_adapter.go"} {
		src, _ := runtimeSources.ReadFile(name)
		f, err := goparser.ParseFile(gotoken.NewFileSet(), name, src, 0)
		if err != nil {
			return "", nil, nil, err
		}
		for _, im := range f.Imports {
			path, _ := strconv.Unquote(im.Path.Value)
			imports[importName(im, path)] = path
		}
		for _, d := range f.Decls {
			if gd, ok := d.(*ast.GenDecl); ok {
				if gd.Tok == gotoken.IMPORT {
					continue
				}
				if name == "compiler.go" || name == "checker.go" {
					keep := false
					for _, s := range gd.Specs {
						switch x := s.(type) {
						case *ast.TypeSpec:
							keep = keep || allowed[x.Name.Name]
						case *ast.ValueSpec:
							for _, n := range x.Names {
								keep = keep || allowed[n.Name]
							}
						}
					}
					if !keep {
						continue
					}
				}
			}
			if fn, ok := d.(*ast.FuncDecl); ok {
				// Demo registration is opt-in HostSource, outside the core runtime.
				if name == "adapters.go" && fn.Name.Name == "DemoHost" {
					continue
				}
				if name == "compiler.go" && fn.Name.Name != "asciiDigit" && fn.Name.Name != "asciiLetter" && fn.Name.Name != "validID" {
					continue
				}
				if name == "checker.go" && fn.Name.Name != "error" && fn.Name.Name != "resolve" {
					continue
				}
				if name == "scheduler.go" && (fn.Name.Name == "Compile" || fn.Name.Name == "CompileWithOptions" || fn.Name.Name == "compileSyntax") {
					continue
				}
				if name == "evaluator.go" && (fn.Name.Name == "eval" || fn.Name.Name == "localStatement") {
					templates[fn.Name.Name] = fn
					if fn.Name.Name == "eval" {
						b.WriteString("func(v *evaluator)eval(e *expression,env *environment,tail bool)(evalResult,error){if e==nil{return evalResult{},nil};if e.native==nil{return evalResult{},diagnostic(\"E-VALUE\",\"missing compiled expression\")};return e.native(v,env,tail)}\n")
					} else {
						b.WriteString("func(v *evaluator)localStatement(s *statement,env *environment)(string,error){if s.native==nil{return \"\",diagnostic(\"E-PURITY\",\"invalid compiled local statement\")};return s.native(v,env)}\n")
					}
					continue
				}
			}
			b.WriteString(printGo(d))
			b.WriteByte('\n')
		}
	}
	return b.String(), imports, templates, nil
}

// Reuse the reference evaluator's case body as a code-generation template.
// The generated function has a fixed operation, with no run-time tag dispatch.
func specialize(template *ast.FuncDecl, tag, name, reference string, expression bool) (string, error) {
	var prefix strings.Builder
	var chosen *ast.CaseClause
	for _, s := range template.Body.List {
		if sw, ok := s.(*ast.SwitchStmt); ok {
			for _, item := range sw.Body.List {
				c := item.(*ast.CaseClause)
				for _, v := range c.List {
					if lit, ok := v.(*ast.BasicLit); ok {
						label, _ := strconv.Unquote(lit.Value)
						if label == tag {
							chosen = c
						}
					}
				}
			}
			break
		}
		// Strip interpreter/native dispatch guards. Native functions retain steps
		// and diagnostic attachment from the reference implementation.
		if x, ok := s.(*ast.IfStmt); ok {
			condition := printGo(x.Cond)
			if strings.Contains(condition, "native") || condition == "e == nil" {
				continue
			}
		}
		// get is absent in simple literal/id cases; defer its declaration.
		if x, ok := s.(*ast.AssignStmt); ok && len(x.Lhs) > 0 {
			if id, ok := x.Lhs[0].(*ast.Ident); ok && id.Name == "get" {
				continue
			}
		}
		prefix.WriteString(printGo(s))
		prefix.WriteByte('\n')
	}
	if chosen == nil {
		return "", fmt.Errorf("unsupported Go target tag %q", tag)
	}
	usesGet := false
	ast.Inspect(chosen, func(n ast.Node) bool {
		if id, ok := n.(*ast.Ident); ok && id.Name == "get" {
			usesGet = true
		}
		return true
	})
	var b strings.Builder
	if expression {
		fmt.Fprintf(&b, "func %s(v *evaluator,env *environment,tail bool)(r evalResult,err error){e:=%s\n", name, reference)
	} else {
		fmt.Fprintf(&b, "func %s(v *evaluator,env *environment)(string,error){s:=%s\n", name, reference)
	}
	b.WriteString(prefix.String())
	if usesGet {
		b.WriteString("get:=func(x *expression)(Value,error){return v.result(x,env)}\n")
	}
	b.WriteString("switch { default:\n")
	for _, s := range chosen.Body {
		b.WriteString(printGo(s))
		b.WriteByte('\n')
	}
	b.WriteString("}\n")
	if expression {
		b.WriteString("return r,err\n}\n")
	} else {
		b.WriteString("return \"\",nil\n}\n")
	}
	return b.String(), nil
}

const generatedMain = `
func main(){if err:=generatedRun();err!=nil{fmt.Fprintln(os.Stderr,err);os.Exit(1)}}
func generatedRun()error{
 input:=flag.String("input","{}","Flow inputs as JSON")
 entryInput:=flag.String("entry-input","","EntryInput JSON with positional and named fields")
 entryOutput:=flag.Bool("entry-output",false,"print the value/void success envelope")
 dataEncoding:=flag.String("data-encoding","json","Data transport: json or c-hex (supports nan and inf)")
 parallel:=flag.Int("parallel",1,"active leaf evaluations; waiting containers yield")
 steps:=flag.Int64("steps",10000000,"step budget; 0 unlimited")
 elements:=flag.Int64("elements",1000000,"list budget; 0 unlimited")
 frames:=flag.Int("frames",DefaultMaxFrames,"non-tail call frame budget; 0 uses default; tail calls reuse frames")
 trace:=flag.Bool("trace",true,"show best-effort print/log/telemetry diagnostics on stderr")
 flag.Parse();if flag.NArg()!=0{return fmt.Errorf("compiled program accepts flags only")}
 if *frames<0{return fmt.Errorf("-frames must be nonnegative (0 uses the default)")}
 if *parallel<0{return fmt.Errorf("-parallel must be nonnegative (0 uses one)")}
 hasInput,hasEntryInput:=false,false;flag.Visit(func(f *flag.Flag){hasInput=hasInput||f.Name=="input";hasEntryInput=hasEntryInput||f.Name=="entry-input"});if hasInput&&hasEntryInput{return fmt.Errorf("choose -input or -entry-input")}
 inputJSON:=*entryInput;if !hasEntryInput{inputJSON="{\"positional\":[],\"named\":"+*input+"}"}
 data,err:=DecodeEntryInputWithEncoding([]byte(inputJSON),*dataEncoding);if err!=nil{return err}
 host,err:=generatedHost();if err!=nil{return err};p,err:=generatedProgram(host);if err!=nil{return err};defer p.host.Close()
 ctx,cancel:=signal.NotifyContext(context.Background(),os.Interrupt);defer cancel()
 var traces *TraceBuffer;if *trace{var stop func();traces,stop=StartTraceOutput(os.Stderr);defer stop()}
 r,err:=p.RunEntry(ctx,data,Options{Parallelism:*parallel,MaxSteps:*steps,MaxElements:*elements,MaxNodes:1000000,MaxFrames:*frames,Trace:traces});if err!=nil{return err}
 out,err:=EncodeData(r.Output,*dataEncoding);if *entryOutput{out,err=EncodeEntryOutput(*r.EntryOutput,*dataEncoding)};if err!=nil{return err};fmt.Println(string(out));return nil
}
`
