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

## 两种生成模式

### 可执行模式

默认生成 `package main` 和一个示例 `main`：

```bash
go run ./cmd/lipc build -o generated.go examples/hello.lip
go run generated.go
```

生成文件暴露：

```go
func Run(context.Context, runtime.Host, map[string]runtime.Value) (runtime.Value, []runtime.TraceEvent, error)
func RunSequential(context.Context, runtime.Host, map[string]runtime.Value) (runtime.Value, []runtime.TraceEvent, error)
func RunParallel(context.Context, runtime.Host, map[string]runtime.Value, int) (runtime.Value, []runtime.TraceEvent, error)
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

## Graph 节点粒度

下面的 LIP：

```lip
a = load_a()
b = load_b()
c = combine(a, b)
```

生成三个图节点：`a`、`b`、`c`。`a` 和 `b` 独立，`c` 等待两者。
`fn` 内部的局部表达式不是图节点，避免 Runtime 被普通算法的细节淹没。
副作用调用可以直接写成语句，例如 `print(value)`；它会成为一个没有
Flow 返回值的图节点。普通的无用表达式不允许单独出现。

常见计算直接使用运算符：`+ - * /`、比较运算符和 `&& ||`。字符串用
`+` 连接，字符串重复用 `*`，需要显式转换时写 `str(value)`；
`add/mul/gt/concat/identity` 不属于 Alpha 0.1 默认 Host。
`==` 和 `!=` 对静态已知的不同类型会在编译期拒绝；`any` 值的具体类型
由 Runtime 在执行时判断。

## 并行规则

生成程序的 `RunSequential` 是参考语义：确定性、顺序执行。默认的 `Run`
会自动使用有界并行调度；需要显式控制并行度时使用 `RunParallel`。

自动调度和 `RunParallel` 只并行：

- 没有未完成依赖的节点；
- `Pure` 节点；或
- 对应 Host operation 通过 `RegisterPure` 注册的节点。

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
