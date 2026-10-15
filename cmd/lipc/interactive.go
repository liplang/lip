package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"lip/script"
	"os"
	"path/filepath"
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
	data, err := script.EncodeJSON(value)
	if err != nil {
		return "<value containing functions or requests>"
	}
	return string(data)
}

func repl(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("repl", flag.ContinueOnError)
	quiet := flags.Bool("quiet", !terminal(os.Stdin) || !terminal(os.Stdout), "hide prompts (automatic for pipes)")
	hostName := flags.String("host", "", "Host: generic Go/Python libraries by default; demo adds teaching contracts")
	project := flags.String("project", "", "project root for static LIP imports (default: current directory)")
	frames := flags.Int("frames", script.DefaultMaxFrames, "non-tail call frame budget per cell and :run (0 uses default)")
	parallel := flags.Int("parallel", 4, "maximum active leaf evaluations per cell and :run (0 uses one)")
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
		fmt.Println("LIP 1.0 · Interactive evaluation")
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
			result, err := session.Eval(ctx, source, options)
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
				fmt.Println("LIP 1.0: pure fn compute values, Flows organize dependencies, Claims collect facts, effect/perform run external actions.")
				fmt.Println("help() shows interactive help; help(value), help(module), help(module.member) describe values, Go exports, or Python libraries.")
				fmt.Println("The generic Go/Python bridge is enabled by default; packages must exist in the current Go module or Python environment.")
				fmt.Println("Use full, case-sensitive Go package paths; configure dependencies with go get in your module, and choose an alias with as.")
				fmt.Println("import go \"strings\"; strings.ToUpper(\"hello\"); import python \"math\"; math.sqrt(3).")
				fmt.Println("The REPL displays return values: fmt.Print/Printf/Println return a byte count, which is displayed too.")
				fmt.Println("native_data(value) takes a Data snapshot; native_release(value) releases a native reference.")
				fmt.Println(":vars shows saved values; :history shows input; :del name[,name...] unbinds names for reuse; :reset clears the session; :cancel discards a draft; :quit exits.")
				fmt.Println(":run [-entry Name] [-input JSON | -entry-input JSON] [-entry-output] [-project DIR] [-frames N] file.lip runs a complete file with static modules.")
				fmt.Println("Quote paths containing spaces or JSON with single/double quotes; no shell runs. :run inherits -host and uses independent file bindings.")
				fmt.Println("fn/Flow declarations, static Data, exports, import lip, Claims, and script statements are supported. Call a declared Flow by name or through |>.")
				fmt.Println("LIP imports resolve under -project DIR (default: current directory) and stay frozen until :reset.")
				fmt.Println(":begin / :end submits a whole script or several declarations as one cell, including forward references and Claim joins.")
				fmt.Println("An if statement without else waits for the next line; enter else, a blank line, or another statement.")
				fmt.Println("Names stay bound until :del removes them. Open () [] {} or triple-quoted strings allow more input; other expressions use a trailing \\ to continue.")
				fmt.Println("Successful cells retain results without rerunning past actions; failed cells commit no bindings, and completed external actions remain.")
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
					err = run(ctx, append([]string{"run", "-host", *hostName, "-parallel", strconv.Itoa(*parallel), "-frames", strconv.Itoa(*frames)}, args...))
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

// Lessons use ordinary LIP source and the same Compile/Run contract as files.
// They are bundled into lipc, so learn works outside a source checkout.
type lesson struct {
	id, title, task, template, solution, hint string
	cases                                     []lessonCase
	demo                                      bool
	entry                                     string
	entryInput                                bool
	sources                                   map[string]script.Source
}
type lessonCase struct{ input, want string }

const answerMark = "__ANSWER__"

var course = []lesson{
	{
		id:       "hello",
		title:    "Hello World",
		task:     "Enter a string expression that returns Hello, LIP!.",
		template: "# Strings use double quotes; the Flow return supplies the program result.\nflow Hello() -> string { return __ANSWER__ }",
		solution: `"Hello, LIP!"`,
		hint:     "Write a string as \"text\".",
		cases:    []lessonCase{{"{}", `"Hello, LIP!"`}},
	},
	{
		id:       "bindings",
		title:    "Immutable bindings",
		task:     "Calculate the total from price and count.",
		template: "flow Total(price:number, count:number) -> number {\n # = binds a name to one value; it does not update a variable.\n total = __ANSWER__\n return total\n}",
		solution: "price * count",
		hint:     "Use * for multiplication; each binding is written once.",
		cases:    []lessonCase{{`{"price":12,"count":3}`, "36"}, {`{"price":5,"count":7}`, "35"}},
	},
	{
		id:       "arithmetic",
		title:    "Arithmetic and parentheses",
		task:     "Add 2 to x, then multiply by 3.",
		template: "flow Calculate(x:number) -> number { return __ANSWER__ }",
		solution: "(x + 2) * 3",
		hint:     "Parentheses control evaluation order.",
		cases:    []lessonCase{{`{"x":4}`, "18"}, {`{"x":-2}`, "0"}},
	},
	{
		id:       "power",
		title:    "Powers and negation",
		task:     "Square the whole value of x, including negative inputs.",
		template: "# ** is exponentiation; -3 ** 2 = -(3 ** 2) = -9.\n# Square a negative base with (-3) ** 2 = 9.\nflow Square(x:number) -> number { return __ANSWER__ }",
		solution: "x ** 2",
		hint:     "2 ** 3 ** 2 = 2 ** (3 ** 2) = 512; 2 ** -3 = 0.125. Use parentheses to make the base explicit.",
		cases:    []lessonCase{{`{"x":-3}`, "9"}, {`{"x":2}`, "4"}},
	},
	{
		id:       "logarithm",
		title:    "Logarithms with the base on the right",
		task:     "Return the logarithm of value to base.",
		template: "# a */ b: b is the base; 8 */ 2 = 3.\nflow Log(value:number, base:number) -> number { return __ANSWER__ }",
		solution: "value */ base",
		hint:     "*/ has multiplication precedence and associates left; value > 0, base > 0, base != 1. Numbers 3 and 3.0 are identical; JSON displays 3.",
		cases:    []lessonCase{{`{"value":8,"base":2}`, "3"}, {`{"value":0.25,"base":2}`, "-2"}},
	},
	{
		id:       "containers",
		title:    "Lists and objects",
		task:     "Return user.name and the first element of scores.",
		template: "flow Read(user:object, scores:list) -> object { return __ANSWER__ }",
		solution: "{name:user.name, first:scores[0]}",
		hint:     "Use .name for object fields; list indices start at 0.",
		cases:    []lessonCase{{`{"user":{"name":"Ada"},"scores":[0.8,0.9]}`, `{"first":0.8,"name":"Ada"}`}, {`{"user":{"name":"Lin"},"scores":[1]}`, `{"first":1,"name":"Lin"}`}},
	},
	{
		id:       "slicing",
		title:    "Index lists and Python-style slices",
		task:     "Return {picked, reverse, stride, text}: select xs at indices, reverse xs, take xs from index 1 in steps of 2, and reverse label.",
		template: "flow Select(xs:list, indices:list, label:string) -> object { return __ANSWER__ }",
		solution: "{picked:xs[indices], reverse:xs[::-1], stride:xs[1::2], text:label[::-1]}",
		hint:     "a[-1] is the last element. a[[2,0,-1]] preserves index order and duplicates. a[start:stop:step] excludes stop and clips bounds; omitted bounds depend on step. Strings count Unicode scalars. slice(a,null,null,-1) also reverses.",
		cases:    []lessonCase{{`{"xs":[0,1,2,3,4],"indices":[-1,0,-1],"label":"a𐀀b"}`, `{"picked":[4,0,4],"reverse":[4,3,2,1,0],"stride":[1,3],"text":"b𐀀a"}`}, {`{"xs":[],"indices":[],"label":""}`, `{"picked":[],"reverse":[],"stride":[],"text":""}`}},
	},
	{
		id:       "conditions",
		title:    "Conditional expressions",
		task:     "Return ready when score is at least 0.8, otherwise retry.",
		template: "flow Decide(score:number) -> string { return __ANSWER__ }",
		solution: `if score >= 0.8 { "ready" } else { "retry" }`,
		hint:     "Both branches of an if expression must produce values.",
		cases:    []lessonCase{{`{"score":0.8}`, `"ready"`}, {`{"score":0.7}`, `"retry"`}},
	},
	{
		id:       "functions",
		title:    "Pure functions and parameters",
		task:     "Call square to compute the square of n.",
		template: "# fn only computes values; its final expression is the result.\nfn square(x:number) -> number { x * x }\nflow Square(n:number) -> number { return __ANSWER__ }",
		solution: "square(n)",
		hint:     "square(x=n) also works; fn cannot perform.",
		cases:    []lessonCase{{`{"n":3}`, "9"}, {`{"n":-4}`, "16"}},
	},
	{
		id: "entries", title: "Static data and entries", entry: "Add", entryInput: true,
		task:     "This lesson selects Add. Compute n + OFFSET using static data; inputs exercise positional and named arguments.",
		template: "OFFSET:number=2\nflow Add(n:number)->number{return __ANSWER__}\nflow Empty()->void{}",
		solution: "n + OFFSET",
		hint:     "Multiple root Flows require an explicit entry: -entry Add. EntryInput is {positional:[],named:{}}; bind each parameter exactly once. Add returns EntryOutput {kind:\"value\",value:result}; Empty returns {kind:\"void\"}.",
		cases:    []lessonCase{{`{"positional":[3],"named":{}}`, "5"}, {`{"positional":[],"named":{"n":-2}}`, "0"}},
	},
	{
		id: "flows", title: "Child Flows and pipelines", entry: "Main",
		task:     "Call Double, then use pure adjust to add 3. Combine both stages in one pipeline.",
		template: "fn adjust(value:number,delta:number)->number{value+delta}\nflow Double(value:number)->number{return value*2}\nflow Main(n:number)->number{return __ANSWER__}",
		solution: "n |> Double |> adjust(delta=3)",
		hint:     "fn compute values; Flows organize graphs. Evaluate the left side once and finish the child graph before the next stage. Flow arguments must be Pure Data. Stages are static named fn/Flows/core functions; use ordinary calls for function values and native functions.",
		cases:    []lessonCase{{`{"n":2}`, "7"}, {`{"n":-3}`, "-3"}},
	},
	{
		id: "modules", title: "Static modules and exports",
		task:     "Return {total:adjusted,base:base} using Adjust and BASE from the fixed pricing module. Edit only main.lip.",
		template: "import lip \"./lib/pricing.lip\" as pricing\nflow Price(n:number)->object{return __ANSWER__}",
		solution: "{total:n |> pricing.Adjust,base:pricing.BASE}",
		hint:     "import lip freezes dependencies at build time; only export declarations are public. add is private. Modules use their own data and names without capturing caller locals. Lesson dependencies are bundled; no disk files are needed.",
		cases:    []lessonCase{{`{"n":3}`, `{"base":2,"total":5}`}, {`{"n":-2}`, `{"base":2,"total":0}`}},
		sources:  map[string]script.Source{"lib/pricing.lip": {Version: "1", Text: "export BASE:number=2\nfn add(x:number)->number{x+BASE}\nexport flow Adjust(x:number)->number{return add(x)}"}},
	},
	{
		id:       "match",
		title:    "match branches",
		task:     "Return zero for 0, one for 1, and other otherwise.",
		template: "flow Label(n:number) -> string { return __ANSWER__ }",
		solution: `match n { 0 => "zero", 1 => "one", _ => "other" }`,
		hint:     "Use => between patterns and results; _ matches any remaining value.",
		cases:    []lessonCase{{`{"n":0}`, `"zero"`}, {`{"n":1}`, `"one"`}, {`{"n":5}`, `"other"`}},
	},
	{
		id:       "filter",
		title:    "List comprehensions",
		task:     "Keep positive numbers in xs and square them.",
		template: "flow Positive(xs:list) -> list { return __ANSWER__ }",
		solution: "[x * x for x in xs if x > 0]",
		hint:     "[result for name in list if condition]; check the condition before computing the result.",
		cases:    []lessonCase{{`{"xs":[-1,0,2,3]}`, "[4,9]"}, {`{"xs":[-2]}`, "[]"}},
	},
	{
		id:       "fold",
		title:    "Ordered reduction",
		task:     "Use fold to sum xs.",
		template: "flow Sum(xs:list) -> number { return __ANSWER__ }",
		solution: "fold(xs, 0, fn(acc:number, x:number) -> number { acc + x })",
		hint:     "fold takes a list, an initial value, and a pure function with two parameters.",
		cases:    []lessonCase{{`{"xs":[1,2,3]}`, "6"}, {`{"xs":[]}`, "0"}},
	},
	{
		id:       "carry",
		title:    "Loops with a carried value",
		task:     "Use for ... with to sum xs; next supplies the carried value for the next iteration.",
		template: "flow Sum(xs:list) -> number {\n total = __ANSWER__\n return total\n}",
		solution: "for x in xs with total = 0 { next total + x }",
		hint:     "Exhaustion returns the last carried value; break value ends the loop early.",
		cases:    []lessonCase{{`{"xs":[2,4,6]}`, "12"}, {`{"xs":[]}`, "0"}},
	},
	{
		id:       "claim",
		title:    "Parallel execution and Claims",
		task:     "Use :edit for a complete program: square xs in parallel, append to facts, and return squares in input order.",
		template: "flow Squares(xs:list) -> list {\n facts = claim()\n # Iterations of for ... parallel may overlap.\n __ANSWER__\n # A complete read of facts waits for all contributions.\n return [row.value for row in facts]\n}",
		solution: "for x in xs parallel { facts <+ x * x }",
		hint:     "<+ appends facts with identities; value holds the contribution. Records follow logical positions. Complete reads wait for all producers. You can also enter the for statement directly.",
		cases:    []lessonCase{{`{"xs":[3,1,2]}`, "[9,1,4]"}, {`{"xs":[]}`, "[]"}},
	},
	{
		id:       "effect",
		title:    "External requests and execution",
		task:     "Construct and perform text.ToUpper(name), a registered Go teaching operation.",
		template: "import go \"strings\" as text\nflow Upper(name:string) -> string {\n # effect constructs a request; perform calls the Host.\n return __ANSWER__\n}",
		solution: "perform effect text.ToUpper(name)",
		hint:     "import declares a namespace; the lesson Host registers strings.ToUpper.",
		cases:    []lessonCase{{`{"name":"Ada"}`, `"ADA"`}, {`{"name":"lip"}`, `"LIP"`}},
		demo:     true,
	},
	{
		id:       "react",
		title:    "Observe, then decide",
		task:     "Use :edit for a complete program: perform draft, pass it to the next ToUpper request, and return the final string.",
		template: "import go \"strings\" as text\nflow Refine(name:string) -> string {\n draft = perform effect text.ToUpper(name)\n # An observed result can feed the next request.\n return __ANSWER__\n}",
		solution: "perform effect text.ToUpper(draft + \"!\")",
		hint:     "This is the basis of call, observe, and decide again; LLMs and tools use the same Host channel.",
		cases:    []lessonCase{{`{"name":"Ada"}`, `"ADA!"`}, {`{"name":"lip"}`, `"LIP!"`}},
		demo:     true,
	},
}

type progress struct {
	Version   int             `json:"version"`
	Current   string          `json:"current"`
	Completed map[string]bool `json:"completed"`
}

func lessonIndex(id string) int {
	if number, err := strconv.Atoi(id); err == nil && number > 0 && number <= len(course) {
		return number - 1
	}
	for i, lesson := range course {
		if lesson.id == id {
			return i
		}
	}
	return -1
}

func learn(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("learn", flag.ContinueOnError)
	list := flags.Bool("list", false, "list lessons")
	start := flags.String("lesson", "", "lesson ID or number")
	path := flags.String("progress", "", "optional progress JSON (default: do not write files)")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("use learn -lesson ID; learn accepts code interactively")
	}
	state := progress{Version: 1, Completed: map[string]bool{}}
	if *path != "" {
		data, err := os.ReadFile(*path)
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		if err == nil {
			if err := json.Unmarshal(data, &state); err != nil {
				return fmt.Errorf("invalid learning progress: %w", err)
			}
			if state.Version != 1 {
				return fmt.Errorf("unsupported learning progress version")
			}
			if state.Completed == nil {
				state.Completed = map[string]bool{}
			}
		}
	}
	index := 0
	selection := *start
	if selection == "" {
		selection = state.Current
	}
	if selection != "" {
		index = lessonIndex(selection)
		if index < 0 {
			return fmt.Errorf("unknown lesson %q; use -list", selection)
		}
	}
	if *list {
		for i, lesson := range course {
			fmt.Printf("%2d  %-12s %s\n", i+1, lesson.id, lesson.title)
		}
		return nil
	}
	fmt.Println("LIP 1.0 · Interactive learning")
	fmt.Println("Enter code to fill the template; :show shows it, :hint gives a hint, :demo runs an example, :help lists commands.")
	fmt.Println("Use :edit for a complete program, then :submit. Progress is not written to disk by default.")
	show := func() {
		l := course[index]
		fmt.Printf("\nLesson %d/%d · %s (%s)\n%s\n", index+1, len(course), l.title, l.id, l.task)
		ids := make([]string, 0, len(l.sources))
		for id := range l.sources {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			fmt.Printf("\n# %s (fixed dependency)\n%s\n", id, l.sources[id].Text)
		}
		fmt.Printf("\n# main.lip\n%s\n", l.template)
	}
	save := func() error {
		if *path == "" {
			return nil
		}
		state.Current = course[index].id
		data, err := json.MarshalIndent(state, "", "  ")
		if err != nil {
			return err
		}
		dir := filepath.Dir(*path)
		file, err := os.CreateTemp(dir, ".lip-learn-*")
		if err != nil {
			return err
		}
		name := file.Name()
		defer os.Remove(name)
		if _, err = file.Write(append(data, '\n')); err != nil {
			file.Close()
			return err
		}
		if err = file.Close(); err != nil {
			return err
		}
		return os.Rename(name, *path)
	}
	show()
	completionSession, _ := script.NewSession(script.Host{})
	defer completionSession.Close()
	input := newInteractiveInput(ctx, func(prefix string) []string {
		return commandCompletions(prefix, completionSession.Complete(prefix), ":show :hint :demo :edit :submit :cancel :next :prev :lesson :list :status :help :quit :exit")
	})
	var draft string
	editing := false
	for ctx.Err() == nil {
		prompt := ""
		if terminal(os.Stdin) && terminal(os.Stdout) {
			prompt = "learn> "
		}
		line, readErr := input.read(ctx, prompt)
		if errors.Is(readErr, errInputCanceled) {
			draft, editing = "", false
			continue
		}
		if readErr != nil && readErr != io.EOF {
			return readErr
		}
		text := strings.TrimSpace(line)
		lesson := course[index]
		var source string
		submit, demo := false, false
		switch {
		case script.CellInMultilineString(draft):
			draft += line
			if !editing && (!script.CellIncomplete(draft) || readErr == io.EOF) {
				source = strings.ReplaceAll(lesson.template, answerMark, strings.TrimSpace(draft))
				submit = true
			}
		case text == ":quit" || text == ":exit":
			return save()
		case text == ":help":
			fmt.Println(":show template; :hint hint; :demo example; :edit complete program; :submit validate; :cancel discard draft.")
			fmt.Println(":next next lesson; :prev previous; :lesson ID jump; :list lessons; :status progress; :quit exit.")
			fmt.Println(editorHelp)
		case text == ":show":
			show()
		case text == ":hint":
			fmt.Println(lesson.hint)
		case text == ":demo":
			source = strings.ReplaceAll(lesson.template, answerMark, lesson.solution)
			fmt.Println(source)
			fmt.Println(lesson.hint)
			submit, demo = true, true
		case text == ":cancel":
			draft, editing = "", false
		case text == ":edit":
			draft, editing = "", true
			fmt.Println("Enter a complete LIP program; :submit to validate, :cancel to discard.")
		case text == ":submit":
			if editing {
				source, submit = draft, true
			} else {
				fmt.Fprintln(os.Stderr, "Use :edit for a complete program, or enter the template answer directly.")
			}
		case editing && !strings.HasPrefix(text, ":"):
			draft += line
		case text == ":status" || text == ":list":
			for i, l := range course {
				mark := " "
				if state.Completed[l.id] {
					mark = "✓"
				}
				fmt.Printf("%s %2d %-12s %s\n", mark, i+1, l.id, l.title)
			}
		case text == ":next" || text == ":prev" || strings.HasPrefix(text, ":lesson "):
			if editing || draft != "" {
				fmt.Fprintln(os.Stderr, "Submit the draft, or discard it with :cancel.")
				break
			}
			newIndex := index
			if text == ":next" {
				newIndex++
			} else if text == ":prev" {
				newIndex--
			} else {
				newIndex = lessonIndex(strings.TrimSpace(strings.TrimPrefix(text, ":lesson ")))
			}
			if newIndex < 0 || newIndex >= len(course) {
				fmt.Fprintln(os.Stderr, "Unknown lesson; :list shows available lessons.")
			} else {
				index = newIndex
				show()
				if err := save(); err != nil {
					return err
				}
			}
		case strings.HasPrefix(text, ":"):
			fmt.Fprintln(os.Stderr, "Unknown command; :help lists commands.")
		case text != "":
			draft += line
			if !script.CellIncomplete(draft) || readErr == io.EOF {
				source = strings.ReplaceAll(lesson.template, answerMark, strings.TrimSpace(draft))
				submit = true
			}
		}
		if submit {
			if err := checkLesson(ctx, lesson, source); err != nil {
				fmt.Fprintln(os.Stderr, err)
			} else if !demo {
				state.Completed[lesson.id] = true
				fmt.Println("Passed. :next opens the next lesson; :show reviews this one.")
				if err := save(); err != nil {
					return err
				}
			}
			if !demo {
				draft, editing = "", false
			}
		}
		if readErr == io.EOF {
			return save()
		}
	}
	return ctx.Err()
}

func checkLesson(ctx context.Context, lesson lesson, source string) error {
	host := script.Host{}
	if lesson.demo {
		var err error
		host, err = script.DemoHost()
		if err != nil {
			return err
		}
	}
	defer host.Close()
	sources := map[string]script.Source{"main.lip": {Version: "local", Text: source}}
	for id, dependency := range lesson.sources {
		sources[id] = dependency
	}
	program, err := script.CompileSourceSet(script.SourceSet{Root: "main.lip", Sources: sources}, host, script.CompileOptions{Entry: lesson.entry})
	if err != nil {
		return err
	}
	for i, test := range lesson.cases {
		value, err := script.DecodeData([]byte(test.input))
		if err != nil {
			return err
		}
		var report script.Report
		if lesson.entryInput {
			report, err = program.RunEntry(ctx, value, interactiveOptions())
		} else {
			inputs, _ := value.Fields()
			report, err = program.Run(ctx, inputs, interactiveOptions())
		}
		if err != nil {
			return fmt.Errorf("Case %d: %w", i+1, err)
		}
		want, err := script.DecodeData([]byte(test.want))
		if err != nil {
			return err
		}
		equal, err := script.Equal(report.Output, want)
		if err != nil {
			return err
		}
		if !equal {
			return fmt.Errorf("Case %d input %s: got %s, expected %s", i+1, test.input, showValue(report.Output), test.want)
		}
		fmt.Printf("Case %d ✓ %s\n", i+1, showValue(report.Output))
	}
	return nil
}
