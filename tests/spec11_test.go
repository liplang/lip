package tests

import (
	"bytes"
	"context"
	"encoding/hex"
	"fmt"
	. "lip/script"
	"math"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestSpec11NonFiniteSemanticsInterpreterAndGo(t *testing.T) {
	cases := []struct{ expression, want string }{
		{`nan`, `nan`}, {`-nan`, `nan`}, {`parse_number("nan")`, `nan`},
		{`nan == nan`, `false`}, {`nan != nan`, `true`},
		{`nan < 1`, `false`}, {`nan <= nan`, `false`}, {`1 > nan`, `false`}, {`nan >= 0`, `false`},
		{`[nan] == [nan]`, `false`}, {`{x:nan} != {x:nan}`, `true`},
		{`nan+1`, `nan`}, {`nan*0`, `nan`}, {`nan/0`, `nan`}, {`1/nan`, `nan`},
		{`nan//0`, `nan`}, {`nan%0`, `nan`}, {`nan ** 0`, `1`}, {`1 ** nan`, `1`},
		{`0 ** nan`, `nan`}, {`nan ** nan`, `nan`}, {`nan */ 1`, `nan`},
		{`match nan {nan=>"literal",number=>"number",_=>"other"}`, `number`},
		{`is_nan(MISSING)`, `true`}, {`str([nan,{x:nan}])`, `[nan,{"x":nan}]`},
		{`{nan:nan}.nan`, `nan`},
		{`inf`, `inf`}, {`+inf`, `inf`}, {`-inf`, `-inf`}, {`LIMITS`, `[inf,-inf]`},
		{`parse_number("inf")`, `inf`}, {`parse_number("+inf")`, `inf`}, {`parse_number("-inf")`, `-inf`},
		{`inf == inf`, `true`}, {`inf == -inf`, `false`}, {`inf != nan`, `true`},
		{`-inf < -1e308 && 1e308 < inf`, `true`}, {`inf <= inf && -inf >= -inf`, `true`},
		{`inf < nan || nan >= -inf`, `false`}, {`[inf,-inf] == LIMITS`, `true`},
		{`inf + 1`, `inf`}, {`1 - inf`, `-inf`}, {`inf + -inf`, `nan`}, {`inf - inf`, `nan`},
		{`-inf * -2`, `inf`}, {`0 * inf`, `nan`}, {`inf / -2`, `-inf`}, {`1 / -inf`, `0`},
		{`inf / inf`, `nan`}, {`nan + inf`, `nan`}, {`-inf // 2`, `nan`}, {`inf % -2`, `nan`},
		{`inf // inf`, `nan`}, {`inf % inf`, `nan`}, {`1 // inf`, `0`}, {`-1 // inf`, `-1`},
		{`1 // -inf`, `-1`}, {`-1 // -inf`, `0`}, {`0 // -inf`, `0`}, {`0 % -inf`, `0`},
		{`1 % inf`, `1`}, {`-1 % inf`, `inf`}, {`1 % -inf`, `-inf`}, {`-1 % -inf`, `-1`},
		{`inf ** 0`, `1`}, {`1 ** inf`, `1`}, {`(-1) ** -inf`, `1`}, {`nan ** inf`, `nan`},
		{`inf ** nan`, `nan`}, {`0 ** inf`, `0`}, {`0 ** -inf`, `inf`}, {`0.5 ** inf`, `0`},
		{`0.5 ** -inf`, `inf`}, {`(-2) ** inf`, `inf`}, {`(-2) ** -inf`, `0`},
		{`inf ** inf`, `inf`}, {`inf ** -inf`, `0`}, {`(-inf) ** 3`, `-inf`},
		{`(-inf) ** 2`, `inf`}, {`(-inf) ** 0.5`, `inf`}, {`(-inf) ** -3`, `0`},
		{`(-inf) ** 9007199254740991`, `-inf`}, {`(-inf) ** 9007199254740992`, `inf`},
		{`inf */ 2`, `inf`}, {`inf */ 0.5`, `-inf`}, {`inf */ inf`, `nan`},
		{`2 */ inf`, `0`}, {`0.5 */ inf`, `0`}, {`1 */ inf`, `0`},
		{`match -inf {-inf=>true,inf=>false,_=>false}`, `true`},
		{`match inf {inf=>true,number=>false}`, `true`}, {`{inf:inf}.inf`, `inf`},
	}
	var expressions, expected []string
	for _, test := range cases {
		expressions = append(expressions, "str("+test.expression+")")
		v, _ := StringValue(test.want)
		b, _ := EncodeJSON(v)
		expected = append(expected, string(b))
	}
	source := `MISSING:number=nan
LIMITS:list=[+inf,-inf]
fn is_nan(x:number)->bool{x != x}
flow Main()->list{return [` + strings.Join(expressions, ",") + `]}`
	want := "[" + strings.Join(expected, ",") + "]"
	p, err := Compile(source, Host{})
	if err != nil {
		t.Fatal(err)
	}
	r, err := p.Run(context.Background(), nil, Options{})
	if err != nil || display(t, r.Output) != want {
		t.Fatal(r, err)
	}
	executable, _ := buildNative(t, p, GoOptions{})
	stdout, stderr, err := runNative(t, executable)
	if err != nil || strings.TrimSpace(stdout) != want {
		t.Fatal(stdout, stderr, err)
	}
}

func TestSpec11NaNFailures(t *testing.T) {
	for _, test := range []struct{ expression, code string }{
		{`0/0`, "E-NUMBER"}, {`(-1)**0.5`, "E-NUMBER"}, {`1e308*2`, "E-NUMBER"},
		{`parse_number("NaN")`, "E-VALUE"}, {`parse_number("+nan")`, "E-VALUE"}, {`parse_number("-nan")`, "E-VALUE"},
		{`nan+"x"`, "E-TYPE"}, {`[1][nan]`, "E-INDEX"}, {`[1][nan:]`, "E-INDEX"},
		{`range(nan)`, "E-VALUE"}, {`range(0,2,nan)`, "E-VALUE"},
		{`inf+"x"`, "E-TYPE"}, {`parse_number("Inf")`, "E-VALUE"},
		{`parse_number("Infinity")`, "E-VALUE"}, {`parse_number(" inf")`, "E-VALUE"},
	} {
		t.Run(test.expression, func(t *testing.T) {
			p, err := Compile(`flow Main()->any{return `+test.expression+`}`, Host{})
			if err == nil {
				_, err = p.Run(context.Background(), nil, Options{})
			}
			if err == nil || !strings.Contains(err.Error(), test.code) {
				t.Fatal(err)
			}
		})
	}
}

func TestSpec11InfinityErrorsInterpreterAndGo(t *testing.T) {
	cases := []struct{ expression, code string }{
		{`inf/0`, "E-NUMBER"}, {`-inf//0`, "E-NUMBER"}, {`inf%0`, "E-NUMBER"},
		{`inf */ 1`, "E-NUMBER"}, {`(-inf) */ 2`, "E-NUMBER"}, {`2 */ -inf`, "E-NUMBER"},
		{`0 */ inf`, "E-NUMBER"}, {`[1][inf]`, "E-INDEX"}, {`[1][:inf]`, "E-INDEX"},
		{`range(inf)`, "E-VALUE"}, {`range(0,2,-inf)`, "E-VALUE"},
		{`1e308*2`, "E-NUMBER"}, {`2**1024`, "E-NUMBER"}, {`0**-1`, "E-NUMBER"},
	}
	var arms []string
	for i, test := range cases {
		arms = append(arms, fmt.Sprintf("%d=>%s", i, test.expression))
	}
	p, err := Compile(`flow Main(i:number)->any{return match i {`+strings.Join(arms, ",")+`,_=>null}}`, Host{})
	if err != nil {
		t.Fatal(err)
	}
	executable, _ := buildNative(t, p, GoOptions{})
	for i, test := range cases {
		t.Run(test.expression, func(t *testing.T) {
			_, err := p.Run(context.Background(), map[string]Value{"i": number(float64(i))}, Options{})
			if err == nil || !strings.Contains(err.Error(), test.code) {
				t.Fatal(err)
			}
			_, stderr, err := runNative(t, executable, "-input", fmt.Sprintf(`{"i":%d}`, i))
			if err == nil || !strings.Contains(stderr, test.code) {
				t.Fatal(stderr, err)
			}
		})
	}
}

func TestSpec11CanonicalAndTransport(t *testing.T) {
	for _, bits := range []uint64{0x7ff8000000000000, 0x7ff0000000000001, 0xfff8000000000123} {
		v, err := NumberValue(math.Float64frombits(bits))
		if err != nil {
			t.Fatal(err)
		}
		b, _ := Canonical(v)
		if hex.EncodeToString(b) != "037ff8000000000000" {
			t.Fatalf("%x", b)
		}
		x, err := FromNative(math.Float64frombits(bits))
		if err != nil {
			t.Fatal(err)
		}
		if b2, _ := Canonical(x); !bytes.Equal(b, b2) {
			t.Fatal("native NaN not normalized")
		}
	}
	nan, _ := NumberValue(math.NaN())
	text, _ := StringValue("nan")
	v, _ := ObjectValue(map[string]Value{"nan": nan, "text": text, "list": ListValue(nan, number(1), number(math.Inf(1)), number(math.Inf(-1)))})
	if _, err := EncodeJSON(v); err == nil {
		t.Fatal("NaN escaped into JSON")
	}
	b, err := EncodeData(v, "c-hex")
	if err != nil {
		t.Fatal(err)
	}
	x, err := DecodeDataWithEncoding(b, "c-hex")
	if err != nil {
		t.Fatal(err)
	}
	c1, _ := Canonical(v)
	c2, _ := Canonical(x)
	if !bytes.Equal(c1, c2) {
		t.Fatal("lossy round trip")
	}
	for _, malformed := range []string{
		`"037FF8000000000000"`, `"0"`, `""`, `"00ff"`, `"07"`,
		`"038000000000000000"`, `"037ff0000000000001"`,
		`"04ffffffffffffffff"`, `"05000000000000000200"`, `"040000000000000001ff"`,
		`"06000000000000000204000000000000000162000400000000000000016100"`,
		`"06000000000000000204000000000000000161000400000000000000016100"`,
		`null`, `{}`, `{"$number":"nan"}`,
	} {
		if _, err := DecodeDataWithEncoding([]byte(malformed), "c-hex"); err == nil {
			t.Fatalf("accepted %s", malformed)
		}
	}
	for _, test := range []struct {
		n            float64
		hex, display string
	}{
		{math.Inf(1), "037ff0000000000000", "inf"}, {math.Inf(-1), "03fff0000000000000", "-inf"},
	} {
		x, err := FromNative(test.n)
		got, displayErr := Display(x)
		if err != nil || displayErr != nil || got != test.display {
			t.Fatal(x, err)
		}
		wire, err := EncodeData(x, "c-hex")
		if err != nil || string(wire) != `"`+test.hex+`"` {
			t.Fatal(string(wire), err)
		}
		y, err := DecodeDataWithEncoding(wire, "c-hex")
		equal, equalErr := Equal(x, y)
		if err != nil || equalErr != nil || !equal {
			t.Fatal(y, err, equalErr)
		}
		if _, err := EncodeJSON(ListValue(x)); err == nil {
			t.Fatal("infinity escaped into JSON")
		}
	}
	for _, raw := range []string{`inf`, `Infinity`, `-Infinity`, `NaN`, `1e309`, `[Infinity]`} {
		if _, err := DecodeData([]byte(raw)); err == nil {
			t.Fatal("accepted invalid JSON", raw)
		}
	}
	ordinary, err := DecodeDataWithEncoding([]byte(`"037ff8000000000000"`), "json")
	if err != nil || ordinary.Kind() != String {
		t.Fatal("profile inferred from shape", err)
	}
	input, _ := ObjectValue(map[string]Value{"positional": ListValue(nan), "named": func() Value { v, _ := ObjectValue(map[string]Value{"text": text}); return v }()})
	wire, err := EncodeEntryInput(input, "c-hex")
	if err != nil || string(wire) != `{"named":{"text":"0400000000000000036e616e"},"positional":["037ff8000000000000"]}` {
		t.Fatal(string(wire), err)
	}
	decoded, err := DecodeEntryInputWithEncoding(wire, "c-hex")
	if err != nil {
		t.Fatal(err)
	}
	a, _ := Canonical(input)
	b2, _ := Canonical(decoded)
	if !bytes.Equal(a, b2) {
		t.Fatal("entry arguments were not encoded individually")
	}
	for _, profile := range []string{"c-hex", "bad"} {
		if _, err := DecodeEntryInputWithEncoding([]byte(`{"positional":["037ff0000000000001"],"named":{}}`), profile); err == nil || !strings.Contains(err.Error(), "E-SCHEMA") {
			t.Fatal(err)
		}
	}
}

func TestSpec11PurityScopesAndCallIDs(t *testing.T) {
	var calls atomic.Int64
	host := testHost(func(_ context.Context, r Request) Response { calls.Add(1); return Result(r.Positional[0]) })
	host.Contracts[0].Effect = UnknownEffect
	host.Contracts[0].Concurrent = false
	source := `import python "example.worker" as worker
fn native(x:number)->number @{perform effect worker.read(x)}
fn outer(x:number)->number {
 inner=fn(y:number)->number @{perform effect worker.read(y)}
 inner(x)
}
fn rec(n:number)->number @{if n==0 {0} else {perform effect worker.read(n)+rec(n-1)}}
fn tail(n:number)->number @{if n==0 {0} else {v=perform effect worker.read(n);tail(n-1)}}
fn run(p:effect)->number @{perform p}
fn make(x:number)->effect {effect worker.read(x)}
flow Main()->list {
 facts=claim()
 for x in [1,2,3] parallel {facts <+ outer(x)}
 f=native
 return [native(4),native(5),map([6,7],f),fold([8,9],0,fn(a:number,b:number)->number @{a+perform effect worker.read(b)}),rec(3),tail(3),run([make(10)][0]),[row.value for row in facts]]
}`
	p, err := Compile(source, host)
	if err != nil {
		t.Fatal(err)
	}
	r, err := p.Run(context.Background(), nil, Options{Parallelism: 8, RunID: "purity"})
	if err != nil || display(t, r.Output) != `[4,5,[6,7],17,6,0,10,[1,2,3]]` {
		t.Fatal(r, err)
	}
	if calls.Load() != 16 || len(r.Journal.Records()) != 16 {
		t.Fatal(calls.Load(), len(r.Journal.Records()))
	}
	seen := map[string]bool{}
	for _, record := range r.Journal.Records() {
		id := display(t, record.Request.CallID.Wire())
		if seen[id] || len(record.Request.CallID.EvalPath) == 0 {
			t.Fatal("reused or missing function invocation identity", id)
		}
		seen[id] = true
		if len(record.Request.CallID.EvalPath) > 2 {
			t.Fatal("function frames accumulated", id)
		}
	}
	_, err = p.Run(context.Background(), nil, Options{Parallelism: 1, RunID: "purity", Journal: r.Journal})
	if err != nil || calls.Load() != 16 {
		t.Fatal("replay invoked provider", calls.Load(), err)
	}
	for _, body := range []string{
		`fn bad(x:number)->number {perform effect worker.read(x)}`,
		`fn bad(x:number)->number @{inner=fn(y:number)->number {perform effect worker.read(y)};inner(x)}`,
		`fn bad(x:number)->number @{for y in [x] parallel {z=y};x}`,
		`fn bad(x:number)->number @{Other(x)} flow Other(x:number)->number {return x}`,
		`fn bad(x:number)->number @{facts=claim();x}`,
	} {
		if _, err := Compile(`import python "example.worker" as worker; `+body, host); err == nil || !(strings.Contains(err.Error(), "E-PURITY") || strings.Contains(err.Error(), "E-PARALLEL")) {
			t.Fatalf("%s: %v", body, err)
		}
	}
	if _, err := Compile(`import python "example.worker" as worker; fn make()->effect @{effect worker.read(1)} flow Main()->void {for x in [1] parallel {perform make()}}`, host); err == nil {
		t.Fatal("marker leaked into returned request Ops")
	}
}

func TestSpec11NativeAdaptersAndGeneratedEntry(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("Python 3 unavailable")
	}
	host := NewNativeHost(Host{})
	defer host.Close()
	source := `import python "math" as py
import go "math" as gm
fn outer(x:number)->number {inner=fn(y:number)->number @{py.sqrt(y)};inner(x)}
fn square(x:number)->number @{gm.Sqrt(x)}
fn native_nan()->number @{py.nan}
fn native_inf()->number @{py.inf}
flow Main(x:number,y:number)->list {return [outer(9),square(16),square(x),native_nan(),py.isnan(x),native_inf(),gm.Inf(-1),py.isinf(y),gm.IsInf(y,1),gm.Abs(-y),gm.Float32bits(y),py.copysign(y,-1)]}`
	p, err := Compile(source, host)
	if err != nil {
		t.Fatal(err)
	}
	nan, _ := NumberValue(math.NaN())
	r, err := p.Run(context.Background(), map[string]Value{"x": nan, "y": number(math.Inf(1))}, Options{Parallelism: 4})
	if err != nil {
		t.Fatal(err)
	}
	got, _ := Display(r.Output)
	if got != `[3,4,nan,nan,true,inf,-inf,true,true,inf,2139095040,-inf]` {
		t.Fatal(got)
	}
	executable, _ := buildNative(t, p, GoOptions{})
	out, stderr, err := runNative(t, executable, "-data-encoding", "c-hex", "-input", `{"x":"037ff8000000000000","y":"037ff0000000000000"}`, "-entry-output", "-parallel", "4")
	want, _ := EncodeEntryOutput(*r.EntryOutput, "c-hex")
	if err != nil || strings.TrimSpace(out) != string(want) {
		t.Fatal(out, stderr, err)
	}
	_, stderr, err = runNative(t, executable, "-input", `{"x":2,"y":3}`)
	if err == nil || !strings.Contains(stderr, "JSON cannot encode NaN") {
		t.Fatal(stderr, err)
	}
	s, err := NewInteractiveSession(Host{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, cell := range []string{`import python "math" as py`, `fn root(x:number)->number @{if x>=0 {py.sqrt(x)} else {nan}}`, `f=root`} {
		if _, err := s.Eval(context.Background(), cell, Options{}); err != nil {
			t.Fatal(cell, err)
		}
	}
	result, err := s.Eval(context.Background(), `[f(9),f(-1),f(inf),py.inf]`, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := Display(result.Value); got != `[3,nan,inf,inf]` {
		t.Fatal(got)
	}
	contract := Contract{Operation: Operation{Language: "python", Path: "math", Member: "isnan"}, Params: []Parameter{{Name: "x", Schema: Schema{Type: "number"}}}, Result: Schema{Type: "bool"}}
	response := PythonHandler("python3", contract)(context.Background(), Request{Positional: []Value{nan}})
	if response.Error != nil {
		t.Fatal(response.Error)
	}
	if got, _ := Display(response.Value); got != "true" {
		t.Fatal(got)
	}
	goContract := Contract{Params: []Parameter{{Name: "x", Schema: Schema{Type: "number"}}}, Result: Schema{Type: "number"}}
	goHandler, err := GoHandler(goContract, func(x float32) float32 { return x })
	if err != nil {
		t.Fatal(err)
	}
	response = goHandler(context.Background(), Request{Positional: []Value{nan}})
	if response.Error != nil {
		t.Fatal(response.Error)
	}
	if got, _ := Display(response.Value); got != "nan" {
		t.Fatal(got)
	}
	for _, n := range []float64{math.Inf(1), math.Inf(-1)} {
		v := number(n)
		response = goHandler(context.Background(), Request{Positional: []Value{v}})
		if response.Error != nil || num(response.Value) != n {
			t.Fatal(response)
		}
		contract.Operation.Member = "fabs"
		contract.Result.Type = "number"
		response = PythonHandler("python3", contract)(context.Background(), Request{Positional: []Value{v}})
		if response.Error != nil || num(response.Value) != math.Inf(1) {
			t.Fatal(response)
		}
	}
	integerHandler, err := GoHandler(goContract, func(x int) float64 { t.Fatal("infinity reached integer provider"); return 0 })
	if err != nil {
		t.Fatal(err)
	}
	if response := integerHandler(context.Background(), Request{Positional: []Value{number(math.Inf(1))}}); response.Error == nil {
		t.Fatal("infinity converted to integer")
	}
}

func TestSpec11NonConcurrentAdmission(t *testing.T) {
	for _, parallel := range []int{1, 8} {
		t.Run(fmt.Sprint(parallel), func(t *testing.T) {
			var active, maxActive atomic.Int64
			host := testHost(func(ctx context.Context, r Request) Response {
				n := active.Add(1)
				for old := maxActive.Load(); n > old && !maxActive.CompareAndSwap(old, n); old = maxActive.Load() {
				}
				defer active.Add(-1)
				select {
				case <-time.After(time.Millisecond):
					return Result(r.Positional[0])
				case <-ctx.Done():
					return Rejected("E-CANCELLED", ctx.Err().Error())
				}
			})
			host.Contracts[0].Concurrent = false
			host.Contracts[0].Resources = map[string]uint64{"worker": 1}
			host.Resources = NewResourcePool(map[string]uint64{"worker": 2})
			p, err := Compile(`import python "example.worker" as worker; fn f(x:number)->number @{perform effect worker.read(x)} flow Main()->list {facts=claim();for x in range(12) parallel {facts <+ f(x)};return [row.value for row in facts]}`, host)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			var wg sync.WaitGroup
			for i := 0; i < 3; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					if _, err := p.Run(ctx, nil, Options{Parallelism: parallel}); err != nil {
						t.Error(err)
					}
				}()
			}
			wg.Wait()
			if maxActive.Load() != 1 || active.Load() != 0 {
				t.Fatal("Concurrent=false was bypassed", maxActive.Load(), active.Load())
			}
		})
	}
}

func TestSpec11BoundariesReplayAndErrors(t *testing.T) {
	var calls atomic.Int64
	host := testHost(func(_ context.Context, r Request) Response { calls.Add(1); return Result(r.Positional[0]) })
	source := `import python "example.worker" as worker
fn f(x:number)->number @{perform effect worker.read(x)}
flow Main(x:number)->list {facts=claim();for y in [x,x] parallel {facts <+ f(y)};return [row.value for row in facts]}`
	p, err := Compile(source, host)
	if err != nil {
		t.Fatal(err)
	}
	nan, _ := NumberValue(math.NaN())
	inputs := map[string]Value{"x": nan}
	r, err := p.Run(context.Background(), inputs, Options{RunID: "nan-replay", Parallelism: 4})
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := Display(r.Output); got != `[nan,nan]` {
		t.Fatal(got)
	}
	inputs["x"], _ = NumberValue(math.Float64frombits(0xfff0000000000001))
	if _, err = p.Run(context.Background(), inputs, Options{RunID: "nan-replay", Parallelism: 1, Journal: r.Journal}); err != nil || calls.Load() != 2 {
		t.Fatal(calls.Load(), err)
	}
	inputs["x"] = number(1)
	if _, err = p.Run(context.Background(), inputs, Options{RunID: "nan-replay", Journal: r.Journal}); err == nil {
		t.Fatal("changed canonical arguments accepted on replay")
	}
	for i, x := range []float64{math.Inf(1), math.Inf(-1)} {
		inputs["x"] = number(x)
		runID := fmt.Sprintf("inf-replay-%d", i)
		before := calls.Load()
		report, err := p.Run(context.Background(), inputs, Options{RunID: runID, Parallelism: 4})
		if err != nil {
			t.Fatal(err)
		}
		want, _ := Display(ListValue(inputs["x"], inputs["x"]))
		if got, _ := Display(report.Output); got != want {
			t.Fatal(got, want)
		}
		if _, err := p.Run(context.Background(), inputs, Options{RunID: runID, Journal: report.Journal}); err != nil || calls.Load() != before+2 {
			t.Fatal(calls.Load(), err)
		}
		inputs["x"] = number(-x)
		if _, err := p.Run(context.Background(), inputs, Options{RunID: runID, Journal: report.Journal}); err == nil {
			t.Fatal("infinity signs conflated on replay")
		}
	}
	for _, test := range []struct{ source, code string }{
		{`fn f(x:number)->number @{x} flow Main()->number{return f("bad")}`, "E-TYPE"},
		{`fn f(x:any)->number @{x} flow Main()->number{return f("bad")}`, "E-TYPE"},
		{`fn f()->number @{1} flow Main()->number{return f(perform effect worker.read(1))}`, "E-ARITY"},
		{`fn f()->number @{1} flow Main()->number{return f()}`, ""},
	} {
		before := calls.Load()
		p, err := Compile("import python \"example.worker\" as worker\n"+test.source, host)
		if err == nil {
			_, err = p.Run(context.Background(), nil, Options{})
		}
		if test.code == "" {
			if err != nil {
				t.Fatal(err)
			}
		} else if err == nil || !strings.Contains(err.Error(), test.code) {
			t.Fatal(err)
		}
		if calls.Load() != before {
			t.Fatal("invalid call executed arguments/body")
		}
		if d, ok := err.(*Diagnostic); ok && d.Phase == "runtime" && strings.Contains(test.source, "x:any") && len(d.EvalPath) == 0 {
			t.Fatal("return annotation lost function EvalPath", d)
		}
	}
	for _, source := range []string{`@{1}`, "fn f()->number @\n{1}", "flow Main()->any{return (fn()->number @\n{1})()}", `nan=1`, `inf=1`} {
		if _, err := Compile(source, Host{}); err == nil {
			t.Fatal("accepted invalid marker/reserved binding", source)
		}
	}
	for _, value := range []string{"nan", "inf", "-inf"} {
		httpProgram, err := Compile(`flow Main(request:object)->object {return {status:200,json:[`+value+`]}}`, Host{})
		if err != nil {
			t.Fatal(err)
		}
		var httpError error
		handler, err := NewHTTPHandler(httpProgram, HTTPOptions{OnError: func(_ *http.Request, _ Report, err error) { httpError = err }})
		if err != nil {
			t.Fatal(err)
		}
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest("GET", "/", nil))
		if recorder.Code != 500 || httpError == nil || !strings.Contains(httpError.Error(), "E-HTTP") {
			t.Fatal(recorder.Code, recorder.Body.String())
		}

	}
}
