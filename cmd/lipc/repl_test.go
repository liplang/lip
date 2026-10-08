package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"lipalpha/compiler"
)

func TestREPLSession(t *testing.T) {
	input := `value = 79 / 134
value
printed = print("only-once")
printed
fn double(n: number) -> number {
    return n * 2
}
double(value)
items = [
    "你好", "a}b", // unmatched delimiters in text/comments do not matter: [
    3
]
items[0]
transient = 2; broken = 1 / 0
value = 2
# bad comment
:vars
value + 1
numerator = 2; denominator = 3; print(numerator / denominator)
when false { repl_result = 9 }; 11
missing_sep = 2 other = 3
:reset
value
2 + 3`
	var output, errors bytes.Buffer
	if code := runREPL(strings.NewReader(input), &output, &errors, true); code != 0 {
		t.Fatalf("exit %d: %s", code, errors.String())
	}
	got := output.String()
	for _, want := range []string{"0.5895522388059702\n", "null\n", "1.1791044776119404\n", "你好\n", "1.5895522388059702\n", "0.6666666666666666\n", "11\n", "5\n"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in %q", want, got)
		}
	}
	if strings.Count(got, "only-once") != 1 || strings.Contains(got, "transient =") || strings.Contains(got, "In [") {
		t.Fatalf("replayed effects, committed a failed cell, or unwanted prompts: %s", got)
	}
	for _, want := range []string{"division by zero", "^", "hint:", "duplicate", "Use //", "undefined or forward", "same line must be separated"} {
		if !strings.Contains(errors.String(), want) {
			t.Fatalf("missing diagnostic %q in %s", want, errors.String())
		}
	}
}

func TestREPLContinuationAndRecovery(t *testing.T) {
	for _, tc := range []struct {
		source string
		want   bool
	}{
		{"fn twice(n: number) {", true},
		{"[1,", true},
		{"(1 +", true},
		{`"{" // [`, false},
		{"[)", false},
		{`"unclosed`, false},
	} {
		if got := incompleteCell(tc.source); got != tc.want {
			t.Fatalf("%q: %t", tc.source, got)
		}
	}
	var output, errors bytes.Buffer
	if code := runREPL(strings.NewReader("[1,\n:cancel\n:unknown\n:help\n:quit\n"), &output, &errors, false); code != 0 {
		t.Fatal(code)
	}
	if !strings.Contains(output.String(), "   ...: ") || !strings.Contains(output.String(), ":vars") || !strings.Contains(errors.String(), "unknown REPL command") {
		t.Fatalf("output %q, errors %q", output.String(), errors.String())
	}
	output.Reset()
	errors.Reset()
	runREPL(strings.NewReader("[1,"), &output, &errors, true)
	if !strings.Contains(errors.String(), "end of input") {
		t.Fatal("incomplete EOF must report an error:", errors.String())
	}
}

func TestREPLInlineCallbacks(t *testing.T) {
	var output, errors bytes.Buffer
	input := `factor = 3
list.map(range(4), fn(x) { x * factor })
list.filter(range(5), fn(x) { x > 2 })
fold(range(5), 0, fn(total, x) {
    total + x
})
list.scan(range(4), 0, fn(total: number, x: number) -> number { return total + x })
list.map(range(2), fn(x) { missing })
list.map(range(3), fn(x) { x + factor })
:quit
`
	if code := runREPL(strings.NewReader(input), &output, &errors, true); code != 0 {
		t.Fatal(code)
	}
	if output.String() != "[0,3,6,9]\n[3,4]\n10\n[0,0,1,3,6]\n[3,4,5]\n" || !strings.Contains(errors.String(), `reference "missing"`) {
		t.Fatalf("output=%q errors=%s", output.String(), errors.String())
	}
}

func TestVoidFlowExecutionAndRuntimeDiagnostics(t *testing.T) {
	for _, tc := range []struct {
		name, source, want string
		fails              bool
	}{
		{"implicit", `flow main() { print(79 / 134) }`, "0.5895522388059702\n", false},
		{"statements", "a = 2\nb = 3\nprint(a / (a + b))", "0.4\n", false},
		{"semicolon-statements", `a = 2; b = 3; print(a / b)`, "0.6666666666666666\n", false},
		{"statement-functions", `fn add(a: number, b: number) -> number { return a + b } print(add(2, 3))`, "5\n", false},
		{"explicit", `flow main() -> void { print(true, 7); return }`, "true 7\n", false},
		{"empty", `flow main() {}`, "", false},
		{"gated", `flow main() { when false { print("hidden") } }`, "", false},
		{"error", "flow main() {\n    print(1 / 0)\n}", "<input>:2:5: division by zero", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			graph, err := compiler.ParseAndBuild(tc.source)
			if err != nil {
				t.Fatal(err)
			}
			code, err := compiler.GenerateGo(graph)
			if err != nil {
				t.Fatal(err)
			}
			directory := t.TempDir()
			binary := filepath.Join(directory, executableName("flow"))
			if err := compileGenerated([]byte(code), binary); err != nil {
				t.Fatal(err)
			}
			output, err := exec.Command(binary).CombinedOutput()
			if (err != nil) != tc.fails || !strings.Contains(string(output), tc.want) {
				t.Fatalf("output %q, error %v", output, err)
			}
			if !tc.fails && string(output) != tc.want {
				t.Fatalf("unexpected automatic result for void Flow: %q", output)
			}
			if tc.fails && (!strings.Contains(string(output), "print(1 / 0)") || !strings.Contains(string(output), "hint:") || strings.Contains(string(output), "__expr_")) {
				t.Fatalf("unhelpful runtime diagnostic: %s", output)
			}
		})
	}
}

func TestCompileFileTerminalDiagnostic(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.lip")
	if err := os.WriteFile(path, []byte("flow main() -> number {\n    print(1)\n}"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := compiler.CompileFile(path)
	if err == nil || !strings.Contains(err.Error(), "^") || !strings.Contains(err.Error(), "omit the output type") {
		t.Fatal(err)
	}
}

func TestREPLPythonAliasesAndContinuedExpressions(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 unavailable")
	}
	var output, errors bytes.Buffer
	input := `// This cell should not consume a number.
import python "math" as m
m.sqrt(9)
a = 2 +
3
if true { 7 }
else { 9 }
import python "statistics" as stats
stats.mean([x for x in range(1, 19)])
[x * x for x in range(1, 4)]
:history
:reset
m.sqrt(9)
:quit
`
	if code := runREPL(strings.NewReader(input), &output, &errors, false); code != 0 {
		t.Fatal(code)
	}
	for _, want := range []string{"Out[2]: 3", "Out[4]: 7", "Out[6]: 9.5", "Out[7]: [1,4,9]", "In [1]: import python", "Values and declarations cleared."} {
		if !strings.Contains(output.String(), want) {
			t.Fatalf("missing %q: %s", want, output.String())
		}
	}
	if !strings.Contains(errors.String(), `import python "m"`) {
		t.Fatal("reset should remove aliases:", errors.String())
	}
}

func TestREPLQuitAndHelpInUnfinishedCell(t *testing.T) {
	var output, errors bytes.Buffer
	if code := runREPL(strings.NewReader("[1,\n:vars\n:history\n:help\n:quit\n"), &output, &errors, true); code != 0 || errors.Len() > 0 {
		t.Fatalf("%d: %s", code, errors.String())
	}
	for _, want := range []string{"No saved values", "No inputs", "Keys:"} {
		if !strings.Contains(output.String(), want) {
			t.Fatal(output.String())
		}
	}
}

func TestREPLPythonStringModuleAlongsideCoreLibrary(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 unavailable")
	}
	var output, errors bytes.Buffer
	input := "import python \"string\" as text\nvalue = text.capwords(\"hello world\")\nprint(value, string.trim(\" x \"))\n:quit\n"
	if code := runREPL(strings.NewReader(input), &output, &errors, true); code != 0 || errors.Len() > 0 || output.String() != "Hello World x\n" {
		t.Fatalf("%d: %q, %s", code, output.String(), errors.String())
	}
}
