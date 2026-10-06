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

`AST` 表示用户写了什么；`Graph` 表示计算依赖什么。两者不能混为一谈。

参数可以写显式类型：`flow Hello(request: string) { ... }`。编译器会在
可确定时拒绝不相容的运算（例如 `1 + "x"`），生成的 Go 入口还会调用
`runtime.CheckType` 校验输入；没有标注的参数是 `any`，但运算符仍不会
隐式转换字符串和数字。

## `lipc` 命令

CLI 提供接近 Go 工具链的最短路径：

```bash
lipc version
lipc help [command]
lipc check hello.lip
lipc build hello.lip                 # 编译可执行文件
lipc run hello.lip -- Alice          # 临时编译并运行，Alice 作为第一个输入
lipc build -emit-go -o hello.go hello.lip
```

`build` 只有在使用 `-emit-go` 或输出路径以 `.go` 结尾时才写 Go 源文件；否则
它调用 Go 工具链生成可执行文件。`lipc file.lip` 保留为生成源码的兼容简写。

生成的可执行入口严格按照 `flow` 参数声明解析命令行：参数按声明顺序传入，不能
缺少或多传。`string` 原样传递，`number` 是有限十进制数，`bool` 只能是
`true`/`false`，`any` 必须传 JSON。生成程序不会读取 `LIP_INPUT`，也不会注入
默认值；这保证 `lipc` 只翻译和执行源程序实际声明的输入。

## 两种生成模式

### 可执行模式

默认生成 `package main` 和一个示例 `main`：

```bash
go run ./cmd/lipc build -o generated.go examples/hello.lip
go run generated.go Alice
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
go run ./cmd/lipc build \
  -o flow/flow_gen.go \
  -package hostflow -no-main flow.lip
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
require python "numpy>=1.26"
require python "scipy"
require go "github.com/acme/adapter"
require host "load_profile"
```

`require` 是可审计的元数据，不是 LIP 内的 Python/Go `import`，也不会触发安装。
`lipc check` 会逐条打印声明；生成库提供 `RequiredDependencies()`，由部署程序
据此检查 Python 环境、Go module 和 Host 注册。Python 依赖按包/版本写出，调用仍
使用真实模块路径（如 `sklearn.preprocessing.scale`）；Worker 继续允许任意已安装
库，是否限制模块由 `AllowedModules`/`DeniedModules` 决定。Go 包必须由承载生成包
的 Go 程序导入和注册，LIP 编译器不会凭一个声明猜测 adapter 实现。

编译器还会检查外部调用是否有对应声明：dotted 调用默认要求匹配的 Python 模块，
bare 调用要求 `require host`；内置 `str`、`print`、`state`、`retry`、`feedback`
和 Python 控制面操作属于语言/Runtime 边界。缺少声明在 `check` 阶段报错。

包含 `require python` 的独立可执行程序会在 `main` 中启动默认 Python Worker；没有
这条声明时，生成入口只使用 `runtime.DefaultHost()`。库模式始终由宿主显式创建
Worker 和 Host，这样 Python 进程的解释器、策略和生命周期不会被藏在库初始化中。
声明 `require host` 或 `require go` 的程序必须使用库模式；独立入口会明确报错，
不会假装已经拥有未注册的 adapter。

调用参数当前使用普通位置参数。为了保持 Graph 节点边界清晰，参数中的函数调用
需要先绑定到一个节点，再作为下一个调用的输入；例如先写
`tensor = torch.tensor(values)`，再写 `relu = torch.nn.functional.relu(tensor)`。
关键字参数语法、嵌套调用和大对象零拷贝传输分别属于后续语言/数据面工作，复杂
Python 返回值可以先通过 Worker 句柄和 `python.to_json` 取回。

## Graph 节点粒度

下面的 LIP：

```lip
require host "load_a"
require host "load_b"
require host "combine"

a = load_a()
b = load_b()
c = combine(a, b)
```

生成三个图节点：`a`、`b`、`c`。`a` 和 `b` 独立，`c` 等待两者。
Flow 参数在 `flow (...)` 中声明；普通局部变量使用一次 `name = expression`
绑定即声明，并且是单赋值。未定义名、前向引用、重复绑定和从 `when` 块逃逸的
绑定都会在 `lipc check` 阶段报错，不存在运行时隐式变量注入。
`fn` 内部的局部表达式不是图节点，避免 Runtime 被普通算法的细节淹没。
副作用调用可以直接写成语句，例如 `print(value)`；它会成为一个没有
Flow 返回值的图节点。普通的无用表达式不允许单独出现。

Alpha 0.2 的列表推导式保持同样的边界：

```lip
values = [1, 2, 3]
doubled = [x * 2 for x in values]
```

`doubled` 是一个静态 Graph 节点，运行时再按 `values` 的元素数量动态
展开。Map 输出保持输入顺序；空输入返回空列表。`RunSequential` 逐个执行，
并行运行时只对纯元素计算或全部使用 `RegisterPure`/`RegisterReadOnly` 的 Host 调用并发，且
受 `RunParallel` 的上限约束。Map source 目前必须是一个标识符，嵌套
comprehension 和普通 `for`/`while` 尚未进入语言核心。

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
Feedback 的等待边界传播。

Host 的 `RegisterPure`、`RegisterReadOnly` 和 `Register` 分别表示纯计算、
外部只读和外部写入。Runtime 对只读操作保留并发可能，对外部写入使用顺序
屏障。图构造者可以用 `NodeSpec.After` 增加不传值的排序约束；`NodeSpec.Effect`
用于显式覆盖运行时效果分类。Alpha 不生成完整的 Effect 类型系统。

常见计算直接使用运算符：`+ - * /`、比较运算符和 `&& ||`。字符串用
`+` 连接，字符串重复用 `*`，需要显式转换时写 `str(value)`；
`add/mul/gt/concat/identity` 不属于 Alpha 0.4 默认 Host。
`==` 和 `!=` 对静态已知的不同类型会在编译期拒绝；`any` 值的具体类型
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
