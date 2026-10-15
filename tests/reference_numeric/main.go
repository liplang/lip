// reference_numeric runs the normative Go block from the specification itself.
// It prints the bit-vector table, or verifies that the checked-in table matches.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func main() {
	specPath := flag.String("spec", "spec/LIP-1.0-SPEC.en.md", "specification path (English or Chinese edition)")
	check := flag.Bool("check", false, "compare generated vectors with the specification")
	flag.Parse()
	if err := run(*specPath, *check); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(specPath string, check bool) error {
	source, err := os.ReadFile(specPath)
	if err != nil {
		return err
	}
	_, section, ok := strings.Cut(string(source), "### 16.5. ")
	if !ok {
		return fmt.Errorf("missing numeric reference section")
	}
	_, fenced, ok := strings.Cut(section, "```go\n")
	if !ok {
		return fmt.Errorf("missing Go reference block")
	}
	reference, _, ok := strings.Cut(fenced, "\n```")
	if !ok {
		return fmt.Errorf("unterminated Go reference block")
	}
	vectors := []struct{ expression, reference string }{
		{"2 ** 0.5", "corePower(2, 0.5)"},
		{"4 ** -0.5", "corePower(4, -0.5)"},
		{"5e-324 ** 0.5", "corePower(0x1p-1074, 0.5)"},
		{"27 ** (1/3)", "corePower(27, numericDiv(1, 3))"},
		{"0.25 */ 2", "numericDiv(coreLog(0.25), coreLog(2))"},
		{"8 */ 0.5", "numericDiv(coreLog(8), coreLog(0.5))"},
		{"8 */ 2", "numericDiv(coreLog(8), coreLog(2))"},
		{"2 */ 3", "numericDiv(coreLog(2), coreLog(3))"},
		{"10 */ 2", "numericDiv(coreLog(10), coreLog(2))"},
	}
	var program strings.Builder
	program.WriteString("package main\nimport (\"fmt\"; \"math\")\n")
	program.WriteString(reference)
	program.WriteString("\nfunc main() {\n")
	fmt.Fprintf(&program, "fmt.Println(%q)\n", "| Expression | binary64 bits |")
	fmt.Fprintf(&program, "fmt.Println(%q)\n", "|---|---|")
	for _, vector := range vectors {
		format := "| `" + vector.expression + "` | `0x%016x` |\n"
		fmt.Fprintf(&program, "fmt.Printf(%q, math.Float64bits(%s))\n", format, vector.reference)
	}
	program.WriteString("}\n")
	dir, err := os.MkdirTemp("", "lip-spec-numeric-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	filename := filepath.Join(dir, "main.go")
	if err := os.WriteFile(filename, []byte(program.String()), 0600); err != nil {
		return err
	}
	cmd := exec.Command("go", "run", filename)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("run numeric reference: %w\n%s", err, stderr.String())
	}
	// Table headings are localized; the separator and exact vector rows are shared.
	_, table, _ := strings.Cut(string(output), "\n")
	if check && !strings.Contains(section, table) {
		return fmt.Errorf("specification bit table differs from reference output:\n%s", output)
	}
	_, err = os.Stdout.Write(output)
	return err
}
