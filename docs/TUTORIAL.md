# LIP Alpha 0.4 编程教程

这份教程从一个能运行的 Flow 开始，逐步介绍依赖、类型、门控、Host
适配器、并行调度、动态 Map、持久实例和有界控制策略。LIP 文件只描述“什么
依赖什么”；Go Runtime 决定什么时候运行。

## 0. 准备环境

需要 Go 1.27。在仓库根目录执行：

```bash
GOCACHE=/tmp/lip-gocache go test ./...
GOCACHE=/tmp/lip-gocache go run -buildvcs=false ./cmd/lipc check examples/hello.lip
GOCACHE=/tmp/lip-gocache go run -buildvcs=false ./cmd/lipc version
```

## 1. 第一个 Flow

新建 `hello.lip`：

```lip
flow Hello(request: string) {
    greeting = "Hello, " + request
    return greeting
}
```

这里有三个要点：

- `flow` 是一次可调度的入口；`request: string` 是输入及其类型；
- `greeting` 是单赋值绑定，也是一个依赖图节点；
- `return` 是 Flow 的结果。

同一个 Flow 可以在不同的 `when` 分支中有多个 `return`（同一块中 `return`
之后不能再写语句）；最终取执行成功的 return 节点中源代码位置最靠后的
那个。所有 return 都被跳过时，结果为 `nil`。

检查、生成并运行：

```bash
go run ./cmd/lipc check hello.lip
go run ./cmd/lipc hello.lip
go run Hello_generated.go
```

也可以直接安装编译命令：

```bash
go install ./cmd/lipc
lipc hello.lip
```

带 `string` 标注的入口会拒绝数字输入。没有标注的参数是 `any`，但运算符
仍不会把数字悄悄转成字符串。

## 2. 运算符和显式转换

数字使用普通运算符：

```lip
flow Arithmetic(input: number) {
    doubled = input * 2
    total = doubled + 1
    return total
}
```

字符串使用 `+` 连接，使用 `*` 重复：

```lip
flow Text(name: string) {
    line = "Hi, " + name + "!"
    rule = "-" * 20
    return line + "\n" + rule
}
```

`+`、`-`、`*`、`/`、比较和逻辑运算都有固定类型规则。需要转换时明确写
`str(value)`：

```lip
flow Describe(value: any) {
    return "value=" + str(value)
}
```

`concat`、`add`、`mul`、`gt` 等不是 Alpha 0.4 的默认函数；普通计算用
运算符，Host 函数保留给文件、网络、数据库等领域能力。

## 3. 自动依赖、等待和并行

```lip
flow Fanout(input: number) {
    left = input + 1
    right = input * 2
    total = left + right
    return total
}
```

`left` 和 `right` 都只依赖 `input`，彼此独立；`total` 自动等待两者。
不需要写 goroutine、wait、join 或 async/await。纯节点，以及用
`Host.RegisterPure` 或 `Host.RegisterReadOnly` 注册的 Host 操作，才具备自动
并行的资格。

严格顺序、自动调度和显式并行度分别对应生成代码中的：

```go
RunSequential(ctx, host, inputs)
Run(ctx, host, inputs)
RunParallel(ctx, host, inputs, 2)
```

## 4. 用 `when` 做门控

```lip
flow Gated(input: number) {
    valid = input > 0
    when valid {
        doubled = input * 2
        return doubled
    }
}
```

当 `valid` 为假时，块内节点变为 `Skipped`，不会调用 Host。块内绑定不能
逃逸到块外；这让门控值的生命周期在编译期就能检查。

## 5. 接入 Go Host Adapter

LIP 只写操作名，Go 实现操作：

```go
import (
    "context"
    "fmt"
    "os"

    "lipalpha/runtime"
)

host := runtime.DefaultHost()
host.RegisterPure("load_profile", func(ctx context.Context, args []runtime.Value) runtime.Result {
    if len(args) != 1 {
        return runtime.Failed(fmt.Errorf("load_profile expects one path"))
    }
    path, ok := args[0].(string)
    if !ok {
        return runtime.Failed(fmt.Errorf("load_profile expects a string path"))
    }
    data, err := os.ReadFile(path)
    if err != nil {
        return runtime.Failed(err)
    }
    return runtime.Ready(string(data))
})
```

库模式生成不带 `main` 的 Go 包：

```bash
go run ./cmd/lipc build examples/host_adapter/flow.lip \
  -o examples/host_adapter/flow/flow_gen.go \
  -package hostflow -no-main
```

完整的文件读取、JSON 解码和两个独立节点并行示例见
`examples/host_adapter`。

## 6. 动态 Map

列表推导式会形成一个运行时动态展开的 Map 节点：

```lip
fn twice(x: number) { return x * 2 }

flow MapNumbers(input: any) {
    values = [1, 2, 3]
    doubled = [twice(x) for x in values]
    return doubled
}
```

Map source 必须是一个标识符，运行时值必须是 slice 或 array。结果与输入
保持相同顺序；空输入返回空列表。纯元素计算可以受限并发，带普通 Host
副作用的 Map 按顺序执行。Map 是一次 Flow 调用内的能力，不会保存状态。

## 7. 持久 Flow、State 和 Tick

需要跨调用保存值时，生成包提供 `NewInstance`：

```lip
flow Counter(input: number) {
    count = state(0)
    doubled = count * 2
    return doubled + input
}
```

Go 侧创建一次实例并重复 Tick：

```go
instance, err := counter.NewInstance(host, map[string]runtime.Value{"input": 1})
value, trace, err := instance.Tick(ctx, nil)
err = instance.SetState("count", 3)
value, trace, err = instance.Tick(ctx, nil)
```

State 初值只在首个 Tick 提交；后续 Tick 会复用未受影响的纯节点。每个
Tick 都有递增的 `TickCount`，Trace 会把复用节点标记为 `Completed`/`reused`。

## 8. Retry、Feedback 和取消

Retry 是有界的：

```lip
result = retry(fetch(input), 3)
```

Agent 式验证也可以用受限 Feedback：

```lip
result = feedback(start(input), revise, verify, 3)
```

这里的 `3` 是最大验证轮数，防止执行形成无限循环。Go Host 操作接收
`context.Context`；取消或 deadline 会让节点变为 `Cancelled`，依赖它的节点
会被跳过。

## 9. Effect 和 Ordering

Host 可以声明操作的调度效果：

```go
host.RegisterReadOnly("load_profile", loadProfile)
host.Register("write_file", writeFile) // external write，顺序屏障
```

读操作可以和独立计算并发，普通 `Register` 操作按顺序执行。需要不传递值
但要求先后关系时，可在 Runtime 图中设置 `NodeSpec.After`。完整边界见
[ALPHA-0.3-SPEC.md](ALPHA-0.3-SPEC.md) 和
[ALPHA-0.4-SPEC.md](ALPHA-0.4-SPEC.md)。

## 10. 如何定位错误

- `lipc check file.lip`：检查语法、名称、作用域、类型和依赖；
- 未定义名、前向引用、重复绑定、循环依赖：编译期错误；
- 标注的输入类型：生成入口检查；
- Host 错误、除零、错误的动态值：运行时返回错误并记录 Trace；
- `RunSequential`：需要复现问题时使用确定性顺序执行。

语言的 Map 定义见 [ALPHA-0.2-SPEC.md](ALPHA-0.2-SPEC.md)，State/Tick
见 [ALPHA-0.3-SPEC.md](ALPHA-0.3-SPEC.md)，Retry、Feedback、取消和
Effect/Ordering 见 [ALPHA-0.4-SPEC.md](ALPHA-0.4-SPEC.md)；编译器和生成
代码说明见 [COMPILER.md](COMPILER.md)。
