# LIP Alpha 0.1 编程教程

这份教程从一个能运行的 Flow 开始，逐步介绍依赖、类型、门控、Host
适配器和并行调度。LIP 文件只描述“什么依赖什么”；Go Runtime 决定
什么时候运行。

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

`concat`、`add`、`mul`、`gt` 等不是 Alpha 0.1 的默认函数；普通计算用
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
不需要写 goroutine、wait、join 或 async/await。只有纯节点，或用
`Host.RegisterPure` 注册的 Host 操作，才会被自动并行。

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

## 6. 如何定位错误

- `lipc check file.lip`：检查语法、名称、作用域、类型和依赖；
- 未定义名、前向引用、重复绑定、循环依赖：编译期错误；
- 标注的输入类型：生成入口检查；
- Host 错误、除零、错误的动态值：运行时返回错误并记录 Trace；
- `RunSequential`：需要复现问题时使用确定性顺序执行。

语言的完整定义见 [ALPHA-0.1-SPEC.md](../ALPHA-0.1-SPEC.md)，编译器和
生成代码说明见 [COMPILER.md](COMPILER.md)。
