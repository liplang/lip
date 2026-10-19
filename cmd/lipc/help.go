package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"lip/script"
)

// isHelpArg recognizes both the conventional flag spelling and the friendly
// positional spelling used by people who try "lipc serve help" first.
func isHelpArg(arg string) bool {
	return arg == "help" || arg == "-h" || arg == "--help"
}

func commandHelpRequested(args []string) bool {
	// Keep -h/--help on the FlagSet path so users also see the actual option
	// defaults. The positional spelling is handled before parsing because the
	// standard flag package quite reasonably treats "help" as a file name.
	return len(args) == 1 && args[0] == "help"
}

// configureFlagHelp keeps flag package diagnostics useful while making -h use
// the same readable examples shown by `lipc help <command>`.
func configureFlagHelp(flags *flag.FlagSet, command string) {
	flags.SetOutput(os.Stderr)
	flags.Usage = func() {
		printCommandHelp(os.Stderr, command)
	}
}

func printGlobalHelp(w io.Writer) {
	fmt.Fprintf(w, "LIP %s · pure values, structured Flows, Claim facts, effect/perform\n\n", script.LanguageVersion)
	fmt.Fprintln(w, "Usage:")
	fmt.Fprintln(w, "  lipc <command> [options] [file.lip]")
	fmt.Fprintln(w, "  lipc help <command>")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Commands:")
	fmt.Fprintln(w, "  run       compile and run a LIP program (the default for a .lip path)")
	fmt.Fprintln(w, "  emit-go   emit standalone Go source")
	fmt.Fprintln(w, "  build     emit and compile a standalone executable")
	fmt.Fprintln(w, "  serve     expose one program or a route manifest over HTTP")
	fmt.Fprintln(w, "  repl      evaluate expressions and declarations interactively")
	fmt.Fprintln(w, "  learn     work through the guided lessons")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Try these first:")
	fmt.Fprintln(w, "  lipc run examples/basics/01_hello.lip")
	fmt.Fprintln(w, "  lipc repl")
	fmt.Fprintln(w, "  lipc learn -list")
	fmt.Fprintln(w, "  lipc serve examples/basics/01_hello.lip")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Help:")
	fmt.Fprintln(w, "  lipc help run          show run options and examples")
	fmt.Fprintln(w, "  lipc serve help        show serve options and route manifest format")
	fmt.Fprintln(w, "  lipc repl -h           show interactive options")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Common details:")
	fmt.Fprintln(w, "  -frames N limits non-tail call frames (0 uses the default).")
	fmt.Fprintln(w, "  -parallel N limits active leaf evaluations; it does not preallocate workers.")
	fmt.Fprintln(w, "  -trace=false disables best-effort print/log/telemetry output on stderr.")
}

func printCommandHelp(w io.Writer, command string) {
	switch command {
	case "run":
		fmt.Fprintln(w, "Usage: lipc run [options] program.lip")
		fmt.Fprintln(w, "       lipc program.lip")
		fmt.Fprintln(w)
		fmt.Fprintln(w, "Compile and run the selected root Flow. The result is written as JSON.")
		fmt.Fprintln(w, "The default entry is Main, or the only root Flow when Main is absent.")
		fmt.Fprintln(w)
		fmt.Fprintln(w, "Examples:")
		fmt.Fprintln(w, "  lipc run examples/basics/01_hello.lip")
		fmt.Fprintln(w, "  lipc run -entry Main -input '{\"name\":\"Ada\"}' app.lip")
		fmt.Fprintln(w, "  lipc run -host demo -parallel 4 examples/evidence.lip")
		fmt.Fprintln(w)
		fmt.Fprintln(w, "Options:")
		fmt.Fprintln(w, "  -entry NAME          select the root Flow (default: Main or the sole root Flow)")
		fmt.Fprintln(w, "  -project DIR         resolve static LIP imports from DIR")
		fmt.Fprintln(w, "  -host demo           enable the teaching Host contracts")
		fmt.Fprintln(w, "  -input JSON          named Flow input (mutually exclusive with -entry-input)")
		fmt.Fprintln(w, "  -entry-input JSON    positional/named EntryInput JSON")
		fmt.Fprintln(w, "  -entry-output        print the value/void success envelope")
		fmt.Fprintln(w, "  -data-encoding MODE  json or c-hex transport")
		fmt.Fprintln(w, "  -parallel N          limit active leaf evaluations")
		fmt.Fprintln(w, "  -steps N             step budget (0 means unlimited)")
		fmt.Fprintln(w, "  -elements N          list allocation budget (0 means unlimited)")
		fmt.Fprintln(w, "  -frames N            non-tail call frame budget (0 uses the default)")
		fmt.Fprintln(w, "  -trace=false         suppress diagnostic output on stderr")
	case "emit-go":
		fmt.Fprintln(w, "Usage: lipc emit-go [options] -o program.go program.lip")
		fmt.Fprintln(w)
		fmt.Fprintln(w, "Compile a LIP program into standalone Go source. The generated file")
		fmt.Fprintln(w, "contains the runtime and can be built from a Go module with its imports.")
		fmt.Fprintln(w)
		fmt.Fprintln(w, "Examples:")
		fmt.Fprintln(w, "  lipc emit-go -o generated.go examples/advanced/26_flows.lip")
		fmt.Fprintln(w)
		fmt.Fprintln(w, "Options: -o FILE is required; -entry, -project, and -host have the same meaning as run.")
	case "build":
		fmt.Fprintln(w, "Usage: lipc build [options] -o executable program.lip")
		fmt.Fprintln(w)
		fmt.Fprintln(w, "Compile a LIP program into a standalone executable. Temporary Go source")
		fmt.Fprintln(w, "is removed after the build, including when the build fails or is cancelled.")
		fmt.Fprintln(w)
		fmt.Fprintln(w, "Examples:")
		fmt.Fprintln(w, "  lipc build -o app examples/advanced/26_flows.lip")
		fmt.Fprintln(w)
		fmt.Fprintln(w, "Options: -o FILE is required; -entry, -project, and -host have the same meaning as run.")
	case "serve":
		fmt.Fprintln(w, "Usage: lipc serve [options] program.lip")
		fmt.Fprintln(w, "       lipc serve [options] -routes routes.json")
		fmt.Fprintln(w)
		fmt.Fprintln(w, "Start an HTTP server. A single source uses -route (default: GET /{$}).")
		fmt.Fprintln(w, "A manifest contains a nonempty JSON array of {pattern, source, entry?};")
		fmt.Fprintln(w, "source paths are resolved relative to the manifest file.")
		fmt.Fprintln(w)
		fmt.Fprintln(w, "Examples:")
		fmt.Fprintln(w, "  lipc serve -addr 127.0.0.1:8080 api.lip")
		fmt.Fprintln(w, "  lipc serve -route 'POST /score' -entry Score api.lip")
		fmt.Fprintln(w, "  lipc serve -routes routes.json")
		fmt.Fprintln(w)
		fmt.Fprintln(w, "Options:")
		fmt.Fprintln(w, "  -addr ADDRESS        HTTP listen address (default: 127.0.0.1:8080)")
		fmt.Fprintln(w, "  -route PATTERN       Go ServeMux pattern for one source")
		fmt.Fprintln(w, "  -routes FILE         JSON route manifest; mutually exclusive with -route")
		fmt.Fprintln(w, "  -entry NAME          root Flow; -project DIR resolves imports")
		fmt.Fprintln(w, "  -host demo           enable the teaching Host contracts")
		fmt.Fprintln(w, "  -timeout DURATION    per-request execution timeout")
		fmt.Fprintln(w, "  -concurrent N        maximum requests across routes")
		fmt.Fprintln(w, "  -body-bytes N        maximum request body size")
		fmt.Fprintln(w, "  -response-bytes N    maximum encoded response size")
		fmt.Fprintln(w, "  -parallel/-steps/-elements/-frames  per-request execution budgets")
		fmt.Fprintln(w, "  -trace=false         suppress diagnostic output on stderr")
	case "repl":
		fmt.Fprintln(w, "Usage: lipc repl [options]")
		fmt.Fprintln(w)
		fmt.Fprintln(w, "Evaluate LIP expressions, declarations, and complete Flow cells.")
		fmt.Fprintln(w, "Type :help at the prompt for editor keys, session commands, and :run.")
		fmt.Fprintln(w)
		fmt.Fprintln(w, "Examples:")
		fmt.Fprintln(w, "  lipc repl")
		fmt.Fprintln(w, "  printf '1 + 2\\n:quit\\n' | lipc repl -quiet")
		fmt.Fprintln(w)
		fmt.Fprintln(w, "Options: -host demo, -project DIR, -steps N (0 unlimited), -frames N, -parallel N, -trace=false, -quiet.")
	case "learn":
		fmt.Fprintln(w, "Usage: lipc learn [options]")
		fmt.Fprintln(w)
		fmt.Fprintln(w, "Work through short lessons with one answer blank at a time.")
		fmt.Fprintln(w, "Use :hint, :examples, :retry, and :next at the prompt.")
		fmt.Fprintln(w)
		fmt.Fprintln(w, "Examples:")
		fmt.Fprintln(w, "  lipc learn -list")
		fmt.Fprintln(w, "  lipc learn -lesson hello -progress .lip-progress.json")
		fmt.Fprintln(w)
		fmt.Fprintln(w, "Options: -list, -lesson ID, -progress FILE, and -trace=false.")
	default:
		fmt.Fprintf(w, "unknown help topic %q\n", command)
	}
}

func knownCommand(command string) bool {
	return strings.Contains(" run emit-go build serve repl learn ", " "+command+" ")
}
