# LIP Alpha 0.1 快速入门

LIP Alpha 0.1 需要 Go 1.27。

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

最短构建方式是直接把 `.lip` 文件作为参数，输出默认为同目录下的
`Hello_generated.go`：

```bash
go run ./cmd/lipc hello.lip
```

生成 Go：

```bash
go run ./cmd/lipc build -o hello_generated.go hello.lip
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

完整示例见 [examples/host_adapter](examples/host_adapter)。

## 5. 自动调度与有限并行

默认的 `Run` 会自动调度独立纯节点。只有明确标记为纯操作的节点才会并行：

```go
value, trace, err := hostflow.Run(ctx, host, inputs)
```

需要显式控制最大并行度时使用：

```go
value, trace, err := hostflow.RunParallel(ctx, host, inputs, 2)
```

普通 `host.Register` 操作保持顺序执行；严格基准模式使用
`hostflow.RunSequential(...)`。
