package main

import (
	"flag"
	"fmt"
	"go/format"
	"os"
	"path/filepath"
	"strings"

	"lipalpha/compiler"
)

const version = "0.1.0"

func main() {
	args := os.Args[1:]
	if len(args) == 0 {
		usage()
		os.Exit(2)
	}
	switch args[0] {
	case "version", "--version", "-v":
		fmt.Println(version)
	case "check":
		check(args[1:])
	case "build":
		build(args[1:])
	default:
		// Shorthand: `lipc file.lip [-o generated.go]`.
		if hasLipFile(args) {
			build(args)
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
}

func build(args []string) {
	fs := flag.NewFlagSet("build", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	out := fs.String("o", "", "output Go file")
	pkg := fs.String("package", "main", "generated Go package name")
	noMain := fs.Bool("no-main", false, "generate a library package without main")
	args = moveLipFileLast(args)
	if err := fs.Parse(args); err != nil {
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
	code, err := compiler.GenerateGoWithOptions(graph, compiler.GenerateOptions{PackageName: *pkg, IncludeMain: !*noMain})
	if err != nil {
		fail(err)
	}
	formatted, err := format.Source([]byte(code))
	if err != nil {
		fail(fmt.Errorf("format generated Go: %w", err))
	}
	output := *out
	if output == "" {
		output = filepath.Join(filepath.Dir(input), graph.Flow+"_generated.go")
	}
	if err := os.MkdirAll(filepath.Dir(output), 0o755); err != nil {
		fail(err)
	}
	if err := os.WriteFile(output, formatted, 0o644); err != nil {
		fail(err)
	}
	fmt.Printf("built %s -> %s\n", input, output)
}

func hasLipFile(args []string) bool {
	for _, arg := range args {
		if strings.HasSuffix(arg, ".lip") {
			return true
		}
	}
	return false
}

func moveLipFileLast(args []string) []string {
	for i, arg := range args {
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
	fmt.Fprintln(os.Stderr, "  lipc file.lip [-o generated.go]")
	fmt.Fprintln(os.Stderr, "  lipc check file.lip")
	fmt.Fprintln(os.Stderr, "  lipc build [-o generated.go] [-package name] [-no-main] file.lip")
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "lipc:", err)
	os.Exit(1)
}
