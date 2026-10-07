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
	graph, err := ParseAndBuild(string(src))
	if err != nil {
		return nil, fmt.Errorf("%s:%w", path, err)
	}
	return graph, nil
}
