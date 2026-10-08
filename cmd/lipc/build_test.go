package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"lipalpha/compiler"
)

func TestStandaloneBuildsIgnoreTemporaryPaths(t *testing.T) {
	graph, err := compiler.ParseAndBuild("print(7)")
	if err != nil {
		t.Fatal(err)
	}
	code, err := compiler.GenerateGo(graph)
	if err != nil {
		t.Fatal(err)
	}
	var previous []byte
	for index := 0; index < 2; index++ {
		root, cleanup, err := temporarySource([]byte(code))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(cleanup)
		binary := filepath.Join(t.TempDir(), executableName("program"))
		if err := compileModule(root, binary); err != nil {
			t.Fatal(err)
		}
		if output, err := exec.Command(binary).CombinedOutput(); err != nil || string(output) != "7\n" {
			t.Fatalf("program output %q, error %v", output, err)
		}
		data, err := os.ReadFile(binary)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(data, []byte(root)) {
			t.Fatal("standalone output embeds its disposable build directory")
		}
		if previous != nil && !bytes.Equal(previous, data) {
			t.Fatal("identical standalone sources produce different binaries in different temporary modules")
		}
		previous = data
	}
}
