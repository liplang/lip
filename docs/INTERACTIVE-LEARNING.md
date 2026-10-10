# 用 `lipc learn` 交互学习 LIP 0.6.4

`lipc learn` 内置 26 节中文课程。每节按讲解、示例、代码练习和验证展开，覆盖
15 个关键字、程序组织、纯计算、list/string 标准库、Runtime 与外部集成。
课程和执行运行库都随 `lipc` 打包，安装后无需保留仓库，也不需要联网。
执行示例与练习需要 Go 1.27 或更高版本；Python 课程另外需要 Python。

在仓库根目录直接开始：

```bash
go run ./cmd/lipc learn
```

已经安装新版工具时：

```bash
lipc learn
lipc learn --list
lipc learn --lesson 7
lipc learn --lesson state-tick
lipc learn --no-progress
```

## 每课怎样学习

1. 阅读讲解和示例，用 `:demo` 实际编译、运行示例。
2. 阅读练习骨架，将 `__ANSWER__` 替换为代码。终端中只输入替换部分。
3. 用 `:tests` 查看验证输入，用 `:hint` 逐步获得提示。
4. 输入代码并回车。编译器检查语法、名称、作用域和类型，再生成 Go 程序，
   用真实 Runtime 执行多组输入并比较结果、打印输出和预期失败。
5. 通过后可以用 `:graph` / `:trace` 观察，或 `:save 路径` 导出完整程序；
   用 `:next` 进入下一课。

例如第一课的练习是：

<!-- lip-check: template -->
```lip
flow Arithmetic() -> number { return __ANSWER__ }
```

输入 `2 + 3 * 4`，运行结果为 `14`。第二课会要求写一个不可变绑定，之后
逐步写函数、列表变换和完整 Flow。验证不限于一组固定输入：例如字符串
和列表课会包含空输入，Unicode 课会包含中文、emoji 和组合字符，错误课
会要求明确失败。对象比较不依赖键顺序，列表比较保留顺序。

看 `:solution` 或跳过课程不会记为通过；提交的代码必须通过验证。
每次提交独立执行，失败后可修改重试，不会重放上次尝试的状态。
State 课的一次提交则会在同一个持久实例里连续运行多个 Tick。

## 多行输入与编辑器

未闭合的括号自动续行。需要输入多个声明或完整程序时，使用显式多行模式：

```text
答题 > :edit
续行 > fn square(x: number) -> number { return x * x }
续行 > flow Workshop(value: number) -> object {
续行 >     return {original: value, squared: square(value)}
续行 > }
续行 > :submit
```

`:submit` 提交的是替换部分；在“完整程序”练习里，替换部分就是整个文件。
`:cancel` 或终端 Ctrl-C 丢弃当前草稿，EOF 不执行未提交的多行代码。
终端支持方向键编辑、历史回看和 Tab 补全，沿用 REPL 的行编辑器。导入补全区分 Python/Go 命名空间和 Host 单操作别名；
显式导入接管 list/string 名字时，补全不再推荐被接管的核心库成员。

也可以在自己的编辑器里完成练习：

```text
答题 > :save exercise.lip
```

这会保存练习骨架。编辑文件，把 `__ANSWER__` 替换为代码，再输入：

```text
答题 > :load exercise.lip
```

`:load` 接收完整文件，`:save` 保存最近一次提交；尚未提交时保存骨架。
保存操作不覆盖已有文件。通过课程后仍停留在本课，可以先导出答案再继续。

## 课程路线

| 编号 | ID | 内容 |
| --- | --- | --- |
| 1 | `expressions` | 八种数学运算、优先级、print、# 注释 |
| 2 | `bindings` | 不可变绑定、依赖、语句分隔 |
| 3 | `data` | 六类值、嵌套列表/对象、字段/索引 |
| 4 | `conditions` | if/else、布尔逻辑、短路、null |
| 5 | `functions` | 纯 fn、参数与返回类型、return |
| 6 | `organization` | 完整文件、函数与 Flow、void |
| 7 | `range-map` | 半开区间、for/in、Map |
| 8 | `fold` | 初值、顺序归约、内联回调 |
| 9 | `strings` | 文本清理和大小写 |
| 10 | `unicode` | Unicode 码点、slice、len、str |
| 11 | `text-numbers` | 切词、解析与聚合、解析错误 |
| 12 | `filter-sort` | 过滤、去重、排序 |
| 13 | `shapes` | 窗口、zip、转置、笛卡尔积 |
| 14 | `groups` | 全局分组、相邻分组、键函数 |
| 15 | `scan-query` | 累计历史、any/all、空输入 |
| 16 | `recursion` | 终止条件、递归类型、调用深度 |
| 17 | `failures` | fail、输入校验、失败契约 |
| 18 | `gates` | match 模式、默认分支、守卫、可选输出、Skipped |
| 19 | `for-loop` | 顺序操作遍历、独立作用域、break/continue |
| 20 | `dependency-graph` | 依赖图、调度、轨迹、CLI 工作流 |
| 21 | `host` | import host/go、适配器与效果 |
| 22 | `state-tick` | 持久状态、SetState、Tick、复用 |
| 23 | `retry` | 有界重试、Await、尝试次数 |
| 24 | `feedback` | 初始候选、修订与验证、有界失败 |
| 25 | `python` | Python 标准库、import python、as |
| 26 | `report` | 独立标签清洗报告综合练习 |

## 命令与速查

`:help` 列出全部命令。课程导航包括 `:lessons`、`:goto 编号或ID`、`:next`、
`:prev` 和 `:lesson`。重新验证最近一次提交用 `:run`。

```text
:reference keywords
:reference operators
:reference builtin
:reference list
:reference string
```

标准库速查直接读取编译器和 Runtime 共用的签名目录，列出全部 36 个 list
操作与 19 个 string 操作，包括可选参数和回调契约。两套库无需 import。
完整语义和边界规则见[列表库](LIST-LIBRARY.md)与[字符串库](STRING-LIBRARY.md)。

## 进度与外部集成

默认在当前目录 `.lip-learn-progress.json` 保存课程位置和已通过课程。
通过练习时立即保存，`:quit` 或 EOF 退出时也保存。下次运行从当前课或
其后的未完成课程继续；`--lesson` 可指定任意课程复习。

```bash
lipc learn --progress /path/to/my-progress.json
lipc learn --no-progress
```

`--no-progress` 不读取或写入进度文件。进度文件损坏会明确报错，不自动
清空旧记录；可以指定另一个文件或关闭进度。草稿和历史输入不写入进度。

Host、Retry 课运行器自带真实注册的教程适配器：`lesson_double` 是纯数值
操作，`lesson_fetch` 是先失败两次再返回 Await 的读操作。这些操作不访问
外部服务，导出后需要自己的 Go Host 注册同名能力，见[宿主集成](COMPILER.md#库模式)。
State 课运行器使用真正的 `NewInstance` / `SetState` / `Tick`，用 `:tests`
查看每次输入与状态更新。Feedback 课使用纯函数策略。

Python 课实际调用自带的 `math` 和 `statistics`，不需要 NumPy 等第三方包。
Python 不可用时会报告执行失败，可以 `:next` 跳过这课；它不会被记为通过。
第三方库、混合 Go/Python 示例和对象生命周期见[Python 集成](PYTHON-INTEGRATION.md)。

## 维护课程

课程数据在 [`cmd/lipc/learn/lessons.json`](../cmd/lipc/learn/lessons.json)，通过
`go:embed` 打包。每课包含讲解、示例及其输入、含一个占位符的练习骨架、
参考答案、逐步提示、必要语法、验证输入与预期结果。

```bash
go test ./cmd/lipc -run '^TestLearn' -count=1
```

测试实际编译并执行每课的示例和参考答案，还检查文档课程索引、全部关键字的覆盖、错误答案
拒绝、重试上限、失败恢复、多行输入、文件导出和进度保存。可选 Python 测试
仅在解释器可用时执行。
