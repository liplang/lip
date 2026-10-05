# LIP Alpha 0.4 快速入门

LIP Alpha 0.4 需要 Go 1.27。

查看当前编译器实现版本：

```bash
GOCACHE=/tmp/lip-gocache go run -buildvcs=false ./cmd/lipc version
```

## 1. 验证仓库

```bash
GOCACHE=/tmp/lip-gocache go test ./...
GOCACHE=/tmp/lip-gocache go vet ./...
GOCACHE=/tmp/lip-gocache go build -buildvcs=false ./...
```

## 2. 写一个 Flow

```lip
flow Hello(request: string) {
    greeting = "Hello, " + request
    return greeting
}
```

保存为 `hello.lip`，先检查：

```bash
go run ./cmd/lipc check hello.lip
```

像 `go build` 一样生成可执行文件：

```bash
go run ./cmd/lipc build hello.lip
```

像 `go run` 一样临时生成并运行：

```bash
go run ./cmd/lipc run hello.lip
```

需要审查或提交生成的 Go 时显式使用 `-emit-go`：

```bash
go run ./cmd/lipc build -emit-go -o hello_generated.go hello.lip
```

直接传 `.lip` 文件仍保留为生成源码的兼容简写：

```bash
go run ./cmd/lipc hello.lip
```

如果已经安装命令，也可以省略 `go run ./cmd/lipc`：

```bash
go install ./cmd/lipc
lipc hello.lip
```

字符串连接使用普通的 `+`，`return` 是 Flow 的结果。生成文件可以直接
编译运行：

```bash
go run hello_generated.go
```

`request: string` 会让生成的入口检查输入类型；传入数字不会被偷偷转成
字符串，而是返回错误。确实需要显式转换时写 `str(value)`：

```lip
flow Describe(value: any) {
    return "value=" + str(value)
}
```

## 3. 函数、列表和字段

```lip
fn twice(x: number) {
    return x * 2
}

flow Example(input: any) {
    values = [1, 2, 3]
    selected = values[1]
    result = twice(selected)
    return result
}
```

`fn` 是局部表达式计算；Flow 中的绑定才是依赖图节点。数字使用 `+ - * /`、
比较使用 `> >= < <= == !=`，逻辑使用 `&& ||`。`print(value)` 是
普通的 Go Host 操作，用来产生控制台副作用；它不负责返回 Flow 结果。

## 4. 使用 Go Host Adapter

LIP 只描述操作名称，Go 负责实现真实能力：

```go
import (
    "context"
    "encoding/json"
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
    var profile map[string]any
    if err := json.Unmarshal(data, &profile); err != nil {
        return runtime.Failed(err)
    }
    return runtime.Ready(profile)
})
```

使用库模式生成 Flow：

```bash
go run ./cmd/lipc examples/host_adapter/flow.lip \
  -o examples/host_adapter/flow/flow_gen.go \
  -package hostflow -no-main
go run ./examples/host_adapter
```

完整示例见 [examples/host_adapter](../examples/host_adapter)。

## 5. 自动调度与有限并行

默认的 `Run` 会自动调度独立纯节点和只读节点。只有明确标记为纯/只读的操作
才具备并行资格：

```go
value, trace, err := hostflow.Run(ctx, host, inputs)
```

需要显式控制最大并行度时使用：

```go
value, trace, err := hostflow.RunParallel(ctx, host, inputs, 2)
```

普通 `host.Register` 操作保持顺序执行；严格基准模式使用
`hostflow.RunSequential(...)`。

## 6. 动态 Map

Alpha 0.4 支持受限列表推导式：

```lip
fn twice(x: number) { return x * 2 }

flow MapNumbers(input: any) {
    values = [1, 2, 3]
    doubled = [twice(x) for x in values]
    return doubled
}
```

Map 的输入必须是 Go slice 或 array，输出保持输入顺序。`RunSequential`
逐个处理；`Run` 和 `RunParallel` 对纯 Map 元素使用调用方提供的并发上限。
Map 不引入持久状态、反馈或事件语义。完整边界见
[ALPHA-0.2-SPEC.md](ALPHA-0.2-SPEC.md)、[ALPHA-0.3-SPEC.md](ALPHA-0.3-SPEC.md)
和 [ALPHA-0.4-SPEC.md](ALPHA-0.4-SPEC.md)。
