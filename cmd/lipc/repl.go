package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"go/format"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"lipalpha/compiler"
	"lipalpha/compiler/ast"
	"lipalpha/compiler/lexer"
	"lipalpha/compiler/parser"
	"lipalpha/compiler/token"
	"lipalpha/runtime"
)

type replSession struct {
	values       map[string]runtime.Value
	functions    []*ast.Function
	dependencies []ast.Dependency
	history      []string
}

func repl(args []string) int {
	fs := commandFlags("repl")
	quiet := fs.Bool("quiet", !terminalInput(os.Stdin) || !terminalInput(os.Stdout), "omit the banner and prompts (automatic for pipes and redirected output)")
	if err := parseFlags(fs, args); err != nil {
		return flagFailure(fs, err)
	}
	if fs.NArg() != 0 {
		return usageError("repl", "repl does not take a source file; enter LIP expressions and bindings directly")
	}
	if _, err := exec.LookPath("go"); err != nil {
		return fail(fmt.Errorf("the compiled REPL requires Go 1.27 or newer: %w", err))
	}
	return runREPL(os.Stdin, os.Stdout, os.Stderr, *quiet)
}

func runREPL(input io.Reader, output, errors io.Writer, quiet bool) int {
	session := &replSession{values: map[string]runtime.Value{}}
	reader := bufio.NewReader(input)
	var editor *lineEditor
	if file, ok := input.(*os.File); ok && terminalInput(file) {
		if target, ok := output.(*os.File); ok && terminalInput(target) {
			editor = &lineEditor{input: reader, output: output, file: file}
			editor.complete = session.completions
		}
	}
	if !quiet {
		fmt.Fprintln(output, "LIP", version, "— compiled interactive session")
		fmt.Fprintln(output, "Enter expressions, bindings or fn declarations. :help for commands; :quit or Ctrl-D to exit.")
	}
	var source string
	for {
		prompt := ""
		if !quiet {
			if source == "" {
				prompt = fmt.Sprintf("In [%d]: ", len(session.history)+1)
			} else {
				prompt = "   ...: "
			}
		}
		var line string
		var readErr error
		if editor != nil {
			editor.history = session.history
			line, readErr = editor.readLine(prompt)
		} else {
			fmt.Fprint(output, prompt)
			line, readErr = reader.ReadString('\n')
		}
		if readErr == errInputCanceled {
			source = ""
			continue
		}
		if readErr != nil && readErr != io.EOF {
			fmt.Fprintln(errors, "lipc:", readErr)
			return 1
		}
		text := strings.TrimSpace(line)
		if strings.HasPrefix(text, ":") {
			if text == ":quit" || text == ":exit" {
				return 0
			} else if text == ":cancel" {
				source = ""
			} else if text == ":help" || text == ":vars" || text == ":history" {
				session.command(text, output, errors)
			} else if source != "" {
				fmt.Fprintln(errors, "lipc: finish the current cell, or use :cancel to discard it")
			} else if session.command(text, output, errors) {
				return 0
			}
		} else {
			source += line
			if !incompleteCell(source) || readErr == io.EOF {
				if hasCellContent(source) {
					session.history = append(session.history, strings.TrimRight(source, "\r\n"))
					if err := session.evaluate(source, output, errors, quiet); err != nil {
						fmt.Fprintln(errors, "lipc:", err)
					}
				}
				source = ""
			}
		}
		if readErr == io.EOF {
			return 0
		}
	}
}

func (s *replSession) command(command string, output, errors io.Writer) bool {
	switch command {
	case ":quit", ":exit":
		return true
	case ":help":
		fmt.Fprintln(output, ":help     Show commands\n:vars     Show saved values\n:history  Show entered cells\n:reset    Clear values and declarations\n:cancel   Discard an unfinished cell\n:quit     Exit (also :exit or EOF)")
		fmt.Fprintln(output, "Bindings are immutable; use a new name or :reset. Separate statements with newlines or ;. Open (), [] or {} continue on the next line.")
		fmt.Fprintln(output, "Keys: ←/→ move, ↑/↓ history, Home/End, Backspace/Delete, Tab complete. Ctrl-A/E start/end, Ctrl-U/K erase, Ctrl-W erase word, Ctrl-L clear screen, Ctrl-C cancel, Ctrl-D exit on empty input. Paste multiple lines, then Enter to run.")
	case ":vars":
		if len(s.values) == 0 {
			fmt.Fprintln(output, "No saved values yet.")
		}
		for _, name := range sortedNames(s.values) {
			value, _ := runtime.FormatValue(s.values[name])
			fmt.Fprintf(output, "%s = %s\n", name, value)
		}
	case ":history":
		if len(s.history) == 0 {
			fmt.Fprintln(output, "No inputs yet.")
		}
		for i, source := range s.history {
			fmt.Fprintf(output, "In [%d]: %s\n", i+1, source)
		}
	case ":reset":
		s.values = map[string]runtime.Value{}
		s.functions, s.dependencies = nil, nil
		fmt.Fprintln(output, "Values and declarations cleared.")
	default:
		fmt.Fprintf(errors, "lipc: unknown REPL command %q; use :help\n", command)
	}
	return false
}

// Ask the parser whether another line can finish the input. This also handles
// split if/else expressions, function headers and unfinished operators.
func incompleteCell(source string) bool {
	tokens, err := lexer.New(source).Lex()
	if err != nil {
		return false
	}
	_, err = parser.New(tokens).ParseCell()
	return parser.IsIncomplete(err)
}

func hasCellContent(source string) bool {
	tokens, err := lexer.New(source).Lex()
	return err != nil || len(tokens) > 1
}

func sortedNames(values map[string]runtime.Value) []string {
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func replValueType(value runtime.Value) string {
	switch value.(type) {
	case float64:
		return "number"
	case string:
		return "string"
	case bool:
		return "bool"
	case []any:
		return "list"
	case map[string]any:
		return "object"
	default:
		return "any"
	}
}

func reserveBindings(statements []ast.Stmt, used map[string]bool) {
	for _, statement := range statements {
		switch value := statement.(type) {
		case *ast.BindStmt:
			used[value.Name] = true
		case *ast.WhenStmt:
			reserveBindings(value.Body, used)
		}
	}
}

func (s *replSession) evaluate(source string, output, errors io.Writer, quiet bool) error {
	path := fmt.Sprintf("<repl:%d>", len(s.history))
	tokens, err := lexer.New(source).Lex()
	if err != nil {
		return compiler.SourceError(path, source, "LIP_LEX_ERROR", err)
	}
	if len(tokens) == 1 { // A comment-only cell has no work to compile.
		return nil
	}
	cell, err := parser.New(tokens).ParseCell()
	if err != nil {
		return compiler.SourceError(path, source, "LIP_SYNTAX_ERROR", err)
	}
	params := sortedNames(s.values)
	types := map[string]string{}
	fields := []ast.ObjectField{}
	used := map[string]bool{}
	for _, name := range params {
		types[name], used[name] = replValueType(s.values[name]), true
		fields = append(fields, ast.ObjectField{Name: name, Value: &ast.IdentExpr{Name: name}})
	}
	reserveBindings(cell.Body, used)
	for _, stmt := range cell.Body {
		if binding, ok := stmt.(*ast.BindStmt); ok {
			fields = append(fields, ast.ObjectField{Name: binding.Name, Value: &ast.IdentExpr{Name: binding.Name, Pos: binding.Pos}})
		}
	}
	var result ast.Expr = &ast.LiteralExpr{Value: nil}
	if cell.Result != nil {
		name := "repl_result"
		for used[name] {
			name += "_"
		}
		cell.Body = append(cell.Body, &ast.BindStmt{Name: name, Expr: cell.Result, Pos: cell.ResultPos})
		result = &ast.IdentExpr{Name: name, Pos: cell.ResultPos}
	}
	cell.Body = append(cell.Body, &ast.ReturnStmt{Expr: &ast.ObjectExpr{Fields: []ast.ObjectField{
		{Name: "vars", Value: &ast.ObjectExpr{Fields: fields}},
		{Name: "result", Value: result},
	}}, Pos: cell.ResultPos})
	functions := append(append([]*ast.Function(nil), s.functions...), cell.Functions...)
	dependencies := append(append([]ast.Dependency(nil), s.dependencies...), cell.Dependencies...)
	graph, err := compiler.Build(&ast.Program{Functions: functions, Dependencies: dependencies, Flow: &ast.Flow{
		Name: "REPL", Params: params, ParamTypes: types, ReturnType: "object", Body: cell.Body, Pos: token.Pos{Line: 1, Column: 1},
	}})
	if err != nil {
		return compiler.SourceError(path, source, "LIP_CHECK_ERROR", err)
	}
	graph.SourceName, graph.Source = path, source
	for _, dependency := range dependencies {
		if dependency.Kind != "python" {
			return fmt.Errorf("REPL Host/Go adapters require a Go host program; use lipc build --no-main for such programs (print needs no import)")
		}
	}
	code, err := compiler.GenerateGoWithOptions(graph, compiler.GenerateOptions{PackageName: "main", IncludeDiagnostics: true})
	if err != nil {
		return err
	}
	formatted, err := format.Source([]byte(code))
	if err != nil {
		return err
	}
	directory, cleanup, err := temporarySource(formatted)
	if err != nil {
		return err
	}
	defer cleanup()
	inputsPath, resultPath := filepath.Join(directory, "inputs.json"), filepath.Join(directory, "result.json")
	inputs, err := json.Marshal(s.values)
	if err != nil {
		return err
	}
	if err := os.WriteFile(inputsPath, inputs, 0o600); err != nil {
		return err
	}
	harness := fmt.Sprintf(replHarness, inputsPath, resultPath)
	if len(dependencies) > 0 {
		harness = strings.Replace(harness, "// PYTHON", "worker, err := runtime.NewPythonWorker(ctx, runtime.PythonWorkerConfig{}); if err != nil { return nil, err }; defer worker.Close(); host = runtime.NewPythonHost(worker)", 1)
	}
	if err := os.WriteFile(filepath.Join(directory, "repl_main.go"), []byte(harness), 0o600); err != nil {
		return err
	}
	binary := filepath.Join(directory, executableName("cell"))
	if err := compileModule(directory, binary); err != nil {
		return fmt.Errorf("compile REPL cell: %w", err)
	}
	command := exec.Command(binary)
	command.Stdout, command.Stderr = output, errors
	if err := runCommand(command); err != nil {
		return fmt.Errorf("run REPL cell: %w", err)
	}
	data, err := os.ReadFile(resultPath)
	if err != nil {
		return err
	}
	var response struct {
		Value struct {
			Vars   map[string]runtime.Value `json:"vars"`
			Result runtime.Value            `json:"result"`
		} `json:"value"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(data, &response); err != nil {
		return fmt.Errorf("decode REPL result: %w", err)
	}
	if response.Error != "" {
		return fmt.Errorf("%s", response.Error)
	}
	// Commit only successful cells. Earlier values are passed as inputs, so
	// earlier print calls, Python calls and other work never run a second time.
	s.values, s.functions, s.dependencies = response.Value.Vars, functions, dependencies
	show := cell.Result != nil
	if call, ok := cell.Result.(*ast.CallExpr); ok && call.Name == "print" {
		show = false
	}
	if show {
		value, err := runtime.FormatValue(response.Value.Result)
		if err != nil {
			return err
		}
		if !quiet {
			fmt.Fprintf(output, "Out[%d]: ", len(s.history))
		}
		fmt.Fprintln(output, value)
	}
	return nil
}

// Results use a private JSON file so explicit print output is never parsed as
// session state. Each cell runs in the caller's working directory.
const replHarness = `package main
import ("context"; "encoding/json"; "fmt"; "os"; "os/signal"; "syscall"; "lipalpha/runtime")
func evaluateCell() (runtime.Value, error) {
 data, err := os.ReadFile(%q); if err != nil { return nil, err }
 var inputs map[string]runtime.Value
 if err := json.Unmarshal(data, &inputs); err != nil { return nil, err }
 ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM); defer stop()
 host := runtime.DefaultHost()
 // PYTHON
 value, trace, err := RunSequential(ctx, host, inputs)
 if err != nil { return nil, executionError(err, trace) }; return value, nil
}
func main() {
 value, err := evaluateCell()
 response := struct { Value runtime.Value ` + "`json:\"value\"`" + `; Error string ` + "`json:\"error\"`" + ` }{Value: value}
 if err != nil { response.Error = err.Error() }
 data, err := json.Marshal(response)
 if err == nil { err = os.WriteFile(%q, data, 0600) }
 if err != nil { fmt.Fprintln(os.Stderr, "REPL result:", err); os.Exit(1) }
}
`
