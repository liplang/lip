# LIP 1.2

[English](README.en.md) | [简体中文](README.md)

**顺序执行的假设是可疑的。**

多数程序把步骤一行行往下写，但很多先后顺序其实只是书写习惯，不是真的需要。
三次互不相关的检索被写成先后执行，就白白多等了两次。

LIP 不默认这种顺序，**LIP 的运行时，会自动安排并行**。

自动并行要成立，还需要两个配套机制：

| 机制 | 解决的问题 |
|------|-----------|
| **`effect` + `perform`**：执行请求 | `effect` 只构造请求，不执行；`perform` 是唯一的执行入口。每个动作的能否执行都在后面判断。 |
| **`claim` + `fold`**：收集信息 | 并行产生的结果用 `<+` 汇入 Claim，按结构身份排列，与谁先完成无关。 |

纯计算交给 `fn`，Go 和 Python 负责数据库、网络、模型等外部能力。
LIP 只表达它们之间的依赖、并发、证据和结束条件。

[语言规范](spec/LIP-1.2-SPEC.md) · [示例教程](examples/README.md) · [实现与验证](IMPLEMENTATION.md)

## 一个例子

假设 Host 注册了 `agent.search` 和 `agent.chat` 两个操作，合约都声明为 Concurrent ReadOnly：

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

程序里没有锁，没有回调，也不需要手动 join。

把 `agent.search` 换成数据库查询或 Python 检索器，程序结构不变。

### Flow：自动调度并行
### effect + perform：请求先审核，再执行
### claim + fold：并行结果的确定性汇聚

## 快速开始

需要 Go 1.25 或更高版本。

```sh
make build
./lipc run examples/basics/01_hello.lip
./lipc run -parallel 4 examples/evidence.lip
./lipc repl            # 交互试算；只保存成功 cell 的结果，失败 cell 不会覆盖已有绑定
./lipc learn -list     # 分步课程：值 → 函数 → Flow → Claim → Host
```

生成独立的 Go 程序：

```sh
./lipc build -o agent examples/advanced/26_flows.lip
./agent -input '{"xs":[3,1,2]}'
```

`-host demo` 提供教学用的合约；实际应用通过 `Host.Contracts` 注册自己的 Go/Python 操作。
`lipc`、`lipc <命令> -h` 可查看命令说明。在 REPL 中，Go 函数的返回值会按合约显示出来；需要输出内容时，用 `print`。

## 目录

- [基础](examples/basics/01_hello.lip)：绑定、算术、容器、字符串
- [结构](examples/structures/12_carry.lip)：条件、函数、map/fold、携带值循环
- [并行与 Claim](examples/advanced/14_parallel_claim.lip)：并行汇聚、输入、诊断
- [请求与生态](examples/ecosystem/18_proposals.lip)：请求的审阅与放行、Go/Python 调用、顺序写入
- [全部示例](examples/README.md) · [LIP 1.2 规范](spec/LIP-1.2-SPEC.md)
- 
