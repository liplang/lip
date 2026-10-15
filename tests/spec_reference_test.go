package tests

import (
	"context"
	"os/exec"
	"testing"
	"time"
)

// Execute the algorithm printed in the spec, independently of script/values.go,
// so edits to the reference cannot silently disagree with its bit-vector table.
func TestSpecNumericReference(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "run", "./reference_numeric", "-spec", "../spec/LIP-1.0-SPEC.md", "-check")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("numeric reference: %v\n%s", err, output)
	}
	t.Logf("normative reference vectors:\n%s", output)
}
