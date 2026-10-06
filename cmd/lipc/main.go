package main

import (
	"flag"
	"fmt"
	"go/format"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"lipalpha/compiler"
)

const version = "0.4.0"

func main() {
	args := os.Args[1:]
	if len(args) == 0 {
		usage()
		os.Exit(2)
	}
	switch args[0] {
	case "version", "--version", "-v":
		fmt.Println(version)
	case "help", "--help", "-h":
		help(args[1:])
	case "check":
		check(args[1:])
	case "build":
		build(args[1:])
	case "run":
		if code := run(args[1:]); code != 0 {
			os.Exit(code)
		}
	default:
		// Shorthand keeps the original source-generation workflow:
		// `lipc file.lip [-o generated.go]`.
		if hasLipFile(args) {
			build(append([]string{"-emit-go"}, args...))
			return
		}
		usage()
		os.Exit(2)
	}
}

func check(args []string) {
	if len(args) != 1 {
		fail(fmt.Errorf("check expects one .lip file"))
	}
	graph, err := compiler.CompileFile(args[0])
	if err != nil {
		fail(err)
	}
	fmt.Printf("ok: flow %s, %d graph nodes\n", graph.Flow, len(graph.Nodes))
	for _, dependency := range graph.Dependencies {
		fmt.Printf("require: %s:%s\n", dependency.Kind, dependency.Spec)
	}
}

func build(args []string) {
	fs := flag.NewFlagSet("build", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	out := fs.String("o", "", "output executable or generated Go file")
	pkg := fs.String("package", "main", "generated Go package name")
	noMain := fs.Bool("no-main", false, "generate a library package without main")
	emitGo := fs.Bool("emit-go", false, "write generated Go source instead of compiling an executable")
	args = moveLipFileLast(args)
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return
		}
		fail(err)
	}
	if fs.NArg() != 1 {
		fail(fmt.Errorf("build expects one .lip file"))
	}
	input := fs.Arg(0)
	graph, err := compiler.CompileFile(input)
	if err != nil {
		fail(err)
	}
	includeMain := !*noMain
	if !includeMain && !*emitGo && !strings.HasSuffix(*out, ".go") {
		fail(fmt.Errorf("-no-main requires -emit-go or a .go output path"))
	}
	if !*emitGo && !strings.HasSuffix(*out, ".go") && *pkg != "main" {
		fail(fmt.Errorf("an executable build requires -package main"))
	}
	if !includeMain && *out == "" {
		*emitGo = true
	}
	code, err := compiler.GenerateGoWithOptions(graph, compiler.GenerateOptions{PackageName: *pkg, IncludeMain: includeMain})
	if err != nil {
		fail(err)
	}
	formatted, err := format.Source([]byte(code))
	if err != nil {
		fail(fmt.Errorf("format generated Go: %w", err))
	}
	output := *out
	if *emitGo || strings.HasSuffix(output, ".go") {
		if output == "" {
			output = filepath.Join(filepath.Dir(input), graph.Flow+"_generated.go")
		}
		if err := writeFile(output, formatted); err != nil {
			fail(err)
		}
		fmt.Printf("generated %s -> %s\n", input, output)
		return
	}
	if output == "" {
		output = filepath.Join(mustWorkingDir(), graph.Flow)
	}
	if err := compileGenerated(formatted, input, output); err != nil {
		fail(err)
	}
	fmt.Printf("built %s -> %s\n", input, output)
}

func run(args []string) int {
	if len(args) == 0 {
		fail(fmt.Errorf("run expects one .lip file"))
	}
	inputIndex := len(args)
	for i, arg := range args {
		if arg == "--" {
			inputIndex = i
			break
		}
	}
	if inputIndex == 0 {
		fail(fmt.Errorf("run expects one .lip file"))
	}
	input := args[0]
	if !strings.HasSuffix(input, ".lip") {
		fail(fmt.Errorf("run expects a .lip file"))
	}
	if inputIndex == len(args) && len(args) > 1 {
		fail(fmt.Errorf("run program arguments must follow --"))
	}
	programArgs := []string{}
	if inputIndex < len(args) {
		programArgs = args[inputIndex+1:]
	}
	graph, err := compiler.CompileFile(input)
	if err != nil {
		fail(err)
	}
	code, err := compiler.GenerateGoWithOptions(graph, compiler.GenerateOptions{PackageName: "main", IncludeMain: true})
	if err != nil {
		fail(err)
	}
	formatted, err := format.Source([]byte(code))
	if err != nil {
		fail(fmt.Errorf("format generated Go: %w", err))
	}
	temp, root, cleanup, err := temporarySource(formatted, input)
	if err != nil {
		fail(err)
	}
	defer cleanup()
	commandArgs := []string{"run", "-buildvcs=false", temp}
	if len(programArgs) > 0 {
		commandArgs = append(commandArgs, programArgs...)
	}
	command := exec.Command("go", commandArgs...)
	command.Dir = root
	command.Stdin = os.Stdin
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	if err := command.Run(); err != nil {
		if exitError, ok := err.(*exec.ExitError); ok {
			return exitError.ExitCode()
		}
		fmt.Fprintln(os.Stderr, "lipc:", err)
		return 1
	}
	return 0
}

func writeFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func compileGenerated(code []byte, input, output string) error {
	temp, root, cleanup, err := temporarySource(code, input)
	if err != nil {
		return err
	}
	defer cleanup()
	absoluteOutput, err := filepath.Abs(output)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(absoluteOutput), 0o755); err != nil {
		return err
	}
	command := exec.Command("go", "build", "-buildvcs=false", "-o", absoluteOutput, temp)
	command.Dir = root
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	return command.Run()
}

func temporarySource(code []byte, input string) (path, root string, cleanup func(), err error) {
	root = moduleRoot(input)
	file, err := os.CreateTemp(root, "lipc-build-*.go")
	if err != nil {
		return "", "", func() {}, err
	}
	path = file.Name()
	cleanup = func() { _ = os.Remove(path) }
	if _, err := file.Write(code); err != nil {
		_ = file.Close()
		cleanup()
		return "", "", func() {}, err
	}
	if err := file.Close(); err != nil {
		cleanup()
		return "", "", func() {}, err
	}
	return path, root, cleanup, nil
}

func moduleRoot(input string) string {
	start, err := filepath.Abs(input)
	if err != nil {
		return mustWorkingDir()
	}
	if info, err := os.Stat(start); err == nil && !info.IsDir() {
		start = filepath.Dir(start)
	}
	for {
		if _, err := os.Stat(filepath.Join(start, "go.mod")); err == nil {
			return start
		}
		parent := filepath.Dir(start)
		if parent == start {
			return mustWorkingDir()
		}
		start = parent
	}
}

func mustWorkingDir() string {
	workingDir, err := os.Getwd()
	if err != nil {
		return "."
	}
	return workingDir
}

func hasLipFile(args []string) bool {
	for index := 0; index < len(args); index++ {
		arg := args[index]
		if arg == "-o" || arg == "-package" {
			if index+1 < len(args) {
				index++
				continue
			}
		}
		if strings.HasSuffix(arg, ".lip") {
			return true
		}
	}
	return false
}

func moveLipFileLast(args []string) []string {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "-o" || arg == "-package" {
			i++
			continue
		}
		if !strings.HasSuffix(arg, ".lip") {
			continue
		}
		out := make([]string, 0, len(args))
		out = append(out, args[:i]...)
		out = append(out, args[i+1:]...)
		out = append(out, arg)
		return out
	}
	return args
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage:")
	fmt.Fprintln(os.Stderr, "  lipc version")
	fmt.Fprintln(os.Stderr, "  lipc help [command]")
	fmt.Fprintln(os.Stderr, "  lipc run file.lip [-- program-args...]")
	fmt.Fprintln(os.Stderr, "  lipc build file.lip [-o executable]")
	fmt.Fprintln(os.Stderr, "  lipc build -emit-go file.lip [-o generated.go]")
	fmt.Fprintln(os.Stderr, "  lipc check file.lip")
	fmt.Fprintln(os.Stderr, "  lipc file.lip [-o generated.go]  # compatibility shorthand")
}

func help(args []string) {
	if len(args) == 0 {
		fmt.Println("lipc — compile and run LIP programs")
		fmt.Println()
		usage()
		fmt.Println()
		fmt.Println("Use 'lipc help build' or 'lipc help run' for command details.")
		return
	}
	switch args[0] {
	case "version":
		fmt.Println("lipc version prints the compiler version.")
	case "check":
		fmt.Println("lipc check file.lip validates syntax, names, types, dependencies and graph structure.")
	case "build":
		fmt.Println("lipc build file.lip compiles a standalone executable.")
		fmt.Println("Use -o path to choose the executable; use -emit-go or a .go output path to emit source.")
		fmt.Println("Library generation: lipc build -emit-go -no-main -package name -o flow.go file.lip")
	case "run":
		fmt.Println("lipc run file.lip compiles a temporary executable and runs it.")
		fmt.Println("Arguments after -- are passed in Flow parameter order; missing or extra arguments fail.")
	default:
		usage()
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "lipc:", err)
	os.Exit(1)
}
