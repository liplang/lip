package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"lip/script"
	"os"
	"sort"
	"strconv"
	"strings"
)

func terminal(file *os.File) bool {
	return terminalInput(file)
}

type inputLine struct {
	text string
	err  error
}

// Signals can cancel the CLI even while it is waiting for terminal input.
func interactiveLines(ctx context.Context, input io.Reader) <-chan inputLine {
	lines := make(chan inputLine)
	go func() {
		defer close(lines)
		reader := bufio.NewReader(input)
		for {
			line, err := reader.ReadString('\n')
			select {
			case lines <- inputLine{line, err}:
			case <-ctx.Done():
				return
			}
			if err != nil {
				return
			}
		}
	}()
	return lines
}

func nextLine(ctx context.Context, lines <-chan inputLine) (string, error) {
	select {
	case line, ok := <-lines:
		if !ok {
			return "", io.EOF
		}
		return line.text, line.err
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

func interactiveOptions() script.Options {
	return script.Options{Parallelism: 4, MaxSteps: 1000000, MaxElements: 1000000, MaxNodes: 100000}
}

func showValue(value script.Value) string {
	if description, ok := script.NativeDescription(value); ok {
		return description
	}
	if value.Kind() == script.Function {
		return "<pure function>"
	}
	if value.Kind() == script.Effect {
		return "<external request; not yet performed>"
	}
	text, err := script.Display(value)
	if err != nil {
		return "<value containing functions or requests>"
	}
	if value.Kind() == script.String {
		data, _ := script.EncodeJSON(value)
		return string(data)
	}
	return text
}

func repl(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("repl", flag.ContinueOnError)
	quiet := flags.Bool("quiet", !terminal(os.Stdin) || !terminal(os.Stdout), "hide prompts (automatic for pipes)")
	hostName := flags.String("host", "", "Host: generic Go/Python libraries by default; demo adds teaching contracts")
	project := flags.String("project", "", "project root for static LIP imports (default: current directory)")
	frames := flags.Int("frames", script.DefaultMaxFrames, "non-tail call frame budget per cell and :run (0 uses default)")
	parallel := flags.Int("parallel", 4, "maximum active leaf evaluations per cell and :run (0 uses one)")
	trace := flags.Bool("trace", true, "show best-effort print/log/telemetry diagnostics on stderr")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("repl accepts expressions interactively; use :run file.lip for a program")
	}
	if *frames < 0 {
		return fmt.Errorf("-frames must be nonnegative (0 uses the default)")
	}
	if *parallel < 0 {
		return fmt.Errorf("-parallel must be nonnegative (0 uses one)")
	}
	options := interactiveOptions()
	options.MaxFrames = *frames
	options.Parallelism = *parallel
	host := script.Host{}
	if *hostName == "demo" {
		var err error
		host, err = script.DemoHost()
		if err != nil {
			return err
		}
	} else if *hostName != "" {
		return fmt.Errorf("unknown Host %q", *hostName)
	}
	session, err := script.NewInteractiveSession(host, script.SessionOptions{ProjectRoot: *project})
	if err != nil {
		return err
	}
	defer session.Close()
	if !*quiet {
		fmt.Printf("LIP %s · Interactive evaluation\n", script.LanguageVersion)
		fmt.Println("Enter LIP expressions, statements, or declarations; :help for commands, :quit or Ctrl-D to exit.")
	}
	input := newInteractiveInput(ctx, func(prefix string) []string {
		return commandCompletions(prefix, session.Complete(prefix), ":help :vars :history :del :reset :cancel :quit :exit :run :begin :end")
	})
	var source string
	var history []string
	collecting, awaitingElse := false, false
	evaluate := func() {
		if strings.TrimSpace(source) != "" {
			history = append(history, source)
			cellOptions := options
			stop := func() {}
			if *trace {
				cellOptions.Trace, stop = script.StartTraceOutput(os.Stderr)
			}
			result, err := session.Eval(ctx, source, cellOptions)
			stop() // Drain diagnostics before entering terminal input again.
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
			} else if result.HasValue {
				if result.Help {
					text, _ := result.Value.Text()
					fmt.Println(text)
				} else {
					fmt.Println(showValue(result.Value))
				}
			}
		}
		source, awaitingElse = "", false
	}
	for ctx.Err() == nil {
		prompt := ""
		if !*quiet {
			if source == "" && !collecting {
				prompt = fmt.Sprintf("In [%d]: ", len(history)+1)
			} else {
				prompt = "   ...: "
			}
		}
		line, readErr := input.read(ctx, prompt)
		if errors.Is(readErr, errInputCanceled) {
			source, collecting, awaitingElse = "", false, false
			continue
		}
		if readErr != nil && readErr != io.EOF {
			return readErr
		}
		text := strings.TrimSpace(line)
		if awaitingElse && !collecting && !strings.HasPrefix(text, ":") && !strings.HasPrefix(text, "#") && !script.CellStartsElse(line) {
			evaluate()
		}
		if strings.HasPrefix(text, ":") && !script.CellInMultilineString(source) {
			switch {
			case text == ":quit" || text == ":exit":
				return nil
			case text == ":cancel":
				source, collecting, awaitingElse = "", false, false
			case text == ":begin" && source == "" && !collecting:
				collecting = true
			case text == ":end" && collecting:
				collecting = false
				evaluate()
			case text == ":help":
				fmt.Printf("LIP %s: pure fn compute values, Flows organize dependencies, Claims collect facts, effect/perform run external actions.\n", script.LanguageVersion)
				fmt.Println("help() shows interactive help; help(value), help(module), help(module.member) describe values, Go exports, or Python libraries.")
				fmt.Println("The generic Go/Python bridge is enabled by default; packages must exist in the current Go module or Python environment.")
				fmt.Println("Use full, case-sensitive Go package paths; configure dependencies with go get in your module, and choose an alias with as.")
				fmt.Println("import go \"strings\"; strings.ToUpper(\"hello\"); import python \"math\"; math.sqrt(3).")
				fmt.Println("The REPL displays return values: fmt.Print/Printf/Println return a byte count, which is displayed too.")
				fmt.Println("native_data(value) takes a Data snapshot; native_release(value) releases a native reference.")
				fmt.Println(":vars shows saved values; :history shows input; :del name[,name...] unbinds names for reuse; :reset clears the session; :cancel discards a draft; :quit exits.")
				fmt.Println(":run [-entry Name] [-input JSON | -entry-input JSON] [-entry-output] [-data-encoding json|c-hex] [-project DIR] [-frames N] file.lip runs a complete file with static modules.")
				fmt.Println("Quote paths containing spaces or JSON with single/double quotes; no shell runs. :run inherits -host and uses independent file bindings.")
				fmt.Println("fn/Flow declarations, static Data, exports, import lip, Claims, and script statements are supported. Call a declared Flow by name or through |>.")
				fmt.Println("LIP imports resolve under -project DIR (default: current directory) and stay frozen until :reset.")
				fmt.Println(":begin / :end submits a whole script or several declarations as one cell, including forward references and Claim joins.")
				fmt.Println("An if statement without else waits for the next line; enter else, a blank line, or another statement.")
				fmt.Println("Names stay bound until :del removes them. Open () [] {} or triple-quoted strings allow more input; other expressions use a trailing \\ to continue.")
				fmt.Println("Successful cells retain results without rerunning past actions; failed cells commit no bindings, and completed external actions remain.")
				fmt.Println("fn f(x:number)->number @{math.sqrt(x)} promises native computational purity. Outer wrappers need no @; nested native bodies need their own marker. nan and inf are number literals; use -data-encoding c-hex for lossless file transport.")
				fmt.Println("Known pure request builders retain operation summaries; closed projections, conditional/returned functions, recursive effect forwarding, and state-identity carries can preserve a known request. Open projections, dynamic calls, and general carries stay Unknown; parallel still requires every candidate Concurrent ReadOnly.")
				fmt.Println("Non-tail calls default to 10,000 active frames; -frames changes the budget. Exhaustion reports E-BUDGET; the next cell can still run.")
				fmt.Println("-parallel accepts any positive integer; it limits active evaluations without preallocating that many workers. :run inherits the limit unless overridden.")
				fmt.Println(editorHelp)
			case text == ":vars":
				values := session.Values()
				names := make([]string, 0, len(values))
				for name := range values {
					names = append(names, name)
				}
				sort.Strings(names)
				for _, name := range names {
					fmt.Printf("%s = %s\n", name, showValue(values[name]))
				}
			case text == ":history":
				for i, cell := range history {
					fmt.Printf("In [%d]: %s\n", i+1, strings.TrimRight(cell, "\r\n"))
				}
			case source != "":
				fmt.Fprintln(os.Stderr, "Finish the current input, or discard it with :cancel.")
			case text == ":reset":
				session.Reset()
				history = nil
				fmt.Println("Session cleared.")
			case text == ":del" || strings.HasPrefix(text, ":del ") || strings.HasPrefix(text, ":del\t"):
				var names []string
				if args := strings.TrimSpace(strings.TrimPrefix(text, ":del")); args != "" {
					for _, name := range strings.Split(args, ",") {
						names = append(names, strings.TrimSpace(name))
					}
				}
				if err := session.Delete(names...); err != nil {
					fmt.Fprintln(os.Stderr, err)
				}
			case strings.HasPrefix(text, ":run "):
				args, err := fileRunArgs(strings.TrimSpace(strings.TrimPrefix(text, ":run ")))
				if err == nil {
					err = run(ctx, append([]string{"run", "-host", *hostName, "-parallel", strconv.Itoa(*parallel), "-frames", strconv.Itoa(*frames), "-trace=" + strconv.FormatBool(*trace)}, args...))
				}
				if err != nil {
					fmt.Fprintln(os.Stderr, err)
				}
			default:
				fmt.Fprintln(os.Stderr, "Unknown command; enter :help for commands.")
			}
		} else {
			source += line
			if readErr == io.EOF || !collecting && !script.CellIncomplete(source) {
				if readErr != io.EOF && text != "" && script.CellAwaitingElse(source) {
					awaitingElse = true
				} else {
					evaluate()
				}
			}
		}
		if readErr == io.EOF {
			return nil
		}
	}
	return ctx.Err()
}

// Group :run arguments without invoking a shell or expanding variables. Single
// quotes preserve JSON verbatim; double quotes and backslashes group paths too.
func fileRunArgs(text string) ([]string, error) {
	var args []string
	var word strings.Builder
	var quote rune
	escaped, started := false, false
	for _, ch := range text {
		switch {
		case escaped:
			word.WriteRune(ch)
			escaped = false
		case ch == '\\' && quote != '\'':
			escaped, started = true, true
		case quote != 0:
			if ch == quote {
				quote = 0
			} else {
				word.WriteRune(ch)
			}
		case ch == '\'' || ch == '"':
			quote, started = ch, true
		case ch == ' ' || ch == '\t':
			if started {
				args = append(args, word.String())
				word.Reset()
				started = false
			}
		default:
			word.WriteRune(ch)
			started = true
		}
	}
	if quote != 0 || escaped {
		return nil, fmt.Errorf(":run has an unfinished quote or escape")
	}
	if started {
		args = append(args, word.String())
	}
	return args, nil
}
