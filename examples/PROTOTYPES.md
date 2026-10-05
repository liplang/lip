# 原型覆盖表（Alpha 0.4）

下面把早期 30 个原型按 Alpha 0.4 的真实能力分为三类：可直接表达、需要
Go Host Adapter、以及必须等后续语言能力。示例统一使用当前语法：Flow、
单赋值绑定、类型标注、运算符、`when` 和 `return`。

## 当前可直接检查的示例

| 原型 | 当前示例 | 说明 |
| --- | --- | --- |
| 1 Hello Data | `hello.lip` | 依赖、表达式、字符串连接 |
| 2 多级依赖 | `chain.lip` | `a → b → c → d` |
| 3 两条独立计算 | `fanout.lip` | 独立节点可自动调度 |
| 4 条件依赖 | `gated.lip` | `when` 门控，假分支为 `Skipped` |
| 5 条件值选择 | `select.lip` | `if ... then ... else ...`，只选择值 |
| 6 Fan-out | `fanout_fanin.lip` | 多个消费者 |
| 7 Fan-in | `fanout_fanin.lip` | `combine` 等待多个依赖 |
| 8 Map / 批量并行 | `map.lip` | 运行时展开 Map，结果保持输入顺序 |
| 11 网络请求 | `async_join.lip` | Host 可返回 `Await` |
| 12 两个异步请求 | `async_join.lip` | 自动等待并 Join |
| 19 HTTP Handler | `http_handler.lip` | Web 能力由 Host 提供 |
| 20 并行数据库查询 | `db_parallel.lip` | 独立查询可注册为 Pure |
| 22 文件 Pipeline | `file_pipeline.lip` | 顺序依赖和门控 |
| 24 科学计算 DAG | `science_dag.lip` | 两条分支自动汇合 |
| 27 Tool Agent | `tool_agent.lip` | 计划、工具、汇总 |
| 28 并行 Tool Calling | `tool_agent.lip` | 工具节点彼此独立 |
| 14 Retry | `retry.lip` | 有界重试，首次执行计入次数 |
| 17 Feedback | `feedback.lip` | 有界候选验证与修正 |
| 29 Agent verification | `feedback.lip` | Host 提供 revise/verify |

这些文件可以全部运行 `lipc check`。其中带有 `fetch_*`、`query_*`、
`model_*` 等名字的示例需要 Go 注册对应 Host 后才能执行；这不是伪代码，
而是 Alpha 的正式互操作边界。

## 部分可表达，但依赖 Host 或显式限制

| 原型 | 当前状态 |
| --- | --- |
| 9 Map + Reduce | Map 已支持；reduce 仍由 Host 操作提供 |
| 10 动态数量 Flow | Map 节点在运行时按输入数量动态展开 |
| 13 超时 | 作为 Host Adapter 的 `context.Context` / 参数实现，不是语言关键字 |
| 21 Cache | 可由 Host 封装；ReadOnly/ExternalWrite 效果由 Runtime 调度 |
| 23 Monte Carlo | 可对 Host 提供的样本列表做 Map；语言尚无 `range` |
| 26 最简单 Agent | 可用 `llm(request)` Host 调用表达 |

## 明确留到后续版本

| 原型 | 缺少的核心能力 |
| --- | --- |
| 15 外部状态变化 | event、stream、tick |
| 16 普通循环 | 普通 `for` / `while` 仍未进入语言核心 |
| 18 Agent retry loop | Feedback + retry + state |
| 30 完整 Agent Workflow | map、feedback、state、retry、event 的组合 |

## Runtime 0.3/0.4 能力

| 能力 | 运行时接口 | 说明 |
| --- | --- | --- |
| 持久 Flow / State | `NewInstance`, `Tick`, `SetState` | 见 `state.lip` |
| 增量重算 | Instance dependency cache | 纯节点可复用 |
| Cancellation | `context.Context` | 状态传播为 `Cancelled` / `Skipped` |
| Effect / Ordering | `RegisterReadOnly`, `Register`, `NodeSpec.After` | 轻量 Runtime metadata |

## 批量检查

```bash
for f in examples/*.lip; do
  go run -buildvcs=false ./cmd/lipc check "$f" || exit 1
done
```

这组原型的目的不是假装 Alpha 已经支持所有范式，而是让每个缺口都能被
一个具体例子准确定位。Alpha 0.4 已落实 Map、State/Tick、Retry、Feedback
和 Runtime Effect/Ordering；事件、流和普通循环仍由后续版本处理。
