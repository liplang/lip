package script

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
)

// Session evaluates interactive cells with the ordinary checker and graph
// runtime. Each cell is a fresh run; only successful immutable values, functions
// and imports survive. Previous external calls are never evaluated again.
// Full Flow/Claim programs use Compile and Run, with an explicit run lifetime.
type Session struct {
	mu       sync.Mutex
	host     Host
	baseHost Host
	python   string
	packed   map[string]bool
	values   map[string]Value
	imports  []importDecl
}

type CellResult struct {
	Value    Value
	HasValue bool
	Report   Report
	// Help requests can be displayed as plain documentation by interactive UIs.
	Help bool
}

func NewSession(host Host) (*Session, error) {
	program, err := Compile("", host)
	if err != nil {
		return nil, err
	}
	return &Session{host: program.host, baseHost: program.host, values: map[string]Value{}, packed: map[string]bool{}}, nil
}

// NewInteractiveSession also enables generic Data calls to installed Python
// modules. Imports and completion never execute Python. NewSession only calls
// operations explicitly registered by its Host.
func NewInteractiveSession(host Host) (*Session, error) {
	s, err := NewSession(host)
	if err == nil {
		s.python = "python3"
	}
	return s, err
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
	names["help"] = true
	for name := range s.values {
		names[name] = true
	}
	for _, im := range s.imports {
		names[im.alias] = true
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
	s.host = s.baseHost
	s.packed = map[string]bool{}
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
	imports := append([]importDecl(nil), syntax.imports...)
	host, packed, help, err := s.prepareCell(syntax)
	if err != nil {
		return CellResult{}, err
	}
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
	check, err := analyzeBindings(syntax, host, initial)
	if err != nil {
		return CellResult{}, err
	}
	if len(check.stores) != 0 {
		return CellResult{}, diagnostic("E-SESSION", "Claim needs a complete Flow; use :run file.lip")
	}
	program := &Program{syntax, check, host}
	saved := copyValues(s.values)
	report, err := program.run(ctx, nil, opts, saved)
	if err != nil {
		return CellResult{Report: report}, err
	}
	s.values, s.imports, s.host, s.packed = saved, imports, host, packed
	hasValue := false
	if n := len(syntax.body.items); n > 0 {
		last := syntax.body.items[n-1]
		hasValue = last.tag == "eval" || last.tag == "bind"
	}
	return CellResult{Value: report.Output, HasValue: hasValue, Report: report, Help: help}, nil
}

const sessionHelp = `LIP 交互帮助
help(value) 查看值、纯函数、核心函数或请求的说明。
help(module) / help(module.member) 查看已导入 Python 模块或成员的文档。
import python "numpy" as np; np.sqrt(3) 调用已安装的 Python 库。
直接外部调用经 Host 执行；effect 构造请求，perform 执行请求。
:vars 查看保存的值；:reset 清空会话；:quit 退出。`

var sessionBuiltinHelp = map[string]string{
	"len":          "len(value) -> number\n返回字符串的 Unicode 字符数、列表元素数或对象字段数。",
	"str":          "str(value) -> string\n字符串保持原值；其他 Data 转成无空白规范 JSON。",
	"range":        "range(stop) / range(start, stop) / range(start, stop, step) -> list\n生成整数列表，不包含 stop；step 不得为 0。",
	"map":          "map(xs, f) -> list\n按顺序调用一参数纯函数 f，返回结果列表。",
	"fold":         "fold(xs, seed, f) -> any\n按顺序调用二参数纯函数 f(acc, item)；空列表返回 seed。",
	"fail":         "fail(message:string)\n以 E-FAIL 结束当前求值。",
	"concat":       "concat(a:list, b:list) -> list\n返回两个列表连接后的新列表。",
	"slice":        "slice(value, start:number, end:number)\n截取列表或字符串，不包含 end；索引从 0 开始。",
	"keys":         "keys(value:object) -> list\n返回按顺序排列的字符串键。",
	"has":          "has(value:object, key:string) -> bool\n判断对象是否有给定键。",
	"put":          "put(value:object, key:string, item:any) -> object\n返回包含新字段或替换字段的新对象。",
	"split":        "split(value:string, separator:string) -> list\n按非空分隔符拆分字符串。",
	"parse_number": "parse_number(value:string) -> number\n将完整数字文本转换成有限 binary64 数值。",
}

// Interactive conveniences lower into ordinary proposals, perform, blocks and
// pure functions before checking. The registry belongs to this cell until it
// succeeds, so a failed cell cannot publish imports or newly discovered calls.
func (s *Session) prepareCell(syntax *syntax) (Host, map[string]bool, bool, error) {
	host := s.host
	host.Contracts = append([]Contract(nil), host.Contracts...)
	contracts := map[string]Contract{}
	for _, c := range host.Contracts {
		contracts[identityKey(c.Operation.Wire())] = c
	}
	packed := map[string]bool{}
	for key, value := range s.packed {
		packed[key] = value
	}
	aliases := map[string]importDecl{}
	names := map[string]bool{"claim": true}
	shadowHelp := false
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
			if s.python != "" && (pythonTarget || stringTarget) {
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
					c.Invoke = pythonHandler(s.python, Contract{Operation: Operation{"python", "pydoc", "render_doc"}, Effect: UnknownEffect}, false, true)
					add(c)
				}
				*e = expression{tag: "perform", tok: e.tok, x: &expression{tag: "proposal", tok: e.tok, name: alias + ".help", args: []argument{{expr: &expression{tag: "lit", tok: e.tok, value: textValue(target)}}}}}
				return
			}
			if ok && imported {
				var lines []string
				lines = append(lines, fmt.Sprintf("import %s %q as %s", im.language, im.path, im.alias))
				for _, c := range host.Contracts {
					if c.Operation.Language == im.language && c.Operation.Path == im.path && (len(parts) == 1 || c.Operation.Member == parts[1]) {
						lines = append(lines, contractHelp(im.alias, c))
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
		op := Operation{im.language, im.path, parts[1]}
		key := identityKey(op.Wire())
		if _, registered := contracts[key]; !registered && im.language == "python" && s.python != "" {
			c := Contract{Operation: op, Version: "lip-repl-data-1", Params: []Parameter{{Name: "args", Schema: Schema{Type: "list"}}, {Name: "kwargs", Schema: Schema{Type: "object"}}}, Result: Schema{Type: "any"}, Effect: UnknownEffect}
			c.Invoke = pythonHandler(s.python, c, true, false)
			add(c)
			packed[key] = true
		}
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
		if packed[key] {
			pos := &expression{tag: "list", tok: e.tok}
			named := &expression{tag: "object", tok: e.tok}
			for _, arg := range proposal.args {
				if arg.name == "" {
					pos.elems = append(pos.elems, arg.expr)
				} else {
					named.fields = append(named.fields, field{name: arg.name, expr: arg.expr, tok: arg.expr.tok})
				}
			}
			proposal.args = []argument{{expr: pos}, {expr: named}}
		}
		if direct {
			body.tail = &expression{tag: "perform", tok: proposal.tok, x: proposal}
			*e = expression{tag: "block", tok: e.tok, body: body}
		}
	}
	for _, name := range sortedFunctionNames(syntax.functions) {
		lower(syntax.functions[name], shadowHelp)
	}
	lowerBlock(syntax.body, shadowHelp)
	help := false
	if n := len(syntax.body.items); n > 0 {
		last := syntax.body.items[n-1]
		help = last.tag == "eval" && documentation[last.expr]
	}
	return host, packed, help, err
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
				text = fmt.Sprintf("%s: 核心纯函数；允许的参数个数 %v\n%s", value.f.builtin, builtinArity[value.f.builtin], sessionBuiltinHelp[value.f.builtin])
			} else {
				var params []string
				for _, p := range value.f.params {
					params = append(params, p.name+":"+p.typ)
				}
				text = fmt.Sprintf("fn(%s) -> %s\n纯函数；捕获不可变值。", strings.Join(params, ", "), value.f.typ)
			}
		} else if value.k == Effect {
			text = fmt.Sprintf("effect %s %q %s\n外部请求，尚未执行；使用 perform 执行。", value.p.contract.Operation.Language, value.p.contract.Operation.Path, value.p.contract.Operation.Member)
		} else {
			descriptions := map[Kind]string{Null: "空值。", Bool: "布尔值 true / false。", Number: "有限 binary64 数值。", String: "不可变 UTF-8 字符串；len / split / slice 可操作字符串。", List: "不可变列表；索引从 0 开始；len / map / fold / slice 可操作列表。", Object: "不可变字符串键对象；通过 .字段 或 [键] 读取；keys / has / put 可操作对象。"}
			text += "\n" + descriptions[value.k]
		}
		return evalResult{value: textValue(text)}, nil
	}
	return &expression{tag: "fn", tok: t, params: []parameter{{name: param, typ: "any", tok: t}}, typ: "string", body: &block{tok: t, tail: tail}}
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
