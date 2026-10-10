# 原型覆盖表（Alpha 0.6.4）

下面把早期 30 个原型按 Alpha 0.6.4 的真实能力分为三类：可直接表达、需要
Go Host Adapter、以及必须等后续语言能力。示例统一使用当前语法：Flow、
单赋值绑定、显式输入/输出类型、运算符，以及 `match` 分支选值和执行。

核心入口新增 `core.lip`（结构化报告）、`range.lip`（区间/平方和）、
`tree.lip`（纯递归）、`lists.lip`（分组/过滤/排序/转置）。它们不需要 adapter。
完整 list 标准库见 [LIST-LIBRARY.md](../docs/LIST-LIBRARY.md)。

## 当前可直接检查的示例

| 原型 | 当前示例 | 说明 |
| --- | --- | --- |
| 1 Hello Data | `hello.lip` | 依赖、表达式、字符串连接 |
| 2 多级依赖 | `chain.lip` | `a → b → c → d` |
| 3 两条独立计算 | `fanout.lip` | 独立节点可自动调度 |
| 4 条件依赖 | `gated.lip` | `match` 门控，假分支为 `Skipped` |
| 5 条件值选择 | `select.lip` | `if condition { a } else { b }`，只选择值 |
| 6 Fan-out | `fanout_fanin.lip` | 多个消费者 |
| 7 Fan-in | `fanout_fanin.lip` | `combine` 等待多个依赖 |
| 8 Map / 批量并行 | `map.lip` | 运行时展开 Map，结果保持输入顺序 |
| 9 Map + Reduce | `core.lip` / `range.lip` / `lists.lip` | Map、fold、scan/sum/product，无需 Host |
| 16 有限集合循环 | `for.lip` | 顺序 for、独立迭代作用域、match 和 break/continue；不构造结果列表 |
| 11 网络请求 | `async_join.lip` | Host 可返回 `Await` |
| 12 两个异步请求 | `async_join.lip` | 自动等待并 Join |
| 19 HTTP Handler | `http_handler.lip` | Web 能力由 Host 提供 |
| 20 并行数据库查询 | `db_parallel.lip` | 独立读取注册为 ReadOnly，可并行且每 Tick 重读 |
| 22 文件 Pipeline | `file_pipeline.lip` | 顺序依赖和门控 |
| 24 科学计算 DAG | `science_dag.lip` | 两条分支自动汇合 |
| 27 Tool Agent | `tool_agent.lip` | 计划、工具、汇总 |
| 28 并行 Tool Calling | `tool_agent.lip` | 工具节点彼此独立 |
| 14 Retry | `retry.lip` | 有界重试，首次执行计入次数 |
| 17 Feedback | `feedback.lip` | 有界候选验证与修正 |
| 29 Agent verification | `feedback.lip` | Host 提供 revise/verify |

这些文件可以全部运行 `lipc check`。其中带有 `fetch_*`、`query_*`、
`model_*` 等名字的示例现在都用 `import host "..."` 写出所需 adapter；执行
仍需要 Go 宿主注册对应 Host。这不是伪代码，而是 Alpha 的正式互操作边界。

## 部分可表达，但依赖 Host 或显式限制

| 原型 | 当前状态 |
| --- | --- |
| 10 动态数量 Flow | Map 节点在运行时按输入数量动态展开 |
| 13 超时 | 作为 Host Adapter 的 `context.Context` / 参数实现，不是语言关键字 |
| 21 Cache | 可由 Host 封装；ReadOnly/ExternalWrite 效果由 Runtime 调度 |
| 23 Monte Carlo | `random_list`/`random` + Map/fold 可生成和聚合样本；分布、种子与高性能生成器仍由显式 Host 提供 |
| 26 最简单 Agent | 可用 `llm(request)` Host 调用表达 |

## 明确留到后续版本

| 原型 | 缺少的核心能力 |
| --- | --- |
| 15 外部状态变化 | Host 驱动 Tick/SetState 已支持；仍缺事件/流语法 |
| 18 Agent retry loop | 有界 Retry/Feedback、有限 for、`for { ... }` 和 State 已支持；事件/流、可变状态与动态调度组合尚待设计 |
| 30 完整 Agent Workflow | Map、Feedback、State、Retry 已支持；事件/流及动态控制组合待设计 |

## 当前 Runtime 能力

| 能力 | 运行时接口 | 说明 |
| --- | --- | --- |
| 持久 Flow / State | `NewInstance`, `Tick`, `SetState` | 见 `state.lip` |
| 增量重算 | Instance dependency cache | 纯节点可复用 |
| 临时值引用释放 | 生成图的消费者计数 | 见 [lifetimes.lip](lifetimes.lip) 与[生命周期契约](../docs/VALUE-LIFETIMES.md)；Go GC 负责回收 |
| Cancellation | `context.Context` | 状态传播为 `Cancelled` / `Skipped` |
| Effect / Ordering | `RegisterReadOnly`, `Register`, `NodeSpec.After` | 轻量 Runtime metadata |

## 批量检查

完成[安装](../docs/QUICKSTART.md#安装)后，在仓库根目录运行：

```bash
for f in examples/*.lip tests/conformance/*.lip; do
  lipc check "$f" || exit 1
done
```

这组原型用于让每个表达能力和缺口都能由具体例子定位。Alpha 0.6.4 已落实 Map、State/Tick、Retry、Feedback
和 Runtime Effect/Ordering，以及有限/无限 `for`、break/continue；事件、流、while 和可变循环变量由后续版本处理。
