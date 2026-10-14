// lipc runs LIP source, emits standalone Go, or builds an executable.
package main

import (
	"context"
	"flag"
	"fmt"
	"lipbeta/script"
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
	case "repl":
		return repl(ctx, args[1:])
	case "learn":
		return learn(ctx, args[1:])
	case "help", "-h", "--help":
		fmt.Println("lipc run [-host demo] [-input JSON] [-parallel N] program.lip\nlipc emit-go [-host demo] -o program.go program.lip\nlipc build [-host demo] -o executable program.lip\nlipc repl [-host demo] [-quiet]\nlipc learn [-list] [-lesson ID] [-progress file]")
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
	parallel := flags.Int("parallel", 1, "maximum active graph nodes")
	steps := flags.Int64("steps", 10000000, "step budget (0 unlimited)")
	elements := flags.Int64("elements", 1000000, "list allocation budget (0 unlimited)")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 1 {
		return usage()
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
	source, err := os.ReadFile(flags.Arg(0))
	if err != nil {
		return err
	}
	program, err := script.Compile(string(source), host)
	if err != nil {
		return err
	}
	if command == "run" {
		data, err := script.DecodeData([]byte(*input))
		if err != nil {
			return err
		}
		fields, ok := data.Fields()
		if !ok {
			return fmt.Errorf("input must be a JSON object")
		}
		report, err := program.Run(ctx, fields, script.Options{Parallelism: *parallel, MaxSteps: *steps, MaxElements: *elements, MaxNodes: 1000000})
		if err != nil {
			return err
		}
		dataJSON, err := script.EncodeJSON(report.Output)
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
	return fmt.Errorf("usage: lipc {run|emit-go|build|repl|learn}; see lipc help")
}
