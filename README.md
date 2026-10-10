# LIP Alpha 0.6.4

[English README](README.en.md) · 参考实现 `0.6.4`（[VERSION](VERSION)）

LIP（Logical / Incremental / Parallel）是一门面向依赖关系的小语言：
**描述依赖，让 Runtime 决定执行。** 用简洁的不可变绑定组合数据、选择、
Map、聚合与纯递归，再通过图和轨迹观察执行。

下面是一份完整的数据程序：

```lip
fn add(total: number, value: number) -> number {
    return total + value
}

flow Report(values: list) -> object {
    doubled = [x * 2 for x in values]
    total = fold(doubled, 0, add)
    return {
        count: len(values),
        values: doubled,
        total: total,
        average: if len(values) == 0 { null } else { total / len(values) }
    }
}
```

需要 Go 1.27 或更高版本。在仓库根目录安装：

```bash
go install ./cmd/lipc
```

安装位置跟随现有 Go 配置：GOBIN 优先，否则是第一个 GOPATH 目录下的 `bin`。
工具目录已在 PATH 中时，直接使用 `lipc`；也可用实际安装路径调用。下面用
`lipc` 表示已安装的工具：

```bash
lipc check examples/core.lip
lipc run examples/core.lip '[1,2,3]'
# {"average":4,"count":3,"total":12,"values":[2,4,6]}
lipc run examples/core.lip '[]'
# {"average":null,"count":0,"total":0,"values":[]}
```

未自定义 Go 配置时，常见路径是 `~/go/bin/lipc`，可直接
`~/go/bin/lipc help`；Windows 通常是用户目录下的 `go\bin\lipc.exe`。
完整路径的调用方式见[快速入门](docs/QUICKSTART.md#安装)。

Vim 9、Neovim 和 Emacs 原生插件提供 `.lip` 高亮、缩进、补全与编译器诊断。
分别使用 Vim9script、Lua 和 Elisp，安装与配置见[编辑器支持](docs/EDITORS.md)。

想循序渐进地学习，运行 `lipc learn`（仓库内也可 `go run ./cmd/lipc learn`）。
26 节中文交互课程提供讲解、可运行示例、代码练习和多组输入验证，覆盖关键字、
程序组织、list/string 库、依赖图、State/Tick、Host 和 Python。
`:hint` 获取逐步提示，`:next` 进入下一课；进度自动保存。
`lipc learn --list` 查看课程，完整用法见[交互学习](docs/INTERACTIVE-LEARNING.md)。

想直接试表达式，运行 `lipc repl`：

```text
In [1]: value = 79 / 134
In [2]: value
Out[2]: 0.5895522388059702
In [3]: print(value)
0.5895522388059702
```

变量和 `fn` 跨输入保留，未闭合的括号支持多行；`:help` 查看命令，`:quit`
退出。左右方向键编辑、上下方向键回看历史，Tab 补全；Ctrl-C 取消当前输入。
后端仍编译每次输入，已完成的打印不会重放。文件也可直接写一组语句：

```lip
a = 2
b = 3
print(a / (a + b))
```

`lipc run examples/statements.lip` 输出 `0.4`。顶层语句等价于放在 `flow main() { ... }`
里，没有输入参数或返回值。显式 Flow 也可省略输出声明和 `return`，或写 `-> void`。
`print` 接受任意类型，有返回值时再声明 `-> number` 等输出类型。
换行可以分隔语句；多条语句放在同一行时，分号必须保留：`a = 2; b = 3; print(a / b)`。

`lipc` 内置 Runtime 源码，安装后可在任意项目目录 `run/build`，无需 LIP 仓库
或项目 `go.mod`。这两个命令仍需 Go 工具链；编译出的核心程序可独立运行。
工具选项放在文件前，程序输入放在文件后，无需 `--`。

## 语言与运行库

| 能力 | 0.6.4 契约 |
| --- | --- |
| 完整程序 | 依赖头（Python/Host/Go 都支持 as 别名）、纯 fn、显式 flow 或顶层语句；有返回值时声明类型，用 return 或 match 分支给出结果 |
| 数据 | null、bool、number、string、list、object；对象/列表可嵌套 |
| 组合 | 不可变绑定、运算符、块式 if、字面量/类型 match、默认分支、守卫、Map、顺序 for、break/continue |
| 数学 | + - * /、向下取整 //、取余 %、乘方 **、对数 */；[示例](examples/math.lip) |
| 核心操作 | str、len、isEmpty/isNotEmpty、半开区间 range、带初值的顺序 fold、显式 fail、random/random_list/random_int/random_choice/random_shuffle |
| list 标准库 | 36 个纯函数：分组、合并、转置、窗口、过滤、排序、去重、累计与笛卡尔积 |
| string 标准库 | 19 个纯操作：清理、切词、分行、合并、查询、截取、替换与数值解析 |
| 递归 | 直接/间接纯递归；尾递归与互相尾调用使用循环跳转，线性累积和满足整数状态表条件的分支递归使用循环；未能转换的递归环显式返回类型，最大调用深度 1024 |
| 执行 | 自动/顺序/有限并行、效果与取消、State/Tick、纯依赖复用 |
| 观察 | check --json 修复诊断、inspect 依赖图、run --trace 生命周期 |
| 已有扩展 | Host Adapter、Await、有界 Retry/Feedback、Python Worker |

`range(end)` 或 `range(start, end[, step])` 最多构造 1,000,000 个元素；`isEmpty(x)` 和 `isNotEmpty(x)` 统一检查字符串、列表和对象。列表与字符串支持 `x[start:end:step]` 切片。列表推导式可直接使用纯列表表达式，也可放入调用参数，例如 `np.mean([x for x in range(1, 19)])`（先 `import python "numpy" as np`）。独立 Map 保留有界并行，嵌套推导式按顺序求值。集合回调可用本地函数名或内联 `fn(x) { x * x }`，例如 `list.map(range(5), fn(x) { x * x })`。fold 的二参数回调也可内联，空列表返回初值。单表达式 fn 的 return 可省略。`random()`、`random_list(n)`、`random_int(...)`、`random_choice(xs)` 和 `random_shuffle(xs)` 生成随机结果，属于有副作用操作。完整签名见 [LIST-LIBRARY.md](docs/LIST-LIBRARY.md)。递归适合树和分治，集合遍历优先用 Map/fold。

`list`/`object` 检查外层形状，动态元素在实际运算中校验。CLI 的 string 参数
原样传递，number 为有限十进制，bool 为 true/false，any/list/object 使用 JSON。
参数、fn/回调结果与 Flow 输出都支持 `T?`，表示 T 或 null；Flow 输出还可在
门控关闭时无值完成。CLI 可空参数用 `null`，文本 null 用 string? 的 JSON 字符串
`'"null"'`。Flow 中可直接组合 `print(np.mean(values))` 等外部调用，按表达式
顺序等待，调度/缓存考虑所有调用效果；fn 和集合回调保持纯计算。

## 依赖是执行语义

Flow 绑定形成图节点，变量引用形成依赖；fn 内部的纯表达式不展开为图。
独立节点允许并行，消费者等待依赖。`if` 选择值；`match` 可以按整个值的字面量选择值或执行分支；if 的
短路不会取消已经声明为独立节点的外部工作。

Host 把真实能力接入图：

```go
host := runtime.DefaultHost()
host.RegisterPure("compute", compute)
host.RegisterReadOnly("load_profile", loadProfile)
host.Register("write_file", writeFile)
```

Pure 可在未变化的 Tick 复用，ReadOnly 可并行但每 Tick 重读，外部写入按顺序
形成屏障。Host 必须如实声明效果、协作取消，并保护自身共享状态；交给 Flow
的数据视为只读。Runtime 不回滚已经执行的外部写入。

持久实例通过 `NewInstance`、`SetState`、`Tick` 更新状态，由宿主推进每次计算。
一次性执行使用 `Run`、`RunSequential` 或 `RunParallel`，输入/结果契约相同。

## 检查、运行和观察

```bash
lipc check --json examples/strings.lip
lipc run examples/strings.lip ' go, LIP, Go, ,你好 '
# {"count":3,"label":"go / lip / 你好","tags":["go","lip","你好"]}
lipc inspect examples/core.lip
lipc run --trace report-trace.json examples/core.lip '[1,2,3]'
lipc run examples/range.lip 5
lipc run examples/lists.lip '[{"department":"A","amount":3},{"department":"B","amount":2}]'
lipc run examples/tree.lip '{"value":1,"left":{"value":2,"left":null,"right":null},"right":null}'

lipc build examples/core.lip
./Report '[1,2,3]'
```

Windows 下运行 `.\Report.exe '[1,2,3]'`。输出路径与 Go 源码生成选项见
[编译器文档](docs/COMPILER.md#lipc-命令)。

inspect 不执行程序，输出 `lip.graph.v1`。trace 写到独立文件，成功和执行失败均
保留 `lip.trace.v1` 的节点状态、Tick 与原因，不影响正常结果输出。

`check --json` 输出 `lip.diagnostics.v1`：第一个错误的位置、源行、分类与修复
指导，仍用退出码表示失败。以意图注释和可复现样例支持生成→检查→验证→修复，
约定见 [VIBE-CODING.md](docs/VIBE-CODING.md)。字符串边界与运算最多 16 MiB，
要求有效 UTF-8；Map/fold 源列表最多 1,000,000 项，在展开前失败。

Go/Python/纯库都用调用与值依赖自然组合，普通 Python 返回值直接继续处理；
完整例子见 [mixed.lip](examples/tutorial/mixed.lip)，运行
`go run ./examples/tutorial/mixed_demo`（只需 Python 标准库）。

外部操作通过 `import` 声明。Python/Host/Go 都支持 `as`，例如 `import python "numpy" as np`、
`import host "service.*" as s`、`import go "fmt" as f`；
不写 `as` 就保留实际模块名，例如 `sklearn`，编译器不根据安装包名改名。
实际使用 Host/Go 操作的程序用 `--no-main --package name`
生成库，由宿主注册 adapter；实际 Python 调用启用常驻 Worker，使用已安装的包。
未使用的导入仅保留元数据。命名空间别名可与内置操作或本地 fn 同名，裸调用和
成员调用分别解析；单操作别名按显式声明解析。
详见 [Host 示例](examples/host_adapter) 和 [Python 集成](docs/PYTHON-INTEGRATION.md)。

## 规范与验证

生成图会在最后一个消费者完成或跳过后解除中间值引用，并行任务仅携带自身依赖。
LIP 管理计算生命周期，Go GC 管理内存回收；输出、State 与有效纯缓存继续保留。
见[值生命周期与测量](docs/VALUE-LIFETIMES.md)。

[语言规范](docs/ALPHA-0.6-SPEC.md)统一定义当前规则，行注释使用 `#`，
条件选值使用块式 `if`，执行分支使用 `match`。顺序操作可写 `for i in range(100) { print(i) }`，
循环内支持绑定、match、嵌套循环和 break/continue。CLI 的 Ctrl-C/超时或 Go 宿主取消 context
会停止运行，普通错误也会停止后续操作；每次迭代独立作用域，
不构造结果列表。列表转换用推导式，累加用 fold。见[循环示例](examples/for.lip)；后续计划见[路线图](docs/ROADMAP.md)。

```bash
bash scripts/verify-release.sh
```

这条命令执行测试、race、vet、构建、全部 LIP 示例检查/生成与核心运行验收。
Python 专项测试在相应环境可用时执行；独立核心示例不要求 Python。

- [快速入门](docs/QUICKSTART.md) · [编程教程](docs/TUTORIAL.md)
- [编辑器支持](docs/EDITORS.md)（Neovim / Vim 9 / Emacs）
- [字符串库](docs/STRING-LIBRARY.md) · [Vibe Coding 流程](docs/VIBE-CODING.md)
- [编译器与宿主 API](docs/COMPILER.md) · [规范索引](docs/SPECS.md)
- [收敛路线图](docs/ROADMAP.md) · [一致性审计](docs/CONSISTENCY-AUDIT.md)
- [原型覆盖](examples/PROTOTYPES.md) · [变更记录](CHANGELOG.md) · [发布清单](RELEASE.md)

本地源码包与平台工具包的生成、SHA-256 校验和离线验收见 [RELEASE.md](RELEASE.md#本地发行包)。
