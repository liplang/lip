package tests

import (
	"context"
	"encoding/hex"
	. "lip/script"
	"math"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func runSource(t *testing.T, source string, host Host, opts Options) Report {
	t.Helper()
	p, err := Compile(source, host)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	r, err := p.Run(ctx, nil, opts)
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func display(t *testing.T, v Value) string {
	t.Helper()
	b, err := EncodeJSON(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestEntryInputBoundary(t *testing.T) {
	var calls atomic.Int64
	host := Host{Contracts: []Contract{{Operation: Operation{Language: "host", Path: "app", Member: "touch"}, Version: "1", Result: Schema{Type: "null"}, Effect: WriteEffect,
		Invoke: func(context.Context, Request) Response { calls.Add(1); return Result(NullValue()) },
	}}}
	p, err := Compile(`import host "app"
flow Main(x:number,y:string)->object {perform effect app.touch(); return {x:x,y:y}}`, host)
	if err != nil {
		t.Fatal(err)
	}
	for _, input := range []string{`{"positional":[2],"named":{"y":"a"}}`, `{"positional":[],"named":{"x":2,"y":"a"}}`} {
		value, err := DecodeEntryInput([]byte(input))
		if err != nil {
			t.Fatal(err)
		}
		report, err := p.RunEntry(context.Background(), value, Options{})
		if err != nil || report.EntryOutput == nil || display(t, *report.EntryOutput) != `{"kind":"value","value":{"x":2,"y":"a"}}` {
			t.Fatal(report, err)
		}
	}
	before := calls.Load()
	for _, input := range []string{
		`null`, `{}`, `{"positional":[]}`, `{"positional":[],"named":{},"extra":1}`,
		`{"positional":{},"named":{}}`, `{"positional":[],"named":null}`,
		`{"positional":[2],"named":{"x":2,"y":"a"}}`, `{"positional":[2,"a",3],"named":{}}`,
		`{"positional":[2],"named":{}}`, `{"positional":[2],"named":{"y":"a","z":1}}`,
		`{"positional":["2"],"named":{"y":"a"}}`, `{"positional":[2],"named":{"y":null}}`,
		`{"positional":[],"named":{"x":2,"x":3,"y":"a"}}`, `{"positional":[],"positional":[],"named":{}}`,
		`{"positional":[1e999],"named":{"y":"a"}}`, `{"positional":[],"named":{"y":"\ud800"}}`,
	} {
		value, err := DecodeEntryInput([]byte(input))
		journal := NewMemoryJournal()
		var report Report
		if err == nil {
			report, err = p.RunEntry(context.Background(), value, Options{Journal: journal})
		}
		d, ok := err.(*Diagnostic)
		if !ok || d.Code != "E-SCHEMA" || d.Site != -1 || len(d.Position) != 0 || report.Error != nil || report.Journal != nil || len(report.Nodes) != 0 || report.Steps != 0 || report.EntryOutput != nil || len(journal.Records()) != 0 || calls.Load() != before {
			t.Fatalf("%s: report=%+v error=%v calls=%d", input, report, err, calls.Load())
		}
	}
	session, err := NewSession(Host{})
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	cell, err := session.Eval(context.Background(), `fn()->number {1}`, Options{})
	if err != nil {
		t.Fatal(err)
	}
	label, _ := StringValue("a")
	nested, _ := ObjectValue(map[string]Value{"x": ListValue(cell.Value), "y": label})
	if report, err := p.Run(context.Background(), map[string]Value{"x": nested, "y": label}, Options{}); err == nil || report.Journal != nil || calls.Load() != before {
		t.Fatal("non-Data input entered a run", report, err)
	}
}

func TestEntryOutputCompletion(t *testing.T) {
	for _, test := range []struct{ source, want string }{
		{`flow Main()->null {return null}`, `{"kind":"value","value":null}`},
		{`flow Main()->void {return}`, `{"kind":"void"}`},
		{"", `{"kind":"void"}`},
		{`fn helper()->number {1}`, `{"kind":"void"}`},
		{`fn()->number {1}()`, `{"kind":"void"}`},
	} {
		report := runSource(t, test.source, Host{}, Options{})
		if report.EntryOutput == nil || display(t, *report.EntryOutput) != test.want {
			t.Fatal(test.source, report)
		}
	}
	entered, release := make(chan struct{}), make(chan struct{})
	host := Host{Contracts: []Contract{{Operation: Operation{Language: "host", Path: "app", Member: "audit"}, Version: "1", Result: Schema{Type: "null"}, Effect: WriteEffect,
		Invoke: func(ctx context.Context, r Request) Response {
			close(entered)
			select {
			case <-release:
				return Result(NullValue())
			case <-ctx.Done():
				return Uncertain(ctx.Err().Error())
			}
		},
	}}}
	p, err := Compile("import host \"app\"\nflow Main()->null {perform effect app.audit(); return null}", host)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan Report, 1)
	go func() { report, _ := p.Run(ctx, nil, Options{Parallelism: 2}); done <- report }()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("audit did not start")
	}
	select {
	case report := <-done:
		t.Fatal("output published before selected audit completed", report)
	default:
	}
	close(release)
	report := <-done
	if report.Error != nil || report.EntryOutput == nil {
		t.Fatal(report)
	}
	p, err = Compile(`flow Main()->null {ignored=fail("stop");return null}`, Host{})
	if err != nil {
		t.Fatal(err)
	}
	report, err = p.Run(context.Background(), nil, Options{Parallelism: 2})
	if err == nil || report.EntryOutput != nil {
		t.Fatal("failed run published success", report, err)
	}
}

func TestDeclarationFilesAndEntrySelection(t *testing.T) {
	source := `flow First(CONFIG:number)->object {return {local:CONFIG,static:read_config()}}
export fn read_config()->object {CONFIG}
flow Second()->number {return CONFIG.values[0]}
CONFIG:object={values:[-0,+2,null,true],label:"fixed"}`
	if _, err := Compile(source, Host{}); err == nil || err.(*Diagnostic).Code != "E-ENTRY" {
		t.Fatal("ambiguous entry accepted", err)
	}
	for _, name := range []string{"Missing", "CONFIG", "read_config"} {
		if _, err := CompileWithOptions(source, Host{}, CompileOptions{Entry: name}); err == nil || err.(*Diagnostic).Code != "E-ENTRY" {
			t.Fatal("non-Flow entry accepted", name, err)
		}
	}
	p, err := CompileWithOptions(source, Host{}, CompileOptions{Entry: "First"})
	if err != nil {
		t.Fatal(err)
	}
	report, err := p.Run(context.Background(), map[string]Value{"CONFIG": number(7)}, Options{})
	if err != nil || display(t, report.Output) != `{"local":7,"static":{"label":"fixed","values":[0,2,null,true]}}` {
		t.Fatal("entry shadowing changed module function capture", report, err)
	}
	p, err = CompileWithOptions(source, Host{}, CompileOptions{Entry: "Second"})
	if err != nil {
		t.Fatal(err)
	}
	report, err = p.Run(context.Background(), nil, Options{})
	if err != nil || display(t, report.Output) != "0" {
		t.Fatal(report, err)
	}
	for _, test := range []struct{ source, code string }{
		{`LIMIT:number=2`, "E-ENTRY"}, {`export fn helper()->number {1}`, "E-ENTRY"},
		{`flow Main()->number {return 2} x=1`, "E-PARSE"}, {"x=1\nLIMIT:number=2", "E-PARSE"},
		{"flow Main()->number {return LIMIT}\nLIMIT:number=len([])", "E-PARSE"},
		{"flow Main()->number {return 1}\nLIMIT:number=2 flow Other()->number {return 2}", "E-PARSE"},
		{"flow Main()->number {return 1}\nLIMIT:number=2\nLIMIT:number=3", "E-DUPLICATE"},
		{"fn Main()->number {1}\nflow Main()->number {return 1}", "E-DUPLICATE"},
		{"flow Main()->number {return 1}\nLIMIT:object={x:1,\"x\":2}", "E-DUPLICATE"},
		{"flow Main()->number {return 1}\nLIMIT:number=\"bad\"", "E-TYPE"},
		{"flow Main()->number {return 1}\nLIMIT:function=null", "E-TYPE"},
		{"flow Main()->number {return 1}\nflow Unused()->number {return missing}", "E-NAME"},
		{"flow Main()->number {return 1}\nflow Unused(x:effect)->number {return 1}", "E-TYPE"},
		{`flow Main()->any {return Main}`, "E-TYPE"},
	} {
		_, err := CompileWithOptions(test.source, Host{}, CompileOptions{Entry: "Main"})
		if d, ok := err.(*Diagnostic); !ok || d.Code != test.code {
			t.Fatalf("%s: %v; want %s", test.source, err, test.code)
		}
	}
	for _, source := range []string{`import host "app"`, "flow Main()->number {return LIMIT}\nLIMIT:number=2"} {
		if _, err := Compile(source, Host{}); err != nil {
			t.Fatal("EOF required a newline", source, err)
		}
	}
}
func TestSpecTailAndFilter(t *testing.T) {
	source := `fn sum_to(n: number, acc: number) -> number {
    if n == 0 { acc } else { sum_to(n - 1, acc + n) }
}

fn filter_fold(xs: list, p: function) -> list {
    fold(xs, [], fn(acc: list, x: any) -> list {
        if p(x) { concat(acc, [x]) } else { acc }
    })
}
fn tag(x: any) -> number { match x { number => 1, _ => 0 } }
flow Main() -> object {
    odd = filter_fold([1,2,3], fn(x: number) -> bool { x % 2 == 1 })
    return {sum:sum_to(10,0), odd:odd, tag:tag(0)}
}`
	r := runSource(t, source, Host{}, Options{})
	if got := display(t, r.Output); got != `{"odd":[1,3],"sum":55,"tag":1}` {
		t.Fatal(got)
	}
}
func TestMultilineStrings(t *testing.T) {
	for _, tt := range []struct{ source, want string }{
		{`""""""`, ""},
		{`"""one " and "" two # [] {}"""`, `one " and "" two # [] {}`},
		{"\"\"\"\n  你好\t🙂\n\"\"\"", "\n  你好\t🙂\n"},
		{"\"\"\"a\r\nb\r\n\"\"\"", "a\nb\n"},
		{`"""\""" \\ \n \r \t \u{1f642}"""`, "\"\"\" \\ \n \r \t 🙂"},
	} {
		r := runSource(t, "flow Main()->string {return "+tt.source+"}", Host{}, Options{})
		got, _ := r.Output.Text()
		if got != tt.want {
			t.Fatalf("%q: got %q want %q", tt.source, got, tt.want)
		}
	}
	for _, source := range []string{
		`"""unfinished`, `"""bad\x"""`, `"""\u{d800}"""`,
		"\"\"\"bare\rCR\"\"\"", "\"\"\"bad\x00control\"\"\"", "\"\"\"bad\\\ncontinuation\"\"\"",
		"\"ordinary\nnewline\"", "\"ordinary\ttab\"",
	} {
		_, err := Compile("flow Main()->string {return "+source+"}", Host{})
		if d, ok := err.(*Diagnostic); !ok || d.Code != "E-LEX" {
			t.Fatalf("%q: %v", source, err)
		}
	}
	p, err := Compile("flow Main()->any {\n text=\"\"\"\nline\r\n\"\"\"\n return [0][3]\n}", Host{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = p.Run(context.Background(), nil, Options{})
	if d, ok := err.(*Diagnostic); !ok || d.Line != 5 {
		t.Fatalf("diagnostic after multiline string: %v", err)
	}
}

func TestCarryElseIfAndBreak(t *testing.T) {
	r := runSource(t, `flow Main() -> number {
 answer = for x in [1,2,3,4] with s = 0 {
   for unused in [0] { break }
   if x == 3 { break s + x }
   else if x == 4 { break 999 }
   else { next s + x }
 }
 return answer
}`, Host{}, Options{})
	if num(r.Output) != 6 {
		t.Fatal(r.Output)
	}
}
func TestProperTailCalls(t *testing.T) {
	r := runSource(t, `fn even(n:number)->number { if n == 0 {0} else {odd(n-1)} }
fn odd(n:number)->number { if n == 0 {1} else {even(n-1)} }
flow Main()->number { return even(100000) }`, Host{}, Options{MaxSteps: 2000000})
	if num(r.Output) != 0 {
		t.Fatal(r.Output)
	}
}
func TestDirectAndIndirectTailCalls(t *testing.T) {
	for _, source := range []string{
		`fn loop(n:number)->number { if n == 0 {0} else {loop(n-1)} }
flow Main()->number { return loop(100000) }`,
		`fn loop(n:number, callback:function)->number { if n == 0 {0} else {callback(n-1,callback)} }
flow Main()->number { return loop(100000,loop) }`,
		`fn loop(n:number)->number {if n==0 {0} else {n-1 |> loop}}
flow Main()->number {return 100000 |> loop}`,
	} {
		r := runSource(t, source, Host{}, Options{MaxSteps: 2000000, MaxFrames: 16})
		if num(r.Output) != 0 {
			t.Fatal(r.Output)
		}
	}
}
func TestPureOperations(t *testing.T) {
	cases := map[string]string{
		`{ len([0]); }`: "1", `{ len([0]); null }`: "null", `if false { 1 / 0 } else { 7 }`: "7", `[1 / x for x in [0,2] if x != 0]`: "[0.5]",
		`keys({"\u{10000}":1,"\u{e000}":2})`: `["","𐀀"]`, `split("a,,b,",",")`: `["a","","b",""]`, `slice("a𐀀b",1,2)`: `"𐀀"`, `parse_number("-0")`: "0", `put({},"x",3)`: "{\"x\":3}", `"10" < "9"`: "true",
		`map(["Ada"], fn(name:any)->string { "hello " + name })`: `["hello Ada"]`,
		`map(["Ada"], fn(name:any)->any { name + "!" })`:         `["Ada!"]`,
	}
	for expr, want := range cases {
		t.Run(expr, func(t *testing.T) {
			r := runSource(t, "flow Main()->any { return "+expr+" }", Host{}, Options{})
			if got := display(t, r.Output); got != want {
				t.Fatalf("got %s want %s", got, want)
			}
		})
	}
}
func TestNumericAndEncodingVectors(t *testing.T) {
	for _, tt := range []struct{ a, b, q, r float64 }{{-5, 3, -2, 1}, {5, -3, -2, -1}, {1, .1, 9, math.Float64frombits(0x3fb9999999999996)}, {-1e-17, 1, -1, math.Nextafter(1, 0)}, {1e-17, -1, -1, math.Nextafter(-1, 0)}, {math.SmallestNonzeroFloat64, 1, 0, math.SmallestNonzeroFloat64}} {
		report := runSource(t, "flow Main()->object { return {q:("+formatNumber(tt.a)+")//("+formatNumber(tt.b)+"), r:("+formatNumber(tt.a)+")%("+formatNumber(tt.b)+")} }", Host{}, Options{})
		fields, _ := report.Output.Fields()
		q, e := fields["q"], error(nil)
		if e != nil || num(q) != tt.q {
			t.Fatalf("quotient %v %v", q, e)
		}
		r := fields["r"]
		if e != nil || num(r) != tt.r {
			t.Fatalf("remainder %.20g want %.20g", num(r), tt.r)
		}
	}
	for _, tt := range []struct {
		v    Value
		want string
	}{{number(1), "033ff0000000000000"}, {textValue("a"), "04000000000000000161"}, {ListValue(), "050000000000000000"}, {object(nil), "060000000000000000"}} {
		b, _ := Canonical(tt.v)
		if hex.EncodeToString(b) != tt.want {
			t.Fatal(hex.EncodeToString(b))
		}
	}
	for _, tt := range []struct {
		n float64
		s string
	}{{1e-6, "0.000001"}, {1e-7, "1e-7"}, {1e21, "1e21"}, {math.SmallestNonzeroFloat64, "5e-324"}} {
		if got := formatNumber(tt.n); got != tt.s {
			t.Fatalf("%s want %s", got, tt.s)
		}
	}
}
func TestJSONDuplicateKeys(t *testing.T) {
	if _, err := DecodeData([]byte(`{"a":1,"\u0061":2}`)); err == nil {
		t.Fatal("duplicate key accepted")
	}
}

func TestPowerLogarithmSpecVectors(t *testing.T) {
	cases := []struct {
		source string
		want   float64
	}{
		{"2 ** 3", 8}, {"8 */ 2", 3}, {"2 ** 3 ** 2", 512},
		{"-3 ** 2", -9}, {"(-3) ** 2", 9}, {"2 ** -3", .125},
		{"2 ** -3 ** 2", 1.0 / 512}, {"-2 ** -2", -.25},
		{"8 */ 2 * 4", 12}, {"256 */ 2 ** 2", 4},
		{"16 */ 2 */ 2", 2}, {"2 * 8 */ 2", 4},
		{"0 ** 0", 1}, {"0 ** 2", 0}, {"(-2) ** 3", -8},
		{"(-2) ** -3", -.125}, {"(-1) ** 9007199254740992", 1},
		{"2 ** -1074", math.SmallestNonzeroFloat64},
		{"2 ** -1075", 0}, {"(-2) ** -1075", 0},
		{"2 ** 1023", math.Ldexp(1, 1023)}, {"4 ** 0.5", 2},
		{"4 ** -0.5", .5}, {"5e-324 ** 0.5", math.Ldexp(1, -537)},
		{"0.25 */ 2", -2}, {"8 */ 0.5", -3},
		{"1 */ 5e-324", 0}, {"5e-324 */ 5e-324", 1},
		{"2 ** 0.5", math.Sqrt(2)}, {"27 ** (1/3)", 3},
		{"2 */ 3", math.Float64frombits(0x3fe430939835353d)},
		{"10 */ 2", math.Float64frombits(0x400a934f0979a372)},
		{"if false { (-2) ** 0.5 } else { 7 }", 7},
	}
	for _, tt := range cases {
		t.Run(tt.source, func(t *testing.T) {
			r := runSource(t, "flow Main()->number { return "+tt.source+" }", Host{}, Options{})
			if actual := math.Float64bits(num(r.Output)); actual != math.Float64bits(tt.want) {
				t.Fatalf("got %.17g (%016x), want %.17g (%016x)", num(r.Output), actual, tt.want, math.Float64bits(tt.want))
			}
		})
	}
	for _, expr := range []string{"0 ** -1", "(-2) ** 0.5", "2 ** 1024", "0 */ 2", "-2 */ 2", "2 */ 0", "2 */ 1", "2 */ -2"} {
		p, err := Compile("flow Main()->number {return "+expr+"}", Host{})
		if err != nil {
			t.Fatal(err)
		}
		_, err = p.Run(context.Background(), nil, Options{})
		if err == nil || !strings.Contains(err.Error(), "E-NUMBER") {
			t.Fatalf("%s: %v", expr, err)
		}
	}
	for _, expr := range []string{`"2" ** 3`, `8 */ true`} {
		_, err := Compile("flow Main()->number {return "+expr+"}", Host{})
		if err == nil || !strings.Contains(err.Error(), "E-TYPE") {
			t.Fatalf("%s: %v", expr, err)
		}
	}
}

// Compare independent library approximations across ordinary inputs. Exact bit
// agreement with the frozen algorithm is checked by vectors and native parity.
func TestPowerLogarithmApproximation(t *testing.T) {
	p, err := Compile(`flow Main(a:number,b:number)->list { return [a ** b, a */ b] }`, Host{})
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range []float64{.01, .5, .9999999999999999, 1, 1.0000000000000002, 2, 3, 10, 1000} {
		for _, b := range []float64{.1, .5, 1.5, 2, 3, 7, 16} {
			r, err := p.Run(context.Background(), map[string]Value{"a": number(a), "b": number(b)}, Options{})
			if err != nil {
				t.Fatal(err)
			}
			xs, _ := r.Output.Elements()
			for i, want := range []float64{math.Pow(a, b), math.Log(a) / math.Log(b)} {
				if got := num(xs[i]); math.Abs(got-want) > 1e-13*math.Abs(want) {
					t.Fatalf("a=%g,b=%g,index=%d: got %.17g want %.17g", a, b, i, got, want)
				}
			}
		}
	}
}
func TestJSONSurrogates(t *testing.T) {
	for _, input := range []string{`"\ud800"`, `"\udc00"`, `"\ud800\u0041"`} {
		if _, err := DecodeData([]byte(input)); err == nil {
			t.Fatal("accepted invalid surrogate", input)
		}
	}
	for _, input := range []string{`"\ud800\udc00"`, `"\\ud800"`} {
		if _, err := DecodeData([]byte(input)); err != nil {
			t.Fatal(err)
		}
	}
}
func TestLexerSitesAndErrors(t *testing.T) {
	compile := func(source string) *Program {
		p, err := Compile(source, Host{})
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	before := compile("flow Main()->number {\nx=2\nreturn x\n}")
	after := compile("flow Main()->number {\n# comment occupies a NEWLINE site\nx=2\nreturn x\n}")
	for site, kind := range before.Sites() {
		if after.Sites()[site+1] != kind {
			t.Fatal("NEWLINE missing from static ordinals")
		}
	}
	for _, bad := range []string{"01", "1.", "1e", "1e309", "\"\\u{d800}\"", "λ"} {
		if _, err := Compile("flow Main()->any { return "+bad+" }", Host{}); err == nil {
			t.Fatalf("accepted %q", bad)
		}
	}
	for _, s := range []string{"2e-324", "3e-324"} {
		compile("flow Main()->number { return " + s + " }")
	}
}

func testHost(handler func(context.Context, Request) Response) Host {
	return Host{Contracts: []Contract{{Operation: Operation{Language: "python", Path: "example.worker", Member: "read"}, Version: "1", Params: []Parameter{{Name: "value", Schema: Schema{Type: "number"}}}, Result: Schema{Type: "number"}, Effect: ReadOnlyEffect, Concurrent: true, Invoke: handler}}}
}
func TestClaimParallelAndReplay(t *testing.T) {
	calls := 0
	host := testHost(func(ctx context.Context, r Request) Response { calls++; return Result(r.Positional[0]) })
	source := `import python "example.worker" as worker
flow Main()->list {
 facts=claim()
 for x in [1,2,3] parallel {
   v=perform effect worker.read(x)
   facts <+ v
 }
 return [item.value for item in facts]
}`
	r := runSource(t, source, host, Options{RunID: "same", Parallelism: 1})
	if got := display(t, r.Output); got != "[1,2,3]" {
		t.Fatal(got)
	}
	runSource(t, source, host, Options{RunID: "same", Journal: r.Journal})
	if calls != 3 {
		t.Fatal(calls)
	}
}
func TestStaticDiagnostics(t *testing.T) {
	cases := []struct{ source, code string }{
		{`flow Main()->number { return 1 < "2" }`, "E-TYPE"}, {`fn f(x:number)->number{x} flow Main()->number{return f(x=1,2)}`, "E-PARSE"},
		{`flow Main()->list { facts=claim(); facts <+ facts; return facts }`, "E-CLAIM-ORDER"},
		{`flow Main()->list { facts=claim(); f=fn()->list{facts}; return facts }`, "E-PURITY"},
		{`import python "example.worker" as worker
flow Main()->void { for x in [0] parallel { p=[effect worker.read(x)]; q=p[0]; out=perform q } }`, "E-PARALLEL"},
	}
	for _, tt := range cases {
		_, e := Compile(tt.source, testHost(func(context.Context, Request) Response { return Result(number(0)) }))
		if e == nil || !strings.Contains(e.Error(), tt.code) {
			t.Fatalf("%s: %v", tt.code, e)
		}
	}
}

func TestNullableAnyAndCallbackChecks(t *testing.T) {
	for _, v := range []Value{NullValue(), number(3), textValue("hello"), BoolValue(true), ListValue(), object(nil)} {
		if err := (Schema{Type: "any?"}).Validate(v); err != nil {
			t.Fatal("any? rejected Data", err)
		}
	}
	r := runSource(t, `fn identity(x:any?)->any? {x}
flow Main()->any? {return map([null,3,"hello",true], identity)}`, Host{}, Options{})
	if display(t, r.Output) != `[null,3,"hello",true]` {
		t.Fatal(r.Output)
	}
	for _, source := range []string{
		`flow Main()->any { return map([], fn(a:number,b:number)->number{a}) }`,
		`fn callback(a:number,b:number)->number{a}
flow Main()->any { alias=callback; return map([], alias) }`,
		`flow Main()->any { return fold([],0,fn(a:number)->number{a}) }`,
		`flow Main()->any { return map([],put) }`,
		`flow Main()->any { return fold([],"bad",fn(a:number,b:number)->number{a+b}) }`,
		`flow Main()->any { return map([1],fn(a:string)->string{a}) }`,
		`flow Main()->void? {}`,
	} {
		if _, err := Compile(source, Host{}); err == nil {
			t.Fatal("accepted statically invalid callback/type", source)
		}
	}
	// Source order, not Go map iteration, determines the first static diagnostic.
	source := `fn z()->number {"wrong"} fn a()->bool {2} flow Main()->void {}`
	_, first := Compile(source, Host{})
	for i := 0; i < 100; i++ {
		_, err := Compile(source, Host{})
		if err == nil || err.Error() != first.Error() {
			t.Fatalf("unstable compile diagnostic: %v vs %v", err, first)
		}
	}
}

func TestMalformedAndTruncatedSourceNeverPanics(t *testing.T) {
	program := `fn total(xs:list)->number {fold(xs,0,fn(a:number,x:number)->number{a+x})}
flow Main()->list {facts=claim();for x in [1,2,3] parallel {facts <+ x*x};return [r.value for r in facts]}`
	for i := 0; i < len(program); i++ {
		// Valid partial scripts may compile; malformed ones must return an error.
		_, _ = Compile(program[:i], Host{})
	}
	for _, source := range []string{"flow {", "{[)}", "fn f(x:)", "flow f()->any{return match 1 {=>}}", "x=for with s=0{next}", "flow f()->void{for x in [1] parallel {continue}}"} {
		if _, err := Compile(source, Host{}); err == nil {
			t.Fatal("malformed program compiled", source)
		}
	}
}

func TestTailReturnChecksFollowCallOrder(t *testing.T) {
	p, err := Compile(`fn outer(f:function,x:any)->number{f(x)}
fn inner(x:any)->string{x}
flow Main()->any{return outer(inner,true)}`, Host{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = p.Run(context.Background(), nil, Options{})
	if err == nil || !strings.Contains(err.Error(), "expected string, got bool") {
		t.Fatal("checked outer return before inner return", err)
	}
}

// These helpers deliberately use the same immutable boundary as embedders.
func num(v Value) float64 { n, _ := v.Float(); return n }
func number(n float64) Value {
	v, err := NumberValue(n)
	if err != nil {
		panic(err)
	}
	return v
}
func textValue(s string) Value {
	v, err := StringValue(s)
	if err != nil {
		panic(err)
	}
	return v
}
func object(fields map[string]Value) Value {
	v, err := ObjectValue(fields)
	if err != nil {
		panic(err)
	}
	return v
}
func formatNumber(n float64) string {
	s, err := Display(number(n))
	if err != nil {
		panic(err)
	}
	return s
}

func TestStructuredFlowAndPipeline(t *testing.T) {
	var calls atomic.Int64
	host := Host{Contracts: []Contract{{Operation: Operation{Language: "host", Path: "app", Member: "read"}, Version: "1", Params: []Parameter{{Name: "x", Schema: Schema{Type: "number"}}}, Result: Schema{Type: "number"}, Effect: ReadOnlyEffect, Concurrent: true, Invoke: func(_ context.Context, r Request) Response { calls.Add(1); return Result(r.Positional[0]) }}}}
	source := `import host "app"
fn plus(x:number,delta:number)->number {x+delta}
flow Read(x:number)->number {
 facts=claim()
 for n in [x,x+1] parallel {facts <+ n}
 observed=perform effect app.read(x)
 return observed+len(facts)
}
flow Main()->object {
 values=[x |> Read |> plus(delta=3) for x in [1,2]]
 nested={first=Read(4);second=Read(first);second}
 sum=for x in [1,2] with acc=0 {value=Read(x);next acc+value}
 return {values:values,nested:nested,sum:sum}
}`
	p, err := CompileWithOptions(source, host, CompileOptions{Entry: "Main"})
	if err != nil {
		t.Fatal(err)
	}
	for _, parallel := range []int{1, 4} {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		report, err := p.Run(ctx, nil, Options{Parallelism: parallel, RunID: "structured"})
		cancel()
		if err != nil || display(t, report.Output) != `{"nested":8,"sum":7,"values":[6,7]}` {
			t.Fatal(report, err)
		}
		seen := map[string]bool{}
		for _, n := range report.Nodes {
			key := display(t, n.Position.Wire())
			if seen[key] {
				t.Fatal("duplicate node position", key)
			}
			seen[key] = true
		}
		if len(report.Journal.Records()) != 6 {
			t.Fatal("child calls were replayed or omitted", len(report.Journal.Records()))
		}
	}
	if calls.Load() != 12 {
		t.Fatal(calls.Load())
	}
}

func TestFlowBoundaries(t *testing.T) {
	for _, test := range []struct{ source, code string }{
		{`flow Main()->number {return Other()} flow Other()->number {return Main()}`, "E-FLOW-CYCLE"},
		{`flow Main()->number {return 1} flow Other()->number {return Other()}`, "E-FLOW-CYCLE"},
		{`fn bad()->number {Other()} flow Main()->number{return 1} flow Other()->number{return 1}`, "E-PURITY"},
		{`flow Read(x:number)->number{return x} flow Main()->number{return Read(Read(1))}`, "E-PURITY"},
		{`flow Read(x:number)->number{return x} flow Main()->number{return 1 |>Read(x=2)}`, "E-ARITY"},
		{`flow Main()->number{return 1 |>map}`, "E-ARITY"},
		{`fn f(x:number)->number{x} flow Main()->number{alias=f;return 1 |>alias}`, "E-TYPE"},
		{`flow Read(x:number)->number{return x} flow Main()->number{return match 1 {1 if Read(1)==1 => 1,_ => 2}}`, "E-PURITY"},
		{`flow Empty()->void{} flow Main()->any{return Empty()}`, "E-TYPE"},
		{`flow Empty(x:number)->void{} flow Main()->any{return 1 |>Empty}`, "E-TYPE"},
	} {
		_, err := CompileWithOptions(test.source, Host{}, CompileOptions{Entry: "Main"})
		if d, ok := err.(*Diagnostic); !ok || d.Code != test.code {
			t.Fatalf("%s: %v; want %s", test.source, err, test.code)
		}
	}
	p, err := CompileWithOptions(`flow Bad()->number {return fail("child")} flow Main()->number{return 1 |>Bad}`, Host{}, CompileOptions{Entry: "Main"})
	if err == nil {
		t.Fatal("arity should reject before execution", p)
	}
	p, err = CompileWithOptions(`flow Bad(x:number)->number {return fail("child")} fn later(x:number,delta:number)->number{x+delta} flow Main()->number{return 1 |>Bad |>later(delta=fail("later"))}`, Host{}, CompileOptions{Entry: "Main"})
	if err != nil {
		t.Fatal(err)
	}
	report, err := p.Run(context.Background(), nil, Options{Parallelism: 1})
	if err == nil || report.Error == nil || report.Error.Message != "child" || len(report.Error.Position) != 3 {
		t.Fatal("child cause was replaced", report, err)
	}
	for _, n := range report.Nodes {
		if n.Status == Error && len(n.Position) != 3 {
			t.Fatal("boundary created another root error", n)
		}
	}
}

func TestStaticModuleSnapshot(t *testing.T) {
	set := SourceSet{Root: "main.lip", Sources: map[string]Source{
		"main.lip": {Version: "local", Text: `import lip "./lib/a.lip" as a
import lip "./lib/b.lip" as b
flow Main(RATE:number)->object {return {a:3 |>a.Adjust,b:b.total(3),local:RATE,static:a.RATE}}
`},
		"lib/a.lip": {Version: "1", Text: `export RATE:number=2
fn helper(x:number)->number {x+RATE}
export flow Adjust(x:number)->number{return helper(x)}`},
		"lib/b.lip": {Version: "2", Text: `RATE:number=10
fn helper(x:number)->number{x*RATE}
export fn total(x:number)->number{helper(x)}`},
	}}
	p, err := CompileSourceSet(set, Host{}, CompileOptions{})
	if err != nil {
		t.Fatal(err)
	}
	report, err := p.Run(context.Background(), map[string]Value{"RATE": number(100)}, Options{Parallelism: 1})
	if err != nil || display(t, report.Output) != `{"a":5,"b":30,"local":100,"static":2}` {
		t.Fatal(report, err)
	}
	fields, _ := p.BuildInfo().Fields()
	lock := fields["dependency_lock"]
	set.Lock = &lock
	if _, err := CompileSourceSet(set, Host{}, CompileOptions{}); err != nil {
		t.Fatal(err)
	}
	source := set.Sources["lib/a.lip"]
	source.Text += "\n# new revision"
	set.Sources["lib/a.lip"] = source
	if _, err := CompileSourceSet(set, Host{}, CompileOptions{}); err == nil || err.(*Diagnostic).Code != "E-MODULE" {
		t.Fatal("stale lock accepted", err)
	}
	set.Lock = nil
	changed, err := CompileSourceSet(set, Host{}, CompileOptions{})
	if err != nil {
		t.Fatal(err)
	}
	next, _ := changed.BuildInfo().Fields()
	if display(t, fields["revision"]) == display(t, next["revision"]) {
		t.Fatal("dependency edit did not change revision")
	}
	for _, test := range []struct{ root, dep, code string }{
		{`import lip "./dep.lip" as d
flow Main()->number{return d.secret()}`, `fn secret()->number{1}`, "E-EXPORT"},
		{`import lip "./dep.lip" as d
flow Main()->number{return 1}`, `import lip "./main.lip" as main`, "E-MODULE-CYCLE"},
		{`import lip "./dep.lip" as d
flow Main()->number{return 1}`, `x=1`, "E-MODULE"},
		{`import lip "./dep.lip" as d
flow Main()->number{return 1}`, `export fn invalid()->number{missing}`, "E-NAME"},
		{`import lip "./dep.lip" as d
flow Main()->number{return 1}`, `export flow Invalid()->number{return Invalid()}`, "E-FLOW-CYCLE"},
		{`import lip "./dep.lip" as d
flow Main()->any{return d}`, `export V:number=1`, "E-TYPE"},
	} {
		_, err := CompileSourceSet(SourceSet{Root: "main.lip", Sources: map[string]Source{"main.lip": {Version: "local", Text: test.root}, "dep.lip": {Version: "1", Text: test.dep}}}, Host{}, CompileOptions{})
		if d, ok := err.(*Diagnostic); !ok || d.Code != test.code {
			t.Fatalf("%s: %v;want %s", test.dep, err, test.code)
		}
	}
}

func TestModuleLexicalIsolation(t *testing.T) {
	for _, test := range []struct{ name, root, dep, code string }{
		{"data", "secret:number=7\nflow Main()->number{return d.read()}", `export fn read()->number{secret}`, "E-NAME"},
		{"function", "fn secret()->number{7}\nflow Main()->number{return d.read()}", `export fn read()->number{secret()}`, "E-NAME"},
		{"flow", "flow Secret()->number{return 7}\nflow Main()->number{return d.Read()}", `export flow Read()->number{return Secret()}`, "E-NAME"},
		{"operation", "import go \"strings\" as text\nflow Main()->string{return d.Read()}", `export flow Read()->string{return perform effect text.ToUpper("ada")}`, "E-OPERATION"},
	} {
		t.Run(test.name, func(t *testing.T) {
			host, err := DemoHost()
			if err != nil {
				t.Fatal(err)
			}
			defer host.Close()
			_, err = CompileSourceSet(SourceSet{Root: "main.lip", Sources: map[string]Source{
				"main.lip": {Version: "local", Text: "import lip \"./dep.lip\" as d\n" + test.root},
				"dep.lip":  {Version: "1", Text: test.dep},
			}}, host, CompileOptions{Entry: "Main"})
			if d, ok := err.(*Diagnostic); !ok || d.Code != test.code || d.SourceID != "dep.lip" {
				t.Fatalf("dependency captured importer scope: %v; want %s in dep.lip", err, test.code)
			}
		})
	}
	set := SourceSet{Root: "main.lip", Sources: map[string]Source{
		"main.lip": {Version: "local", Text: `import lip "./dep.lip" as d
facts:number=40
fn base()->number{facts}
flow Main()->list{facts=claim();facts <+ d.echo(base());return [row.value for row in facts]}`},
		"dep.lip": {Version: "1", Text: `export fn echo(facts:number)->number{facts}`},
	}}
	p, err := CompileSourceSet(set, Host{}, CompileOptions{})
	if err != nil {
		t.Fatal("unrelated Claim name leaked into module function scope", err)
	}
	report, err := p.Run(context.Background(), nil, Options{})
	if err != nil || display(t, report.Output) != "[40]" {
		t.Fatal(report, err)
	}
}

func TestSubFlowErrorPathAndEmptyBudget(t *testing.T) {
	p, err := CompileWithOptions(`flow Bad(x:number)->number{return fail("child")} flow Main()->list{return [Bad(x) for x in [1,2]]}`, Host{}, CompileOptions{Entry: "Main"})
	if err != nil {
		t.Fatal(err)
	}
	report, err := p.Run(context.Background(), nil, Options{Parallelism: 1})
	if err == nil || len(report.Error.EvalPath) != 0 || len(report.Error.Position) != 4 || report.Error.Position[1].Index != "0" {
		t.Fatal("caller path overwrote child root path", report.Error, err)
	}
	p, err = CompileWithOptions(`flow Empty(x:number)->void{} flow Main()->void{for x in [1,2,3] {Empty(x)}}`, Host{}, CompileOptions{Entry: "Main"})
	if err != nil {
		t.Fatal(err)
	}
	report, err = p.Run(context.Background(), nil, Options{MaxNodes: 6, MaxSteps: 1000})
	if err == nil || report.Error.Code != "E-BUDGET" {
		t.Fatal("empty Flow instances escaped node budget", report, err)
	}
}

func TestModuleStaticSiteVector(t *testing.T) {
	set := SourceSet{Root: "z.lip", Sources: map[string]Source{
		"a.lip": {Version: "1", Text: `export flow Value(x:number)->number {return x}`},
		"z.lip": {Version: "local", Text: "import lip \"./a.lip\" as a\nflow Main()->number{return a.Value(2)}"},
	}}
	p, err := CompileSourceSet(set, Host{}, CompileOptions{})
	if err != nil {
		t.Fatal(err)
	}
	report, err := p.Run(context.Background(), nil, Options{})
	if err != nil || num(report.Output) != 2 {
		t.Fatal(report, err)
	}
	found := false
	for _, n := range report.Nodes {
		if len(n.Position) > 1 {
			found = true
			if got := display(t, n.Position.Wire()); got != `[["13","0"],["14","0"],["32","0"]]` {
				t.Fatal("source ordinal/call prefix changed", got)
			}
		}
	}
	if !found {
		t.Fatal("callee was not represented in the parent graph")
	}
	original, _ := p.BuildInfo().Fields()
	for id, source := range set.Sources {
		source.Text = strings.ReplaceAll(source.Text, "\n", "\r\n")
		set.Sources[id] = source
	}
	normalized, err := CompileSourceSet(set, Host{}, CompileOptions{})
	if err != nil {
		t.Fatal(err)
	}
	next, _ := normalized.BuildInfo().Fields()
	same, _ := Equal(original["revision"], next["revision"])
	if !same {
		t.Fatal("CRLF changed revision")
	}
}

func TestLoopSourceFlowAndBodyPositions(t *testing.T) {
	p, err := CompileWithOptions(`flow Items()->list{return [1,2]} flow Main()->void{for x in Items(){unused=x}}`, Host{}, CompileOptions{Entry: "Main"})
	if err != nil {
		t.Fatal(err)
	}
	for _, parallel := range []int{1, 4} {
		report, err := p.Run(context.Background(), nil, Options{Parallelism: parallel})
		if err != nil {
			t.Fatal(err)
		}
		var selector, source, body Position
		seen := map[string]bool{}
		for _, n := range report.Nodes {
			key := display(t, n.Position.Wire())
			if seen[key] {
				t.Fatal("source/body collision", key)
			}
			seen[key] = true
			switch len(n.Position) {
			case 1:
				selector = n.Position
			case 3:
				source = n.Position
			case 2:
				if n.Position[0].Index == "0" {
					body = n.Position
				}
			}
		}
		if len(selector) != 1 || len(source) != 3 || len(body) != 2 || source[0] != selector[0] || body[0] != selector[0] || source[1].Site >= body[1].Site {
			t.Fatal("selector invocation prefix was confused with iteration zero", report.Nodes)
		}
	}
}
