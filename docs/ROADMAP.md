# LIP 后续路线图与一致性审计

这份路线图把 `../playaround` 的设计讨论与当前实现分开：已经实现的能力必须有
规范、示例和测试；尚未实现的想法只能作为候选，不会在语言文档中伪装成现成
语义。

## 审计结论（Alpha 0.4）

当前实现与 playaround 的主线一致：

```text
LIP source
  → AST
  → name/type/dependency analysis
  → Graph IR
  → Runtime scheduling metadata
  → generated Go
```

以下不变量已经由代码和测试共同维护：

- AST、Graph Node 和动态 Map execution instance 分离；
- 数据依赖决定可执行资格，`when` 是 Gate，`if` 是值选择；
- Map 只展开运行时元素，保持输入顺序并遵守并发上限；
- State 只由 `SetState`/Host/API 更新，不自动形成图循环；
- Retry 和 Feedback 都有正整数上限，不会形成无限 Runtime 循环；
- cancellation 通过 Context、Await、Map、Retry、Feedback 向依赖传播；
- `Pure` 可缓存，`ReadOnly` 可并发但每 Tick 重新执行，`ExternalWrite` 形成
  顺序屏障；
- 并发是“允许并发”的调度机会，不是语言层的执行顺序承诺。

`Ready` 在设计文档中是“可以执行”的概念状态；当前 Runtime 的公开
`Status` 用 `Pending`、`Running`、`Completed`、`Error`、`Cancelled`、`Skipped`
表示节点生命周期，`runtime.Result` 的 `Ready(...)` 表示计算结果。这是有意的
边界：Ready value 不会伪装成用户可见的 LIP 值类型。

仍需保持清晰的历史边界：[ALPHA-0.2-SPEC.md](ALPHA-0.2-SPEC.md) 描述 0.2 当时只实现 Map，
[ALPHA-0.3-SPEC.md](ALPHA-0.3-SPEC.md) 增加持久 State/Tick，[ALPHA-0.4-SPEC.md](ALPHA-0.4-SPEC.md) 增加 Retry、
Feedback、Cancellation、Effect/Ordering。旧规范不是当前实现的完整替代品，而
是每个发布边界的记录；README 和 [规范索引](SPECS.md) 提供完整入口。

## 下一步建议

### M8：CLI 和开发体验（已完成）

目标是让 `lipc` 的最短路径接近 Go：

- `lipc version`、`lipc help [command]`；
- `lipc check file.lip`；
- `lipc build file.lip [-o executable]`；
- `lipc run file.lip [-- args...]`；
- `lipc build -emit-go ...` 作为需要审查生成代码时的明确入口；
- 后续加入 `lipc fmt` 和结构化 `--trace`，不让诊断依赖临时改源码。

验收结果：从一个 `.lip` 文件已经可以完成 check、build、run；失败时返回非零
退出码；生成的 Go 仍可单独 `go build`。下一步只需补 `fmt` 和结构化 trace，
不再改变这几个命令的基本语义。

### M9：Trace / Inspect

把当前 Trace 扩展为可读的开发工具：节点耗时、等待依赖、缓存命中、重试次数、
取消原因和 Map 元素错误索引。先做 JSON 输出，再做终端表格；Trace 不改变
Flow 语义。

### M10：更严格的 IR 验证

在 Graph 构建后统一检查：重复 Node 名称、未知 `After`、Ordering cycle、
非法 Gate 依赖、State 的初值依赖和 Output 数量。Runtime 仍保留运行时防线，
但错误应尽量在 `lipc check` 阶段出现。

### M11：数据与资源模型

为 Map、Host 和未来 Python Worker 增加可选资源标签：最大并发、超时、内存/CPU
预算、重试退避和幂等性。资源标签只约束调度，不改变依赖语义。

### M12：Python / 外部 Worker Adapter

先实现可替换的独立进程协议和 Go Adapter，再决定是否需要更紧的通信或嵌入式
解释器。当前的比较结论是：

- P0 先做标准库 echo/失败/延迟 Worker，测量冷启动、长驻 Worker 的 p50/p95、
  取消、重启、背压和协议损坏；
- P1 采用 Go `os/exec` 启动并长期驻留的 stdin/stdout JSONL Worker，Go 负责 request
  ID、deadline、有限队列、Worker 替换和 Effect 映射；不使用每次调用重新 launch；
- P2 先用 memory-mapped `.npy` 处理大数组，再用真实矩阵/DataFrame/模型输入
  比较 Arrow 或共享内存；
- P3 只有在多路复用、跨机部署或 schema 演进成为真实需求时，才引入 Unix Domain
  Socket/gRPC、session 和流式进度；Gob 不作为 Go/Python 的默认协议；
- P4 只有基准证明 IPC/复制是瓶颈且能够接受 GIL、ABI、崩溃和取消限制时，才做
  可选的 CPython/cgo 原型。

每一阶段都保持 Host Adapter 可替换；Python 方案改变时，LIP 图、Tick、Effect
和取消语义不变。完整矩阵见 [PYTHON-INTEGRATION.md](PYTHON-INTEGRATION.md)。

### M13：事件与流（谨慎推进）

只有当 Host→Runtime→Tick 的手动触发模式在真实例子中不足时，才引入事件或
Stream primitive。它们必须明确生命周期、背压、取消和 State 快照，不能直接把
每个 Host callback 变成隐式 Tick。

### 暂不推进

- 完整 Effect Type System；
- 直接在 LIP 中 import 任意 Go/Python 包；
- 无界 Feedback、自动 detach/background；
- 为了“看起来像 Agent”而增加 Agent 专用语法；
- 自定义 VM、GC 或绕过 Go 工具链的后端。
