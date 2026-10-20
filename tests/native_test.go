package tests

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	. "lip/script"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func nativeCell(t *testing.T, s *Session, source, want string) Value {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	result, err := s.Eval(ctx, source, Options{Parallelism: 4})
	if err != nil {
		t.Fatalf("%s: %v", source, err)
	}
	if want != "" && display(t, result.Value) != want {
		t.Fatalf("%s: got %s want %s", source, display(t, result.Value), want)
	}
	return result.Value
}

type bridgeCounter struct{ Value int }

func (c *bridgeCounter) Add(n int) int { c.Value += n; return c.Value }

type sharedNativeLeaf struct{ Value int }
type sharedNativeRoot struct {
	Leaf  sharedNativeLeaf
	Alias *sharedNativeLeaf
}

func sharedNativeValue(mode string) any {
	switch mode {
	case "prefix":
		xs := []any{7, nil}
		xs[1] = xs[:1]
		return xs
	case "empty":
		xs := make([]any, 1)
		xs[0] = xs[:0]
		return xs
	case "shared":
		leaf := []int{7}
		return []any{leaf, leaf}
	case "typed":
		root := &sharedNativeRoot{Leaf: sharedNativeLeaf{7}}
		root.Alias = &root.Leaf
		return root
	case "slice_cycle":
		xs := make([]any, 1)
		xs[0] = xs
		return xs
	case "map_cycle":
		m := map[string]any{}
		m["self"] = m
		return m
	case "mixed_cycle":
		xs := make([]any, 1)
		m := map[string]any{"list": xs}
		xs[0] = m
		return m
	case "pointer_cycle":
		var x any
		x = &x
		return &x
	}
	return nil
}

func TestNativeSharedDataSnapshots(t *testing.T) {
	host := NewNativeHost(Host{})
	defer host.Close()
	var calls atomic.Int64
	host.Native.Go.RegisterPackage("application/shared", map[string]any{"Value": func(mode string) any {
		calls.Add(1)
		return sharedNativeValue(mode)
	}})
	source := `import go "application/shared" as shared
flow Main(mode:string)->any {
 return if mode=="skip" {7} else {native_data(shared.Value(mode))}
}`
	p, err := Compile(source, host)
	if err != nil {
		t.Fatal(err)
	}
	executable, _ := buildNative(t, p, GoOptions{HostSource: `package main
type sharedNativeLeaf struct{Value int}
type sharedNativeRoot struct{Leaf sharedNativeLeaf; Alias *sharedNativeLeaf}
func sharedNativeValue(mode string) any {
 switch mode {
 case "prefix": xs:=[]any{7,nil};xs[1]=xs[:1];return xs
 case "empty": xs:=make([]any,1);xs[0]=xs[:0];return xs
 case "shared": leaf:=[]int{7};return []any{leaf,leaf}
 case "typed": root:=&sharedNativeRoot{Leaf:sharedNativeLeaf{7}};root.Alias=&root.Leaf;return root
 case "slice_cycle": xs:=make([]any,1);xs[0]=xs;return xs
 case "map_cycle": m:=map[string]any{};m["self"]=m;return m
 case "mixed_cycle": xs:=make([]any,1);m:=map[string]any{"list":xs};xs[0]=m;return m
 case "pointer_cycle": var x any;x=&x;return &x
 };return nil
}
func generatedHost()(Host,error){host:=NewNativeHost(Host{});host.Native.Go.RegisterPackage("application/shared",map[string]any{"Value":sharedNativeValue});return host,nil}
`})
	s, err := NewSession(host)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	nativeCell(t, s, source, "")
	for _, test := range []struct{ mode, want string }{
		{"skip", `7`}, {"prefix", `[7,[7]]`}, {"empty", `[[]]`}, {"shared", `[[7],[7]]`},
		{"typed", `{"Alias":{"Value":7},"Leaf":{"Value":7}}`},
		{"slice_cycle", ""}, {"map_cycle", ""}, {"mixed_cycle", ""}, {"pointer_cycle", ""},
	} {
		t.Run(test.mode, func(t *testing.T) {
			input := `{"mode":"` + test.mode + `"}`
			for _, parallel := range []int{1, 7} {
				before := calls.Load()
				report, runErr := p.Run(context.Background(), map[string]Value{"mode": textValue(test.mode)}, Options{Parallelism: parallel})
				stdout, stderr, nativeErr := runNative(t, executable, "-input", input, "-parallel", fmt.Sprint(parallel))
				if test.want != "" {
					if runErr != nil || nativeErr != nil || display(t, report.Output) != test.want || strings.TrimSpace(stdout) != test.want {
						t.Fatal(report, runErr, stdout, stderr, nativeErr)
					}
				} else if d, ok := runErr.(*Diagnostic); !ok || d.Code != "E-VALUE" || report.EntryOutput != nil || nativeErr == nil || stdout != "" || !strings.Contains(stderr, runErr.Error()) {
					t.Fatal("snapshot conversion boundary mismatch", report, runErr, stdout, stderr, nativeErr)
				}
				if test.mode == "skip" && calls.Load() != before {
					t.Fatal("unselected snapshot called provider")
				}
			}
			cell := `Main("` + test.mode + `")`
			if test.want != "" {
				nativeCell(t, s, cell, test.want)
			} else {
				result, err := s.Eval(context.Background(), `uncommitted=`+cell, Options{})
				if d, ok := err.(*Diagnostic); !ok || d.Code != "E-VALUE" || result.HasValue || result.Report.EntryOutput != nil {
					t.Fatal("invalid session snapshot", result, err)
				}
				if _, committed := s.Values()["uncommitted"]; committed {
					t.Fatal("failed snapshot committed its binding")
				}
				nativeCell(t, s, `Main("prefix")`, `[7,[7]]`)
			}
		})
	}
}

func TestNativeRunOwnershipAndFailedCellCleanup(t *testing.T) {
	host := NewNativeHost(Host{})
	defer host.Close()
	host.Native.Go.RegisterPackage("application/ownership", map[string]any{"New": func() *bridgeCounter { return &bridgeCounter{Value: 7} }})
	s, err := NewSession(host)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	nativeCell(t, s, `import go "application/ownership" as lib`, "")
	nativeCell(t, s, `saved=lib.New()`, "")
	count := func() int { return reflect.ValueOf(host.Native.Go).Elem().FieldByName("objects").Len() }
	baseline := count()
	for _, source := range []string{`abandoned=lib.New();fail("stop")`, `unused=lib.New();parse_number("01")`} {
		if _, err := s.Eval(context.Background(), source, Options{}); err == nil {
			t.Fatal("failed cell succeeded")
		}
		if count() != baseline {
			t.Fatal("failed cell retained new native handles", count(), baseline)
		}
		nativeCell(t, s, `saved.Value`, "7")
	}
	nativeCell(t, s, `captured=if true {r=lib.New();fn()->object{r}}else{fn()->object{saved}}`, "")
	nativeCell(t, s, `native_data(captured())`, `{"Value":7}`)
	stale := s.Values()["saved"]
	host.Native.Go.RegisterPackage("application/old", map[string]any{"Get": func() Value { return stale }})
	p, err := Compile(`import go "application/old" as old;flow Main()->any{return old.Get().Value}`, host)
	if err != nil {
		t.Fatal(err)
	}
	_, err = p.Run(context.Background(), nil, Options{})
	if d, ok := err.(*Diagnostic); !ok || d.Code != "E-RESOURCE" {
		t.Fatal("run accepted a session-owned reference", err)
	}
	nativeCell(t, s, `native_release(saved)`, "true")
	_, err = s.Eval(context.Background(), `saved.Value`, Options{})
	if d, ok := err.(*Diagnostic); !ok || d.Code != "E-RESOURCE" {
		t.Fatal("released reference diagnostic", err)
	}
}

func TestGeneratedGoLibrariesWithoutRuntimeToolchain(t *testing.T) {
	host := NewNativeHost(Host{})
	defer host.Close()
	p, err := Compile(`import go "slices"
import go "strings"
flow Main()->object {
 numbers=slices.Clone([3,1,2])
 slices.Sort(numbers)
 words=slices.Clone(["b","a"])
 slices.Sort(words)
 measured=slices.Clone([strings.Count("aba","a")])
 return {numbers:native_data(numbers),words:native_data(words),measured:native_data(measured),named:strings.Replace(s="ababa",old="a",new="x",n=1)}
}`, host)
	if err != nil {
		t.Fatal(err)
	}
	executable, _ := buildNative(t, p, GoOptions{})
	cmd := exec.Command(executable)
	cmd.Env = append(os.Environ(), "PATH="+t.TempDir(), "GOPROXY=off")
	output, err := cmd.CombinedOutput()
	want := `{"measured":[2],"named":"xbaba","numbers":[1,2,3],"words":["a","b"]}`
	if err != nil || strings.TrimSpace(string(output)) != want {
		t.Fatal(string(output), err)
	}
}

func TestNativePythonFailedCellCleanup(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("Python unavailable")
	}
	dir := t.TempDir()
	source := `import gc, weakref
refs=[]
class Token:
    def read(self): return 7
def make():
    obj=Token()
    refs.append(weakref.ref(obj))
    return obj
def alive():
    gc.collect()
    return sum(ref() is not None for ref in refs)
`
	if err := os.WriteFile(filepath.Join(dir, "lip_ownership_fixture.py"), []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PYTHONPATH", dir)
	s, err := NewInteractiveSession(Host{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	nativeCell(t, s, `import python "lip_ownership_fixture" as lib`, "")
	nativeCell(t, s, `saved=lib.make()`, "")
	if _, err := s.Eval(context.Background(), `abandoned=lib.make();parse_number("01")`, Options{}); err == nil {
		t.Fatal("failed cell succeeded")
	}
	nativeCell(t, s, `lib.alive()`, "1")
	nativeCell(t, s, `lib.make();1`, "1")
	nativeCell(t, s, `lib.alive()`, "1")
	nativeCell(t, s, `capture=if true {r=lib.make();fn()->object{r}}else{fn()->object{saved}}`, "")
	nativeCell(t, s, `lib.alive()`, "2")
	nativeCell(t, s, `captured=capture();captured.read()`, "7")
	nativeCell(t, s, `native_release(saved)`, "true")
	_, err = s.Eval(context.Background(), `saved.read()`, Options{})
	if d, ok := err.(*Diagnostic); !ok || d.Code != "E-RESOURCE" {
		t.Fatal(err)
	}
	nativeCell(t, s, `lib.alive()`, "1")
	nativeCell(t, s, `pending=effect lib.make()`, "")
	nativeCell(t, s, `from_request=perform pending;from_request.read()`, "7")
	nativeCell(t, s, `native_release(from_request)`, "true")
	nativeCell(t, s, `lib.alive()`, "1")
}

func TestSessionNativeLargeDataAllocation(t *testing.T) {
	s, err := NewInteractiveSession(Host{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	nativeCell(t, s, `xs=range(250000)`, "")
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	result, err := s.Eval(context.Background(), `len(xs)`, Options{})
	runtime.ReadMemStats(&after)
	if err != nil || display(t, result.Value) != "250000" {
		t.Fatal(result, err)
	}
	// A small cell must not allocate a second per-element metadata array for
	// retained pure data. Leave ample room for ordinary compilation overhead.
	if allocated := after.TotalAlloc - before.TotalAlloc; allocated > 4<<20 {
		t.Fatalf("small cell allocated %d bytes for retained pure data", allocated)
	}
}

func TestSessionNativeMetadataDoesNotRetainInitializers(t *testing.T) {
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	s := func() *Session {
		s, err := NewInteractiveSession(Host{})
		if err != nil {
			t.Fatal(err)
		}
		source := `text="` + strings.Repeat("x", 12<<20) + `";size=len(text);count=fn()->number{size}`
		if _, err := s.Eval(context.Background(), source, Options{}); err != nil {
			t.Fatal(err)
		}
		if err := s.Delete("text", "size"); err != nil {
			t.Fatal(err)
		}
		return s
	}()
	defer s.Close()
	runtime.GC()
	runtime.ReadMemStats(&after)
	if after.HeapAlloc > before.HeapAlloc+6<<20 {
		t.Fatalf("closure retained initializer data: before=%d after=%d", before.HeapAlloc, after.HeapAlloc)
	}
	nativeCell(t, s, `count()`, "12582912")
}

func TestNativeSnapshotsAndContainerPurity(t *testing.T) {
	for _, body := range []string{
		`text=strings.ToUpper("abc"); size=fn()->number{len(text)};return size()`,
		`xs=native_data(strings.Split("a,b,c",",")); size=fn()->number{len(xs)}; return size()`,
		`resource=strings.Split("a,b,c",","); box={resource:resource,label:"abc"}; size=fn()->number{len(box.label)}; return size()`,
		`resource=strings.Split("a,b,c",","); box=[resource,"abc"]; size=fn()->number{len(box[1])}; return size()`,
		`resource=strings.Split("a,b,c",","); box={resource:resource,label:"abc"}; read=fn()->object{box}; copy=read(); return len(copy.resource)`,
	} {
		t.Run(body, func(t *testing.T) {
			host := NewNativeHost(Host{})
			defer host.Close()
			source := `import go "strings"
flow Main()->number {` + body + `}`
			p, err := Compile(source, host)
			if err != nil {
				t.Fatal(err)
			}
			r, err := p.Run(context.Background(), nil, Options{})
			if err != nil || display(t, r.Output) != "3" {
				t.Fatal(r, err)
			}
			executable, _ := buildNative(t, p, GoOptions{})
			stdout, stderr, err := runNative(t, executable)
			if err != nil || strings.TrimSpace(stdout) != "3" {
				t.Fatal(stdout, stderr, err)
			}
			s, err := NewInteractiveSession(Host{})
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			nativeCell(t, s, source, "")
			nativeCell(t, s, `Main()`, "3")
		})
	}
}

func TestGoAdapterApplicationExportsAndResources(t *testing.T) {
	var calls atomic.Int64
	host := NewNativeHost(Host{})
	host.Native.Go.RegisterPackage("application/counter", map[string]any{
		"New":  func(n int) *bridgeCounter { calls.Add(1); return &bridgeCounter{Value: n} },
		"Read": func(c *bridgeCounter) int { return c.Value },
		"Sum": func(xs ...int) int {
			sum := 0
			for _, x := range xs {
				sum += x
			}
			return sum
		},
		"Counter":     reflect.TypeFor[bridgeCounter](),
		"InvalidText": func() string { return string([]byte{0xff}) },
		"Channel":     func() chan int { return make(chan int) },
		"NilFunction": (func(int) int)(nil),
	})
	s, err := NewSession(host)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	nativeCell(t, s, `import go "application/counter" as counter`, "")
	nativeCell(t, s, `c=counter.New(2)`, "")
	nativeCell(t, s, `c.Add(3)`, "5")
	nativeCell(t, s, `counter.Read(c)`, "5")
	nativeCell(t, s, `c.Value`, "5")
	nativeCell(t, s, `native_data(c)`, `{"Value":5}`)
	nativeCell(t, s, `counter.Sum(1,2,3)`, "6")
	nativeCell(t, s, `ctor=counter.Counter`, "")
	nativeCell(t, s, `ctor({Value:7}).Add(1)`, "8")
	nativeCell(t, s, `reader=counter.Read`, "")
	nativeCell(t, s, `reader(c)`, "5")
	nativeCell(t, s, `temporary=counter.New(10)`, "")
	nativeCell(t, s, `native_release(temporary)`, "true")
	if _, err := s.Eval(context.Background(), `temporary.Add(1)`, Options{}); err == nil {
		t.Fatal("released Go reference remained valid")
	}
	before := calls.Load()
	for _, source := range []string{`c.Add(counter.New(0),2)`, `reader(counter.New(0),2)`, `counter.Read(counter.New(0),2)`, `counter.Counter(counter.New(0),2)`} {
		_, err := s.Eval(context.Background(), source, Options{})
		if d, ok := err.(*Diagnostic); !ok || d.Code != "E-ARITY" {
			t.Fatalf("%s: %v", source, err)
		}
	}
	if calls.Load() != before {
		t.Fatal("native arity failure evaluated an effectful argument")
	}
	for _, source := range []string{`counter.InvalidText()`, `native_data(counter.Channel())`, `counter.NilFunction(counter.New(0))`} {
		if _, err := s.Eval(context.Background(), source, Options{}); err == nil {
			t.Fatal("accepted an invalid native value", source)
		}
	}
	if calls.Load() != before {
		t.Fatal("nil callee evaluated its argument")
	}
	if names := s.Complete("counter."); !strings.Contains(strings.Join(names, ","), "counter.Counter") {
		t.Fatal(names)
	}
	program, err := Compile(`import go "application/counter" as counter
flow Main()->number{return counter.Read(counter.New(1))}`, host)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := program.EmitGo(GoOptions{}); err == nil {
		t.Fatal("emission silently dropped application Go registration")
	}
	stale := s.Values()["c"]
	s.Reset()
	host.Native.Go.RegisterPackage("application/stale", map[string]any{"Old": func() Value { return stale }})
	nativeCell(t, s, `import go "application/stale" as stale`, "")
	_, err = s.Eval(context.Background(), `stale.Old().Add(1)`, Options{})
	if err == nil {
		t.Fatal("accepted resource from a previous adapter generation")
	}
}

func TestNativeGoPackagesAndGenerics(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" && runtime.GOOS != "freebsd" {
		t.Skip("automatic loading uses Go plugins")
	}
	s, err := NewInteractiveSession(Host{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	nativeCell(t, s, `import go "strings"`, "")
	for _, test := range []struct{ source, want string }{
		{`strings.ToUpper("1waht")`, `"1WAHT"`},
		{`strings.ToTitle("waht 12 ase? as a")`, `"WAHT 12 ASE? AS A"`},
		{`strings.Split("a,b",",")[1]`, `"b"`},
		{`native_data(strings.Fields("a b"))`, `["a","b"]`},
	} {
		nativeCell(t, s, test.source, test.want)
	}
	if names := s.Complete("strings.To"); !strings.Contains(strings.Join(names, ","), "strings.ToTitle") {
		t.Fatal(names)
	}
	nativeCell(t, s, `import go "bytes"`, "")
	nativeCell(t, s, `buffer=bytes.NewBufferString("hello")`, "")
	nativeCell(t, s, `buffer.WriteString("~")`, "1")
	nativeCell(t, s, `import go "io"`, "")
	nativeCell(t, s, `raw=io.ReadAll(buffer)`, "")
	nativeCell(t, s, `native_data(raw)`, `[104,101,108,108,111,126]`)
	nativeCell(t, s, `import go "encoding/hex" as hex`, "")
	nativeCell(t, s, `hex.EncodeToString(raw)`, `"68656c6c6f7e"`)
	nativeCell(t, s, `import go "unicode"`, "")
	nativeCell(t, s, `strings.Map(unicode.ToUpper,"ab")`, `"AB"`)
	nativeCell(t, s, `import go "errors"`, "")
	nativeCell(t, s, `cause=errors.New("failure")`, "")
	nativeCell(t, s, `cause.Error()`, `"failure"`)
	nativeCell(t, s, `import go "fmt"`, "")
	nativeCell(t, s, `wrapped=fmt.Errorf("wrapped: %w",cause)`, "")
	nativeCell(t, s, `errors.Is(wrapped,cause)`, "true")
	nativeCell(t, s, `import go "time"`, "")
	nativeCell(t, s, `date=time.Date(2026,10,14,0,0,0,0,time.UTC)`, "")
	nativeCell(t, s, `date.Year()`, "2026")
	nativeCell(t, s, `time.UTC.String()`, `"UTC"`)
	if _, err := s.Eval(context.Background(), `time.January(time.Now())`, Options{}); err == nil {
		t.Fatal("accepted a constant as a callable")
	}
	nativeCell(t, s, `import go "slices"`, "")
	for _, test := range []struct{ source, want string }{
		{`xs=slices.Clone([3,1,2])`, ""}, {`slices.Sort(xs)`, "null"}, {`native_data(xs)`, `[1,2,3]`},
		{`ys=slices.Clone(["c","a","b"])`, ""}, {`slices.Sort(ys)`, "null"}, {`native_data(ys)`, `["a","b","c"]`},
		{`slices.Sort(xs)`, "null"}, {`native_data(xs)`, `[1,2,3]`},
	} {
		nativeCell(t, s, test.source, test.want)
	}
	for _, source := range []string{`import go "string"`, `strings.ToUpper()`, `strings.ToUPPER("a")`} {
		if _, err := s.Eval(context.Background(), source, Options{}); err == nil {
			t.Fatal("accepted invalid Go import or symbol", source)
		}
	}
	s.Reset()
	nativeCell(t, s, `import go "strings"`, "")
	nativeCell(t, s, `strings.ToUpper("reset")`, `"RESET"`)
	// A second Host must reuse loaded code without sharing resource references.
	other, err := NewInteractiveSession(Host{})
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	nativeCell(t, other, `import go "strings"`, "")
	nativeCell(t, other, `strings.ToUpper("other")`, `"OTHER"`)
}

func TestNativePythonStateAttributesAndCallOrder(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("Python unavailable")
	}
	dir := t.TempDir()
	module := `from types import SimpleNamespace
count=0
__version__="fixture-1"
def touch():
    global count
    count += 1
    return count
def once(x): return x
prepare=SimpleNamespace(once=once)
get=SimpleNamespace(once=once)
class Counter:
    def __init__(self, value): self.value=value
    def add(self, step=1):
        self.value += step
        return self.value
def make(): return Counter(0)
def cycle():
    x=[]
    x.append(x)
    return x
`
	if err := os.WriteFile(filepath.Join(dir, "native_fixture.py"), []byte(module), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PYTHONPATH", dir)
	s, err := NewInteractiveSession(Host{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	nativeCell(t, s, `import python "native_fixture" as py`, "")
	nativeCell(t, s, `py.__version__`, `"fixture-1"`)
	nativeCell(t, s, `import python "native_fixture"`, "")
	nativeCell(t, s, `native_fixture.__version__`, `"fixture-1"`)
	nativeCell(t, s, `c=py.Counter(2)`, "")
	nativeCell(t, s, `c.add(step=3)`, "5")
	nativeCell(t, s, `c.value`, "5")
	nativeCell(t, s, `py.make().add()`, "1")
	nativeCell(t, s, `method=c.add`, "")
	nativeCell(t, s, `method(2)`, "7")
	nativeCell(t, s, `box=py.SimpleNamespace(inner=c);nested=effect box.inner.add(step=2)`, "")
	nativeCell(t, s, `c.value`, "7")
	nativeCell(t, s, `perform nested`, "9")
	nativeCell(t, s, `shadow=py.SimpleNamespace(prepare=c);path_request=effect shadow.prepare.add(step=0)`, "")
	nativeCell(t, s, `perform path_request`, "9")
	nativeCell(t, s, `bundle={run:py.once};wrapped_call=effect bundle.run(42)`, "")
	nativeCell(t, s, `perform wrapped_call`, "42")
	nativeCell(t, s, `perform effect py.prepare.once(6)`, "6")
	nativeCell(t, s, `perform effect py.get.once(7)`, "7")
	nativeCell(t, s, `py.prepare.once(8)`, "8")
	nativeCell(t, s, `temporary=py.make()`, "")
	nativeCell(t, s, `native_release(temporary)`, "true")
	if _, err := s.Eval(context.Background(), `temporary.add()`, Options{}); err == nil {
		t.Fatal("released Python reference remained valid")
	}
	nativeCell(t, s, `m=py`, "")
	nativeCell(t, s, `m.count`, "0")
	for _, source := range []string{`py.missing(py.touch())`, `py.once(py.touch(),2)`, `c.add(py.touch(),2)`, `method(py.touch(),2)`} {
		if _, err := s.Eval(context.Background(), source, Options{}); err == nil {
			t.Fatal("accepted invalid native call", source)
		}
	}
	nativeCell(t, s, `py.count`, "0")
	nativeCell(t, s, `if false {py.touch()} else {3}`, "3")
	nativeCell(t, s, `py.count`, "0")
	nativeCell(t, s, `p=effect py.touch()`, "")
	nativeCell(t, s, `py.count`, "0")
	nativeCell(t, s, `perform p`, "1")
	nativeCell(t, s, `py.count`, "1")
	if _, err := s.Eval(context.Background(), `failed=py.touch();fail("stop")`, Options{}); err == nil {
		t.Fatal("failed cell succeeded")
	}
	if _, ok := s.Values()["failed"]; ok {
		t.Fatal("failed cell committed binding")
	}
	nativeCell(t, s, `py.count`, "2")
	for _, source := range []string{
		`(match true {true=>c, _=>py.make()}).add()`,
		`(for x in [] with state=c {next state}).add()`,
		`[x for x in [c]][0].add()`,
		`{stored:c}.stored.add()`,
	} {
		nativeCell(t, s, source, "")
	}
	if _, err := s.Eval(context.Background(), `native_data(py.cycle())`, Options{}); err == nil {
		t.Fatal("accepted a cyclic native Data snapshot")
	}
	for _, name := range s.Complete("py.") {
		if strings.HasPrefix(name, "py.perform.") {
			t.Fatal(name)
		}
	}
	for _, path := range []string{"py.get.once", "py.prepare.once"} {
		if !strings.Contains(strings.Join(s.Complete(path), ","), path) {
			t.Fatal("completion lost a real library path", path)
		}
	}
	if _, err := s.Eval(context.Background(), `fn impure()->number{c.add()}`, Options{}); err == nil {
		t.Fatal("native method bypassed purity")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	nativeCell(t, s, `import python "time" as clock`, "")
	if _, err := s.Eval(ctx, `clock.sleep(10)`, Options{}); err == nil {
		t.Fatal("cancellation did not terminate Python call")
	}
	s.Reset()
	nativeCell(t, s, `import python "native_fixture" as py`, "")
	nativeCell(t, s, `py.count`, "0")
	host := NewNativeHost(Host{})
	defer host.Close()
	p, err := Compile(`import python "native_fixture" as py
flow Main()->object {
 original=py.once
 direct=py.once(1)
 shadow=py.SimpleNamespace(prepare=py.Counter(1))
 request=effect shadow.prepare.add(step=2)
 return {prepare:perform effect py.prepare.once(2),get:perform effect py.get.once(3),
         direct:py.prepare.once(4),nested:perform request,original:original(direct+4)}
}`, host)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"direct":4,"get":3,"nested":3,"original":5,"prepare":2}`
	report, err := p.Run(context.Background(), nil, Options{})
	if err != nil || display(t, report.Output) != want {
		t.Fatal(report, err)
	}
	executable, _ := buildNative(t, p, GoOptions{})
	stdout, stderr, err := runNative(t, executable)
	if err != nil || strings.TrimSpace(stdout) != want {
		t.Fatal(stdout, stderr, err)
	}
}

func TestGoModulePackageDiscoveryWithoutRegistry(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" && runtime.GOOS != "freebsd" {
		t.Skip("Go plugins unavailable")
	}
	dir := t.TempDir()
	dependency := filepath.Join(dir, "dependency")
	if err := os.MkdirAll(filepath.Join(dependency, "feature"), 0700); err != nil {
		t.Fatal(err)
	}
	// Resolve a separate module through ordinary require/replace directives,
	// without the network or any package-specific adapter registration.
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.org/nativeclient\n\ngo 1.25\n\nrequire example.org/nativefixture v0.0.0\nreplace example.org/nativefixture => ./dependency\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dependency, "go.mod"), []byte("module example.org/nativefixture\n\ngo 1.25\n"), 0600); err != nil {
		t.Fatal(err)
	}
	source := `package nativefixture
type Counter struct { Value int }
func New(n int)*Counter{return &Counter{Value:n}}
func(c *Counter) Add(n int)int{c.Value+=n;return c.Value}
func Echo[T any](x T)T{return x}
`
	if err := os.WriteFile(filepath.Join(dependency, "counter.go"), []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dependency, "feature", "feature.go"), []byte(`package feature
import "example.org/nativefixture"
func Read(c *nativefixture.Counter) int { return c.Value }
`), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOPROXY", "off")
	t.Setenv("GOWORK", "off")
	t.Chdir(dir)
	s, err := NewInteractiveSession(Host{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	nativeCell(t, s, `import go "example.org/nativefixture" as lib`, "")
	nativeCell(t, s, `c=lib.New(2)`, "")
	nativeCell(t, s, `c.Add(5)`, "7")
	nativeCell(t, s, `lib.Echo("third party")`, `"third party"`)
	nativeCell(t, s, `import go "example.org/nativefixture/feature" as feature`, "")
	nativeCell(t, s, `feature.Read(c)`, "7")

	host := NewNativeHost(Host{})
	defer host.Close()
	program, err := Compile(`import go "example.org/nativefixture" as lib
import go "example.org/nativefixture/feature" as feature
flow Main()->object {
    c=lib.New(2)
    n=c.Add(5)
    return {read:feature.Read(c), echo:lib.Echo("third party"), added:n}
}`, host)
	if err != nil {
		t.Fatal(err)
	}
	executable, _ := buildNative(t, program, GoOptions{})
	stdout, stderr, err := runNative(t, executable)
	if err != nil || strings.TrimSpace(stdout) != `{"added":7,"echo":"third party","read":7}` {
		t.Fatalf("generated third-party calls: %q %q %v", stdout, stderr, err)
	}
}

func TestGoImportDiagnosticsAndDependencySetup(t *testing.T) {
	dir := t.TempDir()
	modfile := filepath.Join(dir, "go.mod")
	module := []byte("module example.org/importdiagnostics\n\ngo 1.25\n")
	if err := os.WriteFile(modfile, module, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOPROXY", "off")
	t.Setenv("GOWORK", "off")
	t.Chdir(dir)
	s, err := NewInteractiveSession(Host{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, name := range []string{"gonum", "Gonum", "Gorgonia", "Gin", "gin", "viper"} {
		_, err := s.Eval(context.Background(), `import go "`+name+`"`, Options{})
		if d, ok := err.(*Diagnostic); !ok || d.Code != "E-IMPORT" || !strings.Contains(d.Message, "full package path") || !strings.Contains(d.Message, "case-sensitive") {
			t.Fatalf("missing package-path guidance for %s: %v", name, err)
		}
	}
	_, err = s.Eval(context.Background(), `import go "example.invalid/unconfigured" as lib`, Options{})
	if d, ok := err.(*Diagnostic); !ok || d.Code != "E-IMPORT" || !strings.Contains(d.Message, "go get") || !strings.Contains(d.Message, "module's directory") {
		t.Fatalf("missing dependency guidance: %v", err)
	}
	// Failed imports must neither configure dependencies nor commit namespaces.
	data, err := os.ReadFile(modfile)
	if err != nil || string(data) != string(module) {
		t.Fatalf("import changed go.mod: %q %v", data, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "go.sum")); !os.IsNotExist(err) {
		t.Fatalf("import created go.sum: %v", err)
	}
	if _, err := s.Eval(context.Background(), `lib.Echo("x")`, Options{}); err == nil {
		t.Fatal("failed import committed its alias")
	}
	nativeCell(t, s, `import go "strings" as text`, "")
	nativeCell(t, s, `text.ToUpper("working")`, `"WORKING"`)
}

func TestInstalledScientificPythonLibraries(t *testing.T) {
	if err := exec.Command("python3", "-c", "import numpy,scipy,pandas").Run(); err != nil {
		t.Skip("scientific libraries unavailable")
	}
	s, err := NewInteractiveSession(Host{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	for _, test := range []struct{ source, want string }{
		{`import python "scipy" as sp`, ""}, {`sp.__version__`, ""},
		{`import python "scipy"`, ""}, {`scipy.__version__`, ""},
		{`import python "numpy" as np`, ""}, {`a=np.array([1,2,3],dtype=np.int64)`, ""},
		{`str(a.dtype)`, `"int64"`}, {`a.reshape(3,1).tolist()`, `[[1],[2],[3]]`},
		{`native_data(a)`, `[1,2,3]`}, {`a[1]`, "2"}, {`len(a)`, "3"},
		{`sp.linalg.norm(a)`, "3.7416573867739413"},
		{`normal=sp.stats.norm(loc=2,scale=3)`, ""}, {`normal.mean()`, "2"},
		{`import python "pandas" as pd`, ""}, {`df=pd.DataFrame({x:[1,2,3]})`, ""},
		{`df.x.sum()`, "6"}, {`df.to_dict(orient="list")`, `{"x":[1,2,3]}`},
	} {
		nativeCell(t, s, test.source, test.want)
	}
}

// Exercise complete library workflows through files, generated executables,
// and persistent cells, including stateful objects and explicit Data snapshots.
func TestPracticalNativeWorkflows(t *testing.T) {
	for _, test := range []struct {
		name, modules, source, want string
		goPlugin                    bool
	}{
		{"sqlite", "sqlite3", `import python "sqlite3" as sql
flow Main()->object {
 db=sql.connect(":memory:")
 db.execute("create table sales(region text, amount real)")
 db.executemany("insert into sales values (?,?)",[["North",10],["South",7],["North",5]])
 db.commit()
 totals=db.execute("select region,sum(amount) from sales group by region order by region").fetchall()
 injection=db.execute("select count(*) from sales where region=?",["'; drop table sales; --"]).fetchall()
 count=db.execute("select count(*) from sales").fetchall()
 db.close()
 native_release(db)
 return {totals:totals,injection:injection,count:count}
}`, `{"count":[[3]],"injection":[[0]],"totals":[["North",15],["South",7]]}`, false},
		{"pandas-etl", "pandas", `import python "pandas" as pd
import python "io"
flow Main()->object {
 csv=io.StringIO("region,units,price\nNorth,2,4.5\nSouth,3,2\nNorth,1,5\nSouth,,9\n")
 raw=pd.read_csv(csv)
 clean=raw.dropna(subset=["units"])
 sales=clean.assign(revenue=clean["units"].mul(clean["price"]))
 totals=sales.groupby("region")["revenue"].sum().sort_index().to_dict()
 rows=sales.sort_values("revenue",ascending=false).to_dict(orient="records")
 return {totals:totals,rows:rows,dropped:len(raw)-len(clean)}
}`, `{"dropped":1,"rows":[{"price":4.5,"region":"North","revenue":9,"units":2},{"price":2,"region":"South","revenue":6,"units":3},{"price":5,"region":"North","revenue":5,"units":1}],"totals":{"North":14,"South":6}}`, false},
		{"linear-model", "numpy,sklearn", `import python "numpy" as np
import python "sklearn.linear_model" as linear
flow Predict(envelope:object,rows:list)->list {
 return native_data(perform effect envelope.model.predict(rows))
}
flow Main()->object {
 model=linear.LinearRegression()
 trained=model.fit([[0],[1],[2],[3],[4],[5]],[1,3,5,7,9,11])
 predictions=Predict({model:trained},[[6],[7]])
 weights=native_data(model.coef_)
 return {predictions_ok:np.allclose(predictions,[13,15]),weight_ok:np.allclose(weights,[2]),bias_ok:(model.intercept_-1)**2<1e-20}
}`, `{"bias_ok":true,"predictions_ok":true,"weight_ok":true}`, false},
		{"classification", "numpy,sklearn", `import python "numpy" as np
import python "sklearn.pipeline" as pipeline
import python "sklearn.preprocessing" as preprocessing
import python "sklearn.linear_model" as linear
flow Main()->object {
 model=pipeline.make_pipeline(preprocessing.StandardScaler(),linear.LogisticRegression(random_state=0))
 trained=model.fit([[-3],[-2],[-1],[1],[2],[3]],[0,0,0,1,1,1])
 predictions=trained.predict([[-4],[4]])
 probabilities=trained.predict_proba([[-4],[4]])
 return {classes:native_data(predictions),finite:native_data(np.isfinite(probabilities).all()),normalized:np.allclose(probabilities.sum(axis=1),[1,1])}
}`, `{"classes":[0,1],"finite":true,"normalized":true}`, false},
		{"numerical-modeling", "numpy,scipy", `import python "numpy" as np
import python "scipy.integrate" as integrate
import python "scipy.optimize" as optimize
flow Main()->object {
 solution=np.linalg.solve([[3,1],[1,2]],[9,8])
 integral=integrate.quad(np.sin,0,np.pi)
 minimum=optimize.minimize(optimize.rosen,[0,0],method="Nelder-Mead",options={xatol:1e-9,fatol:1e-12,maxiter:1000})
 return {solution_ok:np.allclose(solution,[2,3]),integral_ok:(integral[0]-2)**2<1e-20,minimum_ok:np.allclose(minimum.x,[1,1]),converged:native_data(minimum.success)}
}`, `{"converged":true,"integral_ok":true,"minimum_ok":true,"solution_ok":true}`, false},
		{"module-resource", "numpy", `import python "numpy" as np
flow Solve(library:object)->list {
 return native_data(perform effect library.linalg.solve([[3,1],[1,2]],[9,8]))
}
flow Main()->list {library=np;return Solve(library)}`, `[2,3]`, false},
		{"go-html-template", "", `import go "html/template" as html
import go "bytes"
import go "net/url" as url
flow Render(page:object,buffer:object,data:object)->string {
 perform effect page.Execute(buffer,data)
 return perform effect buffer.String()
}
flow Main()->object {
 page=html.New("page").Parse("<h1>{{.title}}</h1><p>{{.user}}</p><a href=\"{{.link}}\">go</a>")
 buffer=bytes.NewBufferString("")
 rendered=Render(page,buffer,{title:"<script>alert(1)</script>",user:"Ada & Lin",link:"javascript:alert(1)"})
 return {html:rendered,query:url.QueryEscape("tea &coffee")}
}`, `{"html":"<h1>&lt;script&gt;alert(1)&lt;/script&gt;</h1><p>Ada &amp; Lin</p><a href=\"#ZgotmplZ\">go</a>","query":"tea+%26coffee"}`, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			if test.modules != "" {
				if err := exec.Command("python3", "-c", "import "+test.modules).Run(); err != nil {
					t.Skipf("Python dependencies unavailable: %s", test.modules)
				}
			}
			if test.goPlugin && runtime.GOOS != "linux" && runtime.GOOS != "darwin" && runtime.GOOS != "freebsd" {
				t.Skip("Go plugins unavailable")
			}
			host := NewNativeHost(Host{})
			defer host.Close()
			p, err := CompileWithOptions(test.source, host, CompileOptions{Entry: "Main"})
			if err != nil {
				t.Fatal(err)
			}
			for _, parallel := range []int{1, 7} {
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				report, err := p.Run(ctx, nil, Options{Parallelism: parallel})
				cancel()
				if err != nil || display(t, report.Output) != test.want {
					t.Fatal("interpreted", report, err)
				}
			}
			executable, _ := buildNative(t, p, GoOptions{})
			stdout, stderr, err := runNative(t, executable, "-parallel", "7")
			if err != nil || strings.TrimSpace(stdout) != test.want {
				t.Fatal("generated", stdout, stderr, err)
			}
			session, err := NewInteractiveSession(Host{})
			if err != nil {
				t.Fatal(err)
			}
			defer session.Close()
			nativeCell(t, session, test.source, "")
			nativeCell(t, session, `Main()`, test.want)
		})
	}
}

func TestNativeReceiverProposals(t *testing.T) {
	var authorizations atomic.Int64
	host := NewNativeHost(Host{Authorize: func(context.Context, Request) error {
		authorizations.Add(1)
		return nil
	}})
	defer host.Close()
	host.Native.Go.RegisterPackage("application/resources", map[string]any{
		"New":  func(n int) *bridgeCounter { return &bridgeCounter{Value: n} },
		"Wrap": func(c *bridgeCounter) any { return struct{ Counter *bridgeCounter }{c} },
	})
	s, err := NewSession(host)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	nativeCell(t, s, `import go "application/resources" as res;counter=res.New(10)`, "")
	before := authorizations.Load()
	nativeCell(t, s, `request=effect counter.Add(2)`, "")
	if authorizations.Load() != before {
		t.Fatal("proposal construction reached the Host")
	}
	review := nativeCell(t, s, `str(request)`, "")
	summary, ok := review.Text()
	if !ok || !strings.Contains(summary, `"<go `) || !strings.Contains(summary, "bridgeCounter") {
		t.Fatalf("native reference missing from review summary: %q", summary)
	}
	for _, leaked := range []string{"$lip.native", "owner", "generation", "CallID"} {
		if strings.Contains(summary, leaked) {
			t.Fatalf("native identity leaked into review summary: %q", summary)
		}
	}
	if authorizations.Load() != before {
		t.Fatal("reviewing a native proposal reached the Host")
	}
	nativeCell(t, s, `counter.Value`, "10")
	nativeCell(t, s, `fn Request(receiver:object,n:number)->effect {effect receiver.Add(n)}
flow Update(receiver:object,n:number)->number {return perform Request(receiver,n)}
flow Keep()->number {return perform effect counter.Add(1)}`, "")
	nativeCell(t, s, `Update(counter,3)`, "13")
	if err := s.Delete("counter"); err != nil {
		t.Fatal(err)
	}
	nativeCell(t, s, `counter=res.New(100)`, "")
	nativeCell(t, s, `perform request`, "15")
	nativeCell(t, s, `Keep()`, "16")
	nativeCell(t, s, `Update(counter,1)`, "101")
	nativeCell(t, s, `box=res.Wrap(counter);nested=effect box.Counter.Add(2)`, "")
	nativeCell(t, s, `counter.Value`, "101")
	nativeCell(t, s, `perform nested`, "103")
	nativeCell(t, s, `bad=effect counter.Add(1,2)`, "")
	if _, err := s.Eval(context.Background(), `perform bad`, Options{}); err == nil || !strings.Contains(err.Error(), "E-ARITY") {
		t.Fatal("method arity was not checked at perform", err)
	}
	nativeCell(t, s, `counter.Value`, "103")
	nativeCell(t, s, `envelope={resource:counter};wrapped=effect envelope.resource.Add(4)`, "")
	nativeCell(t, s, `perform wrapped`, "107")
	nativeCell(t, s, `bundle={create:res.New};created=perform effect bundle.create(8);created.Value`, "8")
	for _, source := range []string{`effect missing.Add(1)`, `effect counter.Add(res.New(1))`} {
		if _, err := s.Eval(context.Background(), source, Options{}); err == nil {
			t.Fatal("accepted invalid proposal", source)
		}
	}
	set := SourceSet{Root: "main.lip", Sources: map[string]Source{
		"main.lip": {Version: "1", Text: `import go "application/resources" as res
import lip "./lib.lip" as lib
missing:object={}
flow Main()->number {counter=res.New(4);return lib.Update(counter,3)}`},
		"lib.lip": {Version: "1", Text: `fn Request(receiver:object,n:number)->effect {effect receiver.Add(n)}
export flow Update(receiver:object,n:number)->number {return perform Request(receiver,n)}`},
	}}
	p, err := CompileSourceSet(set, host, CompileOptions{})
	if err != nil {
		t.Fatal(err)
	}
	report, err := p.Run(context.Background(), nil, Options{})
	if err != nil || display(t, report.Output) != "7" {
		t.Fatal(report, err)
	}
	// A dependency cannot capture an unresolved receiver from its importer.
	lib := set.Sources["lib.lip"]
	lib.Text = `export flow Update(receiver:object,n:number)->number {return perform effect missing.Add(n)}`
	set.Sources["lib.lip"] = lib
	if _, err := CompileSourceSet(set, host, CompileOptions{}); err == nil {
		t.Fatal("dependency resolved an unbound native receiver")
	}
}

func TestNativeHostAuthorizationAndRegisteredPrecedence(t *testing.T) {
	denied := NewNativeHost(Host{Authorize: func(context.Context, Request) error { return errors.New("denied") }})
	denied.Native.Go.RegisterPackage("application/denied", map[string]any{"Write": func() int { t.Fatal("denied native operation executed"); return 0 }})
	s, _ := NewSession(denied)
	defer s.Close()
	nativeCell(t, s, `import go "application/denied" as d`, "")
	if _, err := s.Eval(context.Background(), `d.Write()`, Options{}); err == nil {
		t.Fatal("authorization bypassed")
	}
	custom := Contract{Operation: Operation{Language: "python", Path: "math", Member: "sqrt"}, Version: "custom", Params: []Parameter{{Name: "x", Schema: Schema{Type: "number"}}}, Result: Schema{Type: "number"}, Effect: ReadOnlyEffect, Invoke: func(context.Context, Request) Response { return Result(number(42)) }}
	configured, err := NewInteractiveSession(Host{Contracts: []Contract{custom}})
	if err != nil {
		t.Fatal(err)
	}
	defer configured.Close()
	nativeCell(t, configured, `import python "math"`, "")
	nativeCell(t, configured, `math.sqrt(3)`, "42")
	fileHost := NewNativeHost(Host{Contracts: []Contract{custom}})
	defer fileHost.Close()
	program, err := Compile(`import python "math"
flow Main()->number{return math.sqrt(3)}`, fileHost)
	if err != nil {
		t.Fatal(err)
	}
	report, err := program.Run(context.Background(), nil, Options{})
	if err != nil || display(t, report.Output) != "42" {
		t.Fatalf("explicit file contract: %v %v", report, err)
	}
}

func TestNativeLibrariesInterpreterAndGeneratedProgram(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" && runtime.GOOS != "freebsd" {
		t.Skip("Go plugins unavailable")
	}
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("Python unavailable")
	}
	host := NewNativeHost(Host{})
	defer host.Close()
	program, err := Compile(`import go "strings"
import go "bytes"
import python "collections" as py
fn identity(value:any)->any{value}
flow Main()->object {
    upper=perform effect strings.ToUpper("hello")
    buffer=perform effect bytes.NewBufferString(upper) |> identity |> identity
    size=perform effect buffer.WriteString("!")
    counts=perform effect py.Counter(["a","b","a"]) |> identity
    return {text:buffer.String(), count:counts["a"], written:size}
}`, host)
	if err != nil {
		t.Fatal(err)
	}
	report, err := program.Run(context.Background(), nil, Options{Parallelism: 4})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"count":2,"text":"HELLO!","written":1}`
	if got := display(t, report.Output); got != want {
		t.Fatalf("interpreted: %s", got)
	}
	executable, _ := buildNative(t, program, GoOptions{})
	stdout, stderr, err := runNative(t, executable)
	if err != nil || strings.TrimSpace(stdout) != want {
		t.Fatalf("generated: %q %q %v", stdout, stderr, err)
	}
}

func TestNativeModuleHelpers(t *testing.T) {
	set := SourceSet{Root: "main.lip", Sources: map[string]Source{
		"main.lip": {Version: "local", Text: `import lip "./lib.lip" as lib
fn native_data(x:any)->any{fail("importer must not run")}
flow Main()->object{return lib.Read()}`},
		"lib.lip": {Version: "1", Text: `export flow Read()->object{value=native_data({x:1});if false {native_release(value)};return value}`},
	}}
	if _, err := CompileSourceSet(set, Host{}, CompileOptions{}); err == nil || !strings.Contains(err.Error(), "E-NAME") {
		t.Fatal("disabled Host prelude captured importer function", err)
	}
	host := NewNativeHost(Host{})
	defer host.Close()
	p, err := CompileSourceSet(set, host, CompileOptions{})
	if err != nil {
		t.Fatal(err)
	}
	report, err := p.Run(context.Background(), nil, Options{Parallelism: 1})
	if err != nil || display(t, report.Output) != `{"x":1}` {
		t.Fatal(report, err)
	}
	executable, _ := buildNative(t, p, GoOptions{})
	stdout, stderr, err := runNative(t, executable)
	if err != nil || strings.TrimSpace(stdout) != `{"x":1}` {
		t.Fatal(stdout, stderr, err)
	}
}

func TestNativeModuleReceiverProposals(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("Python unavailable")
	}
	set := SourceSet{Root: "main.lip", Sources: map[string]Source{
		"main.lip": {Version: "1", Text: `import python "io"
import lip "./lib.lip" as lib
fn Request(receiver:object,text:string)->effect {fail("importer must not run")}
flow Main()->string {buffer=io.StringIO();return lib.Append(buffer,"hello")}`},
		"lib.lip": {Version: "1", Text: `fn Request(receiver:object,text:string)->effect {effect receiver.write(text)}
export flow Append(receiver:object,text:string)->string {
 perform Request(receiver,text)
 return perform effect receiver.getvalue()
}`},
	}}
	host := NewNativeHost(Host{})
	defer host.Close()
	p, err := CompileSourceSet(set, host, CompileOptions{})
	if err != nil {
		t.Fatal(err)
	}
	report, err := p.Run(context.Background(), nil, Options{Parallelism: 7})
	if err != nil || display(t, report.Output) != `"hello"` {
		t.Fatal(report, err)
	}
	executable, _ := buildNative(t, p, GoOptions{})
	stdout, stderr, err := runNative(t, executable)
	if err != nil || strings.TrimSpace(stdout) != `"hello"` {
		t.Fatal(stdout, stderr, err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "lib.lip"), []byte(set.Sources["lib.lip"].Text), 0600); err != nil {
		t.Fatal(err)
	}
	s, err := NewInteractiveSession(Host{}, SessionOptions{ProjectRoot: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	nativeCell(t, s, set.Sources["main.lip"].Text, "")
	nativeCell(t, s, `Main()`, `"hello"`)
	nativeCell(t, s, `saved=io.StringIO();lib.Append(saved,"first")`, `"first"`)
	nativeCell(t, s, `lib.Append(saved," second")`, `"first second"`)
}

func TestNativeRequestBuilderComposition(t *testing.T) {
	const declarations = `export fn Build(text:string)->effect {z_leaf(text)}
export fn Factory(text:string)->function {fn()->effect {Build(text)}}
export fn Recur(n:number)->effect {z_recursive(n)}
export fn RecursiveFactory(n:number)->function {fn()->effect {z_recursive(n)}}
export fn Bundle(n:number)->object {{request:z_recursive(n)}}
fn a_recursive(n:number)->effect {if n==0 {effect lib.New("four")} else {z_recursive(n-1)}}
fn z_recursive(n:number)->effect {a_recursive(n)}
fn z_leaf(text:string)->effect {effect lib.New(text)}`
	const main = `import lip "./requests.lip" as requests
import host "checks"
fn local(text:string)->effect {requests.Build(text)}
fn data_request()->effect {effect checks.read()}
fn forward(request:effect)->effect {request}
flow ReadData()->list {
 facts=claim()
 for x in [1,2] parallel {row=perform data_request();facts <+ row.count}
 return [row.value for row in facts]
}
flow Main()->object {
 direct=perform local("hi")
 alias=requests.Build
 aliased=perform alias("lip")
 piped=perform ("hello" |>requests.Build)
 closure=perform requests.Factory("sixsix")()
 recursive=perform requests.Recur(2)
 recursive_closure=perform requests.RecursiveFactory(2)()
 projected=perform requests.Bundle(1).request
 recursive_pipe=perform (2 |>requests.Recur)
 forwarded=perform forward(requests.Recur(2))
 return {sizes:[direct.Len(),aliased.Len(),piped.Len(),closure.Len(),recursive.Len(),recursive_closure.Len(),projected.Len(),recursive_pipe.Len(),forwarded.Len()],rows:ReadData()}
}`
	const want = `{"rows":[3,3],"sizes":[2,3,5,6,4,4,4,4,4]}`
	const hostSource = `package main
import("bytes";"context")
func generatedHost()(Host,error){
 host:=NewNativeHost(Host{Contracts:[]Contract{{Operation:Operation{Language:"host",Path:"checks",Member:"read"},Version:"1",Result:Schema{Type:"object"},Effect:ReadOnlyEffect,Concurrent:true,
 Invoke:func(context.Context,Request)Response{n,_:=NumberValue(3);v,_:=ObjectValue(map[string]Value{"count":n});return Result(v)},
 }}})
 host.Native.Go.RegisterPackage("application/request-order",map[string]any{"New":bytes.NewBufferString})
 return host,nil
}`
	var calls atomic.Int64
	host := NewNativeHost(Host{Contracts: []Contract{{
		Operation: Operation{Language: "host", Path: "checks", Member: "read"}, Version: "1", Result: Schema{Type: "object"}, Effect: ReadOnlyEffect, Concurrent: true,
		Invoke: func(context.Context, Request) Response { return Result(object(map[string]Value{"count": number(3)})) },
	}}})
	defer host.Close()
	host.Native.Go.RegisterPackage("application/request-order", map[string]any{"New": func(text string) *bytes.Buffer {
		calls.Add(1)
		return bytes.NewBufferString(text)
	}})
	variants := []string{declarations, strings.Join(func() []string {
		lines := strings.Split(declarations, "\n")
		for i, j := 0, len(lines)-1; i < j; i, j = i+1, j-1 {
			lines[i], lines[j] = lines[j], lines[i]
		}
		return lines
	}(), "\n"), strings.NewReplacer("a_recursive", "z_base", "z_recursive", "a_caller", "z_leaf", "a_leaf").Replace(declarations)}
	for i, definitions := range variants {
		module := "import go \"application/request-order\" as lib\n" + definitions
		set := SourceSet{Root: "main.lip", Sources: map[string]Source{
			"main.lip": {Version: "1", Text: main}, "requests.lip": {Version: "1", Text: module},
		}}
		before := calls.Load()
		p, err := CompileSourceSet(set, host, CompileOptions{})
		if err != nil || calls.Load() != before {
			t.Fatal("checking request builders called provider", err, calls.Load(), before)
		}
		for _, parallel := range []int{1, 2, 7} {
			report, err := p.Run(context.Background(), nil, Options{Parallelism: parallel})
			if err != nil || display(t, report.Output) != want {
				t.Fatal(i, parallel, report, err)
			}
		}
		executable, _ := buildNative(t, p, GoOptions{HostSource: hostSource})
		stdout, stderr, err := runNative(t, executable, "-parallel", "2")
		if err != nil || strings.TrimSpace(stdout) != want {
			t.Fatal(i, stdout, stderr, err)
		}
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "requests.lip"), []byte(module), 0600); err != nil {
			t.Fatal(err)
		}
		s, err := NewSession(host, SessionOptions{ProjectRoot: dir})
		if err != nil {
			t.Fatal(err)
		}
		before = calls.Load()
		nativeCell(t, s, main, "")
		if calls.Load() != before {
			t.Fatal("declarations constructed native resources")
		}
		nativeCell(t, s, `Main()`, want)
		nativeCell(t, s, `saved=requests.Build`, "")
		nativeCell(t, s, `buffer=perform saved("word");buffer.Len()`, "4")
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestNativeHelperLexicalScope(t *testing.T) {
	var calls atomic.Int64
	host := NewNativeHost(Host{Authorize: func(context.Context, Request) error {
		calls.Add(1)
		return nil
	}})
	defer host.Close()
	source := `fn native_data(x:number)->number{x+10}
fn native_release(x:number)->number{x+20}
fn apply(native_data:function,x:number)->number{native_data(x)}
flow WithParameter(native_release:number)->number{return native_release}
flow Main()->list{
    local=if true {native_release=fn(x:number)->number{x+30};native_release(2)}else{0}
    applied=apply(fn(x:number)->number{x*2},3)
    carried=for native_data in [fn(x:number)->number{x+40}] with state=0 {
        next state+native_data(4)
    }
    facts=claim()
    for native_release in [fn(x:number)->number{x+50}] {facts <+ native_release(5)}
    return [native_data(1),native_release(1),local,applied,carried,[row.value for row in facts],2 |> native_data]
}`
	p, err := CompileWithOptions(source, host, CompileOptions{Entry: "Main"})
	if err != nil {
		t.Fatal(err)
	}
	want := `[11,21,32,6,44,[55],12]`
	report, err := p.Run(context.Background(), nil, Options{Parallelism: 4})
	if err != nil || display(t, report.Output) != want {
		t.Fatal(report, err)
	}
	if calls.Load() != 0 {
		t.Fatal("pure lexical calls reached Host", calls.Load())
	}
	executable, _ := buildNative(t, p, GoOptions{})
	stdout, stderr, err := runNative(t, executable)
	if err != nil || strings.TrimSpace(stdout) != want {
		t.Fatal(stdout, stderr, err)
	}
	s, err := NewSession(host)
	if err != nil {
		t.Fatal(err)
	}
	for _, cell := range []struct{ source, want string }{
		{`fn native_data(x:number)->number{x+7}`, ""},
		{`native_release=fn(x:number)->number{x+9}`, ""},
		{`[native_data(1),native_release(2)]`, `[8,11]`},
		{`(fn(native_data:function)->number{native_data(3)})(fn(x:number)->number{x*4})`, `12`},
	} {
		nativeCell(t, s, cell.source, cell.want)
	}
	if calls.Load() != 0 {
		t.Fatal("saved lexical calls reached Host", calls.Load())
	}
	if _, err := Compile(`flow Main()->number{native_data=claim();return native_data(2)}`, host); err == nil || !strings.Contains(err.Error(), "E-TYPE") {
		t.Fatal("bridge helper bypassed a Claim binding", err)
	}
	set := SourceSet{Root: "main.lip", Sources: map[string]Source{
		"main.lip": {Version: "1", Text: `import lip "./lib.lip" as native_data;flow Main()->number{return native_data(2)}`},
		"lib.lip":  {Version: "1", Text: `export value:number=3`},
	}}
	if _, err := CompileSourceSet(set, host, CompileOptions{}); err == nil || !strings.Contains(err.Error(), "E-TYPE") {
		t.Fatal("bridge helper bypassed a module alias", err)
	}
}

func TestNativePipelineStagesRemainStatic(t *testing.T) {
	var calls atomic.Int64
	host := NewNativeHost(Host{})
	defer host.Close()
	host.Native.Go.RegisterPackage("application/text", map[string]any{
		"Upper": func(s string) string { calls.Add(1); return strings.ToUpper(s) },
	})
	for _, body := range []string{
		`return "hello" |> text.Upper`,
		`callable=text.Upper;return "hello" |> callable`,
	} {
		_, err := Compile(`import go "application/text" as text;flow Main()->string{`+body+`}`, host)
		if d, ok := err.(*Diagnostic); !ok || d.Code != "E-TYPE" {
			t.Fatalf("%s: %v", body, err)
		}
	}
	if _, err := Compile(`flow Main()->object{return {x:1} |> native_data}`, host); err == nil || !strings.Contains(err.Error(), "E-NAME") {
		t.Fatal("bridge helper accepted as a static stage", err)
	}
	if calls.Load() != 0 {
		t.Fatal("stage validation executed a native callable")
	}
}

func TestNativeIndexAndSliceComposition(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("Python 3 required for native slice parity")
	}
	host := NewNativeHost(Host{})
	defer host.Close()
	var calls atomic.Int64
	host.Native.Go.RegisterPackage("application/sequence", map[string]any{"Values": func() []int { calls.Add(1); return []int{0, 1, 2, 3, 4} }})
	source := `import go "application/sequence" as seq
import python "array" as py
import python "builtins" as bi
BASE:list=[0,1,2,3]
INDICES:list=[0,0,0]
flow Main(mode:string)->any {
 return if mode=="skip" {7} else {
  xs=seq.Values()
  match mode {
   "go"=>{last:xs[-1],gather:native_data(xs[[-1,0,-1]]),reverse:native_data(xs[::-1]),stride:native_data(slice(xs,null,null,-2)),nested:native_data([xs][::-1][[0]][0][1:4:2])},
   "pure"=>{f=fn()->number{[xs,7][::-1][0]};g=fn()->number{[xs,8][[1]][0]};h=fn()->number{slice([xs,9],1,null)[0]};[f(),g(),h()]},
   "dynamic"=>{indices=range(1);selected=[xs][indices];native_data(selected[0][::-1])},
   "python"=>{a=py.array("i",[0,1,2,3,4]);{last:a[-1],reverse:native_data(a[::-1]),stride:native_data(a[99:-99:-2]),gather:bi.list(range(5))[[-1,0,-1]],unicode:bi.str("a𐀀b")[::-1]}},
   "bounds"=>xs[-6],
   "budget_gather"=>bi.list(BASE)[INDICES],
   "budget_slice"=>bi.list(BASE)[:],
   _=>native_data(xs[:])
  }
 }
}`
	p, err := Compile(source, host)
	if err != nil {
		t.Fatal(err)
	}
	executable, _ := buildNative(t, p, GoOptions{HostSource: `package main
func generatedHost()(Host,error){host:=NewNativeHost(Host{});host.Native.Go.RegisterPackage("application/sequence",map[string]any{"Values":func() []int{return []int{0,1,2,3,4}}});return host,nil}`})
	s, err := NewSession(host)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	nativeCell(t, s, source, "")
	for _, test := range []struct{ mode, want string }{
		{"skip", "7"},
		{"go", `{"gather":[4,0,4],"last":4,"nested":[1,3],"reverse":[4,3,2,1,0],"stride":[4,2,0]}`},
		{"pure", `[7,8,9]`},
		{"dynamic", `[4,3,2,1,0]`},
		{"python", `{"gather":[4,0,4],"last":4,"reverse":[4,3,2,1,0],"stride":[4,2,0],"unicode":"b𐀀a"}`},
		{"copy", `[0,1,2,3,4]`},
	} {
		before := calls.Load()
		r, err := p.Run(context.Background(), map[string]Value{"mode": textValue(test.mode)}, Options{Parallelism: 4})
		stdout, stderr, nativeErr := runNative(t, executable, "-input", `{"mode":"`+test.mode+`"}`)
		if err != nil || nativeErr != nil || display(t, r.Output) != test.want || strings.TrimSpace(stdout) != test.want {
			t.Fatal(test.mode, r, err, stdout, stderr, nativeErr)
		}
		nativeCell(t, s, `Main("`+test.mode+`")`, test.want)
		if test.mode == "skip" && calls.Load() != before {
			t.Fatal("unselected sequence called provider")
		}
	}
	for _, mode := range []string{"budget_gather", "budget_slice"} {
		_, err := p.Run(context.Background(), map[string]Value{"mode": textValue(mode)}, Options{MaxElements: 2})
		stdout, stderr, nativeErr := runNative(t, executable, "-input", `{"mode":"`+mode+`"}`, "-elements", "2")
		if err == nil || err.(*Diagnostic).Code != "E-BUDGET" || nativeErr == nil || stdout != "" || !strings.Contains(stderr, err.Error()) {
			t.Fatal(mode, err, stdout, stderr, nativeErr)
		}
		if _, err := s.Eval(context.Background(), `bad=Main("`+mode+`")`, Options{MaxElements: 2}); err == nil || err.(*Diagnostic).Code != "E-BUDGET" {
			t.Fatal(mode, err)
		}
	}
	before := calls.Load()
	if _, err := Compile(`import go "application/sequence" as seq;flow Main()->any{return slice(seq.Values(),start=0,stop=1)}`, host); err == nil || err.(*Diagnostic).Code != "E-ARITY" || calls.Load() != before {
		t.Fatal("named slice args escaped static checking", err)
	}

	nativeCell(t, s, `saved=seq.Values()`, "")
	nativeCell(t, s, `view=saved[::-2]`, "")
	nativeCell(t, s, `native_data(view)`, `[4,2,0]`)
	nativeCell(t, s, `native_data(saved)`, `[0,1,2,3,4]`)
	nativeCell(t, s, `view[-1]`, `0`)
	if _, err := s.Eval(context.Background(), `bad=saved[-6]`, Options{}); err == nil || err.(*Diagnostic).Code != "E-INDEX" {
		t.Fatal(err)
	}
	nativeCell(t, s, `view[-1]`, `0`)
}

func TestNativeHigherOrderAndBranchProvenance(t *testing.T) {
	host := NewNativeHost(Host{})
	defer host.Close()
	host.Native.Go.RegisterPackage("application/composition", map[string]any{"New": bytes.NewBufferString})
	const declarations = `fn identity(x:any)->any{x}
fn partial(value:any)->function{fn(f:function)->any{f(value)}}
fn nested(value:any)->function{fn(f:function)->function{fn(g:function)->any{g(f(value))}}}
fn shadow(value:any)->function{fn(value:any)->any{value}}
fn pair(flag:bool,value:any)->list{if flag {[[1,2],value]}else{[[3],value]}}
fn matched(flag:bool,value:any)->list{match flag {true=>[[1,2,3],value],_=>[[4],value]}}
fn named_choice(flag:bool)->function{if flag {fn(value:any)->any{value}}else{fn(other:any)->any{other}}}
fn arity_choice(flag:bool)->function{if flag {fn(value:any)->any{value}}else{fn(ignored:any,other:any)->any{other}}}
fn swapped_choice(flag:bool)->function{if flag {fn(x:any,y:any)->any{x}}else{fn(y:any,x:any)->any{y}}}
fn recursive_pair(n:number,value:any)->list{if n==0 {[[1,2],value]}else{recursive_pair(n-1,value)}}
flow Partial(value:any)->function{return partial(value)}
flow Pair(flag:bool,value:any)->list{return pair(flag,value)}
`
	const main = `flow Main()->object{
 resource=lib.New("hello")
 delayed=partial(resource)
 deeper=nested(resource)(identity)
 left=pair(true,resource)
 right=pair(false,resource)
 pattern=matched(true,resource)
 child=Pair(false,resource)
 recursive=recursive_pair(3,resource)
 pure=fn()->list{[len(left[0]),len(right[0]),len(pattern[0]),len(child[0]),len(recursive[0])]}
 return {pure:pure(),sizes:[delayed(identity).Len(),partial(value=resource)(f=identity).Len(),deeper(identity).Len(),shadow(0)(resource).Len(),Partial(resource)(identity).Len(),left[1].Len(),right[-1].Len(),pattern[1].Len(),child[1].Len(),named_choice(false)(other=resource).Len(),arity_choice(false)(0,resource).Len(),swapped_choice(false)(x=0,y=resource).Len(),recursive[1].Len()],length:partial([resource])(len)}
}`
	const want = `{"length":1,"pure":[2,1,3,1,2],"sizes":[5,5,5,5,5,5,5,5,5,5,5,5,5]}`
	for _, reverse := range []bool{false, true} {
		decl := declarations
		if reverse {
			lines := strings.Split(strings.TrimSpace(decl), "\n")
			for i, j := 0, len(lines)-1; i < j; i, j = i+1, j-1 {
				lines[i], lines[j] = lines[j], lines[i]
			}
			decl = strings.Join(lines, "\n") + "\n"
		}
		source := "import go \"application/composition\" as lib\n" + decl + main
		if reverse {
			source = strings.NewReplacer("partial", "zebra", "identity", "alpha").Replace(source)
		}
		p, err := Compile(source, host)
		if err != nil {
			t.Fatal(reverse, err)
		}
		for _, parallel := range []int{1, 7} {
			r, err := p.Run(context.Background(), nil, Options{Parallelism: parallel})
			if err != nil || display(t, r.Output) != want {
				t.Fatal(reverse, parallel, r, err)
			}
		}
		exe, _ := buildNative(t, p, GoOptions{HostSource: `package main
import "bytes"
func generatedHost()(Host,error){h:=NewNativeHost(Host{});h.Native.Go.RegisterPackage("application/composition",map[string]any{"New":bytes.NewBufferString});return h,nil}`})
		stdout, stderr, err := runNative(t, exe)
		if err != nil || strings.TrimSpace(stdout) != want {
			t.Fatal(reverse, stdout, stderr, err)
		}
	}
	s, err := NewSession(host)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	nativeCell(t, s, "import go \"application/composition\" as lib\n"+declarations, "")
	nativeCell(t, s, `saved=lib.New("hello");delayed=partial(saved);deeper=nested(saved)`, "")
	nativeCell(t, s, `delayed(identity).Len()`, `5`)
	nativeCell(t, s, `stage=deeper(identity)`, "")
	nativeCell(t, s, `stage(identity).Len()`, `5`)
	nativeCell(t, s, `left=Pair(true,saved);(fn()->number{len(left[0])})()`, `2`)
	nativeCell(t, s, `left[-1].Len()`, `5`)
	nativeCell(t, s, `chosen=named_choice(false)`, "")
	nativeCell(t, s, `chosen(other=saved).Len()`, `5`)
}

func TestNativeReturnProvenance(t *testing.T) {
	host := NewNativeHost(Host{})
	defer host.Close()
	host.Native.Go.RegisterPackage("application/refactoring", map[string]any{"New": bytes.NewBufferString})
	const declarations = `fn literal(ignored:object)->list{[1,2,3]}
fn identity(value:any)->any{value}
fn box(value:object,ignored:object)->object{{resource:value,plain:[1,2]}}
fn first(value:object)->object{value.resource}
fn factory(value:any)->function{fn()->any{value}}
fn apply(f:function,value:any)->any{f(value)}
fn inserted(value:object)->object{put({plain:[1,2]},"resource",value)}
fn replaced(box:object,value:any)->object{put(box,"resource",value)}
fn unknown_key()->string{"resource"}
fn overwritten(box:object,value:any)->object{put(put(box,unknown_key(),value),"plain",[1,2,3])}
fn exchange_a(n:number,p:any,q:any,ignored:any)->any{if n==0 {p}else{exchange_b(n-1,q,p,ignored)}}
fn exchange_b(n:number,p:any,q:any,ignored:any)->any{exchange_a(n,p,q,ignored)}
fn repeated(n:number,ignored:any)->list{if n==0 {[1,2,3]}else{repeated(n-1,ignored)}}
fn bundle(n:number,value:any)->object{if n==0 {{resource:value,plain:[1,2]}}else{bundle(n-1,value)}}
fn nested(n:number,value:any)->any{if n==0 {value}else{nested(n-1,{resource:value})}}
fn projected(n:number,value:any)->any{if n==0 {value.resource}else{projected(n-1,value.resource)}}
fn callback(n:number)->function{if n==0 {len}else{other_callback(n-1)}}
fn other_callback(n:number)->function{if n==0 {str}else{callback(n-1)}}
flow Echo(value:any)->any{return value}
flow Create(text:string)->object{return lib.New(text)}
flow Make(text:string)->effect{return effect lib.New(text)}
flow Closure(value:object)->function{return fn()->object{value}}
`
	const main = `flow Main()->object {
 resource=lib.New("hello")
 xs=literal(resource)
 pure=fn()->number{len(xs)}
 piped=resource |>literal
 pure_pipe=fn()->number{len(piped)}
 container=box(value=resource,ignored=resource)
 plain=fn()->number{len(container.plain)}
 forwarded=identity(resource)
 selected=first(container)
 captured=factory(resource)()
 higher=apply(identity,resource)
 mapped=map([resource],identity)[0]
 concatenated=concat([resource],[])[0]
 updated=put({},"resource",resource).resource
 child=Echo(Create("three"))
 requested=perform Make("four")
 closure=Closure(resource)()
 exchanged=exchange_b(1,0,resource,resource)
 recursive_plain=repeated(3,resource)
 recursive_pure=fn()->number{len(recursive_plain)}
 recursive_box=bundle(3,resource)
 field_pure=fn()->number{len(recursive_box.plain)}
 inserted_box=inserted(resource)
 updated_pure=fn()->number{len(inserted_box.plain)}
 replaced_box=replaced(inserted_box,[1,2,3])
 replaced_pure=fn()->number{len(replaced_box.resource)}
 overwritten_box=overwritten(inserted_box,resource)
 overwritten_pure=fn()->number{len(overwritten_box.plain)}
 growing=nested(2,resource)
 shrinking=projected(1,{resource:{resource:resource}})
 return {pure:[pure(),pure_pipe(),plain(),recursive_pure(),field_pure(),callback(2)([1,2]),updated_pure(),replaced_pure(),overwritten_pure()],sizes:[forwarded.Len(),selected.Len(),captured.Len(),higher.Len(),child.Len(),requested.Len(),closure.Len(),mapped.Len(),concatenated.Len(),updated.Len(),exchanged.Len(),recursive_box.resource.Len(),perform effect growing.resource.resource.Len(),perform effect shrinking.Len(),inserted_box.resource.Len(),overwritten_box.resource.Len()]}
}`
	const want = `{"pure":[3,3,2,3,2,2,2,3,3],"sizes":[5,5,5,5,5,4,5,5,5,5,5,5,5,5,5,5]}`
	for _, reverse := range []bool{false, true} {
		decl := declarations
		if reverse {
			lines := strings.Split(strings.TrimSpace(decl), "\n")
			for i, j := 0, len(lines)-1; i < j; i, j = i+1, j-1 {
				lines[i], lines[j] = lines[j], lines[i]
			}
			decl = strings.Join(lines, "\n") + "\n"
		}
		source := "import go \"application/refactoring\" as lib\n" + decl + main
		if reverse {
			source = strings.NewReplacer("exchange_a", "zebra", "exchange_b", "alpha").Replace(source)
		}
		p, err := Compile(source, host)
		if err != nil {
			t.Fatal(reverse, err)
		}
		for _, parallel := range []int{1, 7} {
			report, err := p.Run(context.Background(), nil, Options{Parallelism: parallel})
			if err != nil || display(t, report.Output) != want {
				t.Fatal(reverse, parallel, report, err)
			}
		}
		exe, _ := buildNative(t, p, GoOptions{HostSource: `package main
import "bytes"
func generatedHost()(Host,error){h:=NewNativeHost(Host{});h.Native.Go.RegisterPackage("application/refactoring",map[string]any{"New":bytes.NewBufferString});return h,nil}`})
		stdout, stderr, err := runNative(t, exe)
		if err != nil || strings.TrimSpace(stdout) != want {
			t.Fatal(stdout, stderr, err)
		}
		s, err := NewSession(host)
		if err != nil {
			t.Fatal(err)
		}
		nativeCell(t, s, source, "")
		nativeCell(t, s, `saved=lib.New("word")`, "")
		nativeCell(t, s, `plain=literal(saved);(fn()->number{len(plain)})()`, `3`)
		nativeCell(t, s, `copied=identity(saved);copied.Len()`, `4`)
		nativeCell(t, s, `saved_factory=factory(saved)`, "")
		nativeCell(t, s, `copy=saved_factory();copy.Len()`, `4`)
		nativeCell(t, s, `child_factory=Closure(saved)`, "")
		nativeCell(t, s, `child_copy=child_factory();child_copy.Len()`, `4`)
		nativeCell(t, s, `returned=Echo(Create("sixsix"));returned.Len()`, `6`)
		nativeCell(t, s, `Main()`, want)
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

// Explicit receiver requests may pass through arbitrary ordinary wrappers. Only
// the selected path reaches Host Data; fn/effect siblings remain local values.
func TestNativeExplicitReceiverMixedContainers(t *testing.T) {
	host := NewNativeHost(Host{})
	defer host.Close()
	const source = `import go "bytes"
fn request(box:object)->effect {effect box.inner.buffer.String()}
flow Box(buffer:any)->object {return {inner:{buffer:buffer,callback:fn()->number{99}},other:fn()->null{null}}}
flow Execute(request:effect)->string{return perform request}
flow Main()->object {
    buffer=bytes.NewBufferString("hello")
    box=Box(buffer)
    prepared=request(box)
    text=Execute(prepared)
    alias=if true {box} else {{}}
    nested=perform effect alias.inner.buffer.String()
    missing={callback:fn()->null{null}}
    missing_request=effect missing.absent.String()
    skipped=if false {perform missing_request} else {"safe"}
    native_release(buffer)
    return {text:text,nested:nested,untouched:box.inner.callback(),skipped:skipped}
}`
	p, err := Compile(source, host)
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"nested":"hello","skipped":"safe","text":"hello","untouched":99}`
	for _, parallel := range []int{1, 7} {
		r, err := p.Run(context.Background(), nil, Options{Parallelism: parallel})
		if err != nil || display(t, r.Output) != want || len(r.Journal.Records()) != 4 {
			t.Fatal(r, err)
		}
	}
	executable, _ := buildNative(t, p, GoOptions{})
	for _, parallel := range []string{"1", "7"} {
		stdout, stderr, err := runNative(t, executable, "-parallel", parallel)
		if err != nil || strings.TrimSpace(stdout) != want {
			t.Fatal(stdout, stderr, err)
		}
	}
	session, err := NewSession(host)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	nativeCell(t, session, source, "")
	nativeCell(t, session, `Main()`, want)
	// Pruning the receiver must never exempt actual arguments from Data checks.
	p, err = Compile(`import go "bytes"
flow Main()->number {b=bytes.NewBufferString("");box={b:b,f:fn()->null{null}};perform effect box.b.WriteString(fn()->null{null});return 7}`, host)
	if err != nil {
		t.Fatal(err)
	}
	r, err := p.Run(context.Background(), nil, Options{})
	if err == nil || r.Error == nil || r.Error.Code != "E-SCHEMA" || len(r.Journal.Records()) != 1 {
		t.Fatal(r, err)
	}
}
