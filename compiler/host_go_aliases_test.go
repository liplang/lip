package compiler

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"lipalpha/compiler/ast"
	"lipalpha/compiler/lexer"
	"lipalpha/compiler/parser"
)

func TestHostGoImportAliases(t *testing.T) {
	for _, tc := range []struct{ declaration, call, canonical string }{
		{`import host "fetch" as f`, `f(7)`, "fetch"},
		{`import host "service.fetch" as fetch`, `fetch(7)`, "service.fetch"},
		{`import host "service.*" as s`, `s.fetch(7)`, "service.fetch"},
		{`import host "service.*" as s`, `s.nested.fetch(7)`, "service.nested.fetch"},
		{`import go "fmt" as f`, `f.Println(7)`, "fmt.Println"},
		{`import go "example.com/adapter" as a`, `a.Transform(7)`, "example.com/adapter.Transform"},
		{`import go "fmt"`, `fmt.Println(7)`, "fmt.Println"},
		{`import host "list.*" as seq`, `seq.map(7)`, "list.map"},
		{`import host "state" as s`, `s(7)`, "state"},
		{`import host "retry" as r`, `r(7)`, "retry"},
		{`import go "string" as text`, `text.trim(7)`, "string.trim"},
	} {
		source := tc.declaration + "; flow Main() -> any { return " + tc.call + " }"
		tokens, err := lexer.New(source).Lex()
		if err != nil {
			t.Fatal(err)
		}
		program, err := parser.New(tokens).Parse()
		if err != nil {
			t.Fatal(err)
		}
		before, _ := json.Marshal(program)
		graph, err := Build(program)
		if err != nil {
			t.Fatalf("%s: %v", source, err)
		}
		after, _ := json.Marshal(program)
		if string(before) != string(after) {
			t.Fatal("alias resolution mutated source AST")
		}
		call, ok := graph.Nodes[0].Expr.(*ast.CallExpr)
		if !ok || call.Name != tc.canonical || !call.Host || call.Python || pureExpression(call, nil) {
			t.Fatalf("%s: %+v", source, call)
		}
		if len(graph.Nodes[0].Deps) != 0 {
			t.Fatal(graph.Nodes[0].Deps)
		}
		operations := requiredHostOperations(graph)
		if !containsName(operations, tc.canonical) {
			t.Fatal(operations)
		}
		code, err := GenerateGoWithOptions(graph, GenerateOptions{PackageName: "flow"})
		if err != nil || !strings.Contains(code, `host.Call(ctx, "`+tc.canonical+`"`) {
			t.Fatalf("%s: %v", source, err)
		}
		if _, err := InspectJSON(graph); err != nil {
			t.Fatal(err)
		}
	}
	graph, err := ParseAndBuild(`import host "fetch" as f; import host "fetch" as g; import go "fmt" as p; import go "fmt" as q; f(); g(); p.Println(1); q.Println(2)`)
	if err != nil || len(graph.Dependencies) != 4 {
		t.Fatalf("%+v: %v", graph, err)
	}
	if got := requiredHostOperations(graph); !reflect.DeepEqual(got, []string{"fetch", "fmt.Println"}) {
		t.Fatal(got)
	}
}

func containsName(names []string, want string) bool {
	for _, name := range names {
		if name == want {
			return true
		}
	}
	return false
}

func TestHostGoAliasErrorsAndBoundaries(t *testing.T) {
	for _, tc := range []struct{ source, want string }{
		{`import host "fetch" as f; import go "fmt" as f; print(1)`, "duplicate module alias"},
		{`import go "fmt" as f; import go "f"; print(1)`, "conflicts"},
		{`import host "fetch" as f; import host "f"; print(1)`, "conflicts"},
		{`import go "fmt" as math; import python "math"; print(1)`, "conflicts"},
		{`import go "fmt" as python; print(1)`, "reserved"},
		{`import host "service.*" as s; s()`, "not a function"},
		{`import host "service.*"; service()`, "not a function"},
		{`import host "service.*" as s; service()`, "not a function"},
		{`import host "fetch" as f; f.fetch()`, "call f(...) directly"},
		{`import go "fmt" as f; f()`, "not a function"},
		{`import host "fetch" as f; fn f() { 1 }; print(1)`, "function"},
		{`import host "fetch" as f; fn pure() { f() }; print(pure())`, "must be pure"},
		{`import go "fmt" as f; fn pure() { f.Println(1) }; print(pure())`, "must be pure"},
		{`import host "list.*" as seq; fn pure() { seq.map(7) }; print(pure())`, "must be pure"},
		{`import go "fmt" as f; print(list.map([1], fn(x) { f.Println(x) }))`, "callback must be pure"},
	} {
		if _, err := ParseAndBuild(tc.source); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%s: %v; want %s", tc.source, err, tc.want)
		}
	}
}

func TestAliasesInLoopRetryAndFeedback(t *testing.T) {
	graph, err := ParseAndBuild(`import host "service.*" as s
import host "service.step" as step
import host "service.verify" as verify
import go "fmt" as f
for i in range(3) {
 result = retry(s.fetch(i), 2)
 candidate = feedback(s.fetch(i), step, verify, 3)
 candidate2 = feedback(s.fetch(i), s.step, s.verify, 3)
 f.Println(result, candidate, candidate2)
}`)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(requiredHostOperations(graph), []string{"service.fetch", "service.step", "service.verify", "fmt.Println"}) {
		t.Fatal(requiredHostOperations(graph))
	}
	if _, err := GenerateGoWithOptions(graph, GenerateOptions{PackageName: "flow"}); err != nil {
		t.Fatal(err)
	}
}

func TestAliasesDistinguishBareAndQualifiedCalls(t *testing.T) {
	for _, source := range []string{
		`import python "math" as print; print(print.sqrt(4))`,
		`import go "math" as len; print(len([]), len.Abs(-1))`,
		`import host "service.*" as fold; print(fold([],0,fn(a,x){a+x}),fold.read(1))`,
		`import go "math" as twice; fn twice(x:number){x*2}; print(twice(3),twice.Abs(-2))`,
		`import python "math" as len; import host "len"; print(len(1),len.sqrt(4))`,
		`import host "emit" as print; print(1)`,
		`import host "fetch" as state; state(1)`,
		`import host "fetch" as retry; retry(1)`,
		`import host "emit" as string; string(1); print(string.trim(" x "))`,
		`import host "emit" as list; list(1); print(list.sum([2,3]))`,
	} {
		graph, err := ParseAndBuild(source)
		if err != nil {
			t.Fatalf("%s: %v", source, err)
		}
		if _, err := GenerateGo(graph); err != nil {
			t.Fatalf("%s: %v", source, err)
		}
	}
	graph, err := ParseAndBuild(`import python "math" as print; print(print.sqrt(4))`)
	if err != nil {
		t.Fatal(err)
	}
	outer := graph.Nodes[0].Expr.(*ast.CallExpr)
	inner := outer.Args[0].(*ast.CallExpr)
	if outer.Name != "print" || externalCall(outer) || inner.Name != "math.sqrt" || !inner.Python {
		t.Fatalf("alias changed bare core call: outer=%+v inner=%+v", outer, inner)
	}
	graph, err = ParseAndBuild(`import host "emit" as print; print(1)`)
	if err != nil {
		t.Fatal(err)
	}
	call := graph.Nodes[0].Expr.(*ast.CallExpr)
	if !call.Host || call.Name != "emit" {
		t.Fatalf("single-operation alias was ignored: %+v", call)
	}
}
