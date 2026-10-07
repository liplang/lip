# LIP Alpha 0.6.1 快速入门

需要 Go 1.27 或更高版本。

## 安装

在仓库根目录安装工具：

```bash
go install ./cmd/lipc
```

安装位置跟随 Go 配置：GOBIN 非空时用 GOBIN，否则用第一个 GOPATH 目录下的
`bin`。可用 `go env GOBIN GOPATH` 查看实际位置。

工具目录已在 PATH 中时，直接运行：

```bash
lipc help
```

也可以直接用安装路径，无需修改 PATH。未自定义 Go 配置时，Linux/macOS
常见位置是 `~/go/bin/lipc`，例如：

```bash
~/go/bin/lipc help
```

Windows 的常见位置是用户目录下的 `go\bin\lipc.exe`。未自定义时，PowerShell
可直接调用：

```powershell
& "$env:USERPROFILE\go\bin\lipc.exe" help
```

已有 GOBIN/GOPATH 配置时，使用自己的实际安装路径即可。后文用 `lipc` 表示
工具，可以按习惯使用命令名或完整路径；示例文件路径以仓库根目录为基准。
编译器内置 Runtime，在自己的项目里也能 `run/build`，无需保留仓库或创建
`go.mod`。构建仍需 Go；编译出的核心程序无需 Go。

工具选项放在文件前，程序输入放在文件后：`lipc run hello.lip 小林`，或
`lipc run --trace trace.json hello.lip 小林`。按这个顺序传入即可。

## 1. 先打个招呼

```bash
lipc run examples/tutorial/01_hello.lip 小林
# 你好，小林！
```

它的完整源码只有几行：

```lip
flow Hello(name: string) -> string {
    greeting = "你好，" + name + "！"
    return greeting
}
```

`小林` 是 name 的输入，结果直接显示在终端。按顺序传入声明的参数即可。逐步解释见[编程教程](TUTORIAL.md)。

## 2. 列表变换与聚合

[examples/core.lip](../examples/core.lip) 接收列表，变换、聚合并返回对象：

```lip
fn add(total: number, value: number) -> number { return total + value }

flow Report(values: list) -> object {
    doubled = [x * 2 for x in values]
    total = fold(doubled, 0, add)
    return {
        count: len(values), values: doubled, total: total,
        average: if len(values) == 0 { null } else { total / len(values) }
    }
}
```

```bash
lipc check examples/core.lip
lipc run examples/core.lip '[1,2,3]'
# {"average":4,"count":3,"total":12,"values":[2,4,6]}
lipc run examples/core.lip '[]'
# {"average":null,"count":0,"total":0,"values":[]}
```

参数类型和 Flow 输出写在声明中，`return` 给出结果。`list`、`object` 和 `any`
输入使用 JSON；string 原样传入，number 使用有限十进制数，bool 使用
true/false。列表与对象检查外层形状，动态元素在运算中校验。

## 3. 区间、Map 与聚合

```bash
lipc run examples/range.lip 5
# {"total":30,"values":[0,1,4,9,16]}
```

`range(start, end[, step])` 使用半开区间，允许负步长；0 步长或超过 1,000,000
个元素报错。Map 推导式先绑定区间；fold/list.* 可嵌套纯表达式。`fold(list, seed, fn)`
按输入顺序归约，fn 是本地二参数纯函数；空列表返回 seed。

## 4. 分组、合并和转置

`list.*` 标准库提供 34 个纯操作，不需要 Host。比如：

```lip
list.concat([1, 2], [3, 4])
list.transpose([[1, 2], [3, 4]])
list.partition([1, 2, 3], 2, 1)
```

callback 使用本地纯函数：`list.group_by(rows, department)`、
`list.filter(rows, positive)`、`list.sort_by(rows, amount)`。完整程序见
[examples/lists.lip](../examples/lists.lip)，签名和边界见
[LIST-LIBRARY.md](LIST-LIBRARY.md)。

## 5. 字符串与修复诊断

文本处理无需 Host：

```bash
lipc check --json examples/strings.lip
lipc run examples/strings.lip ' Rust, LIP, rust, ,你好 '
# {"count":3,"label":"rust / lip / 你好","tags":["rust","lip","你好"]}
```

19 个 string 操作覆盖清理、分割、合并、查询、替换和数值解析；错误业务分支
用 fail(message)。细致的逐步教程见 [TUTORIAL.md](TUTORIAL.md)，AI 编写/修复
约定见 [VIBE-CODING.md](VIBE-CODING.md)。

## 6. 纯递归

```lip
fn factorial(n: number) -> number {
    return if n <= 1 { 1 } else { n * factorial(n - 1) }
}
```

直接或间接递归环中的函数必须显式声明返回类型。每次本地调用检查取消，最大
深度 256。树结构的可运行示例：

```bash
lipc run examples/tree.lip '{"value":1,"left":{"value":2,"left":null,"right":null},"right":null}'
# 3
```

集合遍历用 Map/fold，树与分治用递归。

## 7. 看图和错误

```bash
lipc inspect examples/core.lip
lipc run --trace core-trace.json examples/core.lip '[1,2,3]'
lipc run --trace error-trace.json examples/core.lip '[1,"bad"]'
```

inspect 只检查并输出 `lip.graph.v1`，不执行 Host。trace 的 `lip.trace.v1` 记录
节点状态、Tick 和原因，成功与执行失败都写到指定文件；输入或编译失败尚未
执行图。相对 trace 路径相对于调用 lipc 的目录。run 原样保留程序的退出码，
正常结果仍输出 stdout。

## 8. 构建与外部能力

```bash
lipc build examples/core.lip
./Report '[1,2,3]'
```

默认以 Flow 名称生成可执行文件；Windows 下生成 `Report.exe`，使用
`.\Report.exe '[1,2,3]'` 运行。用 `--output 路径` 可以另选文件名或输出目录。
Go 源码生成与库模式见[编译器文档](COMPILER.md#两种生成模式)。

外部能力通过 `require` 声明。Host/Go 程序使用生成库和 adapter；Python
程序使用常驻 Worker。详见
[COMPILER.md](COMPILER.md)、[Host 示例](../examples/host_adapter) 和
[Python 集成](PYTHON-INTEGRATION.md)。

完整语言规则见[规范](ALPHA-0.6-SPEC.md)。运行仓库验证：

```bash
bash scripts/verify-release.sh
```
