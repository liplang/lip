# LIP beta 0.9

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

需要 Go **1.25 或更高版本**；没有固定某个补丁版本，也没有 `toolchain` 版本锁。本轮验证使用 Go 1.27。Python 示例还需要 Python 3，使用的都是标准库。

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

REPL 还支持直接调用已导入的 Python 库，以及通用帮助。Python 3 和相应的库需要已经安装：

```text
In [4]: import python "numpy" as np
In [5]: np.sqrt(3)
1.7320508075688772
In [6]: help(np)
...显示 NumPy 模块文档...
In [7]: help(np.sqrt)
...显示 sqrt 的签名和用法...
```

省略 `as` 时使用路径的最后一段，因此 `import python "numpy"` 后可调用 `numpy.sqrt(3)`。`help()` 显示交互帮助，`help(value)` 说明值、核心函数、纯函数或尚未执行的请求；`help("math.sqrt")` 可按名字查询 Python 文档。帮助以普通文本显示。Go/Host 模块的帮助列出已注册操作的签名。

直接外部调用由 REPL 转换为 `perform effect`，仍经过参数、授权和结果检查；嵌套调用按实参顺序求值。已注册操作沿用 Host 合约；未注册的 Python 成员由交互适配器采用通用 Data 参数/结果合约，声明为 `Unknown`，不推断纯性或并发性。该适配器显式将 NumPy 实数/整数/布尔标量转换为普通值，将数组转换为嵌套列表；非有限数、复数、集合和其他不支持的原生对象报错。`import` 和补全不加载 Python 模块，只有实际调用或查询 Python 文档才启动 worker。

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

生成器把 LIP 表达式编译为专用 Go 函数，并带上图调度和外部调用运行时。生成的程序不用解析 LIP 源码，也不依赖源码目录。纯核心和教学 Host 只需要 Go 标准库；调用 Python 时，运行机器仍需安装 Python。

解释执行和 Go 生成程序共用数值、函数调用、Claim、错误和 Host 边界规则。生成程序启动时会核对 Host 合约，避免编译时和运行时采用不同的操作定义。可执行程序支持 `-input`、`-parallel`、`-steps` 和 `-elements`。

## 接入 Go / Python

应用先通过 `script.Host` 注册允许调用的操作，说明名字、版本、参数和结果、是否读写、是否允许并发，以及权限和资源需求。LIP 的 `import` 声明这些名字的对应关系；不安装依赖，也不授予任意原生调用权限。

嵌入会话使用 `script.NewSession(host)`，支持直接调用已注册操作和本地帮助；`script.NewInteractiveSession(host)` 另外启用上述通用 Python 适配器，也是 `lipc repl` 的默认配置。完整文件程序仍显式使用 `perform effect` 和已注册的 Host 合约。

- `GoHandler` 适配固定 Go 签名，支持 context、结构体、字符串键的 map、列表和末尾 error。
- `PythonHandler` 调用已安装模块中注册的成员，每次调用使用一个独立进程。
- 参数和结果使用普通数据：null、bool、number、string、list、object。bytes、原生函数、资源句柄等需要应用明确转换。
- 权限与参数检查在调用前完成。已完成动作的结果验证失败，会保留完成事实和错误，重放时不会再次调用。

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
