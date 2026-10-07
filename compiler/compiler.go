package compiler

import (
	"fmt"
	"os"
)

func CompileFile(path string) (*Graph, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	graph, stage, err := parseSource(string(src))
	if err != nil {
		report := failedCheck(path, string(src), stage, err)
		return nil, fmt.Errorf("%s:%w%s", path, err, diagnosticContext(report.Diagnostics[0]))
	}
	return graph, nil
}
