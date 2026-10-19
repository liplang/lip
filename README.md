# LIP 1.2

[English](README.en.md) | [简体中文](README.md)

LIP 把“计算一个值”和“完成一组工作”写成两种清楚的东西：`fn` 负责纯计算，`flow` 负责依赖图。再加上三条边界，工具协作、并行汇聚和可追踪结果自然落在同一个模型里：`effect` 构造请求，`perform` 执行动作，`claim` 收集带来源的事实。

```text
输入 → 纯函数整理 → Flow 展开工作 → 并行工具调用 → Claim 汇聚证据 → 纯函数决策 → 输出
```

语言核心很小，Go 和 Python 负责数据库、网络、模型及科学计算。LIP 负责把这些能力组成一个可阅读的工作流：哪些步骤可以并行、什么时候结果完整、哪个工具产生了哪条事实，都由程序结构表达。

[语言规范](spec/LIP-1.2-SPEC.md) · [示例教程](examples/README.md) · [实现与验证](IMPLEMENTATION.md)

## 用几十行写一个 LLM agent

一个 agent 原型通常只有四件事：让模型制定下一步、调用工具、把观察结果放回上下文、在满足条件时结束。LIP 的值、Flow、Claim、请求和携带值循环正好组成这套系统。

下面的 Host 合约假定提供 `agent.chat`、`agent.search` 两个操作；应用只需在 Go/Python Host 中注册它们，LIP 程序无需知道 SDK 细节。

```lip
import host "agent" as ai

fn ask(prompt:string)->effect {
    effect ai.chat(prompt)
}

fn search(query:string)->effect {
    effect ai.search(query)
}

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

这里把 agent 拆成两个 Flow：`Research` 并行检索并生成初稿，`Review` 接收前者的完整结果，执行携带值复核。`Main` 用 `question |> Research |> Review` 把两个阶段连接起来；管道会等前一阶段完整成功后，才把结果交给下一阶段。

这里的 `for with state = initial` 是一个携带值表达式：第一轮看到 `initial`，`next e`
把表达式 `e` 作为下一轮的 `state`，`break e` 则立即返回 `e`。每一轮的 `state`
都不可变，因此这相当于一个显式的 `stateₙ → stateₙ₊₁` 状态机；`next` 是“继续下一轮”，
`break` 是“结束并产出结果”，两者后面都直接接一个值表达式。无 source 的写法会一直转移，
直到某个分支 `break`。

运行时，三个检索动作可以重叠；读取 `evidence` 时会等待所有已选 producer，按结构位置得到完整快照；`Review` 的模型复核每轮读取上轮不可变状态。模型、搜索和数据库仍是 Host 的能力，LIP 只表达它们之间的顺序、并发、证据和结束条件。把 `agent.search` 改成数据库查询或 Python 检索器，程序结构不变。

## 为什么这些特性刚好构成一个系统

**值与函数**让数据处理可组合。绑定不可变，`fn` 可以捕获快照、递归、做 map/fold、返回函数或请求；同一个函数在 REPL、解释器和生成的 Go 程序里有同样的结果。

**请求是值，执行只有一个入口。** `effect op(args)` 不调用 provider，只产生带合约身份的请求。请求可以放进 list/object、闭包、函数参数和 Flow 返回值；`perform` 才进行 schema、授权、资源和 provider 调用。这样 agent 可以先计划一组动作，再在明确的位置执行。

**有限描述让封装仍然可分析。** 1.2 会沿着固定字段、索引列表、切片、`put`、函数参数/返回值、闭包和静态 Flow 返回值保留请求与可调用来源。`identity(p:effect)->effect {p}`、容器中的请求和多层纯函数不会凭包装变成黑箱；候选里只要出现 Write、不可并发读取或未知来源，`parallel` 就会在编译期拒绝。

**Flow 是依赖图。** 顺序语句形成前置关系，独立节点获得并发机会，子 Flow 完整成功后才向调用者发布结果。管道 `|>` 把上一步的值注入下一阶段，阶段未完成时不会提前调用后续工具。

**Claim 是证据汇聚边界。** `<+` 追加一条带 JoinID 的事实；相同身份的重放幂等，不同身份的相同值仍是两条贡献。完整读取等待 producer，live snapshot 则只观察线性化点。模型回答、工具结果和检查结论都可以先成为事实，再由纯函数筛选、评分和决定是否发布。

**Host 合约是系统的外部边界。** 合约同时声明参数 schema、返回 schema、版本、读写效果、并发性、权限和资源。Go/Python adapter、CLI、REPL 和生成程序都经过同一条边界；构造请求不会偷偷调用外部服务，已发生的动作也不会被错误重试掩盖。

**诊断与身份让结果可复查。** 节点位置、调用路径、Claim 来源和 provider 记录使用结构身份，而不是完成先后编号。`print`、`log`、`telemetry` 是单向诊断；真正影响业务的动作仍必须写成 `effect/perform`。

## 一个最小并行程序

```lip
import python "example.worker" as worker

fn read_request(x:number)->effect { effect worker.read(x) }
fn identity(p:effect)->effect { p }

flow Main()->list {
    facts = claim()
    for x in [1, 2, 3] parallel {
        boxed = {request: identity(read_request(x))}
        value = perform boxed.request
        facts <+ value
    }
    return [row.value for row in facts]
}
```

这里的请求经过函数参数和 object 字段仍保留 `worker.read` 的静态来源，所以三个只读动作可以并行；`facts` 的完整读取仍按输入位置返回 `[1,2,3]`。

同一规则也适用于已知的闭合容器、条件/返回函数和递归参数转发。它们不会增加新的语法；静态摘要只在调用点把已知请求来源代入，真实未知的开放容器、动态 callable、Write 或不可并发读取仍不能获得并行资格。完整示例见 [`21_summary_recovery`](examples/ecosystem/21_summary_recovery.lip)。

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

需要 Go 1.25 或更高版本。`-host demo` 提供教学用合约；真实应用通过 `Host.Contracts` 注册自己的 Go/Python 操作。REPL 保存成功 cell 的不可变结果和闭包，失败 cell 不会覆盖旧绑定；`lipc learn` 用同一编辑器逐步练习值、函数、Flow、Claim 和 Host。

## 目录

- [基础示例](examples/basics/01_hello.lip)：绑定、算术、容器和字符串。
- [结构示例](examples/structures/12_carry.lip)：条件、函数、map/fold、携带值循环。
- [高级示例](examples/advanced/14_parallel_claim.lip)：并行 Claim、输入、快调用和诊断。
- [生态示例](examples/ecosystem/18_proposals.lip)：Go/Python 请求、顺序写入和记录式迭代。
- [完整示例说明](examples/README.md)：按概念逐步运行所有 `.lip` 文件。

```sh
go install ./cmd/lipc
lipc help
```

完整的语法、值编码、调用边界、调度、Claim、Host 和确定性规则见 [LIP 1.2 规范](spec/LIP-1.2-SPEC.md)。

## 语言核心与程序结构

语法、值、Flow、Claim 和入口协议的完整说明见 [LIP 1.2 规范](spec/LIP-1.2-SPEC.md)。实际程序通常沿着“准备值 → 构造请求 → Flow 调度 → Claim 汇聚 → 纯函数决策”的顺序组织。

## 交互计算和学习

`lipc repl` 与 `lipc learn` 使用同一套编辑器和运行时：先在 REPL 试算值和请求，再用 `:begin`/`:end` 提交多行 Flow；课程则把同一套组合拆成可运行的小步。

## 为什么 fmt.Print 后面会显示数字

Go 函数的返回值会按 Host 合约回到 REPL；需要展示副作用时使用 `print`、`log` 或 `telemetry`。

## 接入 Go / Python

应用通过 `Host.Contracts` 和 adapter 注册外部能力，LIP 程序只依赖稳定的操作合约。合约给出参数/结果 schema、权限、资源、读写效果和并发性；替换模型或工具实现时，Flow 结构保持不变。

## 所有外部动作都走同一条边界

请求由 `effect` 构造，由 `perform` 执行；Go、Python、HTTP 和自定义 Host 使用同一边界。构造阶段可以自由组合和传递请求，执行阶段统一做 schema、授权、资源和 provider 检查。

## 网站与 HTTP 接入

HTTP handler 将请求映射为入口 Data，将外部服务映射为 Host 操作；业务步骤仍由 Flow 表达，页面/JSON 响应只是入口 Flow 的结果。
