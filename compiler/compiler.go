package compiler

import "os"

func CompileFile(path string) (*Graph, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return ParseAndBuild(string(src))
}
