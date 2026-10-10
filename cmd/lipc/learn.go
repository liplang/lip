package main

import (
	"bufio"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"lipalpha/compiler"
	"lipalpha/compiler/lexer"
	"lipalpha/compiler/parser"
	"lipalpha/compiler/token"
	"lipalpha/internal/listops"
	"lipalpha/internal/stringops"
	"lipalpha/runtime"
)

//go:embed learn/lessons.json
var learnCourseJSON []byte

const learnPlaceholder = "__ANSWER__"
const learnSourceLimit = 1 << 20

type learnLesson struct {
	ID, Title, Stage, Task, Template, Solution, Why, Example, Mode string
	Topics, Explanation, Hints, Required, References               []string
	Cases, Demo                                                    []learnCase
}

type learnCase struct {
	Name   string
	Inputs map[string]runtime.Value
	State  map[string]runtime.Value
	Want   json.RawMessage
	Error  string
	Stdout string
}

type learnProgress struct {
	Schema    string          `json:"schema"`
	Current   string          `json:"current"`
	Completed map[string]bool `json:"completed"`
}

type learnRunner func(string, string, []learnCase) ([]learnResult, error)

type learnSession struct {
	lessons      []learnLesson
	index        int
	progress     learnProgress
	progressPath string
	output       io.Writer
	errors       io.Writer
	runner       learnRunner
	draft        string
	editing      bool
	hint         int
	lastSource   string
	lastResults  []learnResult
}

func learn(args []string) int {
	fs := commandFlags("learn")
	list := fs.Bool("list", false, "list the bundled lessons")
	start := fs.String("lesson", "", "start at a lesson ID or number")
	progressPath := fs.String("progress", ".lip-learn-progress.json", "learning progress file")
	noProgress := fs.Bool("no-progress", false, "do not read or write progress")
	if err := parseFlags(fs, args); err != nil {
		return flagFailure(fs, err)
	}
	if fs.NArg() != 0 {
		return usageError("learn", "learn does not accept positional arguments; use --lesson")
	}
	lessons, err := loadLearnLessons()
	if err != nil {
		return fail(err)
	}
	if *noProgress {
		*progressPath = ""
	} else if *progressPath == "" {
		return usageError("learn", "--progress requires a path; use --no-progress to disable saving")
	}
	progress, err := readLearnProgress(*progressPath)
	if err != nil {
		return fail(err)
	}
	s := &learnSession{lessons: lessons, progress: progress, progressPath: *progressPath,
		output: os.Stdout, errors: os.Stderr, runner: executeLearnSource}
	selection := *start
	if selection == "" {
		selection = progress.Current
	}
	if selection != "" {
		s.index = learnLessonIndex(lessons, selection)
		if s.index < 0 {
			return usageError("learn", fmt.Sprintf("unknown lesson %q; use --list", selection))
		}
	}
	// Resume at the next unfinished lesson, while an explicit --lesson always
	// opens exactly the requested lesson for review.
	if *start == "" && s.progress.Completed[s.lessons[s.index].ID] {
		for offset := 1; offset <= len(s.lessons); offset++ {
			index := (s.index + offset) % len(s.lessons)
			if !s.progress.Completed[s.lessons[index].ID] {
				s.index = index
				break
			}
		}
	}
	if *list {
		s.list()
		return 0
	}
	fmt.Fprintf(s.output, "LIP %s · 交互学习\n", version)
	fmt.Fprintln(s.output, "输入代码后回车验证；:demo 运行示例，:hint 查看提示，:help 查看命令。")
	fmt.Fprintln(s.output, "完整多行程序：:edit → 输入或粘贴代码 → :submit。:quit 或 Ctrl-D 退出。")
	if s.progressPath == "" {
		fmt.Fprintln(s.output, "本次不保存进度。")
	} else {
		fmt.Fprintf(s.output, "进度文件：%s（再次运行会接着学习）\n", s.progressPath)
	}
	return s.run(os.Stdin)
}

func loadLearnLessons() ([]learnLesson, error) {
	var lessons []learnLesson
	if err := json.Unmarshal(learnCourseJSON, &lessons); err != nil {
		return nil, fmt.Errorf("load bundled LIP lessons: %w", err)
	}
	seen := map[string]bool{}
	for _, lesson := range lessons {
		if lesson.ID == "" || seen[lesson.ID] || strings.Count(lesson.Template, learnPlaceholder) != 1 ||
			lesson.Solution == "" || len(lesson.Cases) == 0 || len(lesson.Demo) == 0 || len(lesson.Hints) == 0 {
			return nil, fmt.Errorf("invalid bundled lesson %q", lesson.ID)
		}
		seen[lesson.ID] = true
	}
	if len(lessons) == 0 {
		return nil, fmt.Errorf("no bundled LIP lessons")
	}
	return lessons, nil
}

func readLearnProgress(path string) (learnProgress, error) {
	progress := learnProgress{Schema: "lip.learn.v1", Completed: map[string]bool{}}
	if path == "" {
		return progress, nil
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return progress, nil
	}
	if err == nil {
		err = json.Unmarshal(data, &progress)
		if err == nil && (progress.Schema != "lip.learn.v1" || progress.Completed == nil) {
			err = fmt.Errorf("expected lip.learn.v1 progress")
		}
	}
	if err != nil {
		return progress, fmt.Errorf("read progress %q: %w; use --no-progress or --progress with another path", path, err)
	}
	return progress, nil
}

func (s *learnSession) saveProgress() error {
	s.progress.Current = s.lessons[s.index].ID
	if s.progressPath == "" {
		return nil
	}
	data, err := json.MarshalIndent(s.progress, "", "  ")
	if err != nil {
		return err
	}
	directory := filepath.Dir(s.progressPath)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return err
	}
	file, err := os.CreateTemp(directory, ".lip-learn-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	_, writeErr := file.Write(append(data, '\n'))
	closeErr := file.Close()
	if err := errors.Join(writeErr, closeErr); err != nil {
		return err
	}
	return os.Rename(file.Name(), s.progressPath)
}

func learnLessonIndex(lessons []learnLesson, selection string) int {
	if number, err := strconv.Atoi(selection); err == nil && number > 0 && number <= len(lessons) {
		return number - 1
	}
	for i, lesson := range lessons {
		if lesson.ID == selection {
			return i
		}
	}
	return -1
}

func (s *learnSession) run(input io.Reader) int {
	reader := bufio.NewReader(input)
	var editor *lineEditor
	if file, ok := input.(*os.File); ok && terminalInput(file) {
		if target, ok := s.output.(*os.File); ok && terminalInput(target) {
			editor = &lineEditor{input: reader, output: s.output, file: file, complete: s.completions}
		}
	}
	s.show()
	for {
		prompt := "答题 > "
		if s.draft != "" || s.editing {
			prompt = "续行 > "
		}
		var line string
		var err error
		if editor != nil {
			line, err = editor.readLine(prompt)
			if strings.TrimSpace(line) != "" {
				editor.history = append(editor.history, strings.TrimRight(line, "\r\n"))
			}
		} else {
			fmt.Fprint(s.output, prompt)
			line, err = reader.ReadString('\n')
		}
		if err == errInputCanceled {
			s.draft, s.editing = "", false
			continue
		}
		if err != nil && err != io.EOF {
			fmt.Fprintln(s.errors, "lipc learn:", err)
			return 1
		}
		finished := false
		if strings.HasPrefix(strings.TrimSpace(line), ":") {
			finished = s.command(strings.TrimSpace(line))
		} else if strings.TrimSpace(line) != "" || s.draft != "" {
			s.draft += line
			if len(s.draft) > learnSourceLimit {
				fmt.Fprintln(s.errors, "代码超过 1 MiB，请缩小练习。")
				s.draft, s.editing = "", false
			} else if !s.editing && !incompleteLearnAnswer(s.draft) {
				answer := s.draft
				s.draft = ""
				finished = s.attempt(fillLearnTemplate(s.lessons[s.index].Template, answer))
			}
		}
		if finished || err == io.EOF {
			if s.draft != "" {
				fmt.Fprintln(s.output, "未提交的代码已取消；使用 :edit 和 :submit 提交多行程序。")
			}
			if err := s.saveProgress(); err != nil {
				fmt.Fprintln(s.errors, "保存进度失败：", err)
				return 1
			}
			fmt.Fprintf(s.output, "已通过 %d/%d 课。\n", s.completedCount(), len(s.lessons))
			if s.progressPath != "" {
				fmt.Fprintf(s.output, "进度已保存到 %s。\n", s.progressPath)
			}
			return 0
		}
	}
}

func incompleteLearnAnswer(source string) bool {
	tokens, err := lexer.New(source).Lex()
	if err != nil {
		return false
	}
	var stack []token.Kind
	for _, t := range tokens {
		switch t.Kind {
		case token.LParen, token.LBracket, token.LBrace:
			stack = append(stack, t.Kind)
		case token.RParen, token.RBracket, token.RBrace:
			opening := map[token.Kind]token.Kind{token.RParen: token.LParen, token.RBracket: token.LBracket, token.RBrace: token.LBrace}[t.Kind]
			if len(stack) == 0 || stack[len(stack)-1] != opening {
				return false
			}
			stack = stack[:len(stack)-1]
		}
	}
	return len(stack) > 0 || incompleteCell(source)
}

func fillLearnTemplate(template, answer string) string {
	// A trailing line comment must not consume the scaffold's closing brace,
	// including when piped input ends without a final newline.
	return strings.Replace(template, learnPlaceholder, strings.TrimRight(answer, "\r\n")+"\n", 1)
}

func (s *learnSession) show() {
	l := s.lessons[s.index]
	fmt.Fprintf(s.output, "\n── %02d/%02d · %s / %s ──\n", s.index+1, len(s.lessons), l.Stage, l.Title)
	fmt.Fprintf(s.output, "学习点：%s\n\n", strings.Join(l.Topics, "、"))
	for _, paragraph := range l.Explanation {
		fmt.Fprintln(s.output, paragraph)
	}
	fmt.Fprintln(s.output, "\n示例（:demo 实际运行）：")
	printLearnSource(s.output, l.Example)
	fmt.Fprintln(s.output, "\n练习：", l.Task)
	fmt.Fprintf(s.output, "把 %s 替换为你的代码；输入替换部分即可。\n", learnPlaceholder)
	printLearnSource(s.output, l.Template)
	if len(l.Required) > 0 {
		fmt.Fprintln(s.output, "本课请使用：", strings.Join(l.Required, "、"))
	}
	fmt.Fprintf(s.output, "验证包含 %d 组输入；:tests 查看，:hint 获得逐步提示。\n", len(l.Cases))
	if len(l.References) > 0 {
		fmt.Fprintln(s.output, "延伸阅读：", strings.Join(l.References, " · "))
	}
}

func printLearnSource(output io.Writer, source string) {
	for i, line := range strings.Split(strings.TrimSpace(source), "\n") {
		fmt.Fprintf(output, "  %2d │ %s\n", i+1, line)
	}
}

func (s *learnSession) list() {
	for i, lesson := range s.lessons {
		status := "待学"
		if s.progress.Completed[lesson.ID] {
			status = "通过"
		}
		fmt.Fprintf(s.output, "%02d [%s] %-18s %s / %s\n", i+1, status, lesson.ID, lesson.Stage, lesson.Title)
	}
}

func (s *learnSession) completedCount() int {
	count := 0
	for _, lesson := range s.lessons {
		if s.progress.Completed[lesson.ID] {
			count++
		}
	}
	return count
}

func (s *learnSession) move(index int) {
	s.index, s.hint = index, 0
	s.draft, s.lastSource, s.editing, s.lastResults = "", "", false, nil
	s.show()
}

func (s *learnSession) command(line string) bool {
	command, argument, _ := strings.Cut(line, " ")
	argument = strings.TrimSpace(argument)
	l := s.lessons[s.index]
	switch command {
	case ":quit", ":exit":
		return true
	case ":help":
		fmt.Fprintln(s.output, `:lesson             重读本课
:demo               编译并运行示例
:tests              查看验证输入和预期结果
:hint               逐步提示
:solution           查看参考答案和解释（不会自动记为通过）
:edit / :submit     开始 / 提交多行代码
:cancel             取消当前输入
:run                重新验证上一次提交
:save 路径          保存上次提交或练习骨架为 .lip 文件（不覆盖已有文件）
:load 路径          从 .lip 文件读取完整程序并验证
:graph              查看上次提交的依赖图（尚未提交时查看示例）
:trace              查看上次运行的节点轨迹
:reference [主题]   速查 keywords / operators / builtin / list / string
:lessons            查看课程和进度
:goto 编号或ID      跳转课程
:next / :prev       下一课 / 上一课（跳过不会记为通过）
:quit               保存进度并退出`)
	case ":lesson":
		s.show()
	case ":lessons":
		s.list()
	case ":next", ":prev", ":goto":
		index := s.index + 1
		if command == ":prev" {
			index = s.index - 1
		} else if command == ":goto" {
			index = learnLessonIndex(s.lessons, argument)
		}
		if index < 0 || index >= len(s.lessons) {
			fmt.Fprintln(s.errors, "没有这节课；:lessons 查看编号和 ID。")
		} else {
			s.move(index)
		}
	case ":hint":
		if s.hint < len(l.Hints) {
			fmt.Fprintf(s.output, "提示 %d/%d：%s\n", s.hint+1, len(l.Hints), l.Hints[s.hint])
			s.hint++
		} else {
			fmt.Fprintln(s.output, "提示已全部显示；:solution 查看答案与解释。")
		}
	case ":solution":
		printLearnSource(s.output, l.Solution)
		fmt.Fprintln(s.output, l.Why)
		fmt.Fprintln(s.output, "输入代码并通过验证后才会记录完成。")
	case ":tests":
		for _, tc := range l.Cases {
			inputs, _ := json.Marshal(tc.Inputs)
			fmt.Fprintf(s.output, "  %s：输入 %s", tc.Name, inputs)
			if len(tc.State) > 0 {
				state, _ := json.Marshal(tc.State)
				fmt.Fprintf(s.output, "；宿主 SetState %s", state)
			}
			if tc.Error != "" {
				fmt.Fprintf(s.output, " → 失败包含 %q\n", tc.Error)
			} else {
				fmt.Fprintf(s.output, " → %s；打印 %q\n", tc.Want, tc.Stdout)
			}
		}
	case ":edit":
		s.draft, s.editing = "", true
		fmt.Fprintln(s.output, "多行输入已开始，完成后用 :submit；:cancel 取消。")
	case ":cancel":
		s.draft, s.editing = "", false
		fmt.Fprintln(s.output, "已取消当前输入。")
	case ":submit":
		if strings.TrimSpace(s.draft) == "" {
			fmt.Fprintln(s.errors, "请先输入代码。")
			break
		}
		answer := s.draft
		s.draft, s.editing = "", false
		return s.attempt(fillLearnTemplate(l.Template, answer))
	case ":run":
		if s.lastSource == "" {
			fmt.Fprintln(s.errors, "请先提交代码。")
		} else {
			return s.attempt(s.lastSource)
		}
	case ":demo":
		fmt.Fprintln(s.output, "正在编译并运行示例…")
		results, err := s.runner(l.Example, l.Mode, l.Demo)
		if err != nil {
			fmt.Fprintln(s.errors, err)
		} else {
			s.lastResults = results
			for i, result := range results {
				fmt.Fprintf(s.output, "  %s → %s\n", l.Demo[i].Name, displayLearnResult(result))
			}
		}
	case ":save":
		source := s.lastSource
		if source == "" {
			source = l.Template
		}
		if err := saveLearnSource(argument, source); err != nil {
			fmt.Fprintln(s.errors, err)
		} else {
			fmt.Fprintf(s.output, "已保存 %s；编辑完整文件后用 :load %s 验证。\n", argument, argument)
		}
	case ":load":
		source, err := readLearnSource(argument)
		if err != nil {
			fmt.Fprintln(s.errors, err)
		} else {
			return s.attempt(source)
		}
	case ":graph":
		source := s.lastSource
		if source == "" {
			source = l.Example
			fmt.Fprintln(s.output, "示例的依赖图：")
		}
		graph, err := compiler.ParseAndBuild(source)
		if err != nil {
			fmt.Fprintln(s.errors, err)
			break
		}
		for _, node := range graph.Nodes {
			fmt.Fprintf(s.output, "  %s [%s] ← %s", node.Name, node.Type, strings.Join(node.Deps, ", "))
			if len(node.Gates) > 0 {
				fmt.Fprintf(s.output, "；gate=%s", strings.Join(node.Gates, ", "))
			}
			if node.State {
				fmt.Fprint(s.output, "；State")
			}
			fmt.Fprintln(s.output)
		}
	case ":trace":
		if len(s.lastResults) == 0 {
			fmt.Fprintln(s.output, "先用 :demo 运行示例，或提交练习。")
		}
		for i, result := range s.lastResults {
			fmt.Fprintf(s.output, "  运行 %d：\n", i+1)
			for _, event := range result.Trace {
				fmt.Fprintf(s.output, "    tick=%d %s %s %s\n", event.Tick, event.Node, event.Status, event.Reason)
			}
		}
	case ":reference":
		printLearnReference(s.output, argument)
	default:
		fmt.Fprintln(s.errors, "未知命令；:help 查看交互命令。")
	}
	return false
}

func saveLearnSource(path, source string) error {
	if path == "" {
		return fmt.Errorf("用法：:save 文件路径")
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return fmt.Errorf("保存源码（不覆盖已有文件）：%w", err)
	}
	_, writeErr := io.WriteString(file, strings.TrimSpace(source)+"\n")
	return errors.Join(writeErr, file.Close())
}

func readLearnSource(path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("用法：:load 文件路径")
	}
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, learnSourceLimit+1))
	if len(data) > learnSourceLimit {
		return "", fmt.Errorf("代码超过 1 MiB，请缩小练习")
	}
	return string(data), err
}

func (s *learnSession) completions(prefix string) []string {
	lesson := s.lessons[s.index]
	repl := &replSession{values: map[string]runtime.Value{}}
	if tokens, err := lexer.New(lesson.Example).Lex(); err == nil {
		if program, err := parser.New(tokens).Parse(); err == nil {
			repl.dependencies = program.Dependencies
		}
	}
	base := repl.completions(prefix)
	var candidates []string
	for _, candidate := range base {
		if !strings.HasPrefix(candidate, ":") {
			candidates = append(candidates, candidate)
		}
	}
	candidates = append(candidates, ":help", ":exit", ":lesson", ":demo", ":tests", ":hint", ":solution", ":edit", ":submit", ":cancel", ":run", ":save", ":load", ":graph", ":trace", ":reference", ":lessons", ":goto", ":next", ":prev", ":quit")
	tokens, _ := lexer.New(lesson.Example + "\n" + lesson.Template).Lex()
	for _, t := range tokens {
		if t.Kind == token.Ident && t.Text != learnPlaceholder {
			candidates = append(candidates, t.Text)
		}
	}
	seen := map[string]bool{}
	var result []string
	for _, candidate := range candidates {
		if strings.HasPrefix(candidate, prefix) && !seen[candidate] {
			seen[candidate] = true
			result = append(result, candidate)
		}
	}
	sort.Strings(result)
	return result
}

func printLearnReference(output io.Writer, topic string) {
	switch topic {
	case "", "keywords":
		fmt.Fprintln(output, "15 个关键字：flow（程序入口），fn（纯函数），return（结果），match（模式分支），for / in（推导式或顺序循环），break / continue（结束最近循环或跳过本项），if / else（条件选值），import（外部依赖），as（Python/Host/Go 别名），true / false（布尔值），null（空值）。")
		fmt.Fprintln(output, ":reference operators / builtin / list / string 查看内置操作和标准库签名。number、string、list 等类型名及 state、retry、feedback 是内置名称，不是关键字。")
	case "operators":
		fmt.Fprintln(output, "+ - * /：加减乘除；//：向下取整；%：余数符号跟随除数；**：乘方，右结合且优先于负号；x */ y：以 y 为底的对数，与乘除同级。例：-7 // 2 = -4，-7 % 2 = 1，2 ** 3 ** 2 = 512，8 */ 2 = 3。行注释写 #。")
	case "builtin":
		fmt.Fprintln(output, "str(value) -> string\nlen(string|list|object) -> number\nisEmpty/isNotEmpty(value) -> bool\nrange(end) / range(start, end[, step]) -> list（半开区间）\nfold(list, seed, fn(acc, item)) / list.fold(...) -> any（顺序归约）\nrandom() / random_list(n) -> number / list（[0,1) 随机样本）\nrandom_int(end) / random_int(start, end) -> number\nrandom_choice(list) -> any；random_shuffle(list) -> list\nsort(xs[, reverse]) / sort_by(xs, key[, reverse]) / sort_with(xs, comparator) -> list\nfail(string) -> 失败\nprint(values...) -> null（外部写效果）\nstate(initial) -> any（持久实例状态，仅绑定）\nretry(operation(...), attempts) -> any（上限为正整数字面量，包含第一次调用）\nfeedback(initial, step, verify, attempts) -> any（上限为正整数字面量，有界验证与修订）")
	case "list":
		for _, spec := range listops.All() {
			fmt.Fprintf(output, "%s(%s) -> %s", spec.Name, learnSignature(spec.Types, spec.MinArgs, spec.MaxArgs), spec.Result)
			if spec.Callback >= 0 {
				fmt.Fprintf(output, "；纯 fn 接受 %d 个参数，返回 %s", spec.CallbackArity, spec.CallbackResult)
			}
			fmt.Fprintln(output)
		}
	case "string":
		for _, spec := range stringops.All() {
			fmt.Fprintf(output, "%s(%s) -> %s\n", spec.Name, learnSignature(spec.Types, spec.MinArgs, spec.MaxArgs), spec.Result)
		}
	default:
		fmt.Fprintln(output, "可用主题：keywords、operators、builtin、list、string。")
	}
}

func learnSignature(types []string, min, max int) string {
	if max < 0 {
		return strings.Join(types, ", ") + "..."
	}
	parts := append([]string(nil), types...)
	for i := min; i < len(parts); i++ {
		parts[i] = "[" + parts[i] + "]"
	}
	return strings.Join(parts, ", ")
}
