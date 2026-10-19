package tests

import (
	"context"
	. "lip/script"
	"strings"
	"sync/atomic"
	"testing"
)

// TestStaticSummaryOrderAndBoundaryMatrix keeps the current 1.2 summary rules
// honest under composition order changes.  These cases deliberately exercise
// only already specified transfers; the test does not introduce a new
// inference rule.
func TestStaticSummaryOrderAndBoundaryMatrix(t *testing.T) {
	var calls atomic.Int64
	host := testHost(func(_ context.Context, r Request) Response {
		calls.Add(1)
		return Result(r.Positional[0])
	})
	write := host.Contracts[0]
	write.Operation.Member = "write"
	write.Effect = WriteEffect
	serial := host.Contracts[0]
	serial.Operation.Member = "serial"
	serial.Concurrent = false
	unknown := host.Contracts[0]
	unknown.Operation.Member = "unknown"
	unknown.Effect = UnknownEffect
	host.Contracts = append(host.Contracts, write, serial, unknown)

	compile := func(declarations, expression string, want string) {
		t.Helper()
		source := `import python "example.worker" as worker
` + declarations + `
flow Main(xs:list)->void {
  for x in [1] parallel {
    p=` + expression + `
    perform p
  }
}`
		_, err := Compile(source, host)
		if want == "" {
			if err != nil {
				t.Fatalf("expected success for %s: %v", expression, err)
			}
			return
		}
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("expected %s for %s, got %v", want, expression, err)
		}
	}
	compileObject := func(declarations, expression string, want string) {
		t.Helper()
		source := `import python "example.worker" as worker
` + declarations + `
flow Main(obj:object)->void {
  for x in [1] parallel {
    p=` + expression + `
    perform p
  }
}`
		_, err := Compile(source, host)
		if want == "" {
			if err != nil {
				t.Fatalf("expected success for %s: %v", expression, err)
			}
			return
		}
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("expected %s for %s, got %v", want, expression, err)
		}
	}

	const identity = `fn identity(p:effect)->effect {p}`

	// Changing the order of identities and empty containers must not erase a
	// descriptor that was already exact.
	compile(identity, `map(concat([], [effect worker.read(1)]), identity)[0]`, "")
	compile(identity, `concat([], map([effect worker.read(1)], identity))[0]`, "")
	compile(identity, `slice(concat([effect worker.read(1)], xs), 0, 1)[0]`, "")
	compile(identity, `map(slice(concat([effect worker.read(1)], xs), 0, 1), identity)[0]`, "")
	compile(identity, `[q for q in map([effect worker.read(1)], identity)][0]`, "")

	// Prefix boundaries are exact only when every selected position is inside
	// the tracked prefix.  Reverse order, omitted stop, negative step, and a
	// position beyond the prefix must remain conservative.
	compile("", `slice(concat([effect worker.read(1), effect worker.read(1)], xs), 1, 2, 1)[0]`, "")
	compile("", `slice(concat([effect worker.read(1), effect worker.read(1)], xs), 0, 2, 2)[0]`, "")
	compile("", `slice(concat([effect worker.read(1), effect worker.write(1)], xs), 1, 2)[0]`, "E-PARALLEL")
	compile("", `slice(concat([effect worker.read(1)], xs), 0, 0)[0]`, "E-PARALLEL")
	compile("", `slice(concat([effect worker.read(1)], xs), 0, 2)[0]`, "E-PARALLEL")
	compile("", `slice(concat([effect worker.read(1)], xs), 0, 1, -1)[0]`, "E-PARALLEL")
	compile("", `slice(concat([effect worker.read(1)], xs), 1, 2)[0]`, "E-PARALLEL")
	compile("", `concat([effect worker.read(1)], xs)[-1]`, "E-PARALLEL")
	compile("", `concat([], xs)[0]`, "E-PARALLEL")
	compile("", `concat(xs, [])[0]`, "E-PARALLEL")

	// An untracked list remains a conservative map boundary even when a
	// callback happens to return a fixed request; only tracked slots/prefixes
	// receive callback transfer in this version.  Identity forwarding must not
	// manufacture provenance from the unavailable element description.
	compile("", `map(xs, fn(v:any)->effect {effect worker.read(1)})[0]`, "E-PARALLEL")
	compile(identity, `map(xs, identity)[0]`, "E-PARALLEL")
	compile("", `map(xs, fn(v:any)->effect {effect worker.write(1)})[0]`, "E-PARALLEL")

	// Union candidate order must not affect NoResult filtering or safety.  A
	// known valid candidate survives a missing closed position; an Unknown
	// candidate still blocks parallel admission in either order.
	compile("", `if true {[effect worker.read(1)][0]} else {[effect worker.read(1)][99]}`, "")
	compile("", `if true {[effect worker.read(1)][99]} else {[effect worker.read(1)][0]}`, "")
	compile("", `if true {[effect worker.read(1)][0]} else {xs[0]}`, "E-PARALLEL")
	compile("", `if true {xs[0]} else {[effect worker.read(1)][0]}`, "E-PARALLEL")
	compile("", `map(keys(if true {{a:1}} else {{b:1}}), fn(k:string)->effect {effect worker.read(1)})[0]`, "")
	compileObject("", `map(keys(obj), fn(k:string)->effect {effect worker.read(1)})[0]`, "")

	// A callback with one incompatible candidate remains dynamic even when the
	// candidates return the same request.  A callback with two compatible
	// candidates remains precise regardless of branch order.
	compile(`fn number_cb(v:number)->effect {effect worker.read(1)}
fn any_cb(v:any)->effect {effect worker.read(1)}`, `map([1], if true {number_cb} else {any_cb})[0]`, "")
	compile(`fn number_cb(v:number)->effect {effect worker.read(1)}
fn text_cb(v:string)->effect {effect worker.read(1)}`, `map([1], if true {number_cb} else {text_cb})[0]`, "E-PARALLEL")

	// Accumulator identity composes through empty concat and through all-return
	// branches, but a non-identity candidate must not be discarded.
	compile("", `fold(xs, effect worker.read(1), fn(a:effect, v:any)->effect {if true {a} else {a}})`, "")
	compile("", `fold(xs, effect worker.read(1), fn(a:effect, v:any)->effect {if true {a} else {effect worker.write(1)}})`, "E-PARALLEL")
	compile("", `fold(concat([], [1, 2]), effect worker.read(1), fn(a:effect, v:number)->effect {a})`, "")
	compile("", `fold(range(64), [], fn(a:list, v:number)->list {[effect worker.read(1)]})[0]`, "")
	compile("", `fold(range(65), [], fn(a:list, v:number)->list {[effect worker.read(1)]})[0]`, "E-PARALLEL")

	// State identity requires every normal exit and a source.  Non-identity
	// exits, source-free loops, and body writes remain conservative.
	compile("", `for n in xs with q=effect worker.read(1) {if n==0 {next q} else {break q}}`, "")
	compile("", `for n in xs with q=effect worker.read(1) {if n==0 {next q} else {break effect worker.write(1)}}`, "E-PARALLEL")
	compile("", `for with q=effect worker.read(1) {next q}`, "E-PARALLEL")
	compile("", `for n in xs with q=effect worker.read(1) {v=perform effect worker.write(1); next q}`, "E-PARALLEL")

	if calls.Load() != 0 {
		t.Fatalf("static checking invoked host provider %d times", calls.Load())
	}
}
