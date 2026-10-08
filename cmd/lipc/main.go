package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"go/format"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	stdruntime "runtime"
	"strconv"
	"strings"
	"syscall"

	"lipalpha/compiler"
	"lipalpha/runtime"
)

const version = "0.6.1"

func main() {
	os.Exit(execute(os.Args[1:]))
}

func execute(args []string) int {
	if len(args) == 0 {
		return help(nil)
	}
	switch args[0] {
	case "version", "--version":
		if len(args) == 2 && args[1] == "--help" {
			return help([]string{"version"})
		}
		if len(args) != 1 {
			return usageError("version", "version does not accept arguments")
		}
		fmt.Println(version)
		return 0
	case "help", "--help":
		if len(args) == 2 && args[1] == "--help" {
			return help([]string{"help"})
		}
		return help(args[1:])
	case "check":
		return check(args[1:])
	case "inspect":
		return inspect(args[1:])
	case "build":
		return build(args[1:])
	case "run":
		return run(args[1:])
	case "repl":
		return repl(args[1:])
	default:
		if strings.HasPrefix(args[0], "-") {
			return usageError("", fmt.Sprintf("unknown option %q", args[0]))
		}
		return usageError("", fmt.Sprintf("unknown command %q", args[0]))
	}
}

func check(args []string) int {
	fs := commandFlags("check")
	structured := fs.Bool("json", false, "print lip.diagnostics.v1 JSON for humans and agents")
	if err := parseFlags(fs, args); err != nil {
		return flagFailure(fs, err)
	}
	if fs.NArg() != 1 {
		return usageError("check", "check expects one source file")
	}
	if *structured {
		report := compiler.CheckFile(fs.Arg(0))
		if err := json.NewEncoder(os.Stdout).Encode(report); err != nil {
			fmt.Fprintln(os.Stderr, "lipc:", err)
			return 1
		}
		if !report.OK {
			return 1
		}
		return 0
	}
	graph, err := compiler.CompileFile(fs.Arg(0))
	if err != nil {
		fmt.Fprintln(os.Stderr, "lipc:", err)
		return 1
	}
	fmt.Printf("ok: flow %s, %d graph nodes\n", graph.Flow, len(graph.Nodes))
	for _, dependency := range graph.Dependencies {
		fmt.Printf("import: %s:%s", dependency.Kind, dependency.Spec)
		if dependency.Alias != "" {
			fmt.Printf(" as %s", dependency.Alias)
		}
		fmt.Println()
	}
	return 0
}

func build(args []string) int {
	fs := commandFlags("build")
	out := fs.String("output", "", "output executable or generated Go file")
	pkg := fs.String("package", "main", "generated Go package name")
	noMain := fs.Bool("no-main", false, "generate a library package without main")
	emitGo := fs.Bool("emit-go", false, "write generated Go source instead of compiling an executable")
	if err := parseFlags(fs, args); err != nil {
		return flagFailure(fs, err)
	}
	if fs.NArg() != 1 {
		return usageError("build", "build expects one source file")
	}
	input := fs.Arg(0)
	includeMain := !*noMain
	if includeMain && *pkg != "main" {
		return usageError("build", fmt.Sprintf("package %q requires --no-main", *pkg))
	}
	emitSource := *emitGo || *noMain
	graph, err := compiler.CompileFile(input)
	if err != nil {
		return fail(err)
	}
	code, err := compiler.GenerateGoWithOptions(graph, compiler.GenerateOptions{PackageName: *pkg, IncludeMain: includeMain})
	if err != nil {
		return fail(err)
	}
	formatted, err := format.Source([]byte(code))
	if err != nil {
		return fail(fmt.Errorf("format generated Go: %w", err))
	}
	output := *out
	name := executableName(graph.Flow)
	if emitSource {
		name = graph.Flow + "_generated.go"
	}
	if output == "" {
		output = name
		if emitSource {
			output = filepath.Join(filepath.Dir(input), name)
		}
	} else if outputDirectory(output) {
		output = filepath.Join(output, name)
	}
	if err := distinctOutput(input, output); err != nil {
		return usageError("build", err.Error())
	}
	if emitSource {
		if err := writeFile(output, formatted); err != nil {
			return fail(err)
		}
		fmt.Printf("generated %s -> %s\n", input, output)
		return 0
	}
	if err := compileGenerated(formatted, output); err != nil {
		return fail(err)
	}
	fmt.Printf("built %s -> %s\n", input, output)
	return 0
}

func run(args []string) int {
	fs := commandFlags("run")
	tracePath := fs.String("trace", "", "write execution trace JSON to this file")
	// flag.Parse stops at the first positional argument: the entry file.
	// Everything after it belongs to the program, including option-like values.
	if err := parseFlags(fs, args); err != nil {
		return flagFailure(fs, err)
	}
	if fs.NArg() < 1 {
		return usageError("run", "run expects a source file followed by program inputs")
	}
	input := fs.Arg(0)
	programArgs := fs.Args()[1:]
	if *tracePath != "" {
		absolute, err := filepath.Abs(*tracePath)
		if err != nil {
			return fail(err)
		}
		*tracePath = absolute
		if err := distinctOutput(input, *tracePath); err != nil {
			return usageError("run", err.Error())
		}
	}
	graph, err := compiler.CompileFile(input)
	if err != nil {
		return fail(err)
	}
	params := make([]runtime.Input, len(graph.Params))
	for i, name := range graph.Params {
		params[i] = runtime.Input{Name: name, Type: graph.ParamTypes[name]}
	}
	if _, err := runtime.ParseCLIInputs(programArgs, params); err != nil {
		fmt.Fprintln(os.Stderr, "lipc:", err)
		fmt.Fprintf(os.Stderr, "usage: lipc run %s", displayArgument(input))
		for _, param := range params {
			fmt.Fprintf(os.Stderr, " <%s:%s>", param.Name, param.Type)
		}
		fmt.Fprintln(os.Stderr)
		return 2
	}
	code, err := compiler.GenerateGoWithOptions(graph, compiler.GenerateOptions{PackageName: "main", IncludeMain: true, TracePath: *tracePath})
	if err != nil {
		return fail(err)
	}
	formatted, err := format.Source([]byte(code))
	if err != nil {
		return fail(fmt.Errorf("format generated Go: %w", err))
	}
	directory, err := os.MkdirTemp("", "lipc-run-")
	if err != nil {
		return fail(err)
	}
	defer os.RemoveAll(directory)
	binary := filepath.Join(directory, executableName("program"))
	if err := compileGenerated(formatted, binary); err != nil {
		fmt.Fprintln(os.Stderr, "lipc:", err)
		return 1
	}
	command := exec.Command(binary, programArgs...)
	command.Stdin, command.Stdout, command.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := runCommand(command); err != nil {
		if exitError, ok := err.(*exec.ExitError); ok {
			return exitError.ExitCode()
		}
		return fail(err)
	}
	return 0
}

// Forward cancellation while allowing callers to clean up temporary builds.
func runCommand(command *exec.Cmd) error {
	signals := make(chan os.Signal, 2)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(signals)
	if err := command.Start(); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	for {
		select {
		case received := <-signals:
			_ = command.Process.Signal(received)
		case err := <-done:
			return err
		}
	}
}

func inspect(args []string) int {
	fs := commandFlags("inspect")
	if err := parseFlags(fs, args); err != nil {
		return flagFailure(fs, err)
	}
	if fs.NArg() != 1 {
		return usageError("inspect", "inspect expects one source file")
	}
	graph, err := compiler.CompileFile(fs.Arg(0))
	if err != nil {
		return fail(err)
	}
	data, err := compiler.InspectJSON(graph)
	if err != nil {
		return fail(err)
	}
	fmt.Println(string(data))
	return 0
}

func writeFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func compileGenerated(code []byte, output string) error {
	if _, err := exec.LookPath("go"); err != nil {
		return fmt.Errorf("building LIP programs requires Go 1.27 or newer: %w", err)
	}
	root, cleanup, err := temporarySource(code)
	if err != nil {
		return err
	}
	defer cleanup()
	return compileModule(root, output)
}

func compileModule(root, output string) error {
	absoluteOutput, err := filepath.Abs(output)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(absoluteOutput), 0o755); err != nil {
		return err
	}
	command := exec.Command("go", "build", "-buildvcs=false", "-mod=readonly", "-o", absoluteOutput, ".")
	command.Dir = root
	command.Env = append(os.Environ(), "GOWORK=off", "GO111MODULE=on")
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	return runCommand(command)
}

func temporarySource(code []byte) (root string, cleanup func(), err error) {
	root, err = os.MkdirTemp("", "lipc-build-")
	if err != nil {
		return "", func() {}, err
	}
	cleanup = func() { _ = os.RemoveAll(root) }
	if err := runtime.WriteBuildModule(root); err != nil {
		cleanup()
		return "", func() {}, err
	}
	if err := os.WriteFile(filepath.Join(root, "main.go"), code, 0o644); err != nil {
		cleanup()
		return "", func() {}, err
	}
	return root, cleanup, nil
}

func commandFlags(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	return fs
}

// The entry file ends tool options for every command. Option values are
// consumed before looking for that boundary; program inputs remain untouched.
func parseFlags(fs *flag.FlagSet, args []string) error {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" || arg == "-" || !strings.HasPrefix(arg, "-") {
			break
		}
		if !strings.HasPrefix(arg, "--") {
			return fmt.Errorf("unknown option %q; tool options start with --", arg)
		}
		name, _, inline := strings.Cut(strings.TrimPrefix(arg, "--"), "=")
		option := fs.Lookup(name)
		if option == nil {
			break // Let flag report unknown options and --help.
		}
		if !inline {
			boolean, ok := option.Value.(interface{ IsBoolFlag() bool })
			if !(ok && boolean.IsBoolFlag()) {
				i++
			}
		}
	}
	return fs.Parse(args)
}

func flagFailure(fs *flag.FlagSet, err error) int {
	if err == flag.ErrHelp {
		return help([]string{fs.Name()})
	}
	message := err.Error()
	if name, ok := strings.CutPrefix(message, "flag provided but not defined: -"); ok {
		message = "unknown option --" + name
	} else if name, ok := strings.CutPrefix(message, "flag needs an argument: -"); ok {
		message = "option --" + name + " requires a value"
	} else {
		message = strings.ReplaceAll(message, "for flag -", "for option --")
	}
	return usageError(fs.Name(), message)
}

func usageError(command, message string) int {
	fmt.Fprintln(os.Stderr, "lipc:", message)
	if command == "" {
		fmt.Fprintln(os.Stderr, "See 'lipc help' for available commands.")
	} else {
		fmt.Fprintf(os.Stderr, "See 'lipc help %s' for usage.\n", command)
	}
	return 2
}

func outputDirectory(path string) bool {
	if path == "" {
		return false
	}
	info, err := os.Stat(path)
	return (err == nil && info.IsDir()) || os.IsPathSeparator(path[len(path)-1])
}

func executableName(name string) string {
	goos := os.Getenv("GOOS")
	if goos == "" {
		goos = stdruntime.GOOS
	}
	if goos == "windows" {
		return name + ".exe"
	}
	return name
}

func distinctOutput(input, output string) error {
	sourcePath, err := filepath.Abs(input)
	if err != nil {
		return err
	}
	outputPath, err := filepath.Abs(output)
	if err != nil {
		return err
	}
	sourceInfo, sourceErr := os.Stat(sourcePath)
	outputInfo, outputErr := os.Stat(outputPath)
	if sourcePath == outputPath || (sourceErr == nil && outputErr == nil && os.SameFile(sourceInfo, outputInfo)) {
		return fmt.Errorf("output %q would overwrite the source; choose a different path", output)
	}
	return nil
}

func displayArgument(arg string) string {
	if strings.ContainsAny(arg, " \t\r\n\"'") {
		return strconv.Quote(arg)
	}
	return arg
}

func usage() {
	fmt.Print("usage: lipc <command> [options]\n\n")
	for _, command := range [][3]string{
		{"repl", "[--quiet]", "Start an interactive session"},
		{"run", "[--trace path.json] file.lip [inputs...]", "Compile and run"},
		{"build", "[--output path] file.lip", "Build an executable"},
		{"check", "[--json] file.lip", "Check a program"},
		{"inspect", "file.lip", "Show its dependency graph"},
		{"version", "", "Show the version"},
		{"help", "[command]", "Show help"},
	} {
		fmt.Printf("  %-9s %-40s %s\n", command[0], command[1], command[2])
	}
}

func help(args []string) int {
	if len(args) > 1 {
		return usageError("help", "help accepts one command name")
	}
	if len(args) == 0 {
		fmt.Printf("lipc %s — compile and run LIP programs\n", version)
		fmt.Println()
		usage()
		fmt.Println()
		fmt.Println("Start with: lipc run hello.lip 小林")
		fmt.Println("Use 'lipc help <command>' for options and examples.")
		return 0
	}
	switch args[0] {
	case "repl":
		fmt.Println("usage: lipc repl [--quiet]")
		fmt.Println("Evaluate expressions, bindings and fn declarations with Go 1.27 or newer.")
		fmt.Println("Values and functions persist between cells; completed cells are not replayed.")
		fmt.Println("Unclosed (), [] or {} continue on the next line. :help lists session commands.")
		fmt.Println("Use --quiet for piped input without a banner or prompts. End with :quit or EOF (Ctrl-D).")
	case "version":
		fmt.Println("usage: lipc version")
		fmt.Println("Show the compiler version.")
	case "inspect":
		fmt.Println("usage: lipc inspect file.lip")
		fmt.Println("Check the program and print its dependency graph as lip.graph.v1 JSON; no execution.")
	case "check":
		fmt.Println("usage: lipc check [--json] file.lip")
		fmt.Println("Check syntax, names, types and dependencies; errors include their source location and a hint.")
		fmt.Println("Use --json for lip.diagnostics.v1. Checking stops at the first error.")
	case "build":
		fmt.Println("usage: lipc build [--output path] [--emit-go] [--no-main] [--package name] file.lip")
		fmt.Println("Build an executable with Go 1.27 or newer; runtime sources are bundled.")
		fmt.Println("The default executable is named after the Flow in the current directory (.exe on Windows).")
		fmt.Println("Use --emit-go for Go source; --no-main generates library source directly.")
		fmt.Println("Source defaults to <Flow>_generated.go next to the input. --output also accepts a directory.")
		fmt.Println("Library: lipc build --no-main --package hostflow --output flow.go file.lip")
	case "run":
		fmt.Println("usage: lipc run [--trace path.json] file.lip [inputs...]")
		fmt.Println("Compile and run in the current directory with Go 1.27 or newer; temporary files are cleaned up.")
		fmt.Println("The file may contain a Flow or just top-level statements, such as a = 2; b = 3; print(a / (a + b)).")
		fmt.Println("Tool options go before the file; everything after it is passed in Flow parameter order, including --help or --trace.")
		fmt.Println("Example: lipc run --trace trace.json file.lip args...")
		fmt.Println("Use --trace to record success or execution failure; normal output still goes to stdout.")
		fmt.Println("Inputs bind by position. Missing, extra or invalid inputs are reported before compilation.")
	case "help":
		fmt.Println("usage: lipc help [command]")
		fmt.Println("Show available commands or help for one command; --help works on each command too.")
	default:
		return usageError("", fmt.Sprintf("unknown command %q", args[0]))
	}
	return 0
}

func fail(err error) int {
	fmt.Fprintln(os.Stderr, "lipc:", err)
	return 1
}
