package compiler

import (
	"encoding/json"
	"strings"
	"testing"

	"lipalpha/compiler/ast"
	"lipalpha/compiler/lexer"
	"lipalpha/compiler/parser"
)

func TestPythonImportNamesAndAliases(t *testing.T) {
	for _, tc := range []struct{ declaration, call, canonical, alias string }{
		{`import python "math"`, `math.sqrt(9)`, "math.sqrt", ""},
		{`import python "math" as m`, `m.sqrt(9)`, "math.sqrt", "m"},
		{`import python "numpy>=1.26,<3" as np`, `np.mean([2,3])`, "numpy.mean", "np"},
		{`import python "sklearn" as ml`, `ml.preprocessing.scale([2,3])`, "sklearn.preprocessing.scale", "ml"},
		{`import python "xml.etree.ElementTree" as et`, `et.fromstring("<a/>")`, "xml.etree.ElementTree.fromstring", "et"},
		{`import python "xml.etree.ElementTree" as xml`, `xml.fromstring("<a/>")`, "xml.etree.ElementTree.fromstring", "xml"},
	} {
		source := tc.declaration + "\nflow main() -> any { return " + tc.call + " }"
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
		call, ok := graph.Nodes[len(graph.Nodes)-1].Expr.(*ast.CallExpr)
		if !ok || call.Name != tc.canonical || graph.Dependencies[0].Alias != tc.alias {
			t.Fatalf("%+v", graph)
		}
		code, err := GenerateGoWithOptions(graph, GenerateOptions{PackageName: "flow"})
		if err != nil || !strings.Contains(code, `Alias string`) {
			t.Fatalf("metadata: %v\n%s", err, code)
		}
		data, err := InspectJSON(graph)
		if err != nil {
			t.Fatal(err)
		}
		var document any
		if err := json.Unmarshal(data, &document); err != nil {
			t.Fatal(err)
		}
		data, _ = json.Marshal(document)
		if tc.alias != "" && !strings.Contains(string(data), `"alias":"`+tc.alias+`"`) {
			t.Fatalf("missing alias: %s", data)
		}
	}
}

func TestInvalidImportAliases(t *testing.T) {
	for _, tc := range []struct{ source, message string }{
		{`require python "math"; print(1)`, "has been replaced"},
		{`requires python "math"; print(1)`, "has been replaced"},
		{`import python "math" as print; print(1)`, "reserved"},
		{`import python "math" as list; print(1)`, "reserved"},
		{`import python "math" as m; import python "json" as m; print(1)`, "duplicate module alias"},
		{`import python "math" as json; import python "json"; print(1)`, "conflicts"},
		{`import host "fetch" as f; print(1)`, "aliases"},
		{`import go "example.com/adapter" as a; print(1)`, "aliases"},
		{`import python "math" as m; m = 2`, "binding"},
		{`import python "math" as m; flow main(m: number) {}`, "input"},
		{`import python "math" as m; fn m(n: number) { return n }; print(1)`, "function"},
		{`import python "math" as m; fn f(m: number) { return m }; print(1)`, "parameter"},
		{`import python "math" as m; items = [m for m in [1,2]]`, "Map variable"},
		{`import python "math" as m; m()`, "not a function"},
		{`import python "scikit-learn" as ml; print(1)`, "actual Python module name"},
	} {
		if _, err := ParseAndBuild(tc.source); err == nil || !strings.Contains(err.Error(), tc.message) {
			t.Fatalf("%s: %v; want %s", tc.source, err, tc.message)
		}
	}
	for _, name := range []string{"flow", "a.b", "123", "a b"} {
		_, err := Build(&ast.Program{Dependencies: []ast.Dependency{{Kind: "python", Spec: "math", Alias: name}}, Flow: &ast.Flow{Name: "main", ReturnType: "void"}})
		if err == nil || !strings.Contains(err.Error(), "invalid module alias") {
			t.Fatalf("%q: %v", name, err)
		}
	}
}

func TestPythonStandardLibraryNamespaceCollisions(t *testing.T) {
	for _, source := range []string{
		`import python "string" as text; result = text.capwords("hello world"); print(result,string.trim(" x "))`,
		`import python "string"; result = string.capwords("hello world"); print(result)`,
		`import python "list" as seq; result = seq.map([1]); print(result)`,
	} {
		graph, err := ParseAndBuild(source)
		if err != nil {
			t.Fatal(err)
		}
		if !graph.Nodes[0].Expr.(*ast.CallExpr).Python || pureExpression(graph.Nodes[0].Expr, graph.Functions) {
			t.Fatal("Python import was treated as a core operation")
		}
		code, err := GenerateGo(graph)
		if err != nil || !strings.Contains(code, `host.Call(ctx, "`+graph.Nodes[0].Expr.(*ast.CallExpr).Name+`"`) {
			t.Fatalf("%s: %v", source, err)
		}
	}
	_, err := ParseAndBuild(`import python "list" as seq; fn f(x:list) { return seq.sum(x) }; print(f([1]))`)
	if err == nil || !strings.Contains(err.Error(), "must be pure") {
		t.Fatalf("Python operation in fn: %v", err)
	}
	_, err = ParseAndBuild(`import python "string" as text; print(text.capwords("hello world"))`)
	if err == nil || !strings.Contains(err.Error(), "nested external") {
		t.Fatalf("hidden Python effect: %v", err)
	}
}

func TestRemovedDependencyKeywordIsAnOrdinaryIdentifier(t *testing.T) {
	for _, source := range []string{`require = 2; print(require)`, `requires = "x"; print(requires)`} {
		if _, err := ParseAndBuild(source); err != nil {
			t.Fatalf("%s: %v", source, err)
		}
	}
}
