package compiler

import (
	"reflect"
	"strings"
	"testing"

	"lipalpha/compiler/lexer"
	"lipalpha/compiler/parser"
)

func TestForContracts(t *testing.T) {
	for _, source := range []string{
		`for i in range(4) { doubled = i * 2; print(doubled) }`,
		`for i in [] {}`,
		`flow Forever() { for { break }; print("done") }`,
		`flow Each(values: list) -> number { for x in values { print(x) }; return 7 }`,
		`for i in [true, false] { match i { true => { continue }, _ => { break } } }`,
		`for i in range(3) { for j in range(i) { print(j); break }; print(i) }`,
		`i = [1, 2]; for i in i { print(i) }; print(i)`,
		`for i in range(2) { local = i; print(local) }; for i in range(2) { local = i; print(local) }; local = 7; print(local)`,
		`import python "math"; for math in [{pi: 3}] { print(math.pi) }; pi = math.pi; print(pi)`,
		`import host "service.*"; for i in range(3) { service.emit(i) }`,
	} {
		graph, err := ParseAndBuild(source)
		if err != nil {
			t.Fatalf("%s: %v", source, err)
		}
		if _, err := GenerateGo(graph); err != nil {
			t.Fatalf("%s: %v", source, err)
		}
	}
	for _, tc := range []struct{ source, want string }{
		{`for i in 7 {}`, "source must be a list"},
		{`for i in "abc" {}`, "source must be a list"},
		{`for i in null {}`, "source must be a list"},
		{`for __for_0 in [] {}`, "reserved"},
		{`for i in missing {}`, "reference"},
		{`for i in range(2) { print(later); later = i }`, "reference"},
		{`for i in range(2) { i = 1 }`, "duplicate binding"},
		{`for i in range(2) { local = i; local = 2 }`, "duplicate binding"},
		{`for i in [] {}; print(i)`, "scoped"},
		{`for i in [] { local = 1 }; print(local)`, "scoped"},
		{`flow Each() -> number { for i in range(2) { return i }; return 0 }`, "return is not allowed"},
		{`for i in range(2) { match true { true => { return }, false => {} } }`, "return is not allowed"},
		{`for i in range(2) { persistent = state(i) }`, "state is not allowed"},
		{`for i in range(2) { match true { true => { persistent = state(i) }, false => {} } }`, "state is not allowed"},
		{`break`, "only allowed inside a for"},
		{`continue`, "only allowed inside a for"},
		{`match true { true => { break }, false => {} }`, "only allowed inside a for"},
		{`for i in [] { break; print(1) }`, "statements after"},
		{`for i in [] { continue; print(1) }`, "statements after"},
		{`for i in [] { match true { true => { break }, false => { continue } }; print(i) }`, "statements after"},
		{`fn each() { for i in [] {} }; print(each())`, "only allowed in Flow"},
		{`for i in range(2) { undeclared(i) }`, "not declared"},
		{`for i in 1: { break }`, "unbounded range"},
	} {
		if _, err := ParseAndBuild(tc.source); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%s: got %v, want %s", tc.source, err, tc.want)
		}
	}
}

func TestForCapturesAndInspection(t *testing.T) {
	graph, err := ParseAndBuild(`import host "service.*"
flow Each(flag: bool, values: list, scale: number, unused: any) {
 match flag {
  true => { local = scale; for i in values { service.emit(i * local) } },
  false => { local = scale + 1; for i in values { for j in [i] { service.emit(j * local) } } }
 }
}`)
	if err != nil {
		t.Fatal(err)
	}
	loops := 0
	for _, node := range graph.Nodes {
		if node.For == nil {
			continue
		}
		loops++
		if len(node.Deps) != 2 || node.Deps[0] != "values" || node.For.Captures["local"] != node.Deps[1] {
			t.Fatal(node)
		}
		if !reflect.DeepEqual(node.For.Body.Params, []string{"local", "i"}) {
			t.Fatal(node.For.Body.Params)
		}
	}
	if loops != 2 {
		t.Fatal(loops)
	}
	if got := requiredHostOperations(graph); !reflect.DeepEqual(got, []string{"service.emit"}) {
		t.Fatal(got)
	}
	inspection, err := InspectJSON(graph)
	if err != nil || !strings.Contains(string(inspection), `"kind": "for"`) || !strings.Contains(string(inspection), `"service.emit"`) {
		t.Fatalf("%s: %v", inspection, err)
	}
}

func TestForInteractiveContinuation(t *testing.T) {
	for _, source := range []string{`for`, `for i`, `for i in`, `for i in range(2)`, `for i in range(2) {`, `for i in range(2) { match i { 0 => { continue }, _ => { break } }`} {
		tokens, err := lexer.New(source).Lex()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := parser.New(tokens).ParseCell(); !parser.IsIncomplete(err) {
			t.Fatalf("%s: %v", source, err)
		}
	}
}
