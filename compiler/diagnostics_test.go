package compiler

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckReports(t *testing.T) {
	for _, tc := range []struct {
		source, code string
		line         int
	}{
		{"flow Good(x:string)->string{return x}", "", 0},
		{"flow Bad()->string {\n return string.nope(\"x\")\n}", "LIP_UNKNOWN_OPERATION", 2},
		{"flow Bad()->string {\n return string.trim(1)\n}", "LIP_TYPE_ERROR", 2},
		{"flow Bad()->string {\n return missing\n}", "LIP_NAME_ERROR", 2},
		{"flow Bad()->string {\n return \"oops\n}", "LIP_LEX_ERROR", 2},
		{"flow Bad(x:string) string {return x}", "LIP_SYNTAX_ERROR", 1},
		{"flow Bad(x:string)->string{return fetch(x)}", "LIP_DEPENDENCY_ERROR", 1},
		{"flow Bad()->number{type=1; type=2; return type}", "LIP_CHECK_ERROR", 1},
		{"fn f(x:number){return f(x)} flow Bad()->number{return f(1)}", "LIP_RECURSION_ERROR", 1},
		{"flow Bad()->number { print(79 / 134) }", "LIP_RETURN_ERROR", 1},
		{"flow Bad() { return 1 }", "LIP_RETURN_ERROR", 1},
		{"flow Bad()->number { return }", "LIP_RETURN_ERROR", 1},
		{"flow Bad()->string { return 1 }", "LIP_TYPE_ERROR", 1},
		{"flow Bad()->number { return print(1) }", "LIP_TYPE_ERROR", 1},
		{"flow Bad()->int { return 1 }", "LIP_TYPE_ERROR", 1},
		{"flow Bad(x:void) {}", "LIP_TYPE_ERROR", 1},
		{"a = 2 b = 3 print(a / b)", "LIP_SYNTAX_ERROR", 1},
	} {
		report := CheckSource("中文.lip", tc.source)
		if report.Schema != "lip.diagnostics.v1" || report.File != "中文.lip" || report.OK != (tc.code == "") {
			t.Fatal(report)
		}
		data, err := json.Marshal(report)
		if err != nil || !json.Valid(data) {
			t.Fatal(err)
		}
		if tc.code == "" {
			if len(report.Diagnostics) != 0 || report.Flow != "Good" {
				t.Fatal(report)
			}
		} else {
			if len(report.Diagnostics) != 1 {
				t.Fatal(report)
			}
			d := report.Diagnostics[0]
			if d.Code != tc.code || d.Line != tc.line || d.Column < 1 || d.Severity != "error" || len(d.Hints) == 0 {
				t.Fatalf("%s: %+v", tc.source, d)
			}
			if d.SourceLine != strings.Split(tc.source, "\n")[tc.line-1] {
				t.Fatal(d)
			}
		}
		if second, _ := json.Marshal(CheckSource("中文.lip", tc.source)); string(second) != string(data) {
			t.Fatal("unstable report")
		}
	}
	report := CheckFile(filepath.Join(t.TempDir(), "missing.lip"))
	if report.OK || report.Diagnostics[0].Code != "LIP_IO_ERROR" || report.Diagnostics[0].Line != 0 {
		t.Fatal(report)
	}
}

func TestStatementSeparators(t *testing.T) {
	for _, source := range []string{
		"a = 2\nb = 3\nprint(a / b)",
		"a = 2; b = 3; print(a / b)",
		"a = 2;\nb = 3;\nprint(a / b);",
		`flow main() { a = 2; b = 3; print(a / b) }`,
		`flow main() { print(1); return; }`,
		`flow main() -> number { a = 2; return a; }`,
		"a = (\n 2 + 3\n); print(a)",
		"a = [\n 2, 3\n]\nprint(a)",
		"a = 2 // next statement on a new line\nb = 3\nprint(a / b)",
		`when true { print(2); print(3); }; print(4)`,
		`fn twice(a: number) -> number { return a * 2; }; print(twice(3));`,
		`fn twice(a: number) -> number { return a * 2 }; twice(3)`,
	} {
		if _, err := ParseAndBuild(source); err != nil {
			t.Fatalf("%s: %v", source, err)
		}
	}
	for _, source := range []string{
		`a = 2 b = 3`,
		`print(2) print(3)`,
		`flow main() { a = 2 b = 3; print(a / b) }`,
		`flow main() -> number { a = 2 return a }`,
		`when true { print(2) print(3) }`,
		`when true { print(2) } print(3)`,
		"a = (\n 2 + 3\n) print(a)",
	} {
		report := CheckSource("separators.lip", source)
		if report.OK || !strings.Contains(report.Diagnostics[0].Message, "same line must be separated") || !strings.Contains(report.Diagnostics[0].Hints[0], "a = 2; b = 3") {
			t.Fatalf("%s: %+v", source, report)
		}
	}
	for _, source := range []string{`a = 2;; b = 3`, `print(2; 3)`, `a = [2; 3]`} {
		if _, err := ParseAndBuild(source); err == nil {
			t.Fatal("semicolon is a statement separator, not a comma:", source)
		}
	}
}

func TestComprehensionEffectHints(t *testing.T) {
	for _, tc := range []struct{ source, hint string }{
		{`import host "fetch"; a = [x for x in fetch()]`, "Bind the external source"},
		{`import host "fetch"; print([fetch(x) for x in range(0, 3)])`, "mapped = [fetch(x)"},
	} {
		report := CheckSource("map.lip", tc.source)
		if report.OK || len(report.Diagnostics) != 1 {
			t.Fatal(report)
		}
		d := report.Diagnostics[0]
		if d.Code != "LIP_EFFECT_ERROR" || len(d.Hints) != 1 || !strings.Contains(d.Hints[0], tc.hint) {
			t.Fatal(d)
		}
	}
}

func TestVoidFlowContract(t *testing.T) {
	for _, source := range []string{
		`flow main() {}`,
		`a = 2; b = 3; print(a / (a + b))`,
		`fn add(a: number, b: number) -> number { return a + b } print(add(2, 3))`,
		`when false { print(true) }`,
		`flow main() { print(79 / 134) }`,
		`flow main() -> void { print(79 / 134) }`,
		`flow main() { print(79 / 134); return }`,
		`flow main() { when false { print(true) } }`,
	} {
		graph, err := ParseAndBuild(source)
		if err != nil || graph.ReturnType != "void" {
			t.Fatalf("%s: %v", source, err)
		}
		for _, node := range graph.Nodes {
			if node.Output {
				t.Fatal("void Flow should have no output node", node)
			}
		}
	}
	for _, source := range []string{
		`flow main() { return 1 }`,
		`flow main() -> void { return 1 }`,
		`flow main() -> void? {}`,
		`flow main() { return print(1) }`,
		`flow main() { return print(1); print(2) }`,
		`flow main() { return } flow second() {}`,
		`fn f() -> void { return 1 } flow main() {}`,
		`a = 2 flow main() { print(a) }`,
		`flow main() {} print(2)`,
		`a = 2 fn f() -> number { return 1 }`,
		`return 2`,
		`a = 2; return print(a)`,
	} {
		if _, err := ParseAndBuild(source); err == nil {
			t.Fatal("expected rejection:", source)
		}
	}
	if _, err := ParseAndBuild("flow main() -> number { return\n 1 }"); err != nil {
		t.Fatal("a returned expression may still start on the next line:", err)
	}
}

func TestFocusedDiagnostics(t *testing.T) {
	for _, tc := range []struct{ source, hint string }{
		{`require python "math"`, `import python`},
		{`import python "math" as print`, `Choose an alias`},
		{`import python "scikit-learn"`, `installation name`},
		{`print([1 2])`, `commas`},
		{`print(1; 2)`, `commas`},
		{`a = if true { 1 }`, `both values`},
		{`79 / 134`, `lipc repl`},
		{`print("hello)`, `Close the string`},
		{`a=2;; b=3`, `single semicolon`},
	} {
		report := CheckSource("input.lip", tc.source)
		if report.OK || !strings.Contains(strings.Join(report.Diagnostics[0].Hints, " "), tc.hint) {
			t.Fatalf("%s: %+v", tc.source, report)
		}
	}
	context := diagnosticContext(Diagnostic{Line: 1, Column: 5, SourceLine: "你\tx = missing"})
	if !strings.Contains(context, "1 | 你  x = missing") || !strings.Contains(context, "  |       ^") {
		t.Fatalf("misaligned CJK/tab caret: %q", context)
	}
}

func TestTrailingCommas(t *testing.T) {
	for _, source := range []string{
		`fn sum(a:number,b:number,) -> number { return a+b }; print(sum(2,3,))`,
		`flow main(a:number,) { print(a,) }`,
		`a=[1,2,]; b={x:1,y:2,}; print(a,b,)`,
		"a=[\n 1,\n 2,\n]\nprint(a)",
	} {
		if _, err := ParseAndBuild(source); err != nil {
			t.Fatalf("%s: %v", source, err)
		}
	}
}

func TestDiagnosticsAddressTheActualCause(t *testing.T) {
	for _, tc := range []struct{ source, code, want, avoid string }{
		{`print(str(1,2))`, "LIP_ARGUMENT_ERROR", "str(value)", "parse_number"},
		{`flow main() { return print(1) }`, "LIP_RETURN_ERROR", "omit return", "-> number"},
		{`flow main() -> number { return print(1) }`, "LIP_TYPE_ERROR", "printing only", "-> string"},
		{`fn f() -> string { return 2 }; print(f())`, "LIP_TYPE_ERROR", "fn output", "Flow"},
		{`print(string.split("x"))`, "LIP_ARGUMENT_ERROR", "string.split(", "output type"},
		{`fn callback(x:number) { return x }; print(callback(true))`, "LIP_TYPE_ERROR", "indicated argument", "callback position"},
		{`fn predicate(x:number) -> number { return x }; print(list.filter([1],predicate))`, "LIP_CALLBACK_ERROR", "return value", "do not call"},
		{`fn add(a:number,b:number)->number { return a+b }; print(fold([1],"",add))`, "LIP_CALLBACK_ERROR", "seed", "Pass the name"},
		{`print(if 2 { 1 } else { 0 })`, "LIP_TYPE_ERROR", "bool expression", "parse_number"},
		{`flow f() -> string { return 2 }`, "LIP_TYPE_ERROR", "returned value", "print"},
		{`flow f() -> number { when false { return 2 } }`, "LIP_RETURN_ERROR", "optional output", "effects only"},
		{`fn f(x:number) { return missing }; print(f(1))`, "LIP_NAME_ERROR", "parameters", "when-local"},
		{`print(missing)`, "LIP_NAME_ERROR", "spelling", "when-local"},
		{`print(string.trm("x"))`, "LIP_UNKNOWN_OPERATION", "string.trim", "import host"},
		{`fn f(x:string) { return string.trm(x) }; print(f("x"))`, "LIP_UNKNOWN_OPERATION", "string.trim", "must be pure"},
		{`pritn(1)`, "LIP_UNKNOWN_OPERATION", "print", "str(value)"},
		{`print(strng.trim("x"))`, "LIP_UNKNOWN_OPERATION", "string.trim", "import python"},
		{`import python "math"; print(math.sqrt(9))`, "LIP_EFFECT_ERROR", "Bind the external call", "return type"},
		{`print(list.map([1],fn(x){print(x)}))`, "LIP_EFFECT_ERROR", "capture immutable", "as parameters"},
	} {
		report := CheckSource("input.lip", tc.source)
		if report.OK {
			t.Fatal("accepted invalid source:", tc.source)
		}
		d := report.Diagnostics[0]
		hints := strings.Join(d.Hints, " ")
		if d.Code != tc.code || !strings.Contains(hints, tc.want) || strings.Contains(hints, tc.avoid) {
			t.Fatalf("%s: %+v", tc.source, d)
		}
	}
}
