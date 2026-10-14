package tests

import (
	"context"
	"errors"
	. "lipbeta/script"
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
	module := `count=0
__version__="fixture-1"
def touch():
    global count
    count += 1
    return count
def once(x): return x
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
		if strings.Contains(name, ".get.") || strings.Contains(name, ".prepare.") {
			t.Fatal(name)
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
flow Main()->object {
    upper=perform effect strings.ToUpper("hello")
    buffer=perform effect bytes.NewBufferString(upper)
    size=perform effect buffer.WriteString("!")
    counts=perform effect py.Counter(["a","b","a"])
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
