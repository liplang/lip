# LIP 0.6.3 编译教程

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
显式标注 `string`、`number`、`bool`、`list`、`object` 或 `any`，有返回值时使用一个 `return`，
或在同一个 `match` 的互斥分支中分别 `return`。没有返回值时
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
不能只用空格连接；最后一条语句的分号可省略，连续或独立分号作为空语句接受。
文件、fn、REPL 和 Flow/match/for 块采用相同规则。

`for item in source { ... }` 编译为外层图的顺序屏障，依赖 source 和循环体实际捕获的
外层值。source 求值一次，每项建立新的体内图并顺序执行，等待异步调用；循环每 Tick
重新执行，不缓存迭代局部值。break/continue 停止当前体内图，由最近一层 for 处理；
外层 trace 把循环记为正常完成，实际错误保留迭代索引和体内源码位置。循环体可以绑定、
调用、match 和嵌套 for，不允许 return/state。转换与聚合仍分别用推导式和 fold。

## `lipc` 命令

在仓库根目录安装，安装位置跟随现有 GOBIN/GOPATH 配置。下面的 `lipc` 可以用
PATH 中的命令名或实际安装路径调用，见[快速入门](QUICKSTART.md#安装)：

```bash
go install ./cmd/lipc
lipc version
lipc help run
lipc learn                         # 26 节中文交互课
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
Go 构建使用 trimpath，产物不嵌入临时模块目录，相同运行库可复用 Go 构建缓存；
同一源码在不同临时目录的产物一致。LIP 错误位置仍指向原始源码。
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

`lipc repl [--quiet]` 接收表达式、不可变绑定、纯 `fn` 声明、`match` 和 Python
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

Python 原名和别名可同时使用；先输入 `import python "numpy"`，再输入
`import python "numpy" as np` 会增加 `np` 别名。重复输入同一声明无副作用，
但把同一别名用于另一个模块仍会报错。`np.__version__` 和
`print(np.__version__)` 读取属性；`np.mean` 或 `print(np.mean)` 显示
`<Python function numpy.mean; call with (...)>`，不会调用函数。
属性拼写错误会报告实际模块缺少该属性，并在有相近名称时给出建议。
REPL 也支持 `print(np.mean([1,2,3,4]))` 等嵌套调用，按表达式顺序执行，
保留条件分支和 `&&`/`||` 的短路行为；`fn` 和集合回调仍需保持纯函数。

每个单元单独构建和启动，因此需要 Go 工具链，首次构建可能较慢。Python
Worker 的生命周期是一个单元，保存的值需可用 JSON 表达；不跨单元保留 Python
对象句柄或 State/Tick 实例。模块、函数和类的具名引用保存 Python 路径，
可跨单元查看，并在使用时由当前 Worker 重新解析。需要 Go Host adapter 时使用库模式接入自己的宿主。

Python 的 `f(值, name=值)` 是函数调用中的绑定语法；`python script.py name=值`
本身只传递字符串。若未来支持 Flow 命名输入，可参考 Python 的位置/关键字
绑定检查，但需要另行定义文本编码与原样字符串的区分，见[路线图](ROADMAP.md#flow-命名输入候选)。

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
库，是否限制模块由 `AllowedModules`/`DeniedModules` 决定。Go 定义的数值、bool、string 类型与基础标量使用相同的边界、运算和显示规则；
json.Number 仍属于 number，不作为字符串处理。Host 不可比较的 map 索引键
返回普通错误，Go struct 的导出字段支持 Unicode 首字母大小写读取。
Go 包必须由承载生成包
的 Go 程序导入和注册，LIP 编译器不会凭一个声明猜测 adapter 实现。

Python、Host 和 Go 导入都可用 `as` 起别名：

```lip
import python "math" as m
value = m.sqrt(9)
print(value)
```

`import python "numpy>=1.26" as np` 和
`import python "xml.etree.ElementTree" as et` 也支持。
`sklearn` 来自 Python 模块本身，安装包名 `scikit-learn` 不会被编译器自动改名。
别名在检查时解析为真实路径；`check`、`inspect` 和 `RequiredDependencies()`
保留原声明与别名信息。Host 单操作的别名直接调用，Host 命名空间的别名使用成员调用：

```lip
import host "service.fetch" as fetch
import host "service.*" as s
import go "fmt" as f
value = fetch()
s.emit(value)
f.Println(value)
```

`fetch()`、`s.emit(...)`、`f.Println(...)` 分别解析为宿主注册名
`service.fetch`、`service.emit`、`fmt.Println`。Go 包路径完整保留，例如
`import go "example.com/adapter" as a` 的 `a.Transform(...)` 调用注册名
`example.com/adapter.Transform`；Go 导入声明该路径下的操作命名空间。
别名只改变源码写法，真实 Go 函数由宿主适配器注册；不自动安装包或导入函数。
同一依赖可使用不同别名。命名空间别名可与内置操作或具名 fn 同名，裸调用和成员
调用分别解析；单操作别名按声明优先解析，可使用 print/state/retry 等名字，
但与具名 fn 同名会形成裸调用歧义。单操作别名 as string/list 不影响核心成员调用。
重复别名、关键字、python 控制命名空间和
生成节点前缀仍拒绝。参数、绑定、回调和循环
变量按词法作用域遮蔽导入名，初值先求值。不同后端的无别名同名调用要求显式 as 消歧。
本地 fn 在内部独立注册，不会覆盖同名 Host 操作；Host 的同名能力使用独立 as 别名调用。
同名 Python 模块可以显式导入；建议用 `import python "string" as text`
让 `text.capwords(...)` 和 LIP 标准库的 `string.trim(...)` 并存。
省略 `as` 时保留 Python 原名，相关调用归属于显式导入的模块。

编译器还会检查外部调用是否有对应声明：dotted 调用默认要求匹配的 Python 模块，
bare 调用要求 `import host`，Go 命名空间调用匹配对应 Go 包路径；内置 `str`、`len`、`range`、`fold`、`list.*`、`print`、`state`、`retry`、`feedback`
和 Python 控制面操作属于语言/Runtime 边界。缺少声明在 `check` 阶段报错。

实际使用 Python 调用、属性或反馈操作的独立程序会在 `main` 中启动默认 Worker；
未使用的导入不启动 Worker。库模式由宿主显式创建 Worker 和 Host，以控制
解释器、策略和生命周期。实际使用 Host/Go 操作的程序需要库模式与已注册的
适配器，独立入口明确报错。未使用的 Host/Go 导入只保留在 RequiredDependencies，
不会阻止独立运行或 REPL；Host 通配声明只要求实际使用的成员注册。
适配器检查覆盖程序中出现的分支、循环与 feedback 操作，不依赖当前输入。

调用参数使用普通位置参数。Flow、顶层和 REPL 均可组合外部调用，例如
`relu = torch.nn.functional.relu(torch.tensor(values))` 或 `print(np.mean(values))`。
参数按书写顺序求值并等待异步结果，失败后停止；if/match/布尔短路保持惰性。
调度与增量缓存汇总整个表达式的外部效果，包括推导式 source、state 初始化、retry 与 feedback，
任一未知/写入效果形成屏障，只读调用每 Tick 重读。纯 fn 与集合回调仍禁止外部调用。
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
绑定即声明，并且是单赋值。未定义名、前向引用、重复绑定和从 `match` 分支逃逸的
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
并行运行时只对整个 Map（含 source）为纯计算，或全部使用
`RegisterPure`/`RegisterReadOnly` 的 Host 调用并发，且
受 `RunParallel` 的上限约束。MapSpec.Source 引用已有值，SourceEval 求值列表
表达式（Flow 中允许组合外部调用）；两种形式都由整个 source 的自由变量建立依赖。
调用参数、fn、条件分支和元素表达式里的推导式由 valueEmitter 生成 MapValues 调用，
在所在表达式内顺序求值，不提升为独立图节点，保留惰性分支、求值顺序、变量遮蔽
和每元素错误/取消行为。多层推导式不会叠加 worker 数量。语句式 for 顺序执行操作，
支持最近一层循环的 break/continue；while 和可变循环变量尚未进入语言核心。

## 持久实例和增量执行

`state(initial)` 仍然是一个静态 Graph 节点，但生成包另外暴露
`NewInstance`。Runtime.Instance 保存输入、State、每个逻辑 Tick 的计数器
和纯节点依赖快照。`SetState` 只安排下一次 Tick 的外部状态更新；它不把图
变成循环。State 初始化提交后不会因其他输入变化而再次调用 initializer。初始化可组合外部
调用，首次执行按表达式效果调度；保存后的 State 不重复初始化。

每次 Tick 先合并输入，再比较依赖值。依赖未改变的纯节点可以复用；带有
外部效果的节点会重新执行。Trace 的 `Tick` 字段和 `reused` 原因用于观察这
个过程。一次性 `Run`、`RunSequential`、`RunParallel` 不共享这些实例缓存。

## 值的生命周期

生成图与循环体启用 `GraphOptions{ReleaseIntermediates: true}`。Runtime 用声明的
Deps/Gates 统计剩余消费者，在完成或跳过最后一个消费者后删除执行值表引用；
异步节点等待整个 Await 完成。After 仅消费状态。并行 worker 只携带自身依赖，
输出在返回前单独保留，Graph 在返回后不继续保存输出。

Instance 的输入、State 和有效纯缓存继续保留，缓存重算前解除旧快照。
副作用节点不缓存结果，State 不重复保存 initializer 依赖。这些操作不修改共享对象，
真正内存回收仍由 Go GC 决定。Python 句柄与外部资源保持显式释放契约。

手写 Go 图的 `NewGraph()` 保留完整值表行为。使用 `NewGraphWithOptions` 启用优化时，
名称必须唯一，callback 读取的全部外层值必须声明在 Deps/Gates，值表只读且仅可访问至
最终异步结果完成。完整契约、例子与测量见[值生命周期](VALUE-LIFETIMES.md)。

## Retry、Feedback 和调度效果

编译器把 `retry(call, n)` 和受限 `feedback(initial, step, verify, n)` 降低
为 Runtime policy；`n` 始终是正整数上限，不超过 9007199254740991 且须能由 Go int 表示。取消在 `Await`、Map、Retry 和
Feedback、fold/list 和递归调用的边界传播。

Host 的 `RegisterPure`、`RegisterReadOnly` 和 `Register` 分别表示纯计算、
外部只读和外部写入。Runtime 对只读操作保留并发可能，对外部写入使用顺序
屏障。图构造者可以用 `NodeSpec.After` 增加不传值的排序约束；`NodeSpec.Effect`
用于显式覆盖运行时效果分类。Alpha 不生成完整的 Effect 类型系统。

常见计算直接使用运算符：`+ - * / // % ** */`、比较运算符和 `&& || !`。字符串用
`+` 连接，字符串重复用 `*`，需要显式转换时写 `str(value)`；
`add/mul/gt/concat/identity` 不属于 Alpha 0.6.3 默认 Host。
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
