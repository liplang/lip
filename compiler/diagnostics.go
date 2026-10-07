package compiler

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
)

// CheckReport is a versioned CLI/agent boundary, independent of internal ASTs.
// Checking stops at the first error; hints are guidance, not executable edits.
type CheckReport struct {
	Schema      string       `json:"schema"`
	OK          bool         `json:"ok"`
	File        string       `json:"file"`
	Flow        string       `json:"flow,omitempty"`
	Nodes       int          `json:"nodes,omitempty"`
	Diagnostics []Diagnostic `json:"diagnostics"`
}

type Diagnostic struct {
	Code       string   `json:"code"`
	Severity   string   `json:"severity"`
	Message    string   `json:"message"`
	Line       int      `json:"line,omitempty"`
	Column     int      `json:"column,omitempty"`
	SourceLine string   `json:"source_line,omitempty"`
	Hints      []string `json:"hints"`
}

var diagnosticPosition = regexp.MustCompile(`(?:^|: )([0-9]+):([0-9]+): `)

// Keep terminal errors readable while JSON retains the full structured report.
func diagnosticContext(d Diagnostic) string {
	var context strings.Builder
	if d.SourceLine != "" {
		fmt.Fprintf(&context, "\n  %d | %s", d.Line, d.SourceLine)
	}
	if len(d.Hints) > 0 {
		fmt.Fprintf(&context, "\n  hint: %s", d.Hints[0])
	}
	return context.String()
}

func CheckFile(path string) CheckReport {
	source, err := os.ReadFile(path)
	if err != nil {
		return failedCheck(path, "", "LIP_IO_ERROR", err)
	}
	return CheckSource(path, string(source))
}

func CheckSource(path, source string) CheckReport {
	graph, stage, err := parseSource(source)
	if err != nil {
		return failedCheck(path, source, stage, err)
	}
	return CheckReport{Schema: "lip.diagnostics.v1", OK: true, File: path, Flow: graph.Flow, Nodes: len(graph.Nodes), Diagnostics: []Diagnostic{}}
}

func failedCheck(path, source, stage string, err error) CheckReport {
	diagnostic := Diagnostic{Code: stage, Severity: "error", Message: err.Error(), Hints: []string{}}
	if match := diagnosticPosition.FindStringSubmatch(err.Error()); match != nil {
		diagnostic.Line, _ = strconv.Atoi(match[1])
		diagnostic.Column, _ = strconv.Atoi(match[2])
		lines := strings.Split(source, "\n")
		if diagnostic.Line > 0 && diagnostic.Line <= len(lines) {
			diagnostic.SourceLine = strings.TrimSuffix(lines[diagnostic.Line-1], "\r")
		}
	}
	message := err.Error()
	switch {
	case stage == "LIP_IO_ERROR":
		diagnostic.Hints = []string{"Check that the source path exists and is readable."}
	case stage == "LIP_LEX_ERROR" && strings.Contains(message, "unexpected character '#'"):
		diagnostic.Hints = []string{"Use // for line comments."}
	case stage == "LIP_LEX_ERROR":
		diagnostic.Hints = []string{`Use double-quoted strings with escapes such as \n and \"; use finite decimal numbers.`}
	case stage == "LIP_SYNTAX_ERROR":
		diagnostic.Hints = []string{"Use: flow Name(input: string) -> string { return input }", "Use: if condition { value } else { other_value }. Check matching delimiters."}
	case strings.Contains(message, "duplicate"):
		diagnostic.Hints = []string{"Use distinct binding names and object keys; bindings are immutable."}
	case strings.Contains(message, "unknown string operation"):
		diagnostic.Code = "LIP_UNKNOWN_OPERATION"
		diagnostic.Hints = []string{"Use an operation listed in docs/STRING-LIBRARY.md; string.* is reserved and cannot fall back to Host or Python."}
	case strings.Contains(message, "unknown list operation"):
		diagnostic.Code = "LIP_UNKNOWN_OPERATION"
		diagnostic.Hints = []string{"Use an operation listed in docs/LIST-LIBRARY.md; list.* is reserved and cannot fall back to Host or Python."}
	case strings.Contains(message, "not declared") || strings.Contains(message, "needs an explicit require"):
		diagnostic.Code = "LIP_DEPENDENCY_ERROR"
		diagnostic.Hints = []string{`For a real Host operation, declare require host "operation_name" and register it in the Go host.`, "For an installed Python module, declare require python with its import root. Declarations do not install dependencies."}
	case strings.Contains(message, "undefined or forward reference") || strings.Contains(message, "unknown function parameter") || strings.Contains(message, "scoped to a when block"):
		diagnostic.Code = "LIP_NAME_ERROR"
		diagnostic.Hints = []string{"Check the spelling of the name and bind its value before use.", "A when-local binding cannot be used outside that when block."}
	case strings.Contains(message, "recursive function"):
		diagnostic.Code = "LIP_RECURSION_ERROR"
		diagnostic.Hints = []string{"Declare -> result_type on every function in the recursive cycle and provide a terminating base case."}
	case strings.Contains(message, "must be pure") || strings.Contains(message, "nested external call"):
		diagnostic.Code = "LIP_EFFECT_ERROR"
		diagnostic.Hints = []string{"Bind external operations as separate Flow nodes; local fn and list callbacks may only compose pure expressions."}
	case strings.Contains(message, "callback") || strings.Contains(message, "reducer"):
		diagnostic.Code = "LIP_CALLBACK_ERROR"
		diagnostic.Hints = []string{"Pass a local function name, not a call or a lambda. Match its parameter count and result type to the operation signature."}
	case strings.Contains(message, "type") || strings.Contains(message, "expects") || strings.Contains(message, "cannot combine") || strings.Contains(message, "must be bool"):
		diagnostic.Code = "LIP_TYPE_ERROR"
		diagnostic.Hints = []string{"Match the declared boundary types and operation signature; use str(value) or string.parse_number(text) for explicit conversion.", "list and object check outer shape only; dynamic elements are validated during execution."}
	case strings.Contains(message, "return"):
		diagnostic.Hints = []string{"Write exactly one Flow return with an explicit output type. Choose a value with if; use T? if when can skip the output."}
	default:
		diagnostic.Hints = []string{"Compare the source with a complete example in docs/TUTORIAL.md, then rerun lipc check --json."}
	}
	return CheckReport{Schema: "lip.diagnostics.v1", File: path, Diagnostics: []Diagnostic{diagnostic}}
}
