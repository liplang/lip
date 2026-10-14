# LIP beta 0.9

[简体中文](README.md) | [English](README.en.md)

LIP 是一个小的计算语言，用来组织计算、并行任务和工具调用。你写清楚输入、依赖和结果；运行时把程序组成依赖图，让可以同时做的工作并行执行。

我们只做语言核心。文件、网络、数据库、模型和其他库能力，主要使用 **Go / Python 生态**，通过统一的外部调用通道接入。

## 三个值得了解的机制

**基于图的并行。** 一个计算用到另一个计算的结果，就要等它完成。没有依赖、且允许并发的工作可以同时执行。这样组织多个分析任务时，不必自己维护线程、任务队列和等待计数。

**Claim 汇聚事实。** 并行任务用 `<+` 提交结果，已有事实不会被覆盖。完整读取 Claim 会等相关任务完成，再得到按程序逻辑位置排列的记录。每条记录带着值和来源位置，便于汇总结果、检查缺失证据和追查结论。

**effect / perform 调用外部能力。** `effect` 创建请求，`perform` 才执行。所有 Go、Python 和 Host 操作都经过这一条通道，统一检查参数、返回值、权限和调用记录。请求可以先保存，再作为下一步的输入；`import` 本身不执行原生代码。

```lip
import go "strings" as text

flow Analyze() -> list {
    facts = claim()                 # 创建事实汇聚点
    for name in ["ada", "lin", "sam"] parallel {
        # Host 已注册这个 Go 操作，并声明它允许并发读取。
        result = perform effect text.ToUpper(name)
        facts <+ {input: name, result: result}  # 追加一条事实
    }
    # 读取 facts 等待贡献完成；row.value 是提交的内容。
    return [row.value for row in facts]
}
```

不可变值、纯函数、条件、携带值的循环，再加上这三个机制，就能搭出“思考 → 调用工具 → 观察结果 → 再决定”的 **ReAct** 程序。响应式流程也可以写成“收到事件 → 运行计算 → 汇聚结果 → 执行动作”。一次运行内的依赖由图管理；持续接收事件、启动下一次运行由 Host 管理。当前的 Claim 快照是一次读取，没有自动订阅或阈值触发。

## 开始使用

需要 Go **1.25 或更高版本**；没有固定某个补丁版本，也没有 `toolchain` 版本锁。本轮验证使用 Go 1.27。基础教程的 Python 示例只需 Python 3 标准库；科学计算示例还需安装对应的第三方包。

在 beta 项目根目录（含 `go.mod` 的目录）安装命令：

```sh
go install ./cmd/lipc
```

Go 将 `lipc` 放到 `go env GOBIN` 指定的目录；GOBIN 为空时，默认放到 GOPATH 的 `bin` 目录，通常为 `~/go/bin`。把该目录加入 PATH 后，就可以在任意目录启动交互工具：

```sh
export PATH="$(go env GOPATH)/bin:$PATH"  # 使用默认安装目录时
lipc help
lipc repl
lipc learn
lipc learn -lesson power
lipc run /path/to/program.lip
lipc build -o /path/to/program /path/to/program.lip
```

也可以选择自己的安装目录，例如 Linux/macOS 下：

```sh
mkdir -p "$HOME/.local/bin"
GOBIN="$HOME/.local/bin" go install ./cmd/lipc
export PATH="$HOME/.local/bin:$PATH"
```

需要长期使用时，把相应的 PATH 设置加入自己的 shell 配置文件。Windows 下将安装目录加入用户 PATH，命令名为 `lipc.exe`。REPL 和 learn 已内置运行时及课程，安装后使用这两个命令不需要保留源码目录。

当前 `go.mod` 的模块名为 `lipbeta`，以上是从本地源码安装的方式；远程 `go install 仓库路径/cmd/lipc@版本` 的地址将在确定发布模块路径后提供。

如果链接时报 `mapping output file failed: no space left on device`，通常是 Go 构建使用的临时目录空间或 inode 不足。请自行清理该目录中确认已不用的缓存后重试，或指定有空间的临时目录。此时 lipc 尚未运行，无法自动清理或在这条 Go 命令的错误中追加项目提示。

先检查实际构建位置；下面以默认临时目录 `/tmp` 为例，设置了 `GOTMPDIR` 或 `TMPDIR` 时应检查对应目录：

```sh
df -h . /tmp
df -i . /tmp
go env GOTMPDIR GOCACHE
```

`/tmp` 可能是独立且容量较小的 tmpfs。Go 在临时目录链接后才复制到安装目录，因此安装目标所在磁盘有空间仍可能失败。`go clean -cache` 只清理 `go env GOCACHE` 指向的构建缓存；如果它不在已满的临时文件系统上，就不能释放那里占用的空间。其他 Go 构建缓存应在确认目录及用途后，用 `GOCACHE=/已确认的缓存目录 go clean -cache` 清理；保留正在使用的构建目录和其他文件。

也可以在当前终端指定一个有空间的可写临时目录，再安装：

```sh
mkdir -p .build/tmp
export GOTMPDIR="$PWD/.build/tmp"
export TMPDIR="$GOTMPDIR"
go install ./cmd/lipc
```

这些环境变量只影响当前终端及其子进程；单独执行 `go install` 不会读取 Makefile 的设置。`make build` 自动使用项目内的临时目录。运行自动 Go adapter 或 `lipc build` 时也会使用临时目录，若默认目录仍满，可继续使用上述环境变量。`make clean` 会删除 `.build`，继续使用该配置前需重新创建临时目录；也可换为其他有空间的可写磁盘目录。

在源码目录试跑示例，可以使用项目内构建：

```sh
make build
./lipc run examples/basics/01_hello.lip
./lipc run -parallel 4 examples/evidence.lip
./lipc run -host demo examples/ecosystem/17_go_python.lip
```

输出分别是：

```text
"Hello, LIP!"
{"missing":["b","c"]}
{"digest":"a615eeaee21de5179de080de8c3052c8da901138406ba71c38c032845f7d54f4","mean":2}
```

[示例教程](examples/README.md) 从 Hello World、绑定和算术开始，逐步介绍函数、分支、循环、并行、Claim 和外部调用。`examples/` 中全部是带中文注释的 LIP 程序。

算术支持 `+ - * / // % ** */`。`**` 是幂，`a */ b` 是以右边 b 为底求 a 的对数：

```lip
flow Numbers() -> object {
    return {
        power: 2 ** 3,             # 8
        chain: 2 ** 3 ** 2,        # 512，等于 2 ** (3 ** 2)
        negative: -3 ** 2,         # -9，等于 -(3 ** 2)
        square: (-3) ** 2,         # 9，括号把负数整体作为底数
        reciprocal: 2 ** -3,       # 0.125
        logarithm: 8 */ 2          # 3，与 3.0 是同一个 number
    }
}
```

`*/` 与乘除同级，从左向右结合。对数要求左边大于 0，底数大于 0 且不等于 1；负数底的幂要求整数指数；`0 ** 0` 定义为 1，`0 ** -1` 报错。幂和对数使用规范中的固定 binary64 算法，结果是近似值，不保证正确舍入；定义域错误和非有限结果报 `E-NUMBER`。需要其他数值能力时，通过 Host 调用 Go/Python 库。

Flow 的参数通过 JSON 对象传入：

```sh
./lipc run -input '{"name":"Ada","scores":[0.6,0.9]}' examples/advanced/15_inputs.lip
```

CLI 将最终结果写为 JSON。需要打印或写文件时，使用已注册的外部操作；教学 Host 的 `console.write` 写到 stderr，stdout 保留给结果。

## 交互计算和学习

```sh
./lipc repl
./lipc repl -host demo
./lipc learn
./lipc learn -list
./lipc learn -lesson claim
```

REPL 可以直接输入表达式、不可变绑定、函数和请求：

```text
In [1]: x = 6
6
In [2]: fn twice(n:number) -> number { n * 2 }
In [3]: twice(x)
12
```

REPL 默认启用通用 Go adapter。导入标准库或当前 Go module 可解析的第三方包后，可以调用导出函数、构造类型、读取属性和调用对象方法，无需逐个注册包：

```text
In [4]: import go "strings"
In [5]: strings.ToUpper("1waht")
"1WAHT"
In [6]: strings.ToTitle("waht 12 ase? as a")
"WAHT 12 ASE? AS A"
In [7]: import go "bytes"
In [8]: buffer = bytes.NewBufferString("hello")
<go *bytes.Buffer>
In [9]: buffer.WriteString("!")
1
In [10]: buffer.String()
"hello!"
```

Go 第三方库使用区分大小写的**完整包路径**，项目名或网站上的名称不能代替包路径；`as` 用来指定调用时的简称。例如：

| 库 | Go 包路径 | 导入示例 |
|---|---|---|
| Gonum 矩阵计算 | `gonum.org/v1/gonum/mat` | `import go "gonum.org/v1/gonum/mat" as mat` |
| Gorgonia | `gorgonia.org/gorgonia` | `import go "gorgonia.org/gorgonia" as gorgonia` |
| Gin | `github.com/gin-gonic/gin` | `import go "github.com/gin-gonic/gin" as gin` |
| Viper | `github.com/spf13/viper` | `import go "github.com/spf13/viper" as viper` |

在自己的工作目录配置 Go module，再从该目录启动已安装的 lipc。已有 `go.mod` 时跳过初始化；下面是终端命令：

```sh
mkdir analysis
cd analysis
go mod init example.org/analysis
go get gonum.org/v1/gonum/mat
lipc repl
```

然后在 REPL 中调用：

```text
In [1]: import go "gonum.org/v1/gonum/mat" as mat
In [2]: m = mat.NewDense(2,2,[1,2,3,4])
<go *mat.Dense>
In [3]: m.At(1,0)
3
```

其他库同样先在终端用 `go get` 配置对应包路径，再在 LIP 中导入。安装 lipc 不会替工作目录配置第三方依赖，`import` 也不会自动添加依赖或把短名改成某个预设项目。Gonum 包含多个包，需要选择具体用途对应的路径；上述库都经过通用 adapter。

`fmt.Print`、`Printf`、`Println` 也使用同一个 adapter；Go 返回的字节数会作为表达式结果显示。`import go "string"` 会在导入时报告包不存在，正确包名是 `strings`。

Python 使用持久 worker 和独立通信通道，实际加载当前 Python 环境中的库。模块属性、子模块、类、方法、命名参数和原生函数引用都通过同一桥接机制处理：

```text
In [11]: import python "scipy" as sp
In [12]: sp.__version__
...当前环境中的 SciPy 版本...
In [13]: import python "numpy" as np
In [14]: a = np.array([1,2,3], dtype=np.int64)
<python numpy.ndarray>
In [15]: a.reshape(3,1).tolist()
[[1],[2],[3]]
In [16]: sp.linalg.norm(a)
3.7416573867739413
```

省略 `as` 时使用路径的最后一段，因此 `import python "scipy"` 后可读取 `scipy.__version__`。`help()` 显示交互帮助；`help(module)` 和 `help(module.member)` 查询库文档或 Go 导出签名，`help(value)` 说明语言值，`help("math.sqrt")` 查询 Python 文档。补全不执行库代码。

直接库调用和属性读取在检查前转换为普通 `perform effect`，文件运行、REPL 和生成程序使用相同路径。应用显式注册的同名合约优先；通用操作声明为 `Unknown`，不推断纯性或并发性。`import` 声明命名空间；Go 导入用工具链发现导出符号，Python 第一次实际操作才加载模块。两者都不安装依赖，包初始化仍属于经 Host 授权的实际操作。

打开的括号或大括号支持多行输入，其他续行使用行末 `\`。`:vars` 查看保存的值，`:history` 查看输入，`:reset` 清空会话，`:cancel` 丢弃未完成输入，`:quit` 退出。管道输入自动隐藏提示符：

```sh
printf 'x=6\nx*2\n:quit\n' | ./lipc repl
```

每次输入使用同一套编译和执行规则。成功输入保存值与闭包，之前的 `perform` 不会重跑；失败输入不保存新绑定，已经发生的外部动作不会回滚。每次输入有独立运行身份。完整 Flow、Claim 和并行汇聚可以用 `:run examples/advanced/14_parallel_claim.lip` 执行；文件运行不读取或改变 REPL 绑定。

REPL 和 learn 共用终端编辑器，可以在提交前修改输入。常用快捷键如下：

| 按键 | 动作 |
|---|---|
| ← / →；Ctrl-B / Ctrl-F | 左右移动 |
| Home / End；Ctrl-A / Ctrl-E | 行首 / 行尾 |
| Alt-B / Alt-F；Ctrl-← / Ctrl-→ | 按词移动 |
| Backspace / Delete；Ctrl-D | 删除前后字符；空输入时 Ctrl-D 退出 |
| Ctrl-U / Ctrl-K / Ctrl-W；Alt-D | 剪切到行首 / 行尾 / 前一个词 / 后一个词 |
| Ctrl-Y | 粘回最近剪切的内容 |
| Ctrl-_（常见终端为 Ctrl-/） | 撤销最近编辑 |
| Ctrl-T | 交换光标附近的两个字符 |
| ↑ / ↓；Ctrl-R | 浏览历史 / 搜索历史；搜索时 Ctrl-G 取消，Enter 提交 |
| Tab | 补全核心名字、REPL 保存的名字、已注册导入成员和命令 |
| Ctrl-L；Ctrl-C | 清屏 / 丢弃当前输入及未完成的程序草稿 |

支持终端的括号粘贴模式（bracketed paste），多行程序粘贴后按 Enter 提交。中文、常见组合字符和 emoji 可作为整体移动和删除。编辑时才启用终端 raw 模式，执行程序和调用 provider 前恢复原设置。管道输入保留普通文本模式；终端编辑支持 Linux、macOS/BSD 和启用 VT 的 Windows 控制台，当前实测平台为 Linux。

`learn` 内置 15 节课，从字符串、幂与对数到并行 Claim 和工具调用。直接输入模板的答案即可验证；`:hint` 看提示，`:demo` 运行讲解示例，`:next` 进入下一课。完整程序用 `:edit` 开始、`:submit` 提交。课程使用多个输入验证结果，运行示例不会自动标记为完成。

默认不保存学习进度。需要保存时，指定文件：

```sh
./lipc learn -progress /path/to/progress.json
```

## 生成 Go 程序

```sh
./lipc emit-go -o .build/hello.go examples/basics/01_hello.lip
go build -o .build/hello .build/hello.go
./.build/hello

# 也可以一步生成可执行文件。
./lipc build -o .build/refine examples/refine.lip
./.build/refine
```

受限环境可通过 Makefile 使用项目内缓存：

```sh
make compile SOURCE=examples/ecosystem/17_go_python.lip HOST=demo OUTPUT=.build/interop
./.build/interop -parallel 4
```

生成器把 LIP 表达式编译为专用 Go 函数，并带上图调度和外部调用运行时。生成的程序不用解析 LIP 源码，也不依赖源码目录。纯核心和教学 Host 只需要 Go 标准库。通用 Python 调用需要运行机器安装 Python 和相应包；自动发现的 Go 包会在运行时编译并加载 adapter，因此运行机器仍需匹配的 Go 工具链、CGO 和支持 Go plugin 的平台。

解释执行和 Go 生成程序共用数值、函数调用、Claim、错误和 Host 边界规则。生成程序启动时会核对 Host 合约，避免编译时和运行时采用不同的操作定义。可执行程序支持 `-input`、`-parallel`、`-steps` 和 `-elements`。

## 接入 Go / Python

CLI 默认启用通用桥接。嵌入应用通过 `script.NewNativeHost(host)` 启用，或用 `script.NewInteractiveSession(host)` 创建带桥接的会话；`script.NewSession(host)` 沿用传入的 Host 配置。会话退出应调用 `Close()`，`:reset` 会清空绑定并使已有资源引用失效。完整程序同样支持直接库调用，也可以显式保存和执行 `effect`。

Go adapter 使用包的导出类型信息与反射统一转换参数和结果，支持可变参数、多返回值、结构体、接口和跨包对象传递。多返回值的末尾 error 作为失败通道；唯一的 error 返回值保留为原生对象，使 `errors.New`、`fmt.Errorf` 和错误比较可组合使用。需要其他错误映射时注册显式合约。泛型函数用真实参数类型推导并编译具体实例。自动加载需要 Go 工具链、CGO 和 Go plugin 支持（Linux、macOS、FreeBSD）；依赖必须能由当前 module 解析。嵌入应用还可以直接注册 Go 导出对象，不经过动态编译：

```go
host := script.NewNativeHost(script.Host{})
defer host.Close()
host.Native.Go.RegisterPackage("application/text", map[string]any{
    "Upper": strings.ToUpper,
})
// LIP: import go "application/text" as text; text.Upper("hello")
```

无法从参数推导的泛型函数、泛型类型和应用专用类型，可注册具体实例或构造函数。通过 `EmitGo` 生成这类程序时，用 `GoOptions.HostSource` 提供同样的注册代码。语言纯函数不能直接作为原生回调；可以传递同一运行时的原生函数引用，或由应用注册专用回调 adapter。

Python worker 在同一 Host 内保留模块和对象，通信响应使用独立管道，不混入库的标准输出。它通过 Python 自身的导入、属性和调用协议工作，不维护包白名单，也不把数组提前变成列表。取消或失联后不自动重启或重试，需关闭或重置会话。

| 值 | 通用桥接方式 |
|---|---|
| null、bool、有限 number、string | 普通 Data；超出安全范围的原生整数拒绝转换 |
| LIP list / object | 按 Go 目标类型转换，或变成 Python list / dict |
| Python 普通 list / tuple / 字符串键 dict | 转成 Data，成员可包含资源引用 |
| Go 容器、结构体、指针、函数；Python 数组、DataFrame、bytes、复数等 | 保留原生对象，以 Host 作用域内的资源引用传递 |
| `native_data(value)` | 显式取得 Data 快照；Go 递归读取容器和导出字段，Python 支持容器及 `tolist()`；其他类型需调用库的转换方法 |
| `native_release(value)` | 释放该引用，后续使用会拒绝；它不代替文件等对象自身的关闭方法 |

Go number 按目标签名检查整数范围；string 到 `[]byte` 明确使用 UTF-8。Python 参数中安全的整数值转换为 `int`，其余 number 为 `float`。需要保留精确 dtype 时使用库的类型和构造器。跨 Go/Python 运行时传递数据时，先取得 Data 快照或显式编码 bytes；原生引用不能直接跨运行时传递。非有限数和循环对象不能转换为 Data。

应用也可以通过 `script.Host` 注册更严格的操作合约，声明版本、参数和结果、读写性、并发性、权限及资源需求。`GoHandler` 适配固定 Go 签名，支持 context；`PythonHandler` 为显式注册操作提供一次调用一个进程的通道。权限和参数验证先于实际调用；已完成动作的结果转换失败会保留完成事实，重放不会再次执行。

[教学 Host](script/adapters.go) 注册了 Go 的字符串和摘要操作、Python 的平均值和求和，以及 console 输出。通过 `-host demo` 显式启用；`script.DemoHost()` 创建同一配置，`script.DemoHostGoSource()` 为生成程序提供对应的注册代码。应用也可以使用 `Compile(source, host)`、`Program.Run(...)` 嵌入核心，通过 `Program.EmitGo(...)` 生成包含自身 Host 配置的 Go 程序。

## 项目组织

```text
cmd/lipc/       文件运行、Go 生成、REPL 与课程
script/         编译、静态检查、不可变值、函数求值、图调度与 Host 通道
examples/       带注释的 LIP 程序
tests/          公共接口、交互命令与解释/编译一致性测试
spec/           beta 0.9 规范
.agents/skills/ 可复用的 beta 开发经验
```

代码按语义放在有实际内容的文件中。REPL 会话管理集中在 `script/session.go`，复用编译器与执行引擎；命令行交互和课程集中在 `cmd/lipc/interactive.go`，共用的终端编辑器集中在 `cmd/lipc/editor.go`。终端系统接口按平台用构建标签隔离。

## 保证、范围和验证

固定外部应答、满足[规范](spec/BETA-0.9-SPEC.md)的前提时，合法调度得到相同结果或相同主错误。Claim 的即时 snapshot 记录读取当时的状态，可能受调度影响。错误发生后，已启动的外部动作不会回滚；不同调度也可能启动不同的调用。

当前包含解释器、Go 代码生成和 Go/Python 调用通道。`MemoryJournal` 提供一次运行内的记录与重放，**尚未实现可选的持久恢复配置**，不提供进程崩溃后的 resume、reconcile 或 fencing。库、长期事件监听、存储和部署由应用及 Go/Python 生态提供。

`Options.After` 当前支持顶层节点和区域的依赖约束。`Options` 还支持步数、单次列表大小、图规模和非尾调用帧预算；尾调用不累计函数栈帧。具体限制以 API 和规范为准。

```sh
make test       # 示例、交互和语义回归；实际运行生成的 Go 程序
make race       # 检查解释器及生成程序的并行数据竞争
make vet
make examples   # 逐项显示示例验证结果
make clean     # 清除 lipc、.build 和 Python 缓存
```

示例通过解释器及 Go 生成程序运行，并发上限分别为 1 和 4。回归还覆盖幂和对数的优先级及数值边界、尾调用、类型与参数检查、未选分支、请求链、错误排序、调用重放和权限检查。交互测试在 Linux 的真实伪终端中验证快捷键和终端设置恢复；纯管道输入也单独验证。开发约定和检查经验见 [beta 开发技能](.agents/skills/lipbeta-development/SKILL.md)。
