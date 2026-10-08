package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"lipalpha/compiler"
	"lipalpha/runtime"
)

func TestLearnCourse(t *testing.T) {
	lessons, err := loadLearnLessons()
	if err != nil {
		t.Fatal(err)
	}
	for _, lesson := range lessons {
		t.Run(lesson.ID, func(t *testing.T) {
			if lesson.Mode == "python" {
				if _, err := exec.LookPath("python3"); err != nil {
					if _, err := exec.LookPath("python"); err != nil {
						t.Skip("Python is not installed; the optional course remains available")
					}
				}
			}
			solution := strings.Replace(lesson.Template, learnPlaceholder, lesson.Solution, 1)
			for _, required := range lesson.Required {
				if !learnUses(solution, required) {
					t.Fatalf("reference solution does not use %q", required)
				}
			}
			for _, program := range []struct {
				name, source string
				cases        []learnCase
			}{{"example", lesson.Example, lesson.Demo}, {"solution", solution, lesson.Cases}} {
				results, err := executeLearnSource(program.source, lesson.Mode, program.cases)
				if err != nil {
					t.Fatalf("%s: %v", program.name, err)
				}
				if len(results) != len(program.cases) {
					t.Fatalf("%s: got %d results for %d cases", program.name, len(results), len(program.cases))
				}
				for i, tc := range program.cases {
					if err := compareLearnResult(tc, results[i]); err != nil {
						t.Errorf("%s / %s: %v", program.name, tc.Name, err)
					}
				}
			}
		})
	}
}

func TestLearnKeywordCoverageAndTokenMatching(t *testing.T) {
	lessons, err := loadLearnLessons()
	if err != nil {
		t.Fatal(err)
	}
	var source strings.Builder
	for _, lesson := range lessons {
		for _, definition := range []string{lesson.Example, fillLearnTemplate(lesson.Template, lesson.Solution)} {
			if _, err := compiler.ParseAndBuild(definition); err != nil {
				t.Fatalf("%s has an invalid course definition: %v", lesson.ID, err)
			}
		}
		source.WriteString(lesson.Example + "\n" + lesson.Solution + "\n")
	}
	for _, keyword := range strings.Fields("flow fn return match for in break continue if else import as true false null") {
		if !learnUses(source.String(), keyword) {
			t.Errorf("no runnable example or solution teaches %s", keyword)
		}
	}
	for _, tc := range []struct {
		source, term string
		want         bool
	}{
		{"# fn\nprint(\"fn\")", "fn", false},
		{"# list.map(xs, cb)\nprint(\"list.map\")", "list.map", false},
		{"list . map(xs, fn(x) { x })", "list.map", true},
		{"list.mapping(xs)", "list.map", false},
		{"false_value = true", "false", false},
		{"match false { true => {}, false => {} }", "false", true},
	} {
		if got := learnUses(tc.source, tc.term); got != tc.want {
			t.Errorf("%q uses %q: got %v", tc.source, tc.term, got)
		}
	}
}

func TestLearnUnusedImportsDoNotStartPython(t *testing.T) {
	source := `;;;import python "not_installed"; import host "unused"; import go "math"; flow Pure() -> number { return 7;; };;;`
	graph, err := compiler.ParseAndBuild(source)
	if err != nil {
		t.Fatal(err)
	}
	if learnUsesPython(graph) {
		t.Fatal("unused Python declaration starts Worker")
	}
	cases := []learnCase{{Name: "unused declarations", Inputs: map[string]runtime.Value{}, Want: json.RawMessage("7")}}
	results, err := executeLearnSource(source, "core", cases)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatal(results)
	}
	if err := compareLearnResult(cases[0], results[0]); err != nil {
		t.Fatal(err)
	}
}

func newTestLearnSession(t *testing.T, selection string) (*learnSession, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	lessons, err := loadLearnLessons()
	if err != nil {
		t.Fatal(err)
	}
	var output, errors bytes.Buffer
	session := &learnSession{lessons: lessons, index: learnLessonIndex(lessons, selection),
		progress: learnProgress{Schema: "lip.learn.v1", Completed: map[string]bool{}}, output: &output, errors: &errors}
	if session.index < 0 {
		t.Fatal(selection)
	}
	// The complete course test above exercises real compilation and Runtime.
	// UI scenarios replace only the expensive execution boundary.
	session.runner = func(source, mode string, cases []learnCase) ([]learnResult, error) {
		results := make([]learnResult, len(cases))
		for i, tc := range cases {
			if err := json.Unmarshal(tc.Want, &results[i].Value); err != nil {
				t.Fatal(err)
			}
			results[i].Error, results[i].Stdout = tc.Error, tc.Stdout
			results[i].Trace = []runtime.TraceEvent{{Tick: 1, Node: "result", Status: runtime.Completed}}
		}
		if strings.Contains(source, "1 + 1 * 1") {
			results[0].Value = float64(2)
		}
		return results, nil
	}
	return session, &output, &errors
}

func TestLearnInteractiveRecoveryAndProgress(t *testing.T) {
	s, output, errors := newTestLearnSession(t, "expressions")
	s.progressPath = filepath.Join(t.TempDir(), "progress.json")
	input := `:solution
:next
:prev
1 + 1 * 1
unknown + 1 * 2
broken +
:cancel
:unknown
:hint
2 + 3 * 4
:trace
:quit
`
	if code := s.run(strings.NewReader(input)); code != 0 {
		t.Fatalf("exit %d: %s", code, errors.String())
	}
	if len(s.progress.Completed) != 1 || !s.progress.Completed["expressions"] {
		t.Fatalf("revealing or skipping a lesson must not mark it complete: %+v", s.progress)
	}
	for _, want := range []string{"实际 2", "本课通过", "提示 1/", "tick=1 result Completed", "已通过 1/26"} {
		if !strings.Contains(output.String(), want) {
			t.Errorf("missing %q in %s", want, output.String())
		}
	}
	if !strings.Contains(errors.String(), "未知命令") || !strings.Contains(errors.String(), "^") || !strings.Contains(errors.String(), "hint:") {
		t.Fatal("unknown commands and invalid code must produce actionable diagnostics")
	}
	progress, err := readLearnProgress(s.progressPath)
	if err != nil || progress.Current != "expressions" || !progress.Completed["expressions"] {
		t.Fatalf("saved progress: %+v, %v", progress, err)
	}
	data, _ := os.ReadFile(s.progressPath)
	if err := os.WriteFile(s.progressPath, []byte("not JSON"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readLearnProgress(s.progressPath); err == nil {
		t.Fatal("corrupt progress must not silently reset")
	}
	if err := os.WriteFile(s.progressPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := s.saveProgress(); err != nil {
		t.Fatal("saving over existing progress: ", err)
	}
}

func TestLearnMultilineExportAndReload(t *testing.T) {
	s, output, errors := newTestLearnSession(t, "organization")
	path := filepath.Join(t.TempDir(), "workshop.lip")
	input := ":edit\n" + s.lessons[s.index].Solution + "\n:submit\n:save " + path + "\n:graph\n:load " + path + "\n:quit\n"
	if code := s.run(strings.NewReader(input)); code != 0 || errors.Len() != 0 {
		t.Fatalf("exit %d: %s", code, errors.String())
	}
	if !s.progress.Completed["organization"] || strings.Count(output.String(), "本课通过") != 2 {
		t.Fatal(output.String())
	}
	source, err := readLearnSource(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := compiler.ParseAndBuild(source); err != nil {
		t.Fatal("exported program must compile: ", err)
	}
	if err := saveLearnSource(path, "would overwrite"); err == nil {
		t.Fatal("existing files must not be overwritten")
	}
	still, _ := readLearnSource(path)
	if still != source {
		t.Fatal("export modified an existing file")
	}
}

func TestLearnUnsubmittedInputAndFileLimit(t *testing.T) {
	s, output, errors := newTestLearnSession(t, "organization")
	s.runner = func(string, string, []learnCase) ([]learnResult, error) {
		t.Fatal("unsubmitted multiline input must not run")
		return nil, nil
	}
	if code := s.run(strings.NewReader(":edit\n" + s.lessons[s.index].Solution)); code != 0 || errors.Len() != 0 || len(s.progress.Completed) != 0 {
		t.Fatalf("exit %d: %s", code, errors.String())
	}
	if !strings.Contains(output.String(), "未提交的代码已取消") {
		t.Fatal(output.String())
	}
	for _, tc := range []struct {
		source string
		want   bool
	}{
		{"flow X(x: number) -> number {", true},
		{"{history: list.scan(xs, 0, fn(a, b) { a + b })", true},
		{"if true { 1 }", true},
		{"[)", false},
		{"\"{\" # [", false},
	} {
		if got := incompleteLearnAnswer(tc.source); got != tc.want {
			t.Errorf("%q: got incomplete=%v", tc.source, got)
		}
	}
	path := filepath.Join(t.TempDir(), "large.lip")
	if err := os.WriteFile(path, bytes.Repeat([]byte(" "), learnSourceLimit+1), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readLearnSource(path); err == nil {
		t.Fatal("oversize source must be rejected")
	}
}

func TestLearnPipedCommentAndCompletions(t *testing.T) {
	s, output, errors := newTestLearnSession(t, "expressions")
	if code := s.run(strings.NewReader("2 + 3 * 4 # final comment")); code != 0 || errors.Len() != 0 || !s.progress.Completed["expressions"] {
		t.Fatalf("exit %d: %s\n%s", code, errors.String(), output.String())
	}
	if !strings.Contains(output.String(), "→ 14") {
		t.Fatal("successful answers should display their actual result")
	}
	completions := s.completions(":re")
	if len(completions) != 1 || completions[0] != ":reference" {
		t.Fatalf("offered unsupported REPL commands: %v", completions)
	}
}

func TestLearnDocumentationIndexAndReference(t *testing.T) {
	lessons, err := loadLearnLessons()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile("../../docs/INTERACTIVE-LEARNING.md")
	if err != nil {
		t.Fatal(err)
	}
	rows := regexp.MustCompile("(?m)^\\| ([0-9]+) \\| `([^`]+)` \\|").FindAllSubmatch(data, -1)
	if len(rows) != len(lessons) {
		t.Fatalf("documented %d lessons, course has %d", len(rows), len(lessons))
	}
	for index, lesson := range lessons {
		if string(rows[index][1]) != strconv.Itoa(index+1) || string(rows[index][2]) != lesson.ID {
			t.Errorf("lesson %d: documentation %s, course %s", index+1, rows[index][2], lesson.ID)
		}
	}
	var output bytes.Buffer
	printLearnReference(&output, "builtin")
	if !strings.Contains(output.String(), "print(values...) -> null") {
		t.Fatal("print returns null, while only a Flow output can be void")
	}
	s, _, _ := newTestLearnSession(t, "host")
	// A single Host operation is completed directly, never as a namespace.
	if got := s.completions("twi"); len(got) != 1 || got[0] != "twice" {
		t.Fatalf("Host alias completion: %v", got)
	}
}

func TestLearnRejectsHardcodedAnswersAndInsufficientRetry(t *testing.T) {
	lessons, err := loadLearnLessons()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		id, answer string
	}{
		{"bindings", "discounted = 80"},
		{"retry", "retry(lesson_fetch(input), 2)"},
	} {
		lesson := lessons[learnLessonIndex(lessons, tc.id)]
		source := strings.Replace(lesson.Template, learnPlaceholder, tc.answer, 1)
		results, err := executeLearnSource(source, lesson.Mode, lesson.Cases)
		if err != nil {
			t.Fatal(err)
		}
		rejected := false
		for i, test := range lesson.Cases {
			if compareLearnResult(test, results[i]) != nil {
				rejected = true
			}
		}
		if !rejected {
			t.Errorf("accepted incorrect %s answer %s", tc.id, tc.answer)
		}
	}
}
