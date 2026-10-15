// lipc runs LIP source, emits standalone Go, or builds an executable.
package main

import (
	"context"
	"flag"
	"fmt"
	"lip/script"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	if err := run(ctx, os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return usage()
	}
	command := args[0]
	switch command {
	case "serve":
		return serve(ctx, args[1:])
	case "repl":
		return repl(ctx, args[1:])
	case "learn":
		return learn(ctx, args[1:])
	case "help", "-h", "--help":
		fmt.Println("LIP 1.0 · pure values, structured Flows, Claim facts, effect/perform")
		fmt.Println("lipc run [-entry Name] [-project DIR] [-host demo] [-input JSON | -entry-input JSON] [-entry-output] [-parallel N] program.lip\nlipc emit-go [-entry Name] [-project DIR] [-host demo] -o program.go program.lip\nlipc build [-entry Name] [-project DIR] [-host demo] -o executable program.lip\nlipc serve [-addr 127.0.0.1:8080] [-route 'GET /{$}'] [-entry Name] [-project DIR] [-host demo] program.lip\nlipc serve [-addr 127.0.0.1:8080] -routes routes.json\nlipc repl [-host demo] [-quiet]\nlipc learn [-list] [-lesson ID] [-progress file]")
		return nil
	case "run", "emit-go", "build":
		args = args[1:]
	default:
		command = "run"
	}
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	hostName := flags.String("host", "", "Host: generic Go/Python libraries by default; demo adds teaching contracts")
	output := flags.String("o", "", "generated Go or executable destination")
	input := flags.String("input", "{}", "Flow inputs as a JSON object")
	entry := flags.String("entry", "", "root Flow declaration to run or compile")
	project := flags.String("project", "", "project root for static LIP imports; defaults to source directory")
	entryInput := flags.String("entry-input", "", "EntryInput JSON with positional and named fields")
	entryOutput := flags.Bool("entry-output", false, "print the value/void success envelope")
	parallel := flags.Int("parallel", 1, "maximum active leaf evaluations (waiting containers yield)")
	steps := flags.Int64("steps", 10000000, "step budget (0 unlimited)")
	elements := flags.Int64("elements", 1000000, "list allocation budget (0 unlimited)")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 1 {
		return usage()
	}
	hasInput, hasEntryInput := false, false
	flags.Visit(func(f *flag.Flag) {
		hasInput = hasInput || f.Name == "input"
		hasEntryInput = hasEntryInput || f.Name == "entry-input"
	})
	if hasInput && hasEntryInput {
		return fmt.Errorf("choose -input or -entry-input")
	}
	host := script.Host{}
	goOptions := script.GoOptions{}
	switch *hostName {
	case "":
	case "demo":
		var err error
		host, err = script.DemoHost()
		if err != nil {
			return err
		}
		if command != "run" {
			goOptions.HostSource, err = script.DemoHostGoSource()
			if err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("unknown Host %q", *hostName)
	}
	host = script.NewNativeHost(host)
	defer host.Close()
	sources, err := script.LoadSourceSet(flags.Arg(0), *project)
	if err != nil {
		return err
	}
	program, err := script.CompileSourceSet(sources, host, script.CompileOptions{Entry: *entry})
	if err != nil {
		return err
	}
	if command == "run" {
		inputJSON := *entryInput
		if !hasEntryInput {
			inputJSON = "{\"positional\":[],\"named\":" + *input + "}"
		}
		data, err := script.DecodeEntryInput([]byte(inputJSON))
		if err != nil {
			return err
		}
		report, err := program.RunEntry(ctx, data, script.Options{Parallelism: *parallel, MaxSteps: *steps, MaxElements: *elements, MaxNodes: 1000000})
		if err != nil {
			return err
		}
		outputValue := report.Output
		if *entryOutput {
			outputValue = *report.EntryOutput
		}
		dataJSON, err := script.EncodeJSON(outputValue)
		if err != nil {
			return err
		}
		fmt.Println(string(dataJSON))
		return nil
	}
	if *output == "" {
		return fmt.Errorf("%s requires -o output", command)
	}
	generated, err := program.EmitGo(goOptions)
	if err != nil {
		return err
	}
	if command == "emit-go" {
		return writeFile(*output, generated)
	}
	// Temporary source is always removed, including failed/cancelled Go builds.
	dir, err := os.MkdirTemp("", "lipc-build-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	sourcePath := filepath.Join(dir, "main.go")
	if err := os.WriteFile(sourcePath, generated, 0600); err != nil {
		return err
	}
	absolute, err := filepath.Abs(*output)
	if err != nil {
		return err
	}
	build := exec.CommandContext(ctx, "go", "build", "-o", absolute, sourcePath)
	build.Stdout, build.Stderr = os.Stdout, os.Stderr
	return build.Run()
}
func writeFile(path string, data []byte) error {
	// Never truncate an input source by accidentally selecting it as the output.
	if filepath.Ext(path) != ".go" {
		return fmt.Errorf("emit-go output must use .go extension")
	}
	return os.WriteFile(path, data, 0644)
}
func usage() error {
	return fmt.Errorf("usage: lipc {run|emit-go|build|serve|repl|learn}; see lipc help")
}
