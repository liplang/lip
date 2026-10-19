// lipc runs LIP source, emits standalone Go, or builds an executable.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"lip/script"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
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
		printGlobalHelp(os.Stdout)
		return nil
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
		if len(args) == 1 || len(args) == 2 && isHelpArg(args[1]) {
			printGlobalHelp(os.Stdout)
			return nil
		}
		if len(args) != 2 || !knownCommand(args[1]) {
			return fmt.Errorf("unknown help topic %q; run lipc help for available commands", strings.Join(args[1:], " "))
		}
		printCommandHelp(os.Stdout, args[1])
		return nil
	case "run", "emit-go", "build":
		args = args[1:]
	default:
		if isHelpArg(command) {
			printGlobalHelp(os.Stdout)
			return nil
		}
		if strings.HasPrefix(command, "-") || strings.HasSuffix(command, ".lip") || strings.Contains(command, string(filepath.Separator)) {
			// Keep the convenient `lipc program.lip` shorthand, including its
			// `lipc -host demo program.lip` form.
			command = "run"
		} else {
			return fmt.Errorf("unknown command %q; run lipc help to see available commands", command)
		}
	}
	if commandHelpRequested(args) {
		printCommandHelp(os.Stdout, command)
		return nil
	}
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	configureFlagHelp(flags, command)
	hostName := flags.String("host", "", "Host: generic Go/Python libraries by default; demo adds teaching contracts")
	output := flags.String("o", "", "generated Go or executable destination")
	input := flags.String("input", "{}", "Flow inputs as a JSON object")
	entry := flags.String("entry", "", "root Flow to run or compile (default Main, otherwise the sole root Flow)")
	project := flags.String("project", "", "project root for static LIP imports; defaults to source directory")
	entryInput := flags.String("entry-input", "", "EntryInput JSON with positional and named fields")
	entryOutput := flags.Bool("entry-output", false, "print the value/void success envelope")
	dataEncoding := flags.String("data-encoding", "json", "Data transport: json or c-hex (supports nan and inf)")
	parallel := flags.Int("parallel", 1, "maximum active leaf evaluations (waiting containers yield)")
	steps := flags.Int64("steps", 10000000, "step budget (0 unlimited)")
	elements := flags.Int64("elements", 1000000, "list allocation budget (0 unlimited)")
	frames := flags.Int("frames", script.DefaultMaxFrames, "non-tail call frame budget (0 uses default; tail calls reuse frames)")
	trace := flags.Bool("trace", true, "show best-effort print/log/telemetry diagnostics on stderr")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return fmt.Errorf("%w; run lipc %s -h for usage", err, command)
	}
	if flags.NArg() != 1 {
		return fmt.Errorf("%s requires exactly one source file; run lipc %s -h for examples", command, command)
	}
	if *frames < 0 {
		return fmt.Errorf("-frames must be nonnegative (0 uses the default)")
	}
	if *parallel < 0 {
		return fmt.Errorf("-parallel must be nonnegative (0 uses one)")
	}
	if *steps < 0 {
		return fmt.Errorf("-steps must be nonnegative (0 means unlimited)")
	}
	if *elements < 0 {
		return fmt.Errorf("-elements must be nonnegative (0 means unlimited)")
	}
	hasInput, hasEntryInput, hasOutput := false, false, false
	runtimeFlag := ""
	flags.Visit(func(f *flag.Flag) {
		hasInput = hasInput || f.Name == "input"
		hasEntryInput = hasEntryInput || f.Name == "entry-input"
		hasOutput = hasOutput || f.Name == "o"
		if runtimeFlag == "" && (f.Name == "input" || f.Name == "entry-input" ||
			f.Name == "entry-output" || f.Name == "data-encoding" || f.Name == "parallel" ||
			f.Name == "steps" || f.Name == "elements" || f.Name == "frames" || f.Name == "trace") {
			runtimeFlag = f.Name
		}
	})
	if hasInput && hasEntryInput {
		return fmt.Errorf("choose -input or -entry-input")
	}
	if command == "run" && hasOutput {
		return fmt.Errorf("-o is only valid with emit-go or build; run writes its result to stdout")
	}
	if command != "run" && runtimeFlag != "" {
		return fmt.Errorf("-%s is only valid with run; see lipc %s -h", runtimeFlag, command)
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
		return fmt.Errorf("unknown Host %q; supported values: demo (or omit -host)", *hostName)
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
		data, err := script.DecodeEntryInputWithEncoding([]byte(inputJSON), *dataEncoding)
		if err != nil {
			return err
		}
		var traces *script.TraceBuffer
		if *trace {
			var stop func()
			traces, stop = script.StartTraceOutput(os.Stderr)
			defer stop()
		}
		runOptions := script.Options{
			Parallelism: *parallel,
			MaxSteps:    *steps,
			MaxElements: *elements,
			MaxNodes:    1000000,
			MaxFrames:   *frames,
			Trace:       traces,
		}
		report, err := program.RunEntry(ctx, data, runOptions)
		if err != nil {
			return err
		}
		dataJSON, err := script.EncodeData(report.Output, *dataEncoding)
		if *entryOutput {
			dataJSON, err = script.EncodeEntryOutput(*report.EntryOutput, *dataEncoding)
		}
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
