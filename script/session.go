package script

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// Session evaluates interactive cells with the ordinary checker and graph
// runtime. Each cell is a fresh run; only successful immutable values, functions
// and imports survive. Previous external calls are never evaluated again.
// Flow declarations persist; each invocation and each cell has fresh Claims.
type Session struct {
	mu             sync.Mutex
	host           Host
	baseHost       Host
	values         map[string]Value
	analysis       map[string]analysis
	declared       map[string]bool
	bindings       map[string]string
	imports        []importDecl
	flows          map[string]*flowDecl
	loweredImports []importDecl
	cells          uint64
	nativeOwner    string
	projectRoot    string
	sources        map[string]Source
	physical       map[string]os.FileInfo
}

type SessionOptions struct {
	// ProjectRoot resolves static LIP imports. Empty uses the current directory.
	ProjectRoot string
}

type CellResult struct {
	Value    Value
	HasValue bool
	Report   Report
	// Help requests can be displayed as plain documentation by interactive UIs.
	Help bool
}

func NewSession(host Host, options ...SessionOptions) (*Session, error) {
	if len(options) > 1 {
		return nil, diagnostic("E-SESSION", "expected at most one SessionOptions")
	}
	projectRoot := ""
	if len(options) == 1 {
		projectRoot = options[0].ProjectRoot
	}
	projectRoot, err := filepath.Abs(projectRoot)
	if err != nil {
		return nil, err
	}
	program, err := Compile("", host)
	if err != nil {
		return nil, err
	}
	owner, err := newNativeOwner()
	if err != nil {
		return nil, err
	}
	return &Session{host: program.host, baseHost: program.host, nativeOwner: owner, projectRoot: projectRoot, values: map[string]Value{}, declared: map[string]bool{}, bindings: map[string]string{}}, nil
}

// NewInteractiveSession enables the persistent, package-independent library bridge.
// Imports and completion do not execute native code; operations still use Host.
func NewInteractiveSession(host Host, options ...SessionOptions) (*Session, error) {
	return NewSession(NewNativeHost(host), options...)
}

func (s *Session) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.host.Close()
}

// Values returns a copy. Mutating it cannot change the session or its closures.
func (s *Session) Values() map[string]Value {
	s.mu.Lock()
	defer s.mu.Unlock()
	values := map[string]Value{}
	for name, symbol := range s.bindings {
		if value, ok := s.values[symbol]; ok {
			values[name] = value
		}
	}
	return values
}

// Complete returns names already available in this session; it never executes
// source or imports an unregistered native member to discover candidates.
func (s *Session) Complete(prefix string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	names := map[string]bool{}
	for _, name := range strings.Fields("flow fn return if else match for in parallel break continue with next effect perform import export as lip go python host true false null any bool number string list object function void claim claim.snapshot") {
		names[name] = true
	}
	for name := range builtinArity {
		names[name] = true
	}
	names["help"] = true
	if s.host.Native != nil {
		names["native_data"], names["native_release"] = true, true
	}
	for name := range s.bindings {
		names[name] = true
	}
	for _, im := range s.imports {
		alias := ""
		for name, symbol := range s.bindings {
			if symbol == im.alias {
				alias = name
				break
			}
		}
		if alias == "" {
			continue
		}
		im.alias = alias
		names[im.alias] = true
		names[im.alias+"."] = true
		if im.language == "go" && s.host.Native != nil {
			for _, member := range s.host.Native.completions(im.path) {
				names[im.alias+"."+member] = true
			}
		}
		if im.language == "lip" {
			if id, err := moduleTarget("$repl.lip", im.path); err == nil {
				if module, err := parse(s.sources[id].Text); err == nil {
					for name := range module.exports {
						names[im.alias+"."+name] = true
					}
				}
			}
		}
		for _, contract := range s.host.Contracts {
			op := contract.Operation
			if op.Language == im.language && op.Path == im.path {
				member := op.Member
				if contract.Version == "lip-native-1" {
					member = strings.TrimPrefix(strings.TrimPrefix(member, "perform.get."), "perform.prepare.")
					if member == "perform.get" {
						continue
					}
				}
				names[im.alias+"."+member] = true
			}
		}
	}
	var result []string
	for name := range names {
		if !strings.HasPrefix(name, "$") && strings.HasPrefix(name, prefix) {
			result = append(result, name)
		}
	}
	sort.Strings(result)
	return result
}

func copyValues(values map[string]Value) map[string]Value {
	out := make(map[string]Value, len(values))
	for name, value := range values {
		out[name] = value
	}
	return out
}

func (s *Session) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.host.Close()
	s.values = map[string]Value{}
	s.analysis = nil
	s.declared = map[string]bool{}
	s.bindings = map[string]string{}
	s.imports = nil
	s.flows = nil
	s.loweredImports = nil
	s.cells = 0
	s.sources = nil
	s.physical = nil
	s.host = s.baseHost
}

// Delete atomically unbinds visible names of any kind. Existing immutable
// captures and Flow references retain their original symbols; future cells
// may reuse the names. This does not close native objects or unload modules.
func (s *Session) Delete(names ...string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(names) == 0 {
		return diagnostic("E-SESSION", "usage: :del name[,name...]")
	}
	for _, name := range names {
		tokens, err := lex(name)
		if err != nil || len(tokens) != 2 || tokens[0].kind != "id" || tokens[0].text != name {
			return diagnostic("E-SESSION", "expected a binding name: "+name)
		}
		if s.bindings[name] == "" {
			return diagnostic("E-NAME", "unbound name "+name)
		}
	}
	for _, name := range names {
		delete(s.bindings, name)
	}
	s.pruneBindings()
	return nil
}

// Values captured by functions own their immutable environments. Only retained
// Flows additionally need globals in the next cell's run environment.
func (s *Session) pruneBindings() {
	keep := map[string]bool{}
	var visit func(string)
	visit = func(name string) {
		if keep[name] {
			return
		}
		keep[name] = true
		if f := s.flows[name]; f != nil {
			var expr func(*expression)
			expr = func(e *expression) {
				if e.tag == "id" || e.tag == "flow" {
					visit(e.name)
				}
				walkExpression(e, expr)
			}
			walkBlockExpressions(f.body, expr)
		}
	}
	for _, symbol := range s.bindings {
		visit(symbol)
	}
	for name := range s.values {
		if !keep[name] {
			delete(s.values, name)
			delete(s.analysis, name)
			delete(s.declared, name)
		}
	}
	for name := range s.flows {
		if !keep[name] {
			delete(s.flows, name)
			delete(s.declared, name)
		}
	}
}

// Give every committed public name an immutable symbol. Reuse the module
// renamer's lexical rules so parameters, local scopes, and pipeline arguments
// behave identically; only the session namespace permits explicit unbinding.
func (s *Session) bindCell(tree *syntax) (map[string]string, error) {
	bindings := map[string]string{}
	for name, symbol := range s.bindings {
		bindings[name] = symbol
	}
	claim := func(name string, tok token) error {
		if reserved(name) {
			return at(diagnostic("E-RESERVED", "reserved binding name "+name), tok)
		}
		if bindings[name] != "" {
			return at(diagnostic("E-DUPLICATE", "duplicate binding "+name), tok)
		}
		bindings[name] = fmt.Sprintf("$cell%d_%s", s.cells+1, name)
		return nil
	}
	for _, im := range tree.imports {
		if err := claim(im.alias, im.tok); err != nil {
			return nil, err
		}
	}
	for name, f := range tree.functions {
		if err := claim(name, f.tok); err != nil {
			return nil, err
		}
	}
	for name, f := range tree.flows {
		if err := claim(name, f.tok); err != nil {
			return nil, err
		}
	}
	for name, d := range tree.data {
		if err := claim(name, d.tok); err != nil {
			return nil, err
		}
	}
	names := map[string]string{}
	for name, symbol := range bindings {
		names[name] = symbol
	}
	rename := moduleRenamer{names: names, native: map[string]string{}}
	for i := range tree.imports {
		im := &tree.imports[i]
		rename.native[im.alias] = bindings[im.alias]
		im.alias = bindings[im.alias]
	}
	// Existing aliases also participate in the ordinary lexical import rules.
	for _, im := range s.imports {
		for name, symbol := range s.bindings {
			if symbol == im.alias {
				rename.native[name] = symbol
			}
		}
	}
	functions := map[string]*expression{}
	for name, f := range tree.functions {
		rename.expr(f, map[string]bool{})
		functions[bindings[name]] = f
	}
	flows := map[string]*flowDecl{}
	for name, f := range tree.flows {
		local := map[string]bool{}
		for _, param := range f.params {
			rename.bind(local, param.name, param.tok)
		}
		rename.block(f.body, local)
		f.name = bindings[name]
		flows[f.name] = f
	}
	data := map[string]dataDecl{}
	for name, d := range tree.data {
		data[bindings[name]] = d
	}
	exports := map[string]bool{}
	for name := range tree.exports {
		exports[bindings[name]] = true
	}
	// Keep the same live-Claim shadowing rule before public names are lowered.
	// Other Flow declarations own separate stores and were renamed above.
	rename.stores = map[string]bool{}
	for _, stmt := range tree.body.items {
		if stmt.tag == "claim" {
			rename.stores[stmt.name] = true
		}
	}
	for _, stmt := range tree.body.items {
		rename.stmt(stmt, map[string]bool{})
		if stmt.tag == "bind" || stmt.tag == "claim" {
			name := stmt.name
			if err := claim(name, stmt.tok); err != nil {
				return nil, err
			}
			stmt.name, rename.names[name] = bindings[name], bindings[name]
		}
	}
	if rename.err != nil {
		return nil, rename.err
	}
	tree.functions, tree.flows, tree.data, tree.exports = functions, flows, data, exports
	return bindings, nil
}

// Eval commits bindings only if the entire cell succeeds. External actions
// already performed by a failed cell cannot be rolled back. Identity, journal
// and After options are for Program.Run; interactive cells generate fresh IDs.
func (s *Session) Eval(ctx context.Context, source string, opts Options) (result CellResult, err error) {
	phase := "static"
	defer func() { err = diagnosticPhase(err, phase) }()
	s.mu.Lock()
	defer s.mu.Unlock()
	if opts.RunID != "" || opts.Journal != nil || len(opts.After) != 0 {
		return CellResult{}, diagnostic("E-SESSION", "cells require fresh run identities; use Program.Run for journal or After")
	}
	tree, err := parseCell(source)
	if err != nil {
		return CellResult{}, err
	}
	bindings, err := s.bindCell(tree)
	if err != nil {
		return CellResult{}, err
	}

	tree.imports = append(append([]importDecl(nil), s.imports...), tree.imports...)
	imports := append([]importDecl(nil), tree.imports...)
	rootFlows := map[string]bool{}
	for name := range tree.flows {
		rootFlows[name] = true
	}
	physical := map[string]os.FileInfo{}
	for id, info := range s.physical {
		physical[id] = info
	}
	set := SourceSet{Root: fmt.Sprintf("$repl-%d.lip", s.cells+1), Sources: map[string]Source{}}
	for id, source := range s.sources {
		set.Sources[id] = source
	}
	set.Sources[set.Root] = Source{Version: "local", Text: tree.source}
	for _, im := range imports {
		if im.language == "lip" {
			target, err := moduleTarget(set.Root, im.path)
			if err != nil {
				return CellResult{}, err
			}
			if _, frozen := s.sources[target]; frozen {
				continue
			}
			set, err = loadSourceSet(filepath.Join(s.projectRoot, set.Root), s.projectRoot, tree, s.sources, physical)
			if err != nil {
				return CellResult{}, err
			}
			break
		}
	}
	bound := map[string]bool{}
	for name := range s.values {
		bound[name] = true
	}
	for name := range s.flows {
		bound[name] = true
	}
	tree, build, err := linkSourceSyntax(set, "", s.host.Native != nil, tree, bound)
	if err != nil {
		return CellResult{}, err
	}

	tree.imports = append(tree.imports, s.loweredImports...)
	importCount := len(tree.imports)
	host, help, err := s.prepareCell(ctx, tree, bindings)
	if err != nil {
		return CellResult{}, err
	}
	nativeValues := copyValues(s.values)
	for name, f := range s.flows {
		// Retained Flow definitions are not runtime values. A private shape-only
		// carrier lets lowering use their frozen returns before graph cloning.
		nativeValues[name] = Value{k: Function, f: &function{nativeResult: f.nativeResult, nativeParams: f.nativeParams}}
	}
	initial := map[string]bindingInfo{}
	for name, value := range s.values {
		info := bindingInfo{info: pureInfo(value.k.String()), static: s.declared[name]}
		if prior, ok := s.analysis[name]; ok {
			info.info = prior
		}
		if value.k == Function {
			info.builtin = value.f.builtin
			if info.builtin == "" {
				info.fn = &expression{params: value.f.params, typ: value.f.typ}
			}
		}
		initial[name] = info
	}
	tree.initial = initial
	host, err = lowerNative(ctx, tree, host, nativeValues, s.cellNames())
	if err != nil {
		return CellResult{}, err
	}
	// Each run owns its tree metadata. Clone retained definitions before
	// checking so failures and concurrent runs cannot mutate saved closures.
	nextSite := 0
	mapSyntaxTokens(tree, func(t *token) {
		if t.site >= nextSite {
			nextSite = t.site + 1
		}
	})
	for _, name := range sortedFlowNames(s.flows) {
		f := cloneSessionFlow(s.flows[name])
		sites := map[int]int{}
		view := &syntax{flows: map[string]*flowDecl{name: f}}
		mapSyntaxTokens(view, func(t *token) {
			if t.site >= 0 {
				sites[t.site] = 0
			}
		})
		ordered := make([]int, 0, len(sites))
		for site := range sites {
			ordered = append(ordered, site)
		}
		sort.Ints(ordered)
		for _, site := range ordered {
			sites[site], nextSite = nextSite, nextSite+1
		}
		mapSyntaxTokens(view, func(t *token) {
			if t.site >= 0 {
				t.site = sites[t.site]
			}
		})
		tree.flows[name] = f
		rootFlows[name] = true
	}
	for _, name := range sortedFlowNames(tree.flows) {
		if _, err := checkFlow(tree, host, name); err != nil {
			return CellResult{}, err
		}
	}
	check, err := analyzeBindings(tree, host, initial)
	if err != nil {
		return CellResult{}, err
	}
	program := &Program{syntax: tree, check: check, host: host, build: build}
	saved := copyValues(s.values)
	phase = "runtime"
	if ctx != nil {
		ctx = context.WithValue(ctx, nativeSessionKey{}, s.nativeOwner)
	}
	report, err := program.run(ctx, nil, opts, saved)
	if err != nil {
		return CellResult{Report: report}, err
	}
	s.values, s.imports, s.host = saved, imports, host
	if s.analysis == nil {
		s.analysis = map[string]analysis{}
	}
	for name, fn := range tree.functions {
		s.analysis[name] = fn.info
	}
	for _, stmt := range tree.body.items {
		if stmt.tag == "bind" {
			info := stmt.expr.info
			// A saved binding is a fixed value. Keep its normative proposal Ops,
			// including Unknown, without repeating its evaluation effect.
			info.effect, info.concurrent, info.hasFlow = PureEffect, true, false
			s.analysis[stmt.name] = info
		}
	}
	s.bindings = bindings
	s.physical = physical
	s.cells++
	s.loweredImports = append(s.loweredImports, tree.imports[importCount:]...)
	s.flows = map[string]*flowDecl{}
	for name := range rootFlows {
		s.flows[name] = tree.flows[name]
	}
	delete(set.Sources, set.Root)
	s.sources = set.Sources
	for name := range tree.functions {
		s.declared[name] = true
	}
	for name := range tree.flows {
		s.declared[name] = true
	}
	for name := range tree.data {
		s.declared[name] = true
	}
	hasValue := false
	if n := len(tree.body.items); n > 0 {
		last := tree.body.items[n-1]
		hasValue = last.tag == "eval" || last.tag == "bind" || last.tag == "return" && last.expr != nil
		if last.tag == "eval" && last.expr.info.typ == "void" {
			hasValue = false
		}
	}
	return CellResult{Value: report.Output, HasValue: hasValue, Report: report, Help: help}, nil
}

// Include identifiers inside retained lowered Flows when choosing temporary
// names. A later bridge import must not capture an earlier local temporary.
func (s *Session) cellNames() map[string]bool {
	names := map[string]bool{}
	for name := range s.values {
		names[name] = true
	}
	add := func(name string) {
		if name != "" {
			names[name] = true
		}
	}
	view := &syntax{flows: s.flows}
	mapSyntaxTokens(view, func(t *token) {
		if t.kind == "id" {
			add(t.text)
		}
	})
	for name, f := range s.flows {
		add(name)
		var visit func(*expression)
		visit = func(e *expression) {
			if e.tag == "id" {
				add(e.name)
			}
			walkExpression(e, visit)
		}
		walkBlockExpressions(f.body, visit)
	}
	return names
}

// Copy only mutable syntax. Values, native helpers and captured environments
// are immutable and shared; checker caches belong to the new cell.
func cloneSessionFlow(f *flowDecl) *flowDecl {
	var expr func(*expression) *expression
	var stmt func(*statement) *statement
	var body func(*block) *block
	var arms func([]arm) []arm
	arms = func(xs []arm) []arm {
		out := append([]arm(nil), xs...)
		for i := range out {
			out[i].patterns = append([]pattern(nil), xs[i].patterns...)
			out[i].guard, out[i].result, out[i].body = expr(xs[i].guard), expr(xs[i].result), body(xs[i].body)
		}
		return out
	}
	expr = func(e *expression) *expression {
		if e == nil {
			return nil
		}
		out := *e
		out.x, out.y, out.z, out.body = expr(e.x), expr(e.y), expr(e.z), body(e.body)
		out.params = append([]parameter(nil), e.params...)
		out.args = append([]argument(nil), e.args...)
		for i := range out.args {
			out.args[i].expr = expr(e.args[i].expr)
		}
		out.elems = make([]*expression, len(e.elems))
		for i := range out.elems {
			out.elems[i] = expr(e.elems[i])
		}
		out.fields = append([]field(nil), e.fields...)
		for i := range out.fields {
			out.fields[i].expr = expr(e.fields[i].expr)
		}
		out.arms, out.info = arms(e.arms), analysis{}
		return &out
	}
	stmt = func(s *statement) *statement {
		if s == nil {
			return nil
		}
		out := *s
		out.expr, out.body, out.otherwise, out.other = expr(s.expr), body(s.body), body(s.otherwise), stmt(s.other)
		out.arms = arms(s.arms)
		return &out
	}
	body = func(b *block) *block {
		if b == nil {
			return nil
		}
		out := *b
		out.items = make([]*statement, len(b.items))
		for i := range out.items {
			out.items[i] = stmt(b.items[i])
		}
		out.tail, out.exit = expr(b.tail), stmt(b.exit)
		return &out
	}
	out := *f
	out.params, out.body = append([]parameter(nil), f.params...), body(f.body)
	out.summary, out.checking, out.check = analysis{}, false, nil
	return &out
}

const sessionHelp = `LIP 1.0 interactive help
Enter LIP expressions, statements, fn/Flow declarations, static Data, exports, and imports.
Lists support a[-1], a[[2,0,-1]], and a[start:stop:step]; a[::-1] reverses a list.
Strings support scalar indices and slices by Unicode scalar; slice bounds clip and step cannot be zero.
Named fn/Flow and pure core functions support |> pipelines. Declare a Flow, then call it by name or through |>.
Flow arguments/results may carry functions, effects, and nested containers; Flow itself is not a value.
fn may use print/log/telemetry for one-way diagnostics. They return null; delivery never changes computation.
CLI diagnostics use stderr; -trace=false disables delivery, including inherited :run, without skipping argument checks.
Arguments run in source order and may contain calls. Entry JSON, Host calls, and Claims still require Data.
Claims collect facts with provenance. Complete reads wait for all contributions; active stores last one cell or Flow invocation.
Use :begin / :end for a whole script, forward declarations, or Claim declaration/join/read in one cell.
Non-tail calls default to 10,000 active frames per evaluation; CLI -frames selects the budget. Tail calls reuse frames. Exhaustion reports E-BUDGET without committing bindings; later cells can run.
CLI -parallel accepts any positive integer and limits active evaluations; :run inherits it unless overridden. Work starts on demand, subject to dependencies and registered provider/resource contracts.
LIP imports resolve under the session project root (CLI -project DIR, default: current directory); successful imports stay frozen until :reset.
Use :run [-entry Name] [-input JSON | -entry-input JSON] [-entry-output] [-project DIR] file.lip for independent file execution.
Quote paths containing spaces or JSON with single/double quotes. :run uses independent file bindings and never invokes a shell.
help(value) describes values, pure functions, core functions, or proposals.
help(module) / help(module.member) shows Go export signatures or Python library documentation.
The CLI enables the generic Go/Python bridge; packages must exist in the current Go module or Python environment.
Use full, case-sensitive Go package paths; configure dependencies with go get in your module, and choose an alias with as.
import python "math"; math.sqrt(3) calls a Python library.
import go "strings"; strings.ToUpper("hello") calls an exported Go function.
native_data(value) takes a Data snapshot; native_release(value) releases a native reference.
Direct library calls use the Host boundary; effect constructs a request, perform executes it.
Known pure request builders preserve operation summaries across calls, aliases and cells. Parallel still requires all operations Concurrent ReadOnly; projections, dynamic calls and carry stay Unknown.
The REPL displays expression and binding results. fmt.Print/Printf/Println return the byte count after the adapter handles error; this count is also displayed.
:vars shows saved values; :history shows input; :del name[,name...] unbinds names for reuse; :reset clears the session; :cancel discards a draft; :quit exits.`

var sessionBuiltinHelp = map[string]string{
	"print":        "print(value) -> null\nEmit a bounded diagnostic display without computational feedback; allowed in fn. CLI output goes to stderr.",
	"log":          "log(level:string, value) -> null\nEmit a labelled diagnostic; delivery failure does not change computation. Allowed in fn.",
	"telemetry":    "telemetry(name:string, fields:object) -> null\nEmit a one-way diagnostic event; no acknowledgement or exactly-once guarantee. Allowed in fn.",
	"len":          "len(value) -> number\nReturn the Unicode scalar count, list length, or object field count.",
	"str":          "str(value) -> string\nReturn strings unchanged; encode other Data as canonical JSON without whitespace.",
	"range":        "range(stop) / range(start, stop) / range(start, stop, step) -> list\nGenerate integers excluding stop; step must not be 0.",
	"map":          "map(xs, f) -> list\nCall pure unary f in order and return the results.",
	"fold":         "fold(xs, seed, f) -> any\nCall pure binary f(acc, item) in order; an empty list returns seed.",
	"fail":         "fail(message:string)\nEnd evaluation with E-FAIL.",
	"concat":       "concat(a:list, b:list) -> list\nReturn a new list concatenating both inputs.",
	"slice":        "slice(value, start:number?, stop:number? [, step:number?])\nSlice a list or string, excluding stop, just like value[start:stop:step]. Negative bounds are relative to length and clip; null selects defaults. Step defaults to 1 and cannot be zero; slice(value,null,null,-1) reverses.",
	"keys":         "keys(value:object) -> list\nReturn string keys sorted by UTF-8 bytes.",
	"has":          "has(value:object, key:string) -> bool\nTest whether the object has this key.",
	"put":          "put(value:object, key:string, item:any) -> object\nReturn a new object with the field added or replaced.",
	"split":        "split(value:string, separator:string) -> list\nSplit a string on a nonempty separator.",
	"parse_number": "parse_number(value:string) -> number\nParse the complete number text as a finite binary64 value.",
}

// Interactive conveniences lower into ordinary proposals, perform, blocks and
// pure functions before checking. The registry belongs to this cell until it
// succeeds, so a failed cell cannot publish imports or newly discovered calls.
func (s *Session) prepareCell(ctx context.Context, syntax *syntax, bindings map[string]string) (Host, bool, error) {
	publicName := func(symbol string) string {
		for name, bound := range bindings {
			if bound == symbol {
				return name
			}
		}
		return symbol
	}
	host := s.host
	host.Contracts = append([]Contract(nil), host.Contracts...)
	contracts := map[string]Contract{}
	for _, c := range host.Contracts {
		contracts[identityKey(c.Operation.Wire())] = c
	}
	aliases := map[string]importDecl{}
	names := map[string]bool{"claim": true}
	for name := range s.cellNames() {
		names[name] = true
	}
	shadowHelp := s.flows["help"] != nil
	for _, im := range syntax.imports {
		aliases[im.alias] = im
		names[im.alias] = true
		shadowHelp = shadowHelp || im.alias == "help"
	}
	for name := range s.values {
		names[name] = true
		shadowHelp = shadowHelp || name == "help"
	}
	for name := range syntax.functions {
		names[name] = true
		shadowHelp = shadowHelp || name == "help"
	}
	for name := range syntax.flows {
		names[name] = true
		shadowHelp = shadowHelp || name == "help"
	}
	for name := range syntax.data {
		names[name] = true
		shadowHelp = shadowHelp || name == "help"
	}
	ts, _ := lex(syntax.source)
	for _, t := range ts {
		if t.kind == "id" {
			names[t.text] = true
		}
	}
	fresh := func() string {
		for i := 0; ; i++ {
			name := fmt.Sprintf("__lip_cell_%d", i)
			if !names[name] {
				names[name] = true
				return name
			}
		}
	}
	add := func(c Contract) {
		contracts[identityKey(c.Operation.Wire())] = c
		host.Contracts = append(host.Contracts, c)
	}
	var err error
	documentation := map[*expression]bool{}
	var lower func(*expression, bool)
	var lowerBlock func(*block, bool)
	var lowerStatement func(*statement, bool)
	lowerBlock = func(b *block, shadow bool) {
		if b == nil {
			return
		}
		for _, stmt := range b.items {
			lowerStatement(stmt, shadow)
			if stmt.tag == "bind" && stmt.name == "help" {
				shadow = true
			}
		}
		lower(b.tail, shadow)
		if b.exit != nil {
			lowerStatement(b.exit, shadow)
		}
	}
	lowerStatement = func(stmt *statement, shadow bool) {
		lower(stmt.expr, shadow)
		lowerBlock(stmt.body, shadow || stmt.tag == "for" && stmt.name == "help")
		lowerBlock(stmt.otherwise, shadow)
		if stmt.other != nil {
			lowerStatement(stmt.other, shadow)
		}
		for _, arm := range stmt.arms {
			lower(arm.guard, shadow)
			lowerBlock(arm.body, shadow)
		}
	}
	lower = func(e *expression, shadow bool) {
		if e == nil || err != nil {
			return
		}
		if !shadow && e.tag == "call" && e.x.tag == "id" && e.x.name == "help" {
			documentation[e] = true
			if len(e.args) > 1 || len(e.args) == 1 && e.args[0].name != "" {
				err = at(diagnostic("E-ARITY", "help accepts zero or one positional argument"), e.tok)
				return
			}
			if len(e.args) == 0 {
				*e = expression{tag: "lit", tok: e.tok, value: textValue(sessionHelp)}
				return
			}
			target, ok := memberName(e.args[0].expr)
			parts := strings.SplitN(target, ".", 2)
			im, imported := aliases[parts[0]]
			pythonTarget := ok && imported && im.language == "python"
			stringTarget := e.args[0].expr.tag == "lit" && e.args[0].expr.value.k == String
			if host.Native != nil && (pythonTarget || stringTarget) {
				if imported {
					target = im.path
					if len(parts) == 2 {
						target += "." + parts[1]
					}
				} else {
					target = e.args[0].expr.value.s
				}
				alias := fresh()
				syntax.imports = append(syntax.imports, importDecl{language: "host", path: "lip.repl", alias: alias, tok: e.tok})
				op := Operation{"host", "lip.repl", "help"}
				if _, exists := contracts[identityKey(op.Wire())]; !exists {
					c := Contract{Operation: op, Version: "1", Params: []Parameter{{Name: "target", Schema: Schema{Type: "string"}}}, Result: Schema{Type: "string"}, Effect: UnknownEffect}
					c.Invoke = func(ctx context.Context, r Request) Response {
						target, _ := r.Positional[0].Text()
						module, member := target, ""
						if i := strings.LastIndex(target, "."); i >= 0 {
							module, member = target[:i], target[i+1:]
						}
						req := r
						req.Positional = []Value{ListValue(), object(nil)}
						return host.Native.invoke(ctx, Operation{"python", module, member}, "help", req)
					}
					add(c)
				}
				*e = expression{tag: "perform", tok: e.tok, x: &expression{tag: "proposal", tok: e.tok, name: alias + ".help", args: []argument{{expr: &expression{tag: "lit", tok: e.tok, value: textValue(target)}}}}}
				return
			}
			if ok && imported && im.language == "go" && host.Native != nil {
				pkg, failure := host.Native.discover(ctx, im.path)
				if failure != nil {
					err = at(failure, e.tok)
					return
				}
				lines := []string{fmt.Sprintf("import go %q as %s", im.path, publicName(im.alias))}
				for member, signature := range pkg.members {
					if len(parts) == 1 || parts[1] == member {
						lines = append(lines, signature)
					}
				}
				if len(parts) == 2 && len(lines) == 1 {
					err = at(diagnostic("E-OPERATION", "no exported Go symbol "+target), e.tok)
					return
				}
				sort.Strings(lines[1:])
				*e = expression{tag: "lit", tok: e.tok, value: textValue(strings.Join(lines, "\n"))}
				return
			}
			if ok && imported {
				var lines []string
				lines = append(lines, fmt.Sprintf("import %s %q as %s", im.language, im.path, publicName(im.alias)))
				for _, c := range host.Contracts {
					if c.Operation.Language == im.language && c.Operation.Path == im.path && (len(parts) == 1 || c.Operation.Member == parts[1]) {
						lines = append(lines, contractHelp(publicName(im.alias), c))
					}
				}
				if len(parts) == 2 && len(lines) == 1 {
					err = at(diagnostic("E-OPERATION", "unregistered operation "+target), e.args[0].expr.tok)
					return
				}
				sort.Strings(lines[1:])
				*e = expression{tag: "lit", tok: e.tok, value: textValue(strings.Join(lines, "\n"))}
				return
			}
		}
		if !shadow && e.tag == "id" && e.name == "help" {
			*e = *sessionHelpFunction(e.tok, fresh())
			return
		}
		name, direct := "", false
		if e.tag == "call" {
			name, direct = memberName(e.x)
			parts := strings.SplitN(name, ".", 2)
			_, imported := aliases[parts[0]]
			direct = direct && imported && len(parts) == 2
			if direct && host.Native != nil {
				im := aliases[parts[0]]
				c, registered := contracts[identityKey(Operation{im.language, im.path, parts[1]}.Wire())]
				if (im.language == "go" || im.language == "python") && (!registered || c.Version == "lip-native-1") {
					direct = false
				}
			}
		}
		if e.tag == "fn" {
			for _, p := range e.params {
				shadow = shadow || p.name == "help"
			}
		}
		lower(e.x, shadow || e.tag == "comprehension" && e.name == "help")
		lower(e.y, shadow)
		lower(e.z, shadow || e.tag == "comprehension" && e.name == "help")
		for _, arg := range e.args {
			lower(arg.expr, shadow)
		}
		for _, elem := range e.elems {
			lower(elem, shadow)
		}
		for _, field := range e.fields {
			lower(field.expr, shadow)
		}
		lowerBlock(e.body, shadow || e.tag == "carry" && (e.name == "help" || e.state == "help"))
		for _, arm := range e.arms {
			lower(arm.guard, shadow)
			lower(arm.result, shadow)
			lowerBlock(arm.body, shadow)
		}
		if e.tag != "proposal" && !direct {
			return
		}
		if !direct {
			name = e.name
		}
		parts := strings.SplitN(name, ".", 2)
		im, imported := aliases[parts[0]]
		if !imported || len(parts) != 2 {
			return
		}
		_ = im
		proposal := e
		var body *block
		if direct {
			proposal = &expression{tag: "proposal", tok: e.x.tok, name: name, args: append([]argument(nil), e.args...)}
			body = &block{tok: e.tok}
			// Fix arguments in source order before constructing a pure proposal.
			for i, arg := range proposal.args {
				local := fresh()
				body.items = append(body.items, &statement{tag: "bind", tok: arg.expr.tok, name: local, expr: arg.expr})
				proposal.args[i].expr = &expression{tag: "id", tok: arg.expr.tok, name: local}
			}
		}
		if direct {
			body.tail = &expression{tag: "perform", tok: proposal.tok, x: proposal}
			*e = expression{tag: "block", tok: e.tok, body: body}
		}
	}
	for _, name := range sortedFunctionNames(syntax.functions) {
		lower(syntax.functions[name], shadowHelp)
	}
	for _, name := range sortedFlowNames(syntax.flows) {
		f := syntax.flows[name]
		shadow := shadowHelp
		for _, p := range f.params {
			shadow = shadow || p.name == "help"
		}
		lowerBlock(f.body, shadow)
	}
	lowerBlock(syntax.body, shadowHelp)
	help := false
	if n := len(syntax.body.items); n > 0 {
		last := syntax.body.items[n-1]
		help = last.tag == "eval" && documentation[last.expr]
	}
	return host, help, err
}

func memberName(e *expression) (string, bool) {
	if e.tag == "id" {
		return e.name, true
	}
	if e.tag == "field" {
		if parent, ok := memberName(e.x); ok {
			return parent + "." + e.name, true
		}
	}
	return "", false
}

func contractHelp(alias string, c Contract) string {
	var params []string
	for _, p := range c.Params {
		name := p.Name + ":" + p.Schema.Type
		if p.Optional {
			name += " (optional)"
		}
		params = append(params, name)
	}
	return fmt.Sprintf("%s.%s(%s) -> %s", alias, c.Operation.Member, strings.Join(params, ", "), c.Result.Type)
}

// This tooling function only inspects immutable LIP values. External module
// documentation is a separate, ordinary Host operation.
func sessionHelpFunction(t token, param string) *expression {
	tail := &expression{tag: "lit", tok: t, value: textValue("")}
	tail.native = func(v *evaluator, env *environment, _ bool) (evalResult, error) {
		if err := v.step(t); err != nil {
			return evalResult{}, err
		}
		value, err := env.lookup(param)
		if err != nil {
			return evalResult{}, err
		}
		text := value.k.String()
		if value.k == Function {
			if value.f.builtin != "" {
				text = fmt.Sprintf("%s: pure core function; accepted argument counts %v\n%s", value.f.builtin, builtinArity[value.f.builtin], sessionBuiltinHelp[value.f.builtin])
			} else {
				var params []string
				for _, p := range value.f.params {
					params = append(params, p.name+":"+p.typ)
				}
				text = fmt.Sprintf("fn(%s) -> %s\nPure function; captures immutable values.", strings.Join(params, ", "), value.f.typ)
			}
		} else if value.k == Effect {
			text = fmt.Sprintf("effect %s %q %s\nExternal request; not yet performed. Use perform to execute it.", value.p.contract.Operation.Language, value.p.contract.Operation.Path, value.p.contract.Operation.Member)
		} else {
			descriptions := map[Kind]string{Null: "Null value.", Bool: "Boolean true / false.", Number: "Finite binary64 number.", String: "Immutable UTF-8 string; scalar indices and slices count Unicode scalars: s[-1], s[::-1]; use len / split / slice.", List: "Immutable list; use a[-1], a[[2,0,-1]], a[start:stop:step], or a[::-1]. Index lists preserve order and duplicates; slice bounds clip; step cannot be zero. Use len / map / fold / slice.", Object: "Immutable object with string keys; read with .field or [key]; use keys / has / put."}
			text += "\n" + descriptions[value.k]
		}
		return evalResult{value: textValue(text)}, nil
	}
	return &expression{tag: "fn", tok: t, params: []parameter{{name: param, typ: "any", tok: t}}, typ: "string", body: &block{tok: t, tail: tail}}
}

// CellIncomplete asks the real parser after checking delimiter balance. It
// distinguishes an unfinished cell from a malformed one. Triple-quoted strings
// can span physical lines; ordinary strings still cannot.
func CellIncomplete(source string) bool {
	ts, err := lex(source)
	if err != nil {
		return openMultilineString(err)
	}
	stack := []string{}
	for _, t := range ts {
		switch t.kind {
		case "(", "[", "{":
			stack = append(stack, t.kind)
		case ")", "]", "}":
			if len(stack) == 0 || stack[len(stack)-1] != map[string]string{")": "(", "]": "[", "}": "{"}[t.kind] {
				return false
			}
			stack = stack[:len(stack)-1]
		}
	}
	if len(stack) != 0 || strings.HasSuffix(strings.TrimRight(source, "\r\n"), "\\") {
		return true
	}
	_, err = parseCell(strings.TrimRight(source, "\r\n"))
	d, ok := err.(*Diagnostic)
	return ok && d.Code == "E-PARSE" && d.Site == -1
}

// CellAwaitingElse identifies a complete statement that may still acquire an
// else on the next physical line. A blank line or another statement submits it.
func CellAwaitingElse(source string) bool {
	tree, err := parseCell(source)
	if err != nil || len(tree.body.items) == 0 {
		return false
	}
	stmt := tree.body.items[len(tree.body.items)-1]
	for stmt.tag == "if" {
		if stmt.other != nil {
			stmt = stmt.other
			continue
		}
		return stmt.otherwise == nil
	}
	return false
}

// CellStartsElse ignores whitespace and comments using the language lexer.
func CellStartsElse(source string) bool {
	ts, _ := lex(source)
	for _, t := range ts {
		if t.kind != "\n" {
			return t.kind == "else"
		}
	}
	return false
}

func openMultilineString(err error) bool {
	d, ok := err.(*Diagnostic)
	return ok && d.Code == "E-LEX" && d.Message == "unterminated multiline string"
}

// CellInMultilineString reports an unfinished, otherwise lexically valid string.
// Interactive commands on its subsequent lines are string content.
func CellInMultilineString(source string) bool {
	_, err := lex(source)
	return openMultilineString(err)
}

// Cells combine the file declaration/statement grammar with bare expressions.
// Bodies, proposals, blocks and control structures use the core parser.
func parseCell(source string) (*syntax, error) {
	// Match the file compiler's source normalization, including literal text.
	source = strings.ReplaceAll(source, "\r\n", "\n")
	ts, err := lex(source)
	if err != nil {
		return nil, err
	}
	p := &parser{ts: ts}
	s := &syntax{source: source, flowName: "$cell", result: "void", cell: true,
		functions: map[string]*expression{}, flows: map[string]*flowDecl{},
		data: map[string]dataDecl{}, exports: map[string]bool{}, body: &block{}}
	p.padding()
	for p.peek().kind == "import" && p.err == nil {
		s.imports = append(s.imports, p.importDeclaration())
		p.declarationEnd()
	}
	names := map[string]bool{}
	for p.peek().kind != "eof" && p.err == nil {
		if p.peek().kind == "import" {
			s.imports = append(s.imports, p.importDeclaration())
			p.declarationEnd()
			continue
		}
		if p.declaration(s, names) {
			continue
		}
		t := p.peek()
		var stmt *statement
		binding := t.kind == "id" && p.i+1 < len(ts) && (ts[p.i+1].kind == "=" || ts[p.i+1].kind == "<+")
		if binding || t.kind == "return" || t.kind == "break" || t.kind == "continue" {
			stmt = p.stmt()
		} else if t.kind == "if" || t.kind == "match" || t.kind == "for" {
			// Prefer the script's statement interpretation when it is valid.
			// In particular, void child calls need statement context. Literal
			// and other value tails fall back to the expression grammar.
			before := p.i
			stmt = p.stmt()
			if p.err != nil {
				p.i, p.err = before, nil
				stmt = &statement{tag: "eval", tok: t, expr: p.expr(0)}
			}
		} else {
			stmt = &statement{tag: "eval", tok: t, expr: p.expr(0)}
		}
		if stmt.tag == "return" && stmt.expr != nil {
			s.result = "any"
		}
		s.body.items = append(s.body.items, stmt)
		if p.peek().kind != "eof" {
			p.separator()
		}
	}
	if p.err != nil {
		return nil, p.err
	}
	return s, nil
}
