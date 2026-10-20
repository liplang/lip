# LIP 1.2

[English](README.en.md) | [简体中文](README.md)

LIP 把“计算一个值”和“完成一组工作”写成两种清楚的东西：`fn` 负责纯计算，`flow` 负责依赖图。
再加上三条边界，工具协作、并行汇聚和可追踪结果自然落在同一个模型里：`effect` 构造请求，
`perform` 执行动作，`claim` 收集带来源的事实。

```text
输入 → fn 整理 → Flow 展开工作 → 并行工具调用 → Claim 汇聚证据 → fn 决策 → 输出
```

语言核心很小，Go 和 Python 负责数据库、网络、模型及科学计算。
LIP 负责把这些能力组成一个可阅读的工作流：哪些步骤可以并行、什么时候结果完整、
哪个工具产生了哪条事实，都由程序结构表达。

[语言规范](spec/LIP-1.2-SPEC.md) · [示例教程](examples/README.md) · [实现与验证](IMPLEMENTATION.md)

## 用几十行写一个 LLM agent

一个 agent 原型通常只有四件事：让模型制定下一步、调用工具、把观察结果放回上下文、
在满足条件时结束。LIP 的值、Flow、Claim、请求和携带值循环正好组成这套系统。

下面的 Host 合约假定提供 `agent.chat`、`agent.search` 两个操作；
应用只需在 Go/Python Host 中注册它们，LIP 程序无需知道 SDK 细节。

```lip
import host "agent" as ai

fn ask(prompt:string)->effect { effect ai.chat(prompt) }
fn search(query:string)->effect { effect ai.search(query) }

flow Research(question:string)->object {
    evidence = claim()

    # 独立来源同时检索；每条结果都有自己的结构身份和来源位置。
    for source in ["docs", "papers", "news"] parallel {
        result = perform search(source + ": " + question)
        evidence <+ {source: source, result: result}
    }

    context = [row.value for row in evidence]
    draft = perform ask("Answer this question from the evidence: " + str(context))

    return {question: question, context: context, draft: draft}
}

flow Review(input:object)->object {
    # state 是本轮的不可变快照。next 后面写“下一轮的完整状态”，
    # break 后面写循环的返回值；它们是控制出口，不是给 state 做赋值。
    # 携带值循环就是 agent 的 plan → act → observe → decide。
    answer = for with state = {text: input.draft, round: 0} {
        review = perform ask("Review this answer and say DONE or REVISE: " + state.text)
        next_state = if review == "DONE" {
            {text: state.text, round: state.round + 1}
        } else {
            revised = perform ask("Rewrite this answer using the review: " + review + ": " + state.text)
            {text: revised, round: state.round + 1}
        }
        if review == "DONE" || state.round >= 2 { break next_state }
        else { next next_state }
    }

    return {question: input.question, answer: answer.text, evidence: input.context}
}

flow Main(question:string)->object { return question |> Research |> Review }
```

这里把 agent 拆成两个 Flow：`Research` 并行检索并生成初稿，`Review` 接收前者的完整结果，
执行携带值复核。`Main` 用 `question |> Research |> Review` 把两个阶段连接起来；
管道会等前一阶段完整成功后，才把结果交给下一阶段。

这里的 `for with state = initial` 是一个携带值表达式：第一轮看到 `initial`，
`next e` 把表达式 `e` 作为下一轮的 `state`，`break e` 则立即返回 `e`。
每一轮的 `state` 都不可变，因此这相当于一个显式的 `stateₙ → stateₙ₊₁` 状态机；
`next` 是“继续下一轮”，`break` 是“结束并产出结果”，两者后面都直接接一个值表达式。
无 source 的写法会一直转移，直到某个分支 `break`。

运行时，三个检索动作可以重叠；读取 `evidence` 时会等待所有已选 producer，
按结构位置得到完整快照；`Review` 的模型复核每轮读取上轮不可变状态。模型、
搜索和数据库仍是 Host 的能力，LIP 只表达它们之间的顺序、并发、证据和结束条件。
把 `agent.search` 改成数据库查询或 Python 检索器，程序结构不变。

## 为什么这些特性刚好构成一个系统

**值与函数**让数据处理可组合。绑定不可变，`fn` 可以捕获快照、递归、做 map/fold、返回函数或请求；
同一个函数在 REPL、解释器和生成的 Go 程序里有同样的结果。

**请求是值，执行只有一个入口。** `effect op(args)` 不调用 provider，只产生带合约身份的请求。
请求可以放进 list/object、闭包、函数参数和 Flow 返回值；`perform` 才进行 schema、授权、
资源和 provider 调用。这样 agent 可以先计划一组动作，再在明确的位置执行。需要在程序中
审阅时，`str(request)` 会返回一个稳定的字符串摘要，包含 operation、contract 和 Data 参数；
它是纯计算，不调用 provider，也不暴露 CallID 或运行时资源。

拿到一个 effect 后，coder 按下面顺序审阅，再决定是否保留 `perform`：

1. 先绑定 `request = effect ...`，再用 `print(request)` / `help(request)` 查看易定位的多行摘要：第一行
   是 `operation: go.strings.ToUpper`，其后按 `字段: 内容` 展示版本、参数、schema、effect class、并发、capability 和 resource。
2. 对照 Host contract 检查摘要中的参数/结果 schema、版本和权限；Go/Python 包的路径和 member
   也会出现在 operation 中，包文档仍由 Host/REPL 的帮助工具提供。
3. 让纯的审阅规则读取这个字符串，只有通过规则的分支才执行 `perform request`；拒绝分支不调用 provider。
4. 检查 `perform` 所在的 Flow 节点和前置依赖；只有合约声明为 Concurrent ReadOnly 的已知请求才可放进 `parallel`。

这是一项在 Flow 内完成的 coder/program review：请求可以作为值传给 fn 或 Flow，摘要字符串也
可以作为普通值继续传递。它不是跨 Flow 的人工审批消息通道；如果产品需要运行时人工批准，
仍由 Host 在这条边界外提供协议。

### 一个最小的审阅—执行结构如下：

```lip
import go "strings" as text

fn upper_request(value:string)->effect { effect text.ToUpper(value) }

flow Main()->object {
    request = upper_request("hello")
    # The coder inspects the labelled lines, then chooses whether to set this gate true.
    print(request)
    coder_approved = true
    decision = if coder_approved {
        result = perform request
        {approved: true, result: result}
    } else {
        {approved: false, result: null}
    }
    return decision
}
```

示例把审阅决定集中在 `coder_approved`：先打印带标签的摘要，确认 operation、参数、版本、schema、
效果和权限，再把它设为 `true`；改为 `false` 就拒绝请求。更严格的应用可以把打印过的完整
摘要保存为审核记录，再用字符串相等比较绑定到这次请求；规范摘要的长字面量适合测试或生成
配置，不必手写在日常业务代码里。`str(effect)` 不等于字段投影，也不会改变 effect 的不可
比较性；它只提供一个可审阅、可传递的字符串。完整的 Go/Python 例子见
[18_proposals](examples/ecosystem/18_proposals.lip)，其中还展示了请求的函数/Flow 传递、
并行只读调用和独立的顺序写入。

带标签的显示把每个嵌套叶子也拆成可搜索的路径，便于直接定位冒号后的值：

```text
operation: go.strings.ToUpper
operation.language: "go"
operation.path: "strings"
operation.member: "ToUpper"
kind: "effect"
contract.version: "1"
contract.effect: "ReadOnly"
contract.concurrent: true
contract.parameters[0].name: "text"
contract.parameters[0].schema.type: "string"
contract.result.type: "string"
arguments.positional[0]: "hello"
arguments.named: {}
```

路径显示只用于人读的诊断；要保存完整、无损、可做字符串相等比较的摘要，使用 `str(request)`。

需要严格字符串匹配时，把审核记录作为配置或生成文件读入即可，代码仍然只比较 `str(request)` 的完整字符串：

```lip
summary = str(request)
print(request)
approved = summary == REVIEWED_SUMMARY  # REVIEWED_SUMMARY 由审核工具生成
result = if approved { perform request } else { null }
```

`str(request)` 的完整稳定输出例如是下面这样；它适合日志、审核记录和测试快照，日常代码通常只
保留上面的带标签打印和门控：

```text
{"arguments":{"named":{},"positional":["hello"]},"contract":{"capabilities":[],"concurrent":true,"effect":"ReadOnly","parameters":[{"name":"text","optional":false,"schema":{"type":"string"}}],"resources":{},"result":{"type":"string"},"version":"1"},"kind":"effect","operation":{"language":"go","member":"ToUpper","path":"strings"}}
```

审核决定也可以来自入口输入。入口输入是普通 Data，先转成字符再选择是否放行：

```lip
import go "strings" as text
fn upper_request(value:string)->effect { effect text.ToUpper(value) }

flow ReviewFromInput(input:object)->object {
    request = upper_request(input.text)
    print(request)
    decision = to_chars(input.approve)
    approved = if len(decision) == 0 { false } else { decision[0] == 'y' }
    result = if approved { perform request } else { null }
    return {approved: approved, result: result}
}
```

例如 `-entry ReviewFromInput -entry-input '{"positional":[{"text":"hello","approve":"y"}],"named":{}}'`
会先显示带标签摘要，再执行请求；把 `approve` 改成 `"n"` 则只返回拒绝结果。真实终端输入、
人工审批或跨 Flow 消息仍由 Host 提供相应 operation 和协议。

**有限描述让封装仍然可分析。** 1.2 会沿着固定字段、索引列表、切片、`put`、函数参数/返回值、
闭包和静态 Flow 返回值保留请求与可调用来源。`identity(p:effect)->effect {p}`、
容器中的请求和多层纯函数不会凭包装变成黑箱；候选里只要出现 Write、不可并发读取或未知来源，
`parallel` 就会在编译期拒绝。

**Flow 是依赖图。** 顺序语句形成前置关系，独立节点获得并发机会，
子 Flow 完整成功后才向调用者发布结果。管道 `|>` 把上一步的值注入下一阶段，
阶段未完成时不会提前调用后续工具。

**Claim 是证据汇聚边界。** `<+` 追加一条带 JoinID 的事实；相同身份的重放幂等，
不同身份的相同值仍是两条贡献。完整读取等待 producer，live snapshot 则只观察线性化点。模型回答、
工具结果和检查结论都可以先成为事实，再由纯函数筛选、评分和决定是否发布。

**Host 合约是系统的外部边界。** 合约同时声明参数 schema、返回 schema、版本、读写效果、并发性、
权限和资源。Go/Python adapter、CLI、REPL 和生成程序都经过同一条边界；
构造请求不会偷偷调用外部服务，已发生的动作也不会被错误重试掩盖。

**诊断与身份让结果可复查。** 节点位置、调用路径、Claim 来源和 provider 记录使用结构身份，
而不是完成先后编号。`print`、`log`、`telemetry` 是单向诊断；
真正影响业务的动作仍必须写成 `effect/perform`。


## 从文件到 Go 程序

```sh
make build
./lipc run examples/basics/01_hello.lip
./lipc run -parallel 4 examples/evidence.lip
./lipc repl
./lipc learn -list
```

生成独立程序：

```sh
./lipc emit-go -o agent.go examples/advanced/26_flows.lip
./lipc build -o agent examples/advanced/26_flows.lip
./agent -input '{"xs":[3,1,2]}'
```

需要 Go 1.25 或更高版本。`-host demo` 提供教学用合约；
真实应用通过 `Host.Contracts` 注册自己的 Go/Python 操作。REPL 保存成功 cell 的不可变结果和闭包，
失败 cell 不会覆盖旧绑定；`lipc learn` 用同一编辑器逐步练习值、函数、Flow、Claim 和 Host。

## 目录

- [基础示例](examples/basics/01_hello.lip)：绑定、算术、容器和字符串。
- [结构示例](examples/structures/12_carry.lip)：条件、函数、map/fold、携带值循环。
- [高级示例](examples/advanced/14_parallel_claim.lip)：并行 Claim、输入、快调用和诊断。
- [生态示例](examples/ecosystem/18_proposals.lip)：用 `print(request)` 展示可搜索摘要，在 `if` 中审阅放行后执行 effect、Go/Python 请求、顺序写入和记录式迭代。
- [完整示例说明](examples/README.md)：按概念逐步运行所有 `.lip` 文件。

```sh
go install ./cmd/lipc
lipc help
lipc help serve
lipc serve help
```

不确定参数时，直接运行 `lipc` 会显示命令总览；每个子命令也接受
`help`、`-h` 和 `--help`。例如 `lipc run -h` 会列出输入编码、入口 Flow
和执行预算，`lipc serve -h` 会说明单文件路由与 `-routes` 清单格式。

完整的语法、值编码、调用边界、调度、Claim、Host 和确定性规则见
[LIP 1.2 规范](spec/LIP-1.2-SPEC.md)。

## 语言核心与程序结构

语法、值、Flow、Claim 和入口协议的完整说明见 [LIP 1.2 规范](spec/LIP-1.2-SPEC.md)。
实际程序通常沿着“准备值 → 构造请求 → Flow 调度 → Claim 汇聚 → 纯函数决策”的顺序组织。

## 交互计算和学习

`lipc repl` 与 `lipc learn` 使用同一套编辑器和运行时：先在 REPL 试算值和请求，
再用 `:begin`/`:end` 提交多行 Flow；课程则把同一套组合拆成可运行的小步。

## 为什么 fmt.Print 后面会显示数字

Go 函数的返回值会按 Host 合约回到 REPL；需要展示副作用时使用 `print`、`log` 或 `telemetry`。

## 接入 Go / Python

应用通过 `Host.Contracts` 和 adapter 注册外部能力，LIP 程序只依赖稳定的操作合约。
合约给出参数/结果 schema、权限、资源、读写效果和并发性；替换模型或工具实现时，
Flow 结构保持不变。

## 所有外部动作都走同一条边界

请求由 `effect` 构造，由 `perform` 执行；Go、Python、HTTP 和自定义 Host 使用同一边界。
构造阶段可以自由组合和传递请求，执行阶段统一做 schema、授权、资源和 provider 检查。
需要审阅时通常先 `print(request)`；它把 operation、contract 和 Data 参数拆成便于定位的带标签行，仍是纯
显示，不会执行 provider。需要机器精确绑定时再用 `summary = str(request)`，用
`if summary == reviewed_summary {perform request} else {null}` 把放行点写在 Flow 中；effect 不支持字段投影或比较。

## 网站与 HTTP 接入

HTTP handler 将请求映射为入口 Data，将外部服务映射为 Host 操作；业务步骤仍由 Flow 表达，
页面/JSON 响应只是入口 Flow 的结果。
