package compiler

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"

	"lipalpha/internal/textdisplay"
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
	if d.Line > 0 && d.Column > 0 {
		fmt.Fprintf(&context, "\n  %d | %s", d.Line, textdisplay.ExpandTabs(d.SourceLine))
		if d.Column > 0 {
			padding := []rune(d.SourceLine)
			if d.Column-1 < len(padding) {
				padding = padding[:d.Column-1]
			}
			width := textdisplay.Width(textdisplay.ExpandTabs(string(padding)))
			fmt.Fprintf(&context, "\n  %s | %s^", strings.Repeat(" ", len(strconv.Itoa(d.Line))), strings.Repeat(" ", width))
		}
	}
	for _, hint := range d.Hints {
		fmt.Fprintf(&context, "\n  hint: %s", hint)
	}
	return context.String()
}

// SourceError renders the same contextual diagnostic used by check --json.
// The original error is retained for callers that inspect error chains.
func SourceError(path, source, stage string, err error) error {
	d := failedCheck(path, source, stage, err).Diagnostics[0]
	return fmt.Errorf("%s:%w%s", path, err, diagnosticContext(d))
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
	diagnostic.Code, diagnostic.Hints = diagnosticAdvice(stage, err)
	return CheckReport{Schema: "lip.diagnostics.v1", File: path, Diagnostics: []Diagnostic{diagnostic}}
}
