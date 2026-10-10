package compiler

import (
	"reflect"
	"strings"
	"testing"

	"lipalpha/compiler/lexer"
	"lipalpha/compiler/parser"
)

func TestMatchContracts(t *testing.T) {
	for _, source := range []string{
		`flow Choice(flag: bool) -> any { return match flag { true => 7, false => "off" } }`,
		`fn choice(flag: bool) -> any { match flag { true => 7, false => "off" } }
flow Choice(flag: bool) -> any { return choice(flag) }`,
		`flow Choice(flag: bool) -> any { match flag { true => { return 7 }, false => { return "off" } } }`,
		`flow Choice(flag: bool) -> number { match flag { true => { return 7 }, false => { return 8 } } }`,
		`flow Choice(flag: bool) -> number? { match flag { true => { return 7 }, false => {} } }`,
		`flow Choice(value: any) -> any { return match value { null => 0, true => "yes", -1 => false, _ => value } }`,
		`flow Choice(value: number) -> any { return match value { 0 if false => "guarded", 0 => 7, _ if value > 0 => true, _ => null } }`,
		`flow Choice(value: number) -> any { return match value { 0, 1 => "small", _ => "other" } }`,
		`flow Choice(flag: bool) -> number { return match flag { true, false => 1 } }`,
		`fn describe(value: any) -> string { match value { number => str(value), string => value, null => "nil", _ => "other" } }
flow Choice(value: any) -> string { return describe(value) }`,
		`flow Choice(value: number?) -> string { return match value { number => "number", null => "null" } }`,
		`flow Choice(value: any) -> string { match value { number => { print(value) }, _ => {} }; return "done" }`,
		`flow Choice(value: number) -> number { return match value { -1 => { 1 }, _ => { 2 } } }`,
		`match "开" { "开" => { print(7) }, _ => {} }`,
		`match true { true => { local = 1; print(local) }, false => {} }
match false { true => {}, false => { local = 2; print(local) } }
local = 3; print(local)`,
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
		{`flow Choice(flag: bool) -> any { return match flag { true => 7 } }`, "non-exhaustive"},
		{`match 1 { 1 => { print(1) } }`, "non-exhaustive"},
		{`flow Choice(value: any) -> any { return match value { true => 1, false => 0 } }`, "non-exhaustive"},
		{`print(match true { _ if true => 1 })`, "non-exhaustive"},
		{`print(match 0 { 0 => 1, -0 => 2, _ => 3 })`, "unreachable"},
		{`print(match 0 { 0, 0 => 1, _ => 3 })`, "unreachable"},
		{`print(match 0 { 0 => 1, 0, 2 => 3, _ => 4 })`, "unreachable"},
		{`print(match 0 { _ => 1, 0 => 2 })`, "unreachable"},
		{`print(match true { 1 => 1, _ => 2 })`, "pattern has type"},
		{`print(match true { 1, 2 => 1, _ => 2 })`, "pattern has type"},
		{`fn f(value: string) -> string { match value { number => "number", _ => value } }`, "type pattern number does not match value type string"},
		{`fn f(value: any) -> string { match value { number => "number", number => "again", _ => "other" } }`, "unreachable"},
		{`print(match 0 { _ if 7 => 1, _ => 2 })`, "guard must be bool"},
		{`print(match 0 { _ => 1 _ => 2 })`, "between match arms"},
		{`print(match 0 { 0 -> 1, _ => 2 })`, "expected =>"},
		{`print(match 0 { _, 1 => 2 })`, "wildcard _ must be a separate match arm"},
		{`print(match 0 {})`, "at least one arm"},
		{`print(match 0 { unknown => 1, _ => 2 })`, "match pattern"},
		{`flow Choice(flag: bool) -> number { return match flag { true => 7, false => "off" } }`, "declared output"},
		{`fn choice(flag: bool) -> number { match flag { true => 7, false => "off" } }
flow Choice(flag: bool) -> any { return choice(flag) }`, "declared output"},
		{`flow Choice(flag: bool) -> number { match flag { true => { return 7 }, false => {} } }`, "may produce no value"},
		{`match true { true => { local = 7 }, false => {} }; print(local)`, "scoped to a match arm"},
		{`match true { true => { local = 7 }, false => { print(local) } }`, "reference"},
		{`flow Choice(flag: bool) -> number { match flag { true => { return 7 }, false => { return 8 } }; print(9) }`, "statements after return"},
		{`fn choice(flag: bool) { match flag { true => print(7), false => null } }; print(choice(true))`, "must be pure"},
	} {
		if _, err := ParseAndBuild(tc.source); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%s: got %v, want %s", tc.source, err, tc.want)
		}
	}
}

func TestMatchDependenciesAndScopes(t *testing.T) {
	graph, err := ParseAndBuild(`flow Choice(flag: bool, value: number) -> any {
 match flag {
  true => { local = value * 2; return list.map([local], fn(x) { match x { 0 => local, _ => x } }) },
  false => { local = "off"; return local }
 }
}`)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, node := range graph.Nodes {
		if names[node.Name] {
			t.Fatalf("duplicate graph name %s", node.Name)
		}
		names[node.Name] = true
	}
	if !reflect.DeepEqual(graph.Nodes[0].Deps, []string{"flag"}) {
		t.Fatal(graph.Nodes[0])
	}
	if _, err := InspectJSON(graph); err != nil {
		t.Fatal(err)
	}
	graph, err = ParseAndBuild(`flow Choice(flag: bool, value: number) -> any {
 return match flag { true if value > 0 => value, _ => "off" }
}`)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(graph.Nodes[0].Deps, []string{"flag", "value"}) {
		t.Fatal(graph.Nodes[0])
	}
}

func TestMatchInteractiveContinuation(t *testing.T) {
	for _, source := range []string{`match`, `match true {`, `match true { true`, `match true { true =>`, `match true { true => { print(1)`} {
		tokens, err := lexer.New(source).Lex()
		if err != nil {
			t.Fatal(err)
		}
		_, err = parser.New(tokens).ParseCell()
		if !parser.IsIncomplete(err) {
			t.Fatalf("%q: %v", source, err)
		}
	}
}
