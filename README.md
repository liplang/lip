# LIP Alpha 0.5

[English README](README.en.md)

当前参考实现版本：`0.5.0`（见 [VERSION](VERSION)）。

LIP（Logical / Incremental / Parallel）是一门面向依赖关系的语言。它的根本思想
只有一句话：

> **描述依赖，让 Runtime 决定执行。**

你用接近普通现代语言的绑定、函数和表达式写出“结果依赖什么”，编译器把它们
转换成 Dependency Graph，Runtime 再根据数据依赖、门控、状态变化、取消和效果
约束安排执行：

```lip
user = load_user(id)
orders = load_orders(id)
answer = combine(user, orders)
return answer
```

`user` 和 `orders` 没有彼此依赖，因此具备并发执行的可能；`answer` 自动
等待二者。源代码顺序用于定义和诊断，不是强制的执行顺序。最终是否并行仍由
Runtime 的资源限制、Host 效果和 Ordering 约束决定。

## LIP 的计算模型

LIP 把程序分成两个相互连接的层次：

- **源代码层**表达绑定、函数调用、值选择和执行门控，保持普通代码的可读性；
- **Runtime 层**维护图节点、依赖快照、Logical Tick、取消状态和效果元数据。

由此得到几条稳定的语义边界：

- **依赖是语义。** 变量引用自然形成边，fan-out、fan-in 和 join 不需要额外的
  管道语法。
- **源代码顺序不是调度命令。** 独立节点可以并行；需要顺序时由数据依赖、
  `NodeSpec.After` 或 Host effect 明确表达。
- **静态结构可以动态展开。** `[expr for item in source]` 在图中是一个 Map
  节点，运行时按实际集合展开，结果保持输入顺序并受并发上限约束。
- **State 通过 Tick 增量传播。** 持久 `Instance` 保存 State 和依赖快照；纯节点
  在输入未变化时可以复用，外部读写则按其效果重新执行。
- **Runtime 拥有执行策略。** 并发、等待、重试、取消和资源限制属于运行时决策，
  语言不要求程序员手写 goroutine、线程池、join 或隐式后台任务。
- **Host 是边界。** 文件、网络、数据库和科学计算通过 Go Host Adapter 接入；
  语言核心因此保持可检查，部署方也能决定权限、超时和隔离。

这套设计的先进性可以用可验证的性质来描述，而不是用“自动变快”来承诺：

1. 在满足 Effect/Ordering 声明时，同一份依赖结构可以由顺序调度或有界并行调度
   执行，结果语义保持一致；
2. State、Tick、缓存命中和取消原因可以通过 Trace 观察；
3. Dynamic Map 不需要为每个运行时元素生成静态节点；
4. 纯计算、只读外部访问和外部写入有不同的缓存与排序规则；
5. 生成的是普通 Go，能够使用 Go 的编译、测试、部署和故障诊断工具。

## 编写体验

LIP 让常见 Flow 保持接近普通代码：

```lip
flow Hello(request: string) -> string {
    greeting = "Hello, " + request
    return greeting
}
```

你只需要声明计算关系，不需要为每个独立步骤设计异步 API。需要接入真实能力
时，在 Go 中注册 Host operation：

```go
host.RegisterPure("load_profile", loadProfile)
host.RegisterReadOnly("load_model", loadModel)
host.Register("write_file", writeFile)
```

编译器负责语法、名称、类型和依赖图检查；Runtime 负责执行。出现问题时可以
先在 `check` 阶段发现结构错误，再用 Trace 查看节点状态、Tick 和复用原因。

## 当前能力

| 能力 | Alpha 0.5 状态 |
| --- | --- |
| `flow`、表达式 `fn`、单赋值绑定 | 支持，带名称、作用域和类型检查 |
| Flow 输入/输出契约 | 显式输入/输出类型，单一 `return`，可选门控输出，不注入默认值 |
| `require` 依赖头 | 支持 Python/Go/Host 元数据、检查输出和生成库查询 |
| `when` 执行门控、`if` 值选择 | 支持，语义明确区分 |
| Dynamic Map | 支持运行时展开、稳定顺序、有界并发 |
| 持久 Flow / State / Tick | 支持 `NewInstance`、`Tick`、`SetState`、Trace |
| 增量重算 | 支持纯节点依赖缓存；外部读写按效果重新执行 |
| Retry / Feedback | 支持有明确上限的 Runtime policy |
| Cancellation | 支持 Context、Await、Map、Retry、Feedback 传播 |
| Effect / Ordering | 支持 Host 效果分类和 `NodeSpec.After` |
| Python Worker Adapter | 支持常驻 JSONL Worker、动态 Python 库调用、超时取消和重启 |
| 普通 `for` / `while`、事件/流 DSL | 尚未进入 Alpha 语言核心 |

每个里程碑的边界都有单独规范：
[规范索引](docs/SPECS.md)汇总了
[ALPHA-0.1-SPEC.md](docs/ALPHA-0.1-SPEC.md)、
[ALPHA-0.2-SPEC.md](docs/ALPHA-0.2-SPEC.md)、
[ALPHA-0.3-SPEC.md](docs/ALPHA-0.3-SPEC.md) 和
[ALPHA-0.4-SPEC.md](docs/ALPHA-0.4-SPEC.md) 和
[ALPHA-0.5-SPEC.md](docs/ALPHA-0.5-SPEC.md)。

## lipc：像 Go 一样检查、编译和运行

安装命令后：

```bash
go install ./cmd/lipc
```

也可以在仓库中用 `go run ./cmd/lipc`。常用命令如下：

```bash
# 检查语法、名称、类型和依赖图
lipc check examples/hello.lip

# 像 go build 一样生成可执行文件；默认输出为 Flow 名称
lipc build examples/hello.lip
lipc build -o /tmp/hello examples/hello.lip

# 像 go run 一样临时编译并执行；-- 后的参数传给生成程序
lipc run examples/hello.lip -- Alice

# 需要查看或提交生成的 Go 时显式输出源码
lipc build -emit-go -o hello_generated.go examples/hello.lip

# 生成没有 main 的库包
lipc build -emit-go -no-main -package hostflow \
  -o flow/flow_gen.go flow.lip

# 查看帮助和版本
lipc help
lipc help build
lipc version
```

`lipc build` 默认编译可执行文件；`-emit-go` 或以 `.go` 结尾的输出路径
生成 Go 源码。旧的 `lipc file.lip` 简写仍然保留，用于直接生成源码。

可执行入口只接受 Flow 声明的参数，按声明顺序绑定；缺少参数或多传参数都会失败。
`string` 原样传递，`number` 必须是有限十进制数，`bool` 只能写 `true` 或 `false`，
`any` 必须写成 JSON。编译器不会读取 `LIP_INPUT`，也不会注入 `World`、`1` 或
`false` 之类的默认值，因此示例和生成程序的结果完全由源代码和输入决定。

`flow Name(args) -> Type` 是完整边界；无输入时写 `()`，动态值写 `any`。每个 Flow
恰好一个 `return`；条件值用 `if ... then ... else ...`，允许门控无值完成时写
`Type?`。局部 `fn` 是可组合的纯表达式函数，外部调用形成明确的 Flow 节点。

旧源文件可用 `lipc migrate old.lip -o migrated.lip` 更新，迁移不补值或补 return。
字符串结果原样输出，其他结果输出 JSON。生成的库提供：

```go
Run(ctx, host, inputs)
RunSequential(ctx, host, inputs)
RunParallel(ctx, host, inputs, limit)
NewInstance(host, inputs)
RequiredDependencies()
```

LIP 文件可以在头部显式记录外部依赖：

```lip
require python "numpy>=1.26"
require python "pandas"
require go "github.com/acme/adapter"
require host "load_profile"
```

这些声明是检查和部署元数据，不会自动安装、导入或替换环境。`lipc check` 会
显示依赖，生成库提供 `RequiredDependencies()`。Python Worker 仍按真实模块路径
动态调用任意已安装库；Go 包由承载生成包的 Go 程序导入并注册 Host adapter。
外部调用没有对应 `require` 声明会在检查阶段失败，避免源文件留下隐式能力。

## 持久 State 和 Logical Tick

```lip
flow Counter(input: number) -> number {
    count = state(0)
    doubled = count * 2
    return doubled + input
}
```

```go
instance, err := counter.NewInstance(host, map[string]runtime.Value{"input": 1})
value, trace, err := instance.Tick(ctx, nil)

err = instance.SetState("count", 3)
value, trace, err = instance.Tick(ctx, nil)
```

State 初值在第一次 Tick 提交；后续 Tick 复用未受影响的纯节点。每个 Tick 有递增
的逻辑计数，`TraceEvent` 会记录节点状态和 `reused` 原因，便于解释 Runtime
为什么执行或跳过某个计算。Instance 的执行和观察方法会串行化；Host callback
应避免重入同一个 Instance。

## Go Host 与 Python 路线

LIP 不在语言中执行 Go 或 Python `import`；需要的包可以用 `require` 元数据写在
文件头。Python 科学计算通过 Host Adapter
接入：

```text
LIP node → Go Adapter → Python Worker → installed Python libraries
```

当前已经提供 `runtime.NewProcessHost`（`PythonWorker` 别名）和
`runtime.NewPythonHost`：Go 用 `os/exec` 启动一次 Python
子进程，让它常驻内存，通过 stdin/stdout 管道传 JSONL，并负责 request ID、超时、
取消、重启、背压和效果分类，Python 负责科学计算。`examples/python` 展示了在
LIP Flow 中调用 NumPy/Pandas 并取回结果；Worker 按 `module.submodule.callable`
动态调用已安装的 Python 库，因此新增 PyTorch、JAX、Scikit-learn、Transformers
等库不需要修改 LIP 核心。标准库 Worker 也提供 `sum`、`mean`、`dot` 和矩阵乘法
基线。P2 已提供带大小、SHA-256、dtype 和 shape 元数据的本机只读 blob/mmap 基线；
后续根据真实基准评估 Arrow 或共享内存数据面，控制消息只携带句柄和形状等元数据。
只有基准证明进程边界不可接受时，才评估 Unix socket/gRPC 或嵌入 CPython。

完整的路线比较、协议约束、数据通道和阶段门槛见
[docs/PYTHON-INTEGRATION.md](docs/PYTHON-INTEGRATION.md)。这个边界让没有 Python
的环境仍可编译 LIP，也避免把 GIL、Python 包管理和模型服务生命周期塞进语言核心。

## 文档入口

- [docs/QUICKSTART.md](docs/QUICKSTART.md)：从检查到生成和运行；
- [docs/TUTORIAL.md](docs/TUTORIAL.md)：语言和 Runtime 使用教程；
- [docs/COMPILER.md](docs/COMPILER.md)：AST、Graph、Scheduler 和生成代码；
- [docs/ROADMAP.md](docs/ROADMAP.md)：下一阶段的实现顺序和验收条件；
- [docs/PYTHON-INTEGRATION.md](docs/PYTHON-INTEGRATION.md)：Python 方案比较与协议；
- [docs/CONSISTENCY-AUDIT.md](docs/CONSISTENCY-AUDIT.md)：实现、规范与设计讨论的交叉审计；
- [docs/SPECS.md](docs/SPECS.md)：各 Alpha 里程碑的规范索引；
- [docs/ALPHA-0.5-SPEC.md](docs/ALPHA-0.5-SPEC.md)：当前完整程序契约与迁移门槛；
- [examples/PROTOTYPES.md](examples/PROTOTYPES.md)：30 个原型的覆盖情况；
- [CHANGELOG.md](CHANGELOG.md)：版本变更和发布检查。

## 验证实现

```bash
GOCACHE=/tmp/lip-gocache go test ./...
GOCACHE=/tmp/lip-gocache go test -race ./...
GOCACHE=/tmp/lip-gocache go vet ./...
GOCACHE=/tmp/lip-gocache go build -buildvcs=false ./...
```

参考实现目标为 Go 1.27。Alpha 仍然是实验版本；事件/流、普通循环、完整
Effect 类型系统和直接执行 Python/Go `import` 会在后续里程碑中根据 conformance
和真实基准决定；当前使用 `require` 元数据配合 Python Host 的通用 dotted call。
