package compiler

import (
	"strings"
	"testing"

	"lipalpha/compiler/ast"
)

func TestLexicalImportScopes(t *testing.T) {
	for _, source := range []string{
		`import python "math" as number; print(number.pi)`,
		`import python "math" as list; print(list.sqrt(9))`,
		`import python "math" as m; fn identity(m: number) { m }; print(identity(3), m.pi)`,
		`import python "math" as m; print(list.map([{pi:3}], fn(m) { m.pi }), m.pi)`,
		`import python "math" as m; print([m.pi for m in [{pi:3}]], m.pi)`,
		`import python "math" as m; for m in [{pi:3}] { print(m.pi) }; print(m.pi)`,
		`import python "math" as m; flow F(m: object) -> any { return m.pi }`,
		`import python "math" as m; m = {pi: m.pi}; print(m.pi)`,
		`import python "math" as m; match true { true => { m = {pi:3}; print(m.pi) }, false => { print(m.pi) } }; print(m.pi)`,
		`import python "math"; match true { true => { math = {pi:3}; print(math.pi) }, false => { print(math.pi) } }; print(math.pi)`,
		`import python "math"; for i in range(2) { print(math.pi); match true { true => { math = {pi:3}; print(math.pi) }, false => {} }; print(math.pi) }; print(math.pi)`,
		`import host "fetch" as f; fn identity(f: number) { f }; print(identity(3), f())`,
		`import go "fmt" as f; for f in [1] { print(f) }; f.Println(3)`,
	} {
		graph, err := ParseAndBuild(source)
		if err != nil {
			t.Fatalf("%s: %v", source, err)
		}
		if _, err := GenerateGo(graph); err != nil {
			t.Fatalf("%s: %v", source, err)
		}
	}
	graph, err := ParseAndBuild(`import python "math"; match true { true => { math = {pi:3} }, false => {} }; print(math.pi)`)
	if err != nil {
		t.Fatal(err)
	}
	last := graph.Nodes[len(graph.Nodes)-1]
	if firstPythonCall(last.Expr) == nil || len(last.Deps) != 0 {
		t.Fatalf("arm-local binding leaked: %+v", last)
	}
	for _, source := range []string{
		`import python "math"; math = {pi:3}; print(math.sqrt(9))`,
		`import host "s.*"; for s in [1] { s.emit(1) }`,
		`import go "fmt" as f; for f in [1] { f.Println(1) }`,
	} {
		if _, err := ParseAndBuild(source); err == nil || !strings.Contains(err.Error(), "shadows an import") {
			t.Fatalf("shadowed call: %s: %v", source, err)
		}
	}
}

func TestImportBackendConsistency(t *testing.T) {
	for _, source := range []string{
		`import host "list.map"; flow F() -> any { return list.map(1) }`,
		`import host "string.nope"; flow F() -> string { return string.nope("x") }`,
		`import host "state"; flow F() -> any { return state(1) }`,
	} {
		graph, err := ParseAndBuild(source)
		if err != nil {
			t.Fatal(err)
		}
		call := graph.Nodes[0].Expr.(*ast.CallExpr)
		if !call.Host || graph.Nodes[0].State || pureExpression(call, nil) {
			t.Fatalf("lost explicit Host import: %+v", call)
		}
	}
	for _, source := range []string{
		`import python "math"; import go "math"; print(math.sqrt(9))`,
		`import python "math"; import host "math.*"; print(math.sqrt(9))`,
		`import python "math" as p; import go "math" as g; print(math.sqrt(9))`,
	} {
		if _, err := ParseAndBuild(source); err == nil || !strings.Contains(err.Error(), "multiple backends") {
			t.Fatalf("%s: %v", source, err)
		}
	}
	graph, err := ParseAndBuild(`import python "math" as p; import go "math" as g; flow F() -> number { return p.sqrt(9) }`)
	if err != nil {
		t.Fatal(err)
	}
	if ops := requiredHostOperations(graph); len(ops) != 0 {
		t.Fatalf("Python call required Go registration: %v", ops)
	}
	graph, err = ParseAndBuild(`import go "string" as g; print(string.trim(" x "))`)
	if err != nil {
		t.Fatal(err)
	}
	if ops := requiredHostOperations(graph); len(ops) != 0 {
		t.Fatalf("core call required Go registration: %v", ops)
	}
}

func TestHeterogeneousConditionals(t *testing.T) {
	for _, source := range []string{
		`flow F(flag: bool) -> any { return if flag { 1 } else { "off" } }`,
		`fn f(flag: bool) -> any { if flag { 1 } else { "off" } }; print(f(true))`,
	} {
		if _, err := ParseAndBuild(source); err != nil {
			t.Fatal(err)
		}
	}
	for _, source := range []string{
		`flow F(flag: bool) -> number { return if flag { 1 } else { "off" } }`,
		`fn f(flag: bool) -> number { if flag { 1 } else { "off" } }; print(f(true))`,
		`fn done(x:string)->bool { true }; flow F()->string { return feedback(str(1), state, done, 2) }`,
		`flow F()->string { return feedback(str(1), string.upper, string.lower, 2) }`,
	} {
		if _, err := ParseAndBuild(source); err == nil {
			t.Fatal("accepted invalid result:", source)
		}
	}
}

func TestOptionalSignaturesAndMatchNarrowing(t *testing.T) {
	for _, source := range []string{
		`fn double(x: number?) -> number? { match x { null => null, _ => x * 2 } }; print(double(null), double(3))`,
		`fn identity(x: any?) -> any? { x }; print(identity(2) * 2)`,
		`print(list.filter([1,2], fn(x:any?)->any? { x == 2 }))`,
		`flow F(x: number?) -> number { match x { null => { return 0 }, _ if x > 0 => { return x * 2 }, _ => { return x } } }`,
		`flow F(x: number?) -> bool { return x == 3 }`,
		`print(list.map([null,2], fn(x: number?) -> number? { match x { null => null, _ => x * 2 } }))`,
		`flow F(x: bool?) -> bool { return match x { null => false, true => x, false => x } }`,
	} {
		graph, err := ParseAndBuild(source)
		if err != nil {
			t.Fatalf("%s: %v", source, err)
		}
		if _, err := GenerateGo(graph); err != nil {
			t.Fatalf("%s: %v", source, err)
		}
	}
	for _, source := range []string{
		`fn f(x: number?) -> number { x * 2 }; print(f(3))`,
		`fn f(x: number?) -> number { match x { null if false => 0, _ => x * 2 } }; print(f(3))`,
		`flow F(x: number?) -> number { match x { null => {}, _ => { print(x * 2) } }; return x * 2 }`,
		`fn f(x: void?) { x }; print(1)`,
		`fn f() -> void? { null }; print(1)`,
	} {
		if _, err := ParseAndBuild(source); err == nil {
			t.Fatalf("accepted invalid optional use: %s", source)
		}
	}
}

func TestExternalExpressionComposition(t *testing.T) {
	for _, source := range []string{
		`import host "fetch"; flow F() -> string { return str(fetch()) }`,
		`import host "fetch"; flow F() -> object { return {value: fetch()} }`,
		`import host "fetch"; print([fetch(x) for x in range(3)])`,
		`import host "fetch"; a = [x for x in fetch()]; print(a)`,
		`import python "math" as m; print(m.sqrt(9), m.pi)`,
		`import go "fmt" as f; print(f.Sprintf("%v", 3))`,
	} {
		graph, err := ParseAndBuild(source)
		if err != nil {
			t.Fatalf("%s: %v", source, err)
		}
		code, err := GenerateGo(graph)
		if err != nil || !strings.Contains(code, "host.EffectsOf(") {
			t.Fatalf("missing aggregate effect: %v", err)
		}
	}
}

func TestAttemptCountCannotOverflow(t *testing.T) {
	for _, source := range []string{
		`flow F()->string { return retry(str(1), 9223372036854775808) }`,
		`fn done(x:string)->bool { true }; flow F()->string { return feedback(str(1), string.upper, done, 9007199254740992) }`,
	} {
		if _, err := ParseAndBuild(source); err == nil || !strings.Contains(err.Error(), "safe integer") {
			t.Fatalf("%s: %v", source, err)
		}
	}
}

func TestHostCannotImportGeneratedFunctionNames(t *testing.T) {
	if _, err := ParseAndBuild(`import host "__lip_fn_double" as external; fn double(x:number)->number { x*2 }; print(external(1))`); err == nil || !strings.Contains(err.Error(), "reserved generated-name prefix") {
		t.Fatal(err)
	}
}
