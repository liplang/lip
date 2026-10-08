package main

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"go/format"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"time"

	"lipalpha/compiler"
	"lipalpha/compiler/lexer"
	"lipalpha/runtime"
)

//go:embed learn/harness.go.txt
var learnHarness string

type learnResult struct {
	Value  runtime.Value        `json:"value"`
	Error  string               `json:"error"`
	Stdout string               `json:"stdout"`
	Trace  []runtime.TraceEvent `json:"trace"`
}

func (s *learnSession) attempt(source string) bool {
	l := s.lessons[s.index]
	s.lastSource, s.lastResults = source, nil
	// Compile first so a typo gets a source diagnostic instead of a usage hint.
	if _, err := compiler.ParseAndBuild(source); err != nil {
		fmt.Fprintln(s.errors, compiler.SourceError("<learn:"+l.ID+">", source, "LIP_CHECK_ERROR", err))
		fmt.Fprintln(s.output, "可以修改后重试；:hint 查看提示。")
		return false
	}
	for _, term := range l.Required {
		if !learnUses(source, term) {
			fmt.Fprintf(s.output, "本课练习 %s，请在代码中使用它，再提交。\n", term)
			return false
		}
	}
	fmt.Fprintln(s.output, "正在编译并验证多组输入…")
	results, err := s.runner(source, l.Mode, l.Cases)
	if err != nil {
		fmt.Fprintln(s.errors, err)
		fmt.Fprintln(s.output, "执行未完成，进度未记为通过。可重试，或用 :next 跳过本课。")
		return false
	}
	s.lastResults = results
	if len(results) != len(l.Cases) {
		fmt.Fprintln(s.errors, "验证结果数量不匹配，进度未记为通过。")
		return false
	}
	passed := true
	for i, tc := range l.Cases {
		if err := compareLearnResult(tc, results[i]); err != nil {
			passed = false
			fmt.Fprintf(s.output, "  ✗ %s：%s\n", tc.Name, err)
		} else {
			if tc.Error != "" {
				fmt.Fprintf(s.output, "  ✓ %s：按预期失败（%s）\n", tc.Name, tc.Error)
			} else {
				fmt.Fprintf(s.output, "  ✓ %s → %s\n", tc.Name, displayLearnResult(results[i]))
			}
		}
	}
	if !passed {
		fmt.Fprintln(s.output, "还差一点：:tests 查看输入，:trace 查看实际执行，:hint 查看提示。")
		return false
	}
	s.progress.Completed[l.ID] = true
	fmt.Fprintln(s.output, "本课通过！", l.Why)
	if s.index+1 == len(s.lessons) {
		fmt.Fprintln(s.output, "已到达课程末尾。:save 路径 可导出完整程序，:lessons 查看通过情况，:quit 退出。")
	} else {
		fmt.Fprintln(s.output, ":next 进入下一课；也可以 :graph / :trace 观察结果，或 :save 路径 导出程序。")
	}
	// Preserve a completed lesson even if the terminal is closed unexpectedly.
	if err := s.saveProgress(); err != nil {
		fmt.Fprintln(s.errors, "保存进度失败：", err)
	}
	return false
}

// Match lexer tokens, so keywords inside strings or comments cannot satisfy
// an exercise. Whitespace around dotted names is harmless.
func learnUses(source, term string) bool {
	tokens, err := lexer.New(source).Lex()
	if err != nil {
		return false
	}
	want, err := lexer.New(term).Lex()
	if err != nil || len(want) < 2 {
		return false
	}
	want = want[:len(want)-1]
	for i := 0; i+len(want) <= len(tokens); i++ {
		matched := true
		for j, w := range want {
			if tokens[i+j].Kind != w.Kind || tokens[i+j].Text != w.Text {
				matched = false
				break
			}
		}
		if matched {
			return true
		}
	}
	return false
}

func compareLearnResult(tc learnCase, got learnResult) error {
	if tc.Error != "" {
		if !strings.Contains(got.Error, tc.Error) {
			return fmt.Errorf("预期失败包含 %q，实际 %s", tc.Error, displayLearnResult(got))
		}
	} else {
		if got.Error != "" {
			return fmt.Errorf("执行失败：%s", got.Error)
		}
		var want runtime.Value
		if err := json.Unmarshal(tc.Want, &want); err != nil {
			return fmt.Errorf("课程预期结果无效：%w", err)
		}
		if !reflect.DeepEqual(want, got.Value) {
			value, _ := json.Marshal(got.Value)
			return fmt.Errorf("预期 %s，实际 %s", tc.Want, value)
		}
	}
	if tc.Stdout != got.Stdout {
		return fmt.Errorf("预期打印 %q，实际 %q", tc.Stdout, got.Stdout)
	}
	return nil
}

func displayLearnResult(result learnResult) string {
	if result.Error != "" {
		return "失败：" + result.Error
	}
	value, _ := json.Marshal(result.Value)
	text := string(value)
	if result.Stdout != "" {
		text += fmt.Sprintf("；打印 %q", result.Stdout)
	}
	return text
}

// Exercise programs use the real compiler, bundled Runtime and generated Go.
// A separate result file keeps user print output out of the grading protocol.
func executeLearnSource(source, mode string, cases []learnCase) ([]learnResult, error) {
	graph, err := compiler.ParseAndBuild(source)
	if err != nil {
		return nil, err
	}
	graph.SourceName, graph.Source = "<learn>", source
	code, err := compiler.GenerateGoWithOptions(graph, compiler.GenerateOptions{PackageName: "main", IncludeDiagnostics: true})
	if err != nil {
		return nil, err
	}
	formatted, err := format.Source([]byte(code))
	if err != nil {
		return nil, err
	}
	directory, cleanup, err := temporarySource(formatted)
	if err != nil {
		return nil, err
	}
	defer cleanup()
	requestPath, resultPath := filepath.Join(directory, "request.json"), filepath.Join(directory, "result.json")
	request, err := json.Marshal(struct {
		Mode   string
		Python bool
		Cases  []learnCase
	}{mode, learnUsesPython(graph), cases})
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(requestPath, request, 0o600); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(directory, "learn_main.go"), []byte(learnHarness), 0o600); err != nil {
		return nil, err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	buildCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	binary := filepath.Join(directory, executableName("lesson"))
	command := exec.CommandContext(buildCtx, "go", "build", "-buildvcs=false", "-mod=readonly", "-o", binary, ".")
	command.Dir = directory
	command.Env = append(os.Environ(), "GOWORK=off", "GO111MODULE=on")
	if output, err := command.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("构建练习失败（需要 Go 1.27 或更高版本）：%w\n%s", err, output)
	}
	runCtx, cancelRun := context.WithTimeout(ctx, time.Duration(len(cases)*6+10)*time.Second)
	defer cancelRun()
	command = exec.CommandContext(runCtx, binary, requestPath, resultPath)
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	if err := command.Run(); err != nil {
		return nil, fmt.Errorf("运行练习失败：%w\n%s%s", err, stdout.String(), stderr.String())
	}
	data, err := os.ReadFile(resultPath)
	if err != nil {
		return nil, err
	}
	var results []learnResult
	if err := json.Unmarshal(data, &results); err != nil {
		return nil, fmt.Errorf("读取练习结果：%w", err)
	}
	return results, nil
}

func learnUsesPython(graph *compiler.Graph) bool {
	return graph.RequiresPython()
}
