# LIP 后续路线图与一致性审计

这份路线图把 `../playaround` 的设计讨论与当前实现分开：已经实现的能力必须有
规范、示例和测试；尚未实现的想法只能作为候选，不会在语言文档中伪装成现成
语义。

## 审计结论（Alpha 0.5）

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

P0/P1 已落地为可替换的独立进程协议和 Go Adapter；通用对象句柄也已经有最小
可运行实现。下一步仍按数据规模和基准决定是否需要更紧的通信或嵌入式解释器。
当前的阶段状态是：

- **P0/P1（已完成基线）：** `runtime.NewProcessHost`/`NewPythonWorker` 用 Go
  `os/exec` 启动并长期驻留的 stdin/stdout JSONL Worker。握手、request ID、deadline、
  有限队列、协作取消、Worker 回收/自动重启、协议错误和 Python 异常都有 Go 侧测试；
  `NewPythonHost` 提供通用 fallback；`RegisterPythonPure`、`RegisterPythonReadOnly`
  和 `RegisterPythonSession` 仍可把稳定领域接口及其效果映射到现有 Runtime。内置
  Worker 提供标准库基线和通用 dotted operation（如
  `numpy.sum`、`torch.nn.functional.relu`、`sklearn.preprocessing.scale`），真实
  样例在 `examples/python`。新增库不需要改 Worker；只需确认 Python 环境和模块策略。
- **P0/P1 的边界：** JSON 控制面承载标量、列表、对象和 session 句柄；每个 Worker
  同时处理一个请求，队列有上限；取消无法安全打断扩展时直接回收进程，下一次调用
  懒启动新 Worker。句柄和 blob 的大小/数量配额已属于 P2 基线，空闲回收和故障
  重建仍待完成。
- **P2（进行中）：数据面和句柄治理。** 当前先实现文件支持的只读 blob：使用私有
  目录、原子发布、`fsync`、SHA-256、大小限制和只读 mmap；同时支持已有 `.npy` 文件
  的 `mmap_mode="r"` 加载。下一步用真实矩阵、DataFrame 和模型输入比较 JSON、文件
  映射、Arrow 与共享内存；当前已有 blob 大小、总数据、打开映射和对象句柄配额，下一步
  补空闲回收、泄漏诊断，以及 Tensor、DataFrame、模型、优化器和 Figure 的明确生命周期。
  当前已有通用对象句柄、
  `python.call`、`python.to_json`、`python.release`。

  P2 同时固定了两个边界：生成入口严格按 Flow 参数解析 CLI，不再注入示例输入；
  `require python/go/host "..."` 提供可审计的依赖元数据，`lipc check` 和生成库的
  `RequiredDependencies()` 都能取回它。声明不会自动安装包，Python 仍通过通用
  dotted call 访问 Worker 环境中已安装的任意库，Go adapter 仍由宿主程序导入和注册。
- **P3：多 Worker、可靠 session 和可替换传输。** 先测量单 Worker 在 LIP 并发下的
  排队比例，再决定 Worker pool；为 session 增加创建、版本、空闲回收和故障重建事件，
  分别限制池大小、Python 线程数和 BLAS/GPU 线程数。只有多路复用、跨机部署或协议
  演进成为真实需求时，才把 JSONL 控制传输替换为 Unix Domain Socket/gRPC，并加入
  流式进度。Gob 不作为 Go/Python 的默认协议。
- **P4：嵌入式 CPython 评估。** 用同一组真实矩阵、DataFrame 和模型输入比较进程
  边界与 cgo/CPython 的冷启动、warm p50/p95、吞吐、峰值内存、取消和崩溃恢复；只有
  基准证明 IPC/复制是瓶颈，且能够接受 GIL、ABI、崩溃隔离和取消限制时，才做可选
  原型，不把嵌入方案变成默认后端。

每一阶段都保持 Host Adapter 可替换；Python 方案改变时，LIP 图、Tick、Effect
和取消语义不变。完整矩阵见 [PYTHON-INTEGRATION.md](PYTHON-INTEGRATION.md)。

### M13：Alpha 0.5 完整程序契约（已完成）

这一里程碑先收紧语言边界，再扩展能力。目标是保证一份 `.lip` 文件自身就是可以
检查、翻译和运行的完整主体，编译器不替例程猜测结果：

1. **统一源文件头。** 顶层只允许 `require` 依赖声明、`fn` 声明和唯一 `flow`；
   关键字采用单数 `require`，拒绝 `requires` 等人为别名。Python、Go 和 Host 的
   依赖都进入同一份可查询元数据，禁止隐式安装和隐式导入。
2. **完整输入/输出契约。** Flow 参数是唯一外部输入；所有边界参数显式写类型，
   动态值写 `any`。Flow 显式声明返回类型，恰好一个 `return`，条件值使用 `if`；
   允许无值完成时显式写可选输出 `Type?`。缺少输入、额外输入、缺少输出和类型
   错误必须在相应阶段失败。
3. **忠实编译。** 删除样例值、环境变量注入和测试旁路；`lipc check`、`build`、
   `run`、生成源码和库模式共用同一 AST/Graph。为未定义名、前向引用、重复绑定、
   作用域逃逸、依赖重复和非法顶层结构建立 conformance 测试。
4. **Python 一致边界。** `require python` 只声明环境能力；任意已安装库使用同一
   dotted call、句柄和只读 blob 协议。独立入口可按声明启动 Worker，库模式由宿主
   控制 Worker；科学计算、统计、机器学习、深度学习、视觉和 Transformers 不做
   固定白名单。
5. **迁移和诊断。** 为 Alpha 0.4 的省略类型和旧依赖关键字提供一次性诊断/迁移，
   但 v0.5 不保留含糊的兼容语义。错误必须指出源位置、声明和修复方向。

验收规范见 [ALPHA-0.5-SPEC.md](ALPHA-0.5-SPEC.md)。显式输入/输出、可选门控输出、
纯局部函数组合、迁移命令、严格 CLI、依赖元数据和端到端 conformance 均已落地。
Python Flow 调用真实 NumPy/Pandas，Worker 没有 `pandas.describe` 之类的替身；重启旧
句柄、释放、blob 篡改和配额失败由 Runtime 测试覆盖。版本约束仍由部署环境管理，
不扩张为编译器内的包管理系统。

P1 验收命令：

```bash
GOCACHE=/tmp/lip-gocache go test ./runtime -run Python
GOCACHE=/tmp/lip-gocache go test ./runtime -run '^$' -bench 'PythonWorker'
GOCACHE=/tmp/lip-gocache go run ./examples/python
```

后续推进按这四个门槛执行：

1. **P2 数据面：** 文件支持的只读 blob、`.npy` 映射、`dtype`、`shape`、字节数、
   SHA-256 与释放责任已经有基线实现。下一步用真实矩阵、DataFrame 和模型输入测量
   JSON 复制、文件映射、Arrow 与共享内存的 p95、峰值内存和恢复行为；只有证据显示
   文件映射仍是瓶颈，才加入 Arrow/共享内存，控制面 API 保持不变。
2. **P2 对象与模型句柄（基础已实现）：** Worker 已支持通用句柄、
   `python.call`、`python.to_json`、`python.release` 以及 blob/映射/对象配额；下一步
   为 Tensor、DataFrame、模型、优化器和 Figure 补充显式 session 创建、空闲回收、跨
   Worker 重建和泄漏诊断。不得把 Python 对象指针伪装成 LIP State，也不得让句柄无限期
   存活。
3. **P3 多 Worker / session：** 先测量单 Worker 在 LIP 并发下的排队比例，再决定
   Worker pool。session 必须有创建、版本、空闲回收和故障重建事件；池大小、Python
   线程数和 BLAS/GPU 线程数分别设上限。
4. **P4 嵌入评估：** 用同一组真实矩阵/DataFrame/模型输入比较冷启动、warm p50/p95、
   吞吐、峰值内存、取消和崩溃恢复。没有数据证明 IPC 是瓶颈，就不引入 cgo/CPython。

### M14：事件与流（谨慎推进）

只有当 Host→Runtime→Tick 的手动触发模式在真实例子中不足时，才引入事件或
Stream primitive。它们必须明确生命周期、背压、取消和 State 快照，不能直接把
每个 Host callback 变成隐式 Tick。

### 暂不推进

- 完整 Effect Type System；
- 直接在 LIP 中执行 Python/Go `import` 或自动安装任意包；依赖声明使用
  `require python/go/host "..."` 元数据，实际导入和注册仍由 Worker/Go 宿主负责；
- 无界 Feedback、自动 detach/background；
- 为了“看起来像 Agent”而增加 Agent 专用语法；
- 自定义 VM、GC 或绕过 Go 工具链的后端。
