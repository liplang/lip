package tests

import (
	"context"
	"encoding/hex"
	. "lipbeta/script"
	"math"
	"strings"
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
	} {
		r := runSource(t, source, Host{}, Options{MaxSteps: 2000000})
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
