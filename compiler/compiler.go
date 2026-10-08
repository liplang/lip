package compiler

import (
	"os"
)

func CompileFile(path string) (*Graph, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return nil, SourceError(path, "", "LIP_IO_ERROR", err)
	}
	graph, stage, err := parseSource(string(src))
	if err != nil {
		return nil, SourceError(path, string(src), stage, err)
	}
	graph.SourceName, graph.Source = path, string(src)
	return graph, nil
}
