package tests

import (
	"bytes"
	"context"
	"encoding/json"
	. "lip/script"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestOllamaChatTools(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("Python unavailable")
	}
	source, err := os.ReadFile("../examples/tmp_ollama_chat.lip")
	if err != nil {
		t.Fatal(err)
	}
	// Exercise the actual single-file tools through a separate test entry.
	source = append(source, []byte(`
flow ToolChecks(calls:list)->list {
    runtime = OpenStore()
    results = for call in calls with items = [] {
        result = DispatchTool(runtime, call.name, call.args)
        next concat(items, [result])
    }
    ReapTimers(runtime, "*")
    perform effect runtime.db.close()
    return results
}
`)...)
	host := NewNativeHost(Host{})
	defer host.Close()
	program, err := CompileWithOptions(string(source), host, CompileOptions{Entry: "ToolChecks"})
	if err != nil {
		t.Fatal(err)
	}
	executable, _ := buildNative(t, program, GoOptions{})
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	input := `{"calls":[
{"name":"calculate","args":{"operation":"add","a":1.5,"b":2,"label":"预算 ' ? %_"}},
{"name":"calculate","args":{"operation":"subtract","a":1,"b":4}},
{"name":"calculate","args":{"operation":"multiply","a":123,"b":456}},
{"name":"calculate","args":{"operation":"divide","a":7,"b":2}},
{"name":"calculate","args":{"operation":"divide","a":1,"b":0}},
{"name":"calculate","args":{"operation":"add","a":true,"b":2}},
{"name":"calculate","args":{"operation":"multiply","a":1e308,"b":2}},
{"name":"calculate","args":{"operation":"add","a":1e308,"b":1e308}},
{"name":"calculate","args":{"operation":"subtract","a":-1e308,"b":1e308}},
{"name":"calculate","args":{"operation":"divide","a":1e308,"b":0.1}},
{"name":"calculate","args":{"operation":[],"a":1,"b":2}},
{"name":"calculate","args":{"operation":"add","a":"1","b":2}},
{"name":"calculate","args":{"operation":"add","a":1,"b":2,"extra":3}},
{"name":"calculate","args":[]},
{"name":"calculate","args":{}},
{"name":"unknown_tool","args":{}},
{"name":"list_calculations","args":{"limit":0}},
{"name":"list_calculations","args":{"limit":1.5}},
{"name":"list_calculations","args":{"limit":true}},
{"name":"list_calculations","args":{"limit":101}},
{"name":"get_current_time","args":{"extra":1}},
{"name":"set_reminder","args":{"message":"喝水"}},
{"name":"set_reminder","args":{"message":"喝水","after_seconds":0}},
{"name":"set_reminder","args":{"message":"喝水","after_seconds":true}},
{"name":"set_reminder","args":{"message":"喝水","after_seconds":1,"at":"2030-01-01T00:00:00Z"}},
{"name":"set_reminder","args":{"message":"喝水","at":"2030-01-01T00:00:00"}},
{"name":"set_reminder","args":{"message":"喝水","at":"2000-01-01T00:00:00Z"}},
{"name":"set_reminder","args":{"message":"喝水","at":"invalid"}},
{"name":"set_reminder","args":{"message":"\u001b[2J","after_seconds":1}},
{"name":"set_reminder","args":{"message":" ","after_seconds":1}},
{"name":"cancel_reminder","args":{"id":1}},
{"name":"cancel_reminder","args":{"id":"1 OR 1=1"}},
{"name":"list_calculations","args":{"query":"' ? %_","limit":2}},
{"name":"list_reminders","args":{"status":"all"}}
]}`
	for _, mode := range []string{"interpreter", "generated"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("LIP_CHAT_DB", filepath.Join(t.TempDir(), "nested", "state.sqlite3"))
			t.Setenv("LIP_CHAT_TZ", "Asia/Taipei")
			run := func(input string) []map[string]any {
				t.Helper()
				var output string
				if mode == "interpreter" {
					data, err := DecodeData([]byte(input))
					if err != nil {
						t.Fatal(err)
					}
					fields, _ := data.Fields()
					report, err := program.Run(ctx, fields, Options{Parallelism: 4})
					if err != nil {
						t.Fatal(err)
					}
					output = display(t, report.Output)
				} else {
					stdout, stderr, err := runNative(t, executable, "-input", input, "-parallel", "4")
					if err != nil {
						t.Fatalf("tools: %v\n%s", err, stderr)
					}
					output = stdout
				}
				var results []map[string]any
				if err := json.Unmarshal([]byte(output), &results); err != nil {
					t.Fatalf("%v: %s", err, output)
				}
				return results
			}
			results := run(input)
			for i, result := range results {
				wantOK := i < 4 || i >= len(results)-2
				if result["ok"] != wantOK {
					t.Fatalf("call %d: %v", i, result)
				}
			}
			for i, want := range []float64{3.5, -3, 56088, 3.5} {
				if results[i]["value"] != want || results[i]["saved"] != true {
					t.Fatalf("calculation %d: %v", i, results[i])
				}
			}
			filtered := results[len(results)-2]["calculations"].([]any)
			if len(filtered) != 1 || filtered[0].(map[string]any)["label"] != "预算 ' ? %_" {
				t.Fatal(filtered)
			}
			if rows := results[len(results)-1]["reminders"].([]any); len(rows) != 0 {
				t.Fatal("invalid reminder was saved", rows)
			}
			persisted := run(`{"calls":[{"name":"list_calculations","args":{}}]}`)
			if rows := persisted[0]["calculations"].([]any); len(rows) != 4 {
				t.Fatal("calculations not persisted or invalid write accepted", rows)
			}
			times := run(`{"calls":[{"name":"get_current_time","args":{}},
{"name":"shift_time","args":{"hours":31,"minutes":52}},
{"name":"shift_time","args":{"minutes":-24}},
{"name":"shift_time","args":{}},
{"name":"shift_time","args":{"hours":"31"}},
{"name":"shift_time","args":{"hours":true}},
{"name":"shift_time","args":{"seconds":null}},
{"name":"shift_time","args":{"days":1}},
{"name":"shift_time","args":{"hours":1e308}},
{"name":"shift_time","args":{"hours":8784,"minutes":1}}]}`)
			for i, result := range times {
				if result["ok"] != (i < 3) {
					t.Fatalf("time call %d: %v", i, result)
				}
			}
			if !strings.HasSuffix(times[0]["value"].(string), "+08:00") ||
				!strings.Contains(times[0]["display"].(string), "UTC+0800") || times[0]["timezone"] != "Asia/Taipei" {
				t.Fatal("time must be local and unambiguous", times[0])
			}
			for i, delta := range []time.Duration{31*time.Hour + 52*time.Minute, -24 * time.Minute} {
				shift := times[i+1]
				base, err := time.Parse(time.RFC3339Nano, shift["base"].(string))
				if err != nil {
					t.Fatal(err)
				}
				value, err := time.Parse(time.RFC3339Nano, shift["value"].(string))
				if err != nil || value.Sub(base) != delta {
					t.Fatal("incorrect date arithmetic", shift, err)
				}
			}
			future := time.Now().Add(time.Hour).Format(time.RFC3339Nano)
			absolute := run(`{"calls":[{"name":"set_reminder","args":{"message":"绝对时间","at":"` + future + `"}},` +
				`{"name":"cancel_reminder","args":{"id":"1"}}]}`)
			for _, result := range absolute {
				if result["ok"] != true {
					t.Fatal("valid absolute reminder failed", result)
				}
			}
			for _, at := range []string{"2026-02-29T10:00:00Z", "2024-02-30T10:00:00Z", "2026-00-01T10:00:00Z",
				"2026-12-32T10:00:00Z", "2026-12-01T24:00:00Z", "2026-12-01T10:60:00Z", "2026-12-01T10:00:60Z",
				"2026-12-01T10:00:00+99:00", "2026-12-01T10:00:00+08:60", "2026-12-01T10:00:00.Z"} {
				results := run(`{"calls":[{"name":"set_reminder","args":{"message":"无效日历","at":"` + at + `"}}]}`)
				if results[0]["ok"] != false {
					t.Fatal("invalid absolute reminder accepted", at, results)
				}
			}
		})
	}
}

// Capture background notifications while stdin remains open and blocked.
type chatOutput struct {
	mu       sync.Mutex
	data     bytes.Buffer
	reminder chan struct{}
	once     sync.Once
}

func (out *chatOutput) Write(data []byte) (int, error) {
	out.mu.Lock()
	defer out.mu.Unlock()
	n, err := out.data.Write(data)
	if strings.Contains(out.data.String(), "提醒 #1> 空闲时主动提醒") {
		out.once.Do(func() { close(out.reminder) })
	}
	return n, err
}

func TestOllamaChatInterpreterAndGo(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("Python unavailable")
	}
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("PYTHONPATH", filepath.Join(root, "tests", "fixtures"))
	t.Setenv("LIP_CHAT_TZ", "Asia/Taipei")
	dir := t.TempDir()
	binary := filepath.Join(dir, "lipc")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	buildArgs := []string{"build", "-o", binary, "./cmd/lipc"}
	if os.Getenv("LIP_NATIVE_RACE") == "1" {
		buildArgs = append([]string{"build", "-race"}, buildArgs[1:]...)
	}
	build := exec.CommandContext(ctx, "go", buildArgs...)
	build.Dir = root
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v\n%s", err, out)
	}
	source, err := os.ReadFile(filepath.Join(root, "examples", "tmp_ollama_chat.lip"))
	if err != nil {
		t.Fatal(err)
	}
	host := NewNativeHost(Host{})
	defer host.Close()
	program, err := Compile(string(source), host)
	if err != nil {
		t.Fatal(err)
	}
	executable, _ := buildNative(t, program, GoOptions{})
	for _, mode := range []struct {
		name, executable string
		args             []string
	}{
		{"interpreter", binary, []string{"run", "-parallel", "4", filepath.Join(root, "examples", "tmp_ollama_chat.lip")}},
		{"generated", executable, []string{"-parallel", "4"}},
	} {
		t.Run(mode.name, func(t *testing.T) {
			t.Setenv("LIP_CHAT_DB", filepath.Join(dir, mode.name+".sqlite3"))
			run := func(input string) string {
				t.Helper()
				command := exec.CommandContext(ctx, mode.executable, mode.args...)
				command.Dir = dir
				command.Stdin = strings.NewReader(input)
				out, err := command.CombinedOutput()
				if err != nil {
					t.Fatalf("chat: %v\n%s", err, out)
				}
				return string(out)
			}
			input := "\n/help\nsetup\n" + strings.Repeat("chat\n", 14) + "loop\n/calculations\n/reminders\n/exit\n"
			output := run(input)
			for _, want := range []string{"验证完成", "56088", "采购预算", "cancelled", "8 轮模型请求上限", "会话结束"} {
				if !strings.Contains(output, want) {
					t.Fatalf("missing %q in\n%s", want, output)
				}
			}
			output = run("/time\n/after 31h52m\n/after bad\n现在几点了\n从现在再过31小时52分钟\n你是什么助手 agent？\n编辑体验太差，响应速度太慢了\n/new\nfresh\nnumeric-strings\nlong\nlong\nlong\nchat\n/exit\n")
			for _, want := range []string{"UTC+0800", "用法：/after", "shift_time", "我是本地 Ollama 助手", "已清空聊天上下文", "数字参数兼容已验证"} {
				if !strings.Contains(output, want) {
					t.Fatalf("missing %q in\n%s", want, output)
				}
			}
			if output := run("read\n"); !strings.Contains(output, "跨重启记录已验证") {
				t.Fatal(output)
			}
			run("resume\n/exit\n")
			advance := exec.CommandContext(ctx, "python3", "-B", "-c",
				"import sqlite3,sys; db=sqlite3.connect(sys.argv[1]); db.execute(\"UPDATE reminders SET due_at=0\"); db.commit(); db.close()", os.Getenv("LIP_CHAT_DB"))
			if out, err := advance.CombinedOutput(); err != nil {
				t.Fatalf("advance saved reminder: %v\n%s", err, out)
			}
			if output := run("/reminders\n/exit\n"); !strings.Contains(output, "提醒 #2> 重启后补发") || strings.Contains(output, "提醒 #1>") {
				t.Fatal("overdue reminder missing or cancelled reminder delivered", output)
			}
			if output := run("/exit\n"); strings.Contains(output, "提醒 #") {
				t.Fatal("delivered reminder repeated", output)
			}

			// A new database starts reminder IDs at 1. Do not send another input
			// until the notification appears: polling only on user turns would hang.
			t.Setenv("LIP_CHAT_DB", filepath.Join(dir, mode.name+"-idle.sqlite3"))
			command := exec.CommandContext(ctx, mode.executable, mode.args...)
			command.Dir = dir
			out := &chatOutput{reminder: make(chan struct{})}
			command.Stdout, command.Stderr = out, out
			stdin, err := command.StdinPipe()
			if err != nil {
				t.Fatal(err)
			}
			if err := command.Start(); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = command.Process.Kill() })
			if _, err := stdin.Write([]byte("idle\n")); err != nil {
				t.Fatal(err)
			}
			select {
			case <-out.reminder:
			case <-time.After(15 * time.Second):
				_ = command.Process.Kill()
				_ = command.Wait()
				t.Fatalf("no reminder while waiting for input:\n%s", out.data.String())
			}
			_ = stdin.Close()
			if err := command.Wait(); err != nil {
				t.Fatalf("idle chat: %v\n%s", err, out.data.String())
			}
			t.Run("terminal", func(t *testing.T) {
				if runtime.GOOS != "linux" {
					t.Skip("PTY integration currently verified on Linux")
				}
				available := exec.CommandContext(ctx, "python3", "-B", "-c", "import prompt_toolkit")
				if err := available.Run(); err != nil {
					t.Skip("terminal editing needs prompt_toolkit")
				}
				t.Setenv("LIP_CHAT_DB", filepath.Join(dir, mode.name+"-terminal.sqlite3"))
				args := append([]string{"-B", filepath.Join(root, "tests", "fixtures", "ollama_terminal.py"), mode.executable}, mode.args...)
				probe := exec.CommandContext(ctx, "python3", args...)
				probe.Dir = dir
				if out, err := probe.CombinedOutput(); err != nil {
					t.Fatalf("terminal chat: %v\n%s", err, out)
				}
			})
		})
	}
}
