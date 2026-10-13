package tests

import (
	"bytes"
	"context"
	. "lipbeta/script"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestSessionRetainsValuesWithoutRepeatingCalls(t *testing.T) {
	var calls atomic.Int64
	host := testHost(func(_ context.Context, request Request) Response {
		calls.Add(1)
		return Result(request.Positional[0])
	})
	s, err := NewSession(host)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	eval := func(source, want string) {
		t.Helper()
		result, err := s.Eval(ctx, source, Options{Parallelism: 4, MaxSteps: 100000})
		if err != nil {
			t.Fatalf("%s: %v", source, err)
		}
		if want != "" && (!result.HasValue || display(t, result.Value) != want) {
			t.Fatalf("%s: got %v %s want %s", source, result.HasValue, display(t, result.Value), want)
		}
	}
	eval(`import python "example.worker" as worker`, "")
	eval("x = perform effect worker.read(7)", "7")
	eval("fn plus(n:number)->number { n + x }", "")
	eval("plus(3)", "10")
	eval("alias = plus", "")
	eval("alias(n=5)", "12")
	eval("proposal = effect worker.read(x + 1)", "")
	if calls.Load() != 1 {
		t.Fatalf("creating or reading saved values repeated a call: %d", calls.Load())
	}
	eval("out = perform proposal", "8")
	eval("map([1,2], plus)", "[8,9]")
	eval("for n in [1,2,3] with total=0 { next total+n }", "6")
	if names := s.Complete("worker.r"); len(names) != 1 || names[0] != "worker.read" {
		t.Fatalf("registered operation completions: %v", names)
	}
	if names := s.Complete("pl"); len(names) != 1 || names[0] != "plus" {
		t.Fatalf("saved closure completions: %v", names)
	}
	if calls.Load() != 2 {
		t.Fatalf("saved request was performed %d times", calls.Load())
	}
	values := s.Values()
	values["x"] = number(999)
	eval("plus(0)", "7")
	if _, err := s.Eval(ctx, "x = 9", Options{}); err == nil {
		t.Fatal("allowed rebinding a saved value")
	}
	if _, err := s.Eval(ctx, `newvalue = perform effect worker.read(9); fail("stop")`, Options{}); err == nil {
		t.Fatal("failed cell succeeded")
	}
	if _, saved := s.Values()["newvalue"]; saved || calls.Load() != 3 {
		t.Fatal("failed cell committed bindings or rolled back an external action")
	}
	if _, err := s.Eval(ctx, `import go "strings" as x`, Options{}); err == nil {
		t.Fatal("allowed an import to hide a saved value")
	}
	eval("x", "7")
	s.Reset()
	if len(s.Values()) != 0 {
		t.Fatal("reset did not clear saved values")
	}
	if _, err := s.Eval(ctx, "plus(1)", Options{}); err == nil {
		t.Fatal("reset retained a function")
	}
	if _, err := s.Eval(ctx, "perform effect worker.read(0)", Options{}); err == nil {
		t.Fatal("reset retained an import")
	}
}

func TestSessionFunctionsAndRunBoundaries(t *testing.T) {
	s, _ := NewSession(Host{})
	ctx := context.Background()
	_, err := s.Eval(ctx, `fn even(n:number)->bool { if n==0 {true} else {odd(n-1)} }
fn odd(n:number)->bool { if n==0 {false} else {even(n-1)} }`, Options{})
	if err != nil {
		t.Fatal(err)
	}
	result, err := s.Eval(ctx, "even(100000)", Options{MaxFrames: 5, MaxSteps: 2000000})
	if err != nil || display(t, result.Value) != "true" {
		t.Fatal(err)
	}
	for _, source := range []string{"facts=claim()", "flow Main()->number{return 1}", "return 1", "fn even()->number {1}"} {
		if _, err := s.Eval(ctx, source, Options{}); err == nil {
			t.Fatal("accepted invalid session boundary", source)
		}
	}
	if _, err := s.Eval(ctx, "1", Options{RunID: "reuse"}); err == nil {
		t.Fatal("accepted cross-cell run identity reuse")
	}
	result, err = s.Eval(ctx, "if true { 1 } else { 2 }", Options{})
	if err != nil || display(t, result.Value) != "1" {
		t.Fatal(err)
	}
}

func TestCellContinuationUsesCoreGrammar(t *testing.T) {
	for _, source := range []string{"[1,\n", "fn square(x:number)->number {\n", "1 + \\\n", "(1 +\n", "if true {1} else"} {
		if !CellIncomplete(source) {
			t.Fatalf("unfinished cell considered complete: %q", source)
		}
	}
	for _, source := range []string{`"["`, "[1,2]\n", "[1)\n", `"bad`, "1 + )", "if true {1} else {2}", "# comment\n"} {
		if CellIncomplete(source) {
			t.Fatalf("complete/malformed cell requested more input: %q", source)
		}
	}
}

// Public CLI tests stay under tests/. A single built lipc serves all sessions.
func TestInteractiveCLI(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "lipc")
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	build := exec.CommandContext(ctx, "go", "build", "-o", binary, "./cmd/lipc")
	build.Dir = ".."
	if data, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build lipc: %v\n%s", err, data)
	}
	run := func(input string, args ...string) (string, string) {
		t.Helper()
		cmd := exec.CommandContext(ctx, binary, args...)
		cmd.Dir = ".."
		cmd.Stdin = strings.NewReader(input)
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		if err := cmd.Run(); err != nil {
			t.Fatalf("lipc %v: %v\n%s", args, err, stderr.String())
		}
		return stdout.String(), stderr.String()
	}
	t.Run("repl", func(t *testing.T) {
		stdout, stderr := run("x=2\nfn twice(n:number)->number {\n n*2\n}\ntwice(x)\nif true {\n x+1\n} else {0}\nx=9\nx\n:quit\n", "repl")
		if stdout != "2\n4\n3\n2\n" || !strings.Contains(stderr, "E-DUPLICATE") {
			t.Fatalf("stdout %q stderr %q", stdout, stderr)
		}
	})
	t.Run("repl-flow", func(t *testing.T) {
		stdout, stderr := run(":run examples/advanced/14_parallel_claim.lip\n:quit\n", "repl")
		if stderr != "" || !strings.Contains(stdout, `"count":3`) {
			t.Fatal(stdout, stderr)
		}
	})
	t.Run("repl-host", func(t *testing.T) {
		stdout, stderr := run("import go \"strings\" as text\nx=perform effect text.ToUpper(\"Ada\")\nx+\"!\"\n:quit\n", "repl", "-host", "demo")
		if stdout != "\"ADA\"\n\"ADA!\"\n" || stderr != "" {
			t.Fatal(stdout, stderr)
		}
	})
	t.Run("all-lessons", func(t *testing.T) {
		stdout, stderr := run("", "learn", "-list")
		if stderr != "" {
			t.Fatal(stderr)
		}
		var input strings.Builder
		lessons := 0
		for _, line := range strings.Split(stdout, "\n") {
			fields := strings.Fields(line)
			if len(fields) < 2 {
				continue
			}
			lessons++
			fmtLine := ":lesson " + fields[1] + "\n:demo\n"
			input.WriteString(fmtLine)
		}
		input.WriteString(":quit\n")
		stdout, stderr = run(input.String(), "learn")
		if lessons < 10 || stderr != "" || strings.Count(stdout, "用例 1 ✓") != lessons {
			t.Fatal("lesson demos", lessons, stdout, stderr)
		}
	})
	t.Run("answers-and-progress", func(t *testing.T) {
		path := filepath.Join(dir, "progress.json")
		stdout, stderr := run("36\nprice * count\n:next\n:quit\n", "learn", "-lesson", "bindings", "-progress", path)
		if !strings.Contains(stdout, "通过") || !strings.Contains(stderr, "预期 35") {
			t.Fatal(stdout, stderr)
		}
		data, err := os.ReadFile(path)
		if err != nil || !bytes.Contains(data, []byte(`"bindings": true`)) || !bytes.Contains(data, []byte(`"current": "arithmetic"`)) {
			t.Fatal(string(data), err)
		}
		stdout, stderr = run(":quit\n", "learn", "-progress", path)
		if stderr != "" || !strings.Contains(stdout, "arithmetic") {
			t.Fatal(stdout, stderr)
		}
	})
	t.Run("full-program", func(t *testing.T) {
		stdout, stderr := run(":edit\nflow Squares(xs:list)->list {\n facts=claim()\n for x in xs parallel {facts <+ x*x}\n return [row.value for row in facts]\n}\n:submit\n:quit\n", "learn", "-lesson", "claim")
		if stderr != "" || !strings.Contains(stdout, "通过") {
			t.Fatal(stdout, stderr)
		}
	})
	t.Run("terminal-editing", func(t *testing.T) {
		if runtime.GOOS != "linux" {
			t.Skip("PTY integration currently verified on Linux")
		}
		python, err := exec.LookPath("python3")
		if err != nil {
			t.Skip("PTY integration needs Python 3")
		}
		cmd := exec.CommandContext(ctx, python, "-c", terminalEditingProbe, binary)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("terminal editing: %v\n%s", err, out)
		}
	})
}

// Run against an actual PTY: escape bytes in a pipe would not exercise raw mode,
// redraw, terminal restoration, or Unicode editing in the interactive tool.
const terminalEditingProbe = `
import os, pty, select, signal, struct, subprocess, sys, termios, time, fcntl

class Terminal:
    def __init__(self, *args):
        self.master, self.slave = pty.openpty()
        fcntl.ioctl(self.slave, termios.TIOCSWINSZ, struct.pack('HHHH', 24, 80, 0, 0))
        self.original = termios.tcgetattr(self.slave)
        self.proc = subprocess.Popen([sys.argv[1], *args], stdin=self.slave, stdout=self.slave, stderr=self.slave)
        self.buffer = b''
        self.ready()
    def wait(self, token):
        deadline = time.monotonic() + 8
        while token not in self.buffer:
            if time.monotonic() > deadline:
                raise AssertionError('timeout waiting for %r; received %r' % (token, self.buffer[-3000:]))
            if select.select([self.master], [], [], .05)[0]:
                self.buffer += os.read(self.master, 65536)
        i = self.buffer.index(token) + len(token)
        self.buffer = self.buffer[i:]
    def ready(self):
        self.wait(b'\x1b[?2004h')
        assert not (termios.tcgetattr(self.slave)[3] & termios.ICANON)
    def send(self, text):
        os.write(self.master, text.encode('utf-8'))
    def value(self, keys, expected):
        self.send(keys)
        self.wait(b'\x1b[?2004l' + expected.encode() + b'\r\n')
        self.ready()
    def close(self, interrupt=False):
        try:
            if interrupt: self.proc.send_signal(signal.SIGINT)
            else: self.send(':quit\r')
            code = self.proc.wait(timeout=8)
            assert code == (1 if interrupt else 0), code
            assert termios.tcgetattr(self.slave) == self.original, 'terminal settings not restored'
        finally:
            if self.proc.poll() is None: self.proc.kill(); self.proc.wait()
            os.close(self.master); os.close(self.slave)

t = Terminal('repl')
try:
    t.value('2 ** 4\x1b[D\x1b[3~3\r', '8')       # left + Delete
    t.value('-3 ** 2\r', '-9')
    t.value('(-3) ** 2\r', '9')
    t.value('8 */ 3\x7f2\r', '3')
    t.value('3 + 4\x01\x0b\x19\r', '7')        # Home, kill, yank
    t.value('2+3\x7f\x1f\r', '5')               # undo deletion
    t.value('12\x14\r', '21')                    # transpose
    t.value('7 + 9\x17' + '2\r', '9')            # kill previous word
    t.value('3 + 4\x1bb\x1bd' + '5\r', '8')     # Alt word move/delete
    t.value('2 + 3\x01\x1b[C\x15' + '4\x05\r', '7') # cut to start + End
    t.value('42\r', '42')
    t.value('\x1b[A\r', '42')                    # history
    t.value('6\x1b[A\x1b[B\r', '6')             # recover current draft
    t.value('\x12' + '42\r', '42')               # reverse history search
    t.value('9\x12missing\x07\r', '9')           # cancel history search
    t.value('parse_n\t("3")\r', '3')            # core completion
    t.value('answer=5\r', '5')
    t.value('answ\t ** 2\r', '25')               # saved binding completion
    t.value('"你a"\x1b[D\x7f好\x1b[F\r', '"你好"')
    t.value('"Ae\u0301"\x1b[D\x7fB\x1b[F\r', '"AB"')
    t.value('"A👩‍💻"\x1b[D\x7fB\x1b[F\r', '"AB"')
    t.value('\x1b[200~if true {\r\n2 ** 3\r\n} else {0}\x1b[201~\r', '8')
    t.send('fn broken(x:number)->number {\r'); t.ready()
    t.send('unfinished\x03'); t.wait(b'^C'); t.ready()
    t.value('1+2\r', '3')                         # Ctrl-C clears pending cell
finally:
    t.close()

t = Terminal('learn', '-lesson', 'arithmetic')
try:
    t.send('(x + 2) * 4\x1b[D\x1b[C\x7f3\r')  # both arrows edit lesson answer
    t.wait('通过'.encode()); t.ready()
    t.send(':edit\r'); t.ready()
    t.send('broken\x03'); t.wait(b'^C'); t.ready()
    t.send('(x + 2) * 3\r')
    t.wait('通过'.encode()); t.ready()
finally:
    t.close()

t = Terminal('repl')
t.close(interrupt=True)                           # asynchronous cancel restores raw mode

t = Terminal('repl', '-host', 'demo')
try:
    t.send('import go "strings" as text\r'); t.ready()
    t.value('perform effect text.ToU\t("ada")\r', '"ADA"')
finally:
    t.close()
`
