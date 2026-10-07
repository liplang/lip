package tests

import (
	"bytes"
	"encoding/json"
	"go/format"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"lipalpha/compiler"
)

// Expected outputs and failures are checked through the actual generated CLI,
// so a passing compiler check alone cannot make a tutorial look runnable.
func TestTutorialExamples(t *testing.T) {
	root, _ := filepath.Abs("..")
	dir := t.TempDir()
	data, err := os.ReadFile(filepath.Join(root, "examples/tutorial/cases.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Source string
		Args   []string
		Want   string
		Exit   int
		Error  string
	}
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}
	binaries := map[string]string{}
	for _, tc := range cases {
		binary := binaries[tc.Source]
		if binary == "" {
			source := filepath.Join(root, "examples/tutorial", tc.Source)
			graph, err := compiler.CompileFile(source)
			if err != nil {
				t.Fatal(err)
			}
			code, err := compiler.GenerateGo(graph)
			if err != nil {
				t.Fatal(err)
			}
			generated := filepath.Join(dir, tc.Source+".go")
			writeTestFile(t, generated, code)
			binary = filepath.Join(dir, tc.Source+".bin")
			command := exec.Command("go", "build", "-buildvcs=false", "-o", binary, generated)
			command.Dir = root
			if out, err := command.CombinedOutput(); err != nil {
				t.Fatalf("%s: %v\n%s", tc.Source, err, out)
			}
			binaries[tc.Source] = binary
		}
		t.Run(tc.Source+strings.Join(tc.Args, ","), func(t *testing.T) {
			command := exec.Command(binary, tc.Args...)
			var stdout, stderr bytes.Buffer
			command.Stdout = &stdout
			command.Stderr = &stderr
			err := command.Run()
			code := 0
			if err != nil {
				exit, ok := err.(*exec.ExitError)
				if !ok {
					t.Fatal(err)
				}
				code = exit.ExitCode()
			}
			if code != tc.Exit {
				t.Fatalf("exit=%d want=%d: %s", code, tc.Exit, stderr.String())
			}
			if code == 0 {
				if stdout.String() != tc.Want+"\n" || stderr.Len() != 0 {
					t.Fatalf("got %q stderr %q, want %q", stdout.String(), stderr.String(), tc.Want)
				}
			} else if !strings.Contains(stderr.String(), tc.Error) {
				t.Fatalf("got %s, want %s", stderr.String(), tc.Error)
			}
		})
	}
	// The tracked Host library must also match its tutorial source.
	graph, err := compiler.CompileFile(filepath.Join(root, "examples/tutorial/session.lip"))
	if err != nil {
		t.Fatal(err)
	}
	code, err := compiler.GenerateGoWithOptions(graph, compiler.GenerateOptions{PackageName: "sessionflow"})
	if err != nil {
		t.Fatal(err)
	}
	formatted, err := format.Source([]byte(code))
	if err != nil {
		t.Fatal(err)
	}
	tracked, err := os.ReadFile(filepath.Join(root, "examples/tutorial/sessionflow/flow_gen.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(formatted, tracked) {
		t.Fatal("regenerate tutorial sessionflow/flow_gen.go")
	}
}

func TestJSONCheckCLI(t *testing.T) {
	root, _ := filepath.Abs("..")
	dir := t.TempDir()
	lipc := filepath.Join(dir, "lipc")
	build := exec.Command("go", "build", "-buildvcs=false", "-o", lipc, "./cmd/lipc")
	build.Dir = root
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	bad := filepath.Join(dir, "bad.lip")
	writeTestFile(t, bad, "flow Bad()->string {\n return string.trim(1)\n}")
	for _, tc := range []struct {
		args       []string
		code       int
		diagnostic string
	}{
		{[]string{"check", "--json", "examples/strings.lip"}, 0, ""},
		{[]string{"check", "--json", bad}, 1, "LIP_TYPE_ERROR"},
		{[]string{"check", "--json", filepath.Join(dir, "missing.lip")}, 1, "LIP_IO_ERROR"},
	} {
		command := exec.Command(lipc, tc.args...)
		command.Dir = root
		var stdout, stderr bytes.Buffer
		command.Stdout = &stdout
		command.Stderr = &stderr
		err := command.Run()
		code := 0
		if err != nil {
			exit, ok := err.(*exec.ExitError)
			if !ok {
				t.Fatal(err)
			}
			code = exit.ExitCode()
		}
		if code != tc.code || stderr.Len() != 0 {
			t.Fatalf("%v: code %d stderr %s", tc.args, code, stderr.String())
		}
		var report compiler.CheckReport
		if err := json.Unmarshal(stdout.Bytes(), &report); err != nil || report.Schema != "lip.diagnostics.v1" || report.OK != (code == 0) {
			t.Fatalf("%s: %v", stdout.String(), err)
		}
		if tc.diagnostic != "" && report.Diagnostics[0].Code != tc.diagnostic {
			t.Fatal(report)
		}
	}
}

func TestTutorialSourcesMatchDocumentation(t *testing.T) {
	root, _ := filepath.Abs("..")
	data, err := os.ReadFile(filepath.Join(root, "docs/TUTORIAL.md"))
	if err != nil {
		t.Fatal(err)
	}
	pattern := regexp.MustCompile("(?s)<!-- example: ([^ ]+) -->\\s*```lip\\n(.*?)\\n```")
	matches := pattern.FindAllSubmatch(data, -1)
	if len(matches) < 12 {
		t.Fatalf("only %d linked runnable tutorial sources", len(matches))
	}
	for _, match := range matches {
		source, err := os.ReadFile(filepath.Join(root, string(match[1])))
		if err != nil {
			t.Fatal(err)
		}
		if strings.TrimSpace(string(source)) != strings.TrimSpace(string(match[2])) {
			t.Errorf("tutorial snippet differs from %s", match[1])
		}
	}
}
