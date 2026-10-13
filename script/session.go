package script

import (
	"context"
	"sort"
	"strings"
	"sync"
)

// Session evaluates interactive cells with the ordinary checker and graph
// runtime. Each cell is a fresh run; only successful immutable values, functions
// and imports survive. Previous external calls are never evaluated again.
// Full Flow/Claim programs use Compile and Run, with an explicit run lifetime.
type Session struct {
	mu      sync.Mutex
	host    Host
	values  map[string]Value
	imports []importDecl
}

type CellResult struct {
	Value    Value
	HasValue bool
	Report   Report
}

func NewSession(host Host) (*Session, error) {
	program, err := Compile("", host)
	if err != nil {
		return nil, err
	}
	return &Session{host: program.host, values: map[string]Value{}}, nil
}

// Values returns a copy. Mutating it cannot change the session or its closures.
func (s *Session) Values() map[string]Value {
	s.mu.Lock()
	defer s.mu.Unlock()
	return copyValues(s.values)
}

// Complete returns names already available in this session; it never executes
// source or imports an unregistered native member to discover candidates.
func (s *Session) Complete(prefix string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	names := map[string]bool{}
	for _, name := range strings.Fields("flow fn return if else match for in parallel break continue with next effect perform import as true false null any bool number string list object function void claim claim.snapshot") {
		names[name] = true
	}
	for name := range builtinArity {
		names[name] = true
	}
	for name := range s.values {
		names[name] = true
	}
	for _, im := range s.imports {
		names[im.alias+"."] = true
		for _, contract := range s.host.Contracts {
			op := contract.Operation
			if op.Language == im.language && op.Path == im.path {
				names[im.alias+"."+op.Member] = true
			}
		}
	}
	var result []string
	for name := range names {
		if strings.HasPrefix(name, prefix) {
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
	s.values = map[string]Value{}
	s.imports = nil
}

// Eval commits bindings only if the entire cell succeeds. External actions
// already performed by a failed cell cannot be rolled back. Identity, journal
// and After options are for Program.Run; interactive cells generate fresh IDs.
func (s *Session) Eval(ctx context.Context, source string, opts Options) (CellResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if opts.RunID != "" || opts.Journal != nil || len(opts.After) != 0 {
		return CellResult{}, diagnostic("E-SESSION", "cells require fresh run identities; use Program.Run for journal or After")
	}
	syntax, err := parseCell(source)
	if err != nil {
		return CellResult{}, err
	}
	newImports := syntax.imports
	syntax.imports = append(append([]importDecl(nil), s.imports...), newImports...)
	initial := map[string]bindingInfo{}
	for name, value := range s.values {
		info := bindingInfo{info: pureInfo(value.k.String())}
		if value.k == Function {
			info.builtin = value.f.builtin
			if info.builtin == "" {
				info.fn = &expression{params: value.f.params, typ: value.f.typ}
			}
		}
		if value.k == Effect {
			info.info.ops = map[string]bool{identityKey(value.p.contract.Operation.Wire()): true}
		}
		initial[name] = info
	}
	check, err := analyzeBindings(syntax, s.host, initial)
	if err != nil {
		return CellResult{}, err
	}
	if len(check.stores) != 0 {
		return CellResult{}, diagnostic("E-SESSION", "Claim needs a complete Flow; use :run file.lip")
	}
	program := &Program{syntax, check, s.host}
	saved := copyValues(s.values)
	report, err := program.run(ctx, nil, opts, saved)
	if err != nil {
		return CellResult{Report: report}, err
	}
	s.values, s.imports = saved, syntax.imports
	hasValue := false
	if n := len(syntax.body.items); n > 0 {
		last := syntax.body.items[n-1]
		hasValue = last.tag == "eval" || last.tag == "bind"
	}
	return CellResult{Value: report.Output, HasValue: hasValue, Report: report}, nil
}

// CellIncomplete asks the real parser after checking delimiter balance. It
// distinguishes an unfinished cell from a malformed one; strings cannot span
// physical lines. A trailing backslash explicitly continues an expression.
func CellIncomplete(source string) bool {
	ts, err := lex(source)
	if err != nil {
		return false
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

// Cells extend the tooling grammar with bare expressions; function bodies,
// proposals, blocks and control structures are parsed by the core parser.
func parseCell(source string) (*syntax, error) {
	ts, err := lex(source)
	if err != nil {
		return nil, err
	}
	p := &parser{ts: ts}
	s := &syntax{source: source, flowName: "cell", result: "void", functions: map[string]*expression{}, body: &block{}}
	p.padding()
	for p.peek().kind == "import" && p.err == nil {
		s.imports = append(s.imports, p.importDeclaration())
		if p.peek().kind != "eof" {
			p.separator()
		}
	}
	for p.peek().kind == "fn" && p.i+1 < len(ts) && ts[p.i+1].kind == "id" && p.err == nil {
		t := p.take()
		name := p.need("id").text
		f := p.function(t)
		f.name = name
		if _, exists := s.functions[name]; exists {
			p.err = at(diagnostic("E-DUPLICATE", "duplicate function "+name), t)
		}
		s.functions[name] = f
		p.padding()
	}
	for p.peek().kind != "eof" && p.err == nil {
		t := p.peek()
		if t.kind == "flow" || t.kind == "return" {
			return nil, at(diagnostic("E-SESSION", "enter expressions/bindings/functions; use :run for a complete Flow"), t)
		}
		var stmt *statement
		binding := t.kind == "id" && p.i+1 < len(ts) && (ts[p.i+1].kind == "=" || ts[p.i+1].kind == "<+")
		if binding || t.kind == "for" && !p.cellCarry() {
			stmt = p.stmt()
		} else {
			stmt = &statement{tag: "eval", tok: t, expr: p.expr(0)}
		}
		s.body.items = append(s.body.items, stmt)
		if p.peek().kind != "eof" {
			p.separator()
		}
	}
	return s, p.err
}

func (p *parser) cellCarry() bool {
	depth := 0
	for j := p.i + 1; j < len(p.ts); j++ {
		k := p.ts[j].kind
		if depth == 0 && k == "with" {
			return true
		}
		if depth == 0 && (k == "{" || k == "eof" || k == "\n" || k == ";") {
			return false
		}
		if k == "(" || k == "[" || k == "{" {
			depth++
		} else if k == ")" || k == "]" || k == "}" {
			depth--
		}
	}
	return false
}
