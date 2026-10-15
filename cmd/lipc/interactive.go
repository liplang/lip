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
		return "<纯函数>"
	}
	if value.Kind() == script.Effect {
		return "<外部请求，尚未执行>"
	}
	data, err := script.EncodeJSON(value)
	if err != nil {
		return "<包含函数或请求的值>"
	}
	return string(data)
}

func repl(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("repl", flag.ContinueOnError)
	quiet := flags.Bool("quiet", !terminal(os.Stdin) || !terminal(os.Stdout), "hide prompts (automatic for pipes)")
	hostName := flags.String("host", "", "Host: generic Go/Python libraries by default; demo adds teaching contracts")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("repl accepts expressions interactively; use :run file.lip for a program")
	}
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
	session, err := script.NewInteractiveSession(host)
	if err != nil {
		return err
	}
	defer session.Close()
	if !*quiet {
		fmt.Println("LIP 1.0 · 交互计算")
		fmt.Println("输入表达式、绑定或 fn；:help 查看命令，:quit 或 Ctrl-D 退出。")
	}
	input := newInteractiveInput(ctx, func(prefix string) []string {
		return commandCompletions(prefix, session.Complete(prefix), ":help :vars :history :reset :cancel :quit :exit :run")
	})
	var source string
	var history []string
	for ctx.Err() == nil {
		prompt := ""
		if !*quiet {
			if source == "" {
				prompt = fmt.Sprintf("In [%d]: ", len(history)+1)
			} else {
				prompt = "   ...: "
			}
		}
		line, readErr := input.read(ctx, prompt)
		if errors.Is(readErr, errInputCanceled) {
			source = ""
			continue
		}
		if readErr != nil && readErr != io.EOF {
			return readErr
		}
		text := strings.TrimSpace(line)
		if strings.HasPrefix(text, ":") && !script.CellInMultilineString(source) {
			switch {
			case text == ":quit" || text == ":exit":
				return nil
			case text == ":cancel":
				source = ""
			case text == ":help":
				fmt.Println("LIP 1.0：纯 fn 计算值，Flow 组织依赖，Claim 汇聚事实，effect/perform 执行外部动作。")
				fmt.Println("help() 查看交互帮助；help(value)、help(module)、help(module.member) 查看值、Go 导出签名或 Python 库文档。")
				fmt.Println("默认使用通用 Go/Python 桥接；包需由当前 Go module 或 Python 环境提供。")
				fmt.Println("Go 第三方库使用区分大小写的完整包路径；在终端用 go get 配置当前 module 的依赖，as 指定简称。")
				fmt.Println("import go \"strings\"; strings.ToUpper(\"hello\")；import python \"math\"; math.sqrt(3)。")
				fmt.Println("native_data(value) 取得普通数据快照；native_release(value) 释放原生对象引用。")
				fmt.Println(":vars 查看保存的值；:history 查看输入；:reset 清空会话；:cancel 丢弃未完成输入；:quit 退出。")
				fmt.Println(":run [-entry Name] [-input JSON | -entry-input JSON] [-entry-output] [-project DIR] file.lip 执行完整文件与静态模块。")
				fmt.Println("参数按命令行分组，含空格的路径或 JSON 用单/双引号；不执行 shell。默认沿用当前 -host，不读取或改变会话绑定。")
				fmt.Println("具名 fn 支持 |> 管道；Flow、静态 Data 声明和 import lip 使用文件运行。")
				fmt.Println("名字不可重复绑定。打开的 () [] {} 或三引号字符串可继续输入；其他表达式用行末 \\ 续行。")
				fmt.Println("成功输入保存结果，之前的 perform 不会重跑；失败输入不保存绑定，已发生的外部动作不会回滚。")
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
				fmt.Fprintln(os.Stderr, "先完成当前输入，或用 :cancel 丢弃它。")
			case text == ":reset":
				session.Reset()
				history = nil
				fmt.Println("会话已清空。")
			case strings.HasPrefix(text, ":run "):
				args, err := fileRunArgs(strings.TrimSpace(strings.TrimPrefix(text, ":run ")))
				if err == nil {
					err = run(ctx, append([]string{"run", "-host", *hostName, "-parallel", "4"}, args...))
				}
				if err != nil {
					fmt.Fprintln(os.Stderr, err)
				}
			default:
				fmt.Fprintln(os.Stderr, "未知命令；输入 :help 查看命令。")
			}
		} else {
			source += line
			if !script.CellIncomplete(source) || readErr == io.EOF {
				if strings.TrimSpace(source) != "" {
					history = append(history, source)
					result, err := session.Eval(ctx, source, interactiveOptions())
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
				source = ""
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
		task:     "输入字符串表达式，返回 Hello, LIP!。",
		template: "# 字符串使用双引号，Flow 的 return 给出程序结果。\nflow Hello() -> string { return __ANSWER__ }",
		solution: `"Hello, LIP!"`,
		hint:     "字符串写作 \"文字\"。",
		cases:    []lessonCase{{"{}", `"Hello, LIP!"`}},
	},
	{
		id:       "bindings",
		title:    "不可变绑定",
		task:     "已有 price 和 count，计算总价。",
		template: "flow Total(price:number, count:number) -> number {\n # = 给名字绑定一个值，不是反复赋值。\n total = __ANSWER__\n return total\n}",
		solution: "price * count",
		hint:     "乘法用 *；每个绑定只写一次。",
		cases:    []lessonCase{{`{"price":12,"count":3}`, "36"}, {`{"price":5,"count":7}`, "35"}},
	},
	{
		id:       "arithmetic",
		title:    "算术与括号",
		task:     "先把 x 加 2，再乘 3。",
		template: "flow Calculate(x:number) -> number { return __ANSWER__ }",
		solution: "(x + 2) * 3",
		hint:     "括号控制求值顺序。",
		cases:    []lessonCase{{`{"x":4}`, "18"}, {`{"x":-2}`, "0"}},
	},
	{
		id:       "power",
		title:    "幂与负号",
		task:     "对 x 整体求平方。x 为负数时也应得到非负结果。",
		template: "# ** 是幂；-3 ** 2 = -(3 ** 2) = -9。\n# 负数整体的平方写作 (-3) ** 2 = 9。\nflow Square(x:number) -> number { return __ANSWER__ }",
		solution: "x ** 2",
		hint:     "2 ** 3 ** 2 = 2 ** (3 ** 2) = 512；2 ** -3 = 0.125。括号可明确底数边界。",
		cases:    []lessonCase{{`{"x":-3}`, "9"}, {`{"x":2}`, "4"}},
	},
	{
		id:       "logarithm",
		title:    "以右侧为底求对数",
		task:     "返回以 base 为底的 value 的对数。",
		template: "# a */ b：b 是底数；8 */ 2 = 3。\nflow Log(value:number, base:number) -> number { return __ANSWER__ }",
		solution: "value */ base",
		hint:     "*/ 与乘除同级，从左向右结合；value > 0、base > 0 且 base != 1。number 不区分 3 和 3.0，JSON 显示为 3。",
		cases:    []lessonCase{{`{"value":8,"base":2}`, "3"}, {`{"value":0.25,"base":2}`, "-2"}},
	},
	{
		id:       "containers",
		title:    "列表和对象",
		task:     "返回 user 的 name 字段与 scores 的第一个元素。",
		template: "flow Read(user:object, scores:list) -> object { return __ANSWER__ }",
		solution: "{name:user.name, first:scores[0]}",
		hint:     "对象字段用 .name；列表索引从 0 开始。",
		cases:    []lessonCase{{`{"user":{"name":"Ada"},"scores":[0.8,0.9]}`, `{"first":0.8,"name":"Ada"}`}, {`{"user":{"name":"Lin"},"scores":[1]}`, `{"first":1,"name":"Lin"}`}},
	},
	{
		id:       "conditions",
		title:    "条件表达式",
		task:     "score 至少 0.8 时返回 ready，否则返回 retry。",
		template: "flow Decide(score:number) -> string { return __ANSWER__ }",
		solution: `if score >= 0.8 { "ready" } else { "retry" }`,
		hint:     "if 表达式两边都要给出值。",
		cases:    []lessonCase{{`{"score":0.8}`, `"ready"`}, {`{"score":0.7}`, `"retry"`}},
	},
	{
		id:       "functions",
		title:    "纯函数和参数",
		task:     "调用 square 计算 n 的平方。",
		template: "# fn 只计算值；函数体末尾的表达式是结果。\nfn square(x:number) -> number { x * x }\nflow Square(n:number) -> number { return __ANSWER__ }",
		solution: "square(n)",
		hint:     "也可以写 square(x=n)；fn 不能 perform。",
		cases:    []lessonCase{{`{"n":3}`, "9"}, {`{"n":-4}`, "16"}},
	},
	{
		id: "entries", title: "静态数据与入口", entry: "Add", entryInput: true,
		task:     "本课选择 Add 入口。使用静态 OFFSET 计算 n + OFFSET；输入分别使用位置参数和命名参数。",
		template: "OFFSET:number=2\nflow Add(n:number)->number{return __ANSWER__}\nflow Empty()->void{}",
		solution: "n + OFFSET",
		hint:     "多个根 Flow 必须显式选入口，CLI 用 -entry Add。EntryInput 为 {positional:[],named:{}}；参数恰好绑定一次。Add 的 EntryOutput 为 {kind:\"value\",value:结果}，Empty 为 {kind:\"void\"}。",
		cases:    []lessonCase{{`{"positional":[3],"named":{}}`, "5"}, {`{"positional":[],"named":{"n":-2}}`, "0"}},
	},
	{
		id: "flows", title: "子 Flow 与管道", entry: "Main",
		task:     "先调用 Double，再用纯函数 adjust 加 3。用一条管道组合两个阶段。",
		template: "fn adjust(value:number,delta:number)->number{value+delta}\nflow Double(value:number)->number{return value*2}\nflow Main(n:number)->number{return __ANSWER__}",
		solution: "n |> Double |> adjust(delta=3)",
		hint:     "fn 计算值，Flow 组织图。管道左侧只求值一次，整个子图成功后才进入下一阶段。Flow 实参须为纯 Data；阶段为静态具名 fn/Flow/核心函数，普通函数值和原生库函数使用普通调用。",
		cases:    []lessonCase{{`{"n":2}`, "7"}, {`{"n":-3}`, "-3"}},
	},
	{
		id: "modules", title: "静态模块与导出",
		task:     "返回 {total:调整结果,base:基数}；使用固定 pricing 模块的 Adjust 和 BASE。本课只编辑 main.lip。",
		template: "import lip \"./lib/pricing.lip\" as pricing\nflow Price(n:number)->object{return __ANSWER__}",
		solution: "{total:n |> pricing.Adjust,base:pricing.BASE}",
		hint:     "import lip 在构建时冻结依赖，只能读取 export 声明。add 是私有函数；模块使用自己的数据和名字，不捕获调用方的局部值。课程依赖内置，不需要磁盘文件。",
		cases:    []lessonCase{{`{"n":3}`, `{"base":2,"total":5}`}, {`{"n":-2}`, `{"base":2,"total":0}`}},
		sources:  map[string]script.Source{"lib/pricing.lip": {Version: "1", Text: "export BASE:number=2\nfn add(x:number)->number{x+BASE}\nexport flow Adjust(x:number)->number{return add(x)}"}},
	},
	{
		id:       "match",
		title:    "match 分支",
		task:     "n 为 0 返回 zero，为 1 返回 one，其余返回 other。",
		template: "flow Label(n:number) -> string { return __ANSWER__ }",
		solution: `match n { 0 => "zero", 1 => "one", _ => "other" }`,
		hint:     "用 => 连接模式与结果；_ 匹配其余值。",
		cases:    []lessonCase{{`{"n":0}`, `"zero"`}, {`{"n":1}`, `"one"`}, {`{"n":5}`, `"other"`}},
	},
	{
		id:       "filter",
		title:    "列表推导式",
		task:     "保留 xs 中的正数，并计算它们的平方。",
		template: "flow Positive(xs:list) -> list { return __ANSWER__ }",
		solution: "[x * x for x in xs if x > 0]",
		hint:     "[结果 for 名字 in 列表 if 条件]；先检查条件再计算结果。",
		cases:    []lessonCase{{`{"xs":[-1,0,2,3]}`, "[4,9]"}, {`{"xs":[-2]}`, "[]"}},
	},
	{
		id:       "fold",
		title:    "有序归约",
		task:     "使用 fold 求 xs 的和。",
		template: "flow Sum(xs:list) -> number { return __ANSWER__ }",
		solution: "fold(xs, 0, fn(acc:number, x:number) -> number { acc + x })",
		hint:     "fold 的三个参数是列表、初值、二参数纯函数。",
		cases:    []lessonCase{{`{"xs":[1,2,3]}`, "6"}, {`{"xs":[]}`, "0"}},
	},
	{
		id:       "carry",
		title:    "携带值的循环",
		task:     "用 for ... with 逐项累加 xs；每轮通过 next 给出新值。",
		template: "flow Sum(xs:list) -> number {\n total = __ANSWER__\n return total\n}",
		solution: "for x in xs with total = 0 { next total + x }",
		hint:     "循环耗尽时返回最后的携带值；break 值 可提前结束。",
		cases:    []lessonCase{{`{"xs":[2,4,6]}`, "12"}, {`{"xs":[]}`, "0"}},
	},
	{
		id:       "claim",
		title:    "并行与 Claim",
		task:     "用 :edit 输入完整程序：并行计算 xs 的平方，追加到 facts，最后按输入顺序返回平方列表。",
		template: "flow Squares(xs:list) -> list {\n facts = claim()\n # for ... parallel 的每轮可以并行。\n __ANSWER__\n # 完整读取 facts 会等待所有贡献完成。\n return [row.value for row in facts]\n}",
		solution: "for x in xs parallel { facts <+ x * x }",
		hint:     "<+ 追加带身份的事实；value 是提交的值，记录按逻辑位置排列。完整读取等待所有 producer，不按任务完成顺序汇聚。也可以直接输入要填入的 for 语句。",
		cases:    []lessonCase{{`{"xs":[3,1,2]}`, "[9,1,4]"}, {`{"xs":[]}`, "[]"}},
	},
	{
		id:       "effect",
		title:    "外部请求和执行",
		task:     "构造并执行 text.ToUpper(name)，使用已注册的 Go 教学操作。",
		template: "import go \"strings\" as text\nflow Upper(name:string) -> string {\n # effect 构造请求，perform 才调用 Host。\n return __ANSWER__\n}",
		solution: "perform effect text.ToUpper(name)",
		hint:     "import 只声明名字映射；Host 已为本课注册 strings.ToUpper。",
		cases:    []lessonCase{{`{"name":"Ada"}`, `"ADA"`}, {`{"name":"lip"}`, `"LIP"`}},
		demo:     true,
	},
	{
		id:       "react",
		title:    "观察后再决定",
		task:     "用 :edit 输入完整程序：执行 draft，再把它作为参数执行下一次 ToUpper，返回最终字符串。",
		template: "import go \"strings\" as text\nflow Refine(name:string) -> string {\n draft = perform effect text.ToUpper(name)\n # 观察到的结果可以进入下一次请求。\n return __ANSWER__\n}",
		solution: "perform effect text.ToUpper(draft + \"!\")",
		hint:     "这就是调用—观察—再决策的基础；LLM 与工具也走同一条 Host 通道。",
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
	fmt.Println("LIP 1.0 · 交互学习")
	fmt.Println("输入要填入模板的代码；:show 看模板，:hint 看提示，:demo 运行讲解示例，:help 看命令。")
	fmt.Println("完整程序用 :edit 开始输入，:submit 提交。默认不写进度文件。")
	show := func() {
		l := course[index]
		fmt.Printf("\n第 %d/%d 课 · %s (%s)\n%s\n", index+1, len(course), l.title, l.id, l.task)
		ids := make([]string, 0, len(l.sources))
		for id := range l.sources {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			fmt.Printf("\n# %s（固定依赖）\n%s\n", id, l.sources[id].Text)
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
			fmt.Println(":show 模板；:hint 提示；:demo 示例；:edit 完整程序；:submit 验证；:cancel 丢弃草稿。")
			fmt.Println(":next 下一课；:prev 上一课；:lesson ID 跳转；:list 课程；:status 进度；:quit 退出。")
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
			fmt.Println("输入完整 LIP 程序，:submit 提交；:cancel 丢弃。")
		case text == ":submit":
			if editing {
				source, submit = draft, true
			} else {
				fmt.Fprintln(os.Stderr, "先用 :edit 输入完整程序，或直接输入模板的答案。")
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
				fmt.Fprintln(os.Stderr, "先提交草稿，或用 :cancel 丢弃。")
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
				fmt.Fprintln(os.Stderr, "没有这门课；:list 查看课程。")
			} else {
				index = newIndex
				show()
				if err := save(); err != nil {
					return err
				}
			}
		case strings.HasPrefix(text, ":"):
			fmt.Fprintln(os.Stderr, "未知命令；:help 查看命令。")
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
				fmt.Println("通过。:next 进入下一课；:show 可以复习。")
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
			return fmt.Errorf("用例 %d: %w", i+1, err)
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
			return fmt.Errorf("用例 %d 输入 %s：得到 %s，预期 %s", i+1, test.input, showValue(report.Output), test.want)
		}
		fmt.Printf("用例 %d ✓ %s\n", i+1, showValue(report.Output))
	}
	return nil
}
