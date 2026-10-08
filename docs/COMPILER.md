# LIP 编译教程

## 编译流水线

```text
.lip
  ↓
Lexer
  ↓
Parser / AST
  ↓
Name + scope resolution
  ↓
Dependency Graph IR
  ↓
Go code generation
  ↓
gofmt (by `lipc build`)
  ↓
optional `go build`
```

`AST` 表示用户写了什么；`Graph` 表示计算依赖什么。分别服务于源码分析和执行调度。

有返回值的 Flow 必须写出完整边界：`flow Hello(request: string) -> string { ... }`。所有参数
显式标注 `string`、`number`、`bool`、`list`、`object` 或 `any`，有返回值时恰好一个 `return`。没有返回值时
省略输出声明和 return，例如 `flow main() { print(79 / 134) }`；也可显式写 `-> void`。门控可能
让输出被跳过时声明 `Type?`。静态矛盾在编译期失败；动态输入、函数结果和 Flow 输出
在运行时校验，包括异步 Host 结果。

无参数、无返回值的单组语句可以直接放在文件顶层：

```lip
a = 2
b = 3
print(a / (a + b))
```

这等价于 `flow main() { ... }`；check、inspect、run、build 使用同一编译路径，
默认可执行文件名为 `main`。`import` 和 `fn` 声明仍位于执行语句前。
需要输入参数或返回值时使用显式 Flow，文件不能混用两种入口形式。

执行语句用换行或分号分隔。同一行时必须写 `a = 2; b = 3; print(a / b)`，
不能只用空格连接；最后一条语句的分号可省略。REPL 和 Flow/when 块采用相同规则。

## `lipc` 命令

在仓库根目录安装，安装位置跟随现有 GOBIN/GOPATH 配置。下面的 `lipc` 可以用
PATH 中的命令名或实际安装路径调用，见[快速入门](QUICKSTART.md#安装)：

```bash
go install ./cmd/lipc
lipc version
lipc help run
lipc repl
lipc check examples/hello.lip
lipc check --json examples/hello.lip
lipc inspect examples/hello.lip
lipc run --trace hello-trace.json examples/hello.lip Alice
lipc build --output hello examples/hello.lip  # 编译可执行文件
lipc run examples/hello.lip Alice       # 临时编译并运行
lipc build --emit-go --output hello.go examples/hello.lip
```

`build` 默认生成可执行文件，`--emit-go` 生成 Go 源码，`--no-main` 生成
Go 库；库包名由 `--package` 指定。模式由选项决定，输出文件名由 `--output` 决定。

默认可执行文件以 Flow 命名，放在当前目录；Windows 目标带 `.exe`。
源码默认是输入文件旁的 `<Flow>_generated.go`。`--output` 接受文件名、已有目录
或以路径分隔符结尾的新目录，自动创建父目录。

处理源文件的命令采用 `lipc 命令 [--选项] 文件`。入口文件结束工具选项解析，`run`
把文件后的全部内容按 Flow 参数顺序传入：

```bash
lipc run examples/tutorial/01_hello.lip 小林
lipc run --trace hello-trace.json examples/tutorial/01_hello.lip 小林
lipc run examples/tutorial/01_hello.lip --help  # 名字是 --help
```

`--help` 放在文件前时显示工具帮助；文件后的 `--help`、负数、`--` 和其他
文本都作为程序输入。带空格的输入用终端引号包起来。选项值可写成
`--trace path` 或 `--trace=path`。以减号开头的源文件可写成 `./-hello.lip`。

`run/build` 使用内置 Runtime 在临时模块中编译，完成后清理临时文件。
`run` 在调用者的当前目录执行，因此相对文件路径和本地 Python 模块自然可用。
构建需要 Go 1.27 或更高版本；编译出的核心程序可以独立运行。
`--emit-go` 的源码引用 `lipalpha/runtime`，适合已有 Go 模块中的集成。

程序输入在调用 Go 前完成校验；缺少输入时提示参数名、类型和当前源文件的
调用方式。构建输出和 trace 使用独立路径，保护源文件及其链接别名。

直接运行 `lipc`、`lipc help` 或 `lipc 命令 --help` 向 stdout 输出帮助，退出 0。
用法错误退出 2，诊断写 stderr；源码、构建或执行失败退出 1，`run` 保留程序
退出码。普通诊断带出错行和修复提示；`check --json` 输出 `lip.diagnostics.v1`。
宿主也可调用 `CheckFile(path)` 与 `CheckSource(path, source)`，详见
[VIBE-CODING.md](VIBE-CODING.md)。

输入按 Flow 声明解析：`string` 原样传入，`number` 使用有限十进制数，`bool`
使用 `true`/`false`，`any`/`list`/`object` 使用 JSON。列表与对象先检查外层
形状，动态元素在运算中检查。`run`、独立可执行文件与生成源码共用这套规则。

### CLI 设计依据

启动器首先区分工具选项和程序参数，程序再决定如何使用收到的字符串。
常见语言的处理如下：

| 工具 | 调用形状 | 分界方式 |
| --- | --- | --- |
| [Go](https://pkg.go.dev/cmd/go#hdr-Compile_and_run_Go_program) | `go run [构建选项] 包 [参数...]` | 包或源码列表之后是程序参数 |
| [Rust / Cargo](https://doc.rust-lang.org/cargo/commands/cargo-run.html) | `cargo run [工具选项] -- [参数...]` | 通常运行当前项目，无入口文件，使用 `--` |
| [Dart](https://dart.dev/tools/dart-run) | `dart run [工具选项] 入口 [参数...]` | 文件或包入口之后是程序参数 |
| [Kotlin 启动器](https://kotlinlang.org/docs/command-line.html#run-the-application) | `kotlin [工具选项] 主类 [参数...]` | 主类或可执行入口之后是程序参数 |
| [Python](https://docs.python.org/3/using/cmdline.html#interface-options) | `python [解释器选项] 脚本 [参数...]` | 脚本之后进入 `sys.argv` |

LIP 每个入口文件只有一个 Flow，文件足以提供自然边界，因此采用 Go/Dart
这一类顺序。工具无需扫描文件后的内容来猜测它是选项、负数、JSON 还是文件名。
当前由 Runtime 将程序输入按 Flow 声明的类型和顺序解析，生成的可执行文件
使用同一规则。

### 交互式 REPL

`lipc repl [--quiet]` 接收表达式、不可变绑定、纯 `fn` 声明、`when` 和 Python
依赖声明。直接输入 `79 / 134` 会显示结果，`value = 79 / 134` 保存变量，
`print(value)` 只显示显式输出。无需 Flow 外壳或 return。未闭合的 `()`、`[]`、`{}`
允许多行；未完成的运算、fn 头和 if/else 表达式也会续行。字符串和注释中的括号不触发续行。

`:help`、`:vars`、`:history`、`:reset`、`:cancel`、`:quit` 管理会话；EOF 也退出。
管道输入自动隐藏欢迎语、提示符和结果编号；`--quiet` 也可显式开启。

终端中支持左右方向键、Home/End、Backspace/Delete；上下方向键回看历史，
返回最新位置时恢复正在编辑的草稿。Tab 补全变量、函数、模块别名、标准库操作
和会话命令；Ctrl-A/E 到开头/末尾，Ctrl-U/K 删除前半/后半，Ctrl-W 删除前一个词，
Ctrl-L 清屏。Ctrl-C 丢弃当前输入和未完成单元，Ctrl-D 在空输入时退出，
有内容时向后删除。中文、组合字符和连写 emoji 按显示字符移动和删除。
支持终端的括号粘贴模式：整段多行代码先进入编辑区，再按 Enter 提交。

每个输入单元仍经过正常的语法、类型与依赖检查，编译后按顺序运行。成功的
变量值通过独立 JSON 文件保存，并作为下个单元的输入；打印输出不参与解析。
函数和依赖声明跨单元保留，历史副作用不会重放。失败后可以继续输入，变量
只在成功时提交；外部副作用遵循原来的不回滚规则。绑定不可重赋值，用新名字
或 `:reset` 开始。

每个单元单独构建和启动，因此需要 Go 工具链，首次构建可能较慢。Python
Worker 的生命周期是一个单元，保存的值需可用 JSON 表达；不跨单元保留 Python
对象句柄或 State/Tick 实例。需要 Go Host adapter 时使用库模式接入自己的宿主。

Python 的 `f(值, name=值)` 是函数调用中的绑定语法；`python script.py name=值`
本身只传递字符串。若未来支持 Flow 命名输入，可参考 Python 的位置/关键字
绑定检查，但需要另行定义文本编码与原样字符串的区分，见[路线图](ROADMAP.md#flow-命名输入候选设计)。

## 两种生成模式

### 可执行模式

默认生成 `package main` 和遵守输入/输出契约的 `main`：

```bash
lipc build --emit-go --output hello.go examples/hello.lip
go run hello.go Alice
```

生成文件暴露：

```go
func Run(context.Context, runtime.Host, map[string]runtime.Value) (runtime.Value, []runtime.TraceEvent, error)
func RunSequential(context.Context, runtime.Host, map[string]runtime.Value) (runtime.Value, []runtime.TraceEvent, error)
func RunParallel(context.Context, runtime.Host, map[string]runtime.Value, int) (runtime.Value, []runtime.TraceEvent, error)
func RequiredDependencies() []Dependency
```

### 库模式

库模式不生成 `main`，供自己的 Go 程序接入：

```bash
lipc build --no-main --package hostflow \
  --output examples/host_adapter/flow/flow_gen.go examples/host_adapter/flow.lip
```

然后在 Go 中：

```go
host := runtime.DefaultHost()
host.Register("write_file", writeFileAdapter)
value, trace, err := hostflow.Run(ctx, host, inputs)
```

调用名也可以是 dotted path，例如 `numpy.linalg.solve(matrix, vector)`。
编译器把它作为一个 Host operation 原样写入生成的 Go；使用
`runtime.NewPythonHost(worker)` 时，Python Worker 会动态导入并调用该路径，
因此新增 Python 库不需要修改编译器。

如果 Flow 依赖外部环境，可以在文件头显式声明依赖：

```lip
import python "numpy>=1.26"
import python "scipy"
import go "github.com/acme/adapter"
import host "load_profile"
```

`import` 声明外部能力，编译器检查声明，运行时通过 Worker 或 Host 执行调用；
它不安装依赖，也不自动注册 Go adapter。旧依赖关键字会提示改用 `import`。
`lipc check` 会逐条打印声明；生成库提供 `RequiredDependencies()`，由部署程序
据此检查 Python 环境、Go module 和 Host 注册。Python 依赖按包/版本写出，调用仍
使用真实模块路径（如 `sklearn.preprocessing.scale`）；Worker 继续允许任意已安装
库，是否限制模块由 `AllowedModules`/`DeniedModules` 决定。Go 包必须由承载生成包
的 Go 程序导入和注册，LIP 编译器不会凭一个声明猜测 adapter 实现。

Python 可显式起别名，省略 `as` 时保留模块原名：

```lip
import python "math" as m
value = m.sqrt(9)
print(value)
```

`import python "numpy>=1.26" as np` 和
`import python "xml.etree.ElementTree" as et` 也支持。
`sklearn` 来自 Python 模块本身，安装包名 `scikit-learn` 不会被编译器自动改名。
别名在检查时解析为真实路径；`check`、`inspect` 和 `RequiredDependencies()`
保留原声明与别名信息。Host/Go 名称仍由宿主注册，`as` 只用于 Python 模块。
同名 Python 模块可以显式导入；建议用 `import python "string" as text`
让 `text.capwords(...)` 和 LIP 标准库的 `string.trim(...)` 并存。
省略 `as` 时保留 Python 原名，相关调用归属于显式导入的模块。

编译器还会检查外部调用是否有对应声明：dotted 调用默认要求匹配的 Python 模块，
bare 调用要求 `import host`；内置 `str`、`len`、`range`、`fold`、`list.*`、`print`、`state`、`retry`、`feedback`
和 Python 控制面操作属于语言/Runtime 边界。缺少声明在 `check` 阶段报错。

包含 `import python` 的独立可执行程序会在 `main` 中启动默认 Python Worker；没有
这条声明时，生成入口只使用 `runtime.DefaultHost()`。库模式始终由宿主显式创建
Worker 和 Host，这样 Python 进程的解释器、策略和生命周期不会被藏在库初始化中。
声明 `import host` 或 `import go` 的程序必须使用库模式；独立入口会明确报错，
不会假装已经拥有未注册的 adapter。

调用参数使用普通位置参数。局部纯函数、`str`、`len`、`range`、`fold` 和 `list.*` 可以嵌套；外部调用需要先绑定到一个
节点，再作为下一个调用的输入；例如先写
`tensor = torch.tensor(values)`，再写 `relu = torch.nn.functional.relu(tensor)`。
Python 关键字参数语法仍是后续语言工作；大对象已有只读 blob/mmap 基线，复杂
Python 返回值可以先通过 Worker 句柄和 `python.to_json` 取回。

## Graph 节点粒度

下面的 LIP：

```lip
import host "load_a"
import host "load_b"
import host "combine"

flow Combine() -> any {
    a = load_a()
    b = load_b()
    c = combine(a, b)
    return c
}
```

生成 `a`、`b`、`c` 三个计算节点和一个输出节点。`a` 和 `b` 独立，`c` 等待两者。
Flow 参数在 `flow (...)` 中声明；普通局部变量使用一次 `name = expression`
绑定即声明，并且是单赋值。未定义名、前向引用、重复绑定和从 `when` 块逃逸的
绑定都会在 `lipc check` 阶段报错，不存在运行时隐式变量注入。
`fn` 内部的局部表达式不是图节点，避免 Runtime 被普通算法的细节淹没。函数保持纯计算，
可组合其他局部函数与纯操作，不接受外部调用。递归环必须声明返回类型；
生成的函数在每次调用检查取消和最大深度 256，非递归返回类型可以推导。
副作用调用可以直接写成语句，例如 `print(value)`；它会成为一个没有
Flow 返回值的图节点。普通的无用表达式不允许单独出现。

列表推导式保持同样的边界：

```lip
doubled = [x * 2 for x in range(1, 4)]
total = list.sum([x * x for x in range(1, 4)])
```

`doubled` 是一个静态 Graph 节点，依赖和门控就绪后计算一次 source，运行时按其元素数量动态
展开。Map 输出保持输入顺序；空输入返回空列表。`RunSequential` 逐个执行，
并行运行时只对纯元素计算或全部使用 `RegisterPure`/`RegisterReadOnly` 的 Host 调用并发，且
受 `RunParallel` 的上限约束。MapSpec.Source 引用已有值，SourceEval 求值纯列表
表达式；两种形式都由整个 source 的自由变量建立依赖。
调用参数、fn、条件分支和元素表达式里的推导式由 valueEmitter 生成 MapValues 调用，
在所在表达式内顺序求值，不提升为独立图节点，保留惰性分支、求值顺序、变量遮蔽
和每元素错误/取消行为。多层推导式不会叠加 worker 数量。普通 `for`/`while` 尚未进入语言核心。

## 持久实例和增量执行

`state(initial)` 仍然是一个静态 Graph 节点，但生成包另外暴露
`NewInstance`。Runtime.Instance 保存输入、State、每个逻辑 Tick 的计数器
和纯节点依赖快照。`SetState` 只安排下一次 Tick 的外部状态更新；它不把图
变成循环。State 初始化提交后不会因其他输入变化而再次调用 initializer。

每次 Tick 先合并输入，再比较依赖值。依赖未改变的纯节点可以复用；带有
外部效果的节点会重新执行。Trace 的 `Tick` 字段和 `reused` 原因用于观察这
个过程。一次性 `Run`、`RunSequential`、`RunParallel` 不共享这些实例缓存。

## Retry、Feedback 和调度效果

编译器把 `retry(call, n)` 和受限 `feedback(initial, step, verify, n)` 降低
为 Runtime policy；`n` 始终是正整数上限。取消在 `Await`、Map、Retry 和
Feedback、fold/list 和递归调用的边界传播。

Host 的 `RegisterPure`、`RegisterReadOnly` 和 `Register` 分别表示纯计算、
外部只读和外部写入。Runtime 对只读操作保留并发可能，对外部写入使用顺序
屏障。图构造者可以用 `NodeSpec.After` 增加不传值的排序约束；`NodeSpec.Effect`
用于显式覆盖运行时效果分类。Alpha 不生成完整的 Effect 类型系统。

常见计算直接使用运算符：`+ - * /`、比较运算符和 `&& || !`。字符串用
`+` 连接，字符串重复用 `*`，需要显式转换时写 `str(value)`；
`add/mul/gt/concat/identity` 不属于 Alpha 0.6 默认 Host。
`==` 和 `!=` 除 null 比较外，对静态已知的不同类型会在编译期拒绝；`any` 值的具体类型
由 Runtime 在执行时判断。

## 并行规则

生成程序的 `RunSequential` 是参考语义：确定性、顺序执行。默认的 `Run`
会自动使用有界并行调度；需要显式控制并行度时使用 `RunParallel`。

自动调度和 `RunParallel` 只并行：

- 没有未完成依赖的节点；
- `Pure` 节点；或
- 对应 Host operation 通过 `RegisterPure` 或 `RegisterReadOnly` 注册的节点。

副作用操作通过普通 `Register` 注册，调度器会把它们作为顺序屏障。
并行度由调用方传入，不能由单个节点无限制地产生 goroutine。

## 图与 Trace JSON

`lipc inspect` 输出检查后的 `lip.graph.v1`，包括真实数据依赖、gates、纯计算
标记、调用名和源位置，不暴露 AST 内部表示或执行 Host。
`lipc run --trace path.json` 输出 `lip.trace.v1` 的状态名称、Tick 和原因，自动
创建 trace 的父目录；
成功与执行失败都保留，输入/编译失败尚未执行图。CLI 先编译临时二进制再执行，
保留程序退出码并转发中断；生成 main 的失败路径也执行 Worker 清理。

`list.*` 的静态签名与 Runtime 签名共用 `internal/listops`，由生成代码直接
调用固定纯函数；具名 callback 名称不形成数据依赖，内联 callback 的自由变量
形成数据依赖。LambdaExpr 只出现在集合回调位置，生成 runtime.Op，参数局部遮蔽，
捕获沿用外层 valueEmitter 的名称映射；检查参数/结果类型、取消与调用深度。
具名和内联 fn 共用单表达式体解析，return 可省略。完整约束见
[LIST-LIBRARY.md](LIST-LIBRARY.md) 和 [当前 spec](ALPHA-0.6-SPEC.md)。

## 错误和 Trace

每个节点会经历：

```text
Pending → Running → Completed
Pending → Running → Error
Pending → Skipped
```

Trace 从节点开始执行或被跳过时记录；`Pending` 只是初始状态，不单独产生
Trace 事件。

`Run` 和 `RunParallel` 都返回 Trace。发生错误时，下游节点不会继续执行。
并行运行时，独立节点的完成顺序和首个报告的独立错误可能不同；需要按源
码顺序复现时使用 `RunSequential`。
