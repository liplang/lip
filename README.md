# LIP Alpha 0.6

[English README](README.en.md) · 参考实现 `0.6.1`（[VERSION](VERSION)）

LIP（Logical / Incremental / Parallel）是一门面向依赖关系的小语言：
**描述依赖，让 Runtime 决定执行。** 用简洁的不可变绑定组合数据、选择、
Map、聚合与纯递归，再通过图和轨迹观察执行。

语法参考 Rust，区间遵循 Python 的习惯。下面是一份完整的数据程序：

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

`lipc` 内置 Runtime 源码，安装后可在任意项目目录 `run/build`，无需 LIP 仓库
或项目 `go.mod`。这两个命令仍需 Go 工具链；编译出的核心程序可独立运行。
工具选项放在文件前，程序输入放在文件后，无需 `--`。

## 语言与运行库

| 能力 | 0.6 契约 |
| --- | --- |
| 完整程序 | 依赖头、纯 fn、唯一 flow、显式输入/输出、单一 return |
| 数据 | null、bool、number、string、list、object；对象/列表可嵌套 |
| 组合 | 不可变绑定、运算符、Rust 式 if、when 门控、Map |
| 纯操作 | str、len、半开区间 range、带初值的顺序 fold、显式 fail |
| list 标准库 | 34 个纯函数：分组、合并、转置、窗口、过滤、排序、去重、累计与笛卡尔积 |
| string 标准库 | 19 个纯操作：清理、切词、分行、合并、查询、截取、替换与数值解析 |
| 递归 | 直接/间接纯递归；递归环显式返回类型，最大调用深度 256 |
| 执行 | 自动/顺序/有限并行、效果与取消、State/Tick、纯依赖复用 |
| 观察 | check --json 修复诊断、inspect 依赖图、run --trace 生命周期 |
| 已有扩展 | Host Adapter、Await、有界 Retry/Feedback、Python Worker |

`range(start, end[, step])` 最多构造 1,000,000 个元素；Map 推导式使用已绑定的区间，fold/list.* 可嵌套纯表达式。list 库通过纯函数补齐集合处理，完整签名见 [LIST-LIBRARY.md](docs/LIST-LIBRARY.md)。fold 的 reducer 是本文件的二参数纯 fn，空列表返回初值。递归适合树和分治，集合遍历优先用 Map/fold。

`list`/`object` 检查外层形状，动态元素在实际运算中校验。CLI 的 string 参数
原样传递，number 为有限十进制，bool 为 true/false，any/list/object 使用 JSON。
Flow 的 `T?` 输出可以是 null，或在门控关闭时无值完成。

## 依赖是执行语义

Flow 绑定形成图节点，变量引用形成依赖；fn 内部的纯表达式不展开为图。
独立节点允许并行，消费者等待依赖。`if` 选择值，`when` 控制执行资格；if 的
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
lipc run examples/strings.lip ' Rust, LIP, rust, ,你好 '
# {"count":3,"label":"rust / lip / 你好","tags":["rust","lip","你好"]}
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

外部操作通过 `require` 声明。Host/Go 程序用 `--no-main --package name`
生成库，由宿主注册 adapter；Python 依赖启用常驻 Worker，使用已安装的包。
详见 [Host 示例](examples/host_adapter) 和 [Python 集成](docs/PYTHON-INTEGRATION.md)。

## 规范与验证

[语言规范](docs/ALPHA-0.6-SPEC.md)统一定义当前规则，行注释使用 `//`，
条件选值使用块式 `if`，执行门控使用 `when`。后续计划见[路线图](docs/ROADMAP.md)。

```bash
bash scripts/verify-release.sh
```

这条命令执行测试、race、vet、构建、全部 LIP 示例检查/生成与核心运行验收。
Python 专项测试在相应环境可用时执行；独立核心示例不要求 Python。

- [快速入门](docs/QUICKSTART.md) · [编程教程](docs/TUTORIAL.md)
- [字符串库](docs/STRING-LIBRARY.md) · [Vibe Coding 流程](docs/VIBE-CODING.md)
- [编译器与宿主 API](docs/COMPILER.md) · [规范索引](docs/SPECS.md)
- [收敛路线图](docs/ROADMAP.md) · [一致性审计](docs/CONSISTENCY-AUDIT.md)
- [原型覆盖](examples/PROTOTYPES.md) · [变更记录](CHANGELOG.md) · [发布清单](RELEASE.md)
