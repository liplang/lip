# Python 科学计算与进程集成设计

Python 是 LIP 的 Host 能力，不是 LIP 编译器的语言依赖。LIP 负责依赖图、
Logical Tick、取消和调度；Python 负责 NumPy、SciPy、Pandas、PyTorch 以及
现有科学计算生态。这样即使没有安装 Python，普通 `.lip` 文件仍然可以被检查、
编译和运行。

这份文档比较不同的集成路线。结论是有条件的：先采用可替换的进程协议，只有
基准数据证明进程边界已经成为瓶颈时，才考虑更紧的耦合。

当前 Alpha 0.4 实现只有 Go `runtime.Host` 边界，还没有 `ProcessHost`、Python
Worker 或 Arrow 数据面；下面的 P0–P4 是设计和验收顺序，不是已经发布的 API。

## 结论先行：推荐的基本形态

是的，当前最推荐的基本形态就是 **Go 进程管理一个长期驻留的 Python 子进程，
通过 IPC 请求计算**：

```text
Go LIP host
  └─ exec.Command(...).Start() → python worker（常驻内存）
       ├─ stdin/stdout pipes + JSON Lines（P1 默认）
       └─ Unix Domain Socket + 同一协议（P2 可选）
```

Python Worker 在 Go Adapter 初始化时启动一次，完成握手后保持进程和已加载的模型、
数组或运行环境；后续 LIP 节点只发送带 request ID 的请求，不反复 launch。空闲
Worker 仍然占用内存，因此应有明确的 Worker 数量、空闲回收和最大生命周期策略；
需要模型复用时，可用一个有界 Worker pool，而不是无限创建进程。

第一版优先使用 `os/exec` 的 stdin/stdout 管道：它没有额外 socket 依赖，容易在
测试中记录和重放，Python 标准库即可实现。Unix Domain Socket 适合后续需要多个
并发请求、双向事件、独立连接或 Worker 池管理时使用；它是传输层替换，不应改变
request、cancel、error 和 session 语义。可以参考 `pyproc` 一类库，但不把某个库
锁定为 LIP 的架构依赖。

JSON Lines 适合第一版的控制消息和小数据。**不建议把 Gob 作为 Go/Python 的
跨语言协议**：Gob 是 Go 的编码格式，Python 端需要另写兼容实现，类型演进、调试
和安全边界都更难控制。若 JSON 在实际基准中成为瓶颈，再单独评估 MessagePack、
CBOR 或 protobuf；大型 NumPy 数组应走 memory-mapped 文件、Arrow 或共享内存，
通过 JSON 只传句柄、dtype、shape、字节数和校验信息。

### 常驻 Worker 的生命周期

1. Go 用 `os/exec` 启动 Python，分离 stdin/stdout 和 stderr，并执行版本/能力握手；
2. Adapter 为每个请求分配唯一 ID，发送 operation、参数、session 和 deadline；
3. Worker 返回完成响应，也可以发送同一 ID 的有限 progress 事件；
4. Go 取消或超时时先发送 cancel frame，Worker 不能及时合作时关闭并替换进程；
5. Go 在优雅关闭时等待已接受的请求，释放临时数据面资源，再关闭 Worker。

这样可以同时得到进程隔离、模型常驻和可恢复性：Python 崩溃不会拖垮 Go，Go
也不会把 Python 的状态偷偷变成 LIP 的 State。

## 先区分两个问题

Python 集成有两个独立的设计问题：

1. **调用通道（control plane）**：如何发送 operation、参数、取消、超时和结果；
2. **数据通道（data plane）**：如何传递大型数组、表格和模型权重。

JSON Lines、Unix socket、gRPC 和 HTTP 主要解决调用通道。Arrow、memory-mapped
文件和共享内存主要解决数据通道。它们可以组合，例如“Unix socket 控制面 +
Arrow 文件数据面”，不必一次选择一个包办所有问题的方案。

## 候选路线比较

下表中的“高/中/低”是 Alpha 阶段的工程判断，不是跨机器的性能承诺。启动延迟
指首次建立执行环境的成本；单次延迟指 Worker 已经存在时的额外通信成本。

| 路线 | 隔离与故障边界 | 启动/单次延迟 | 吞吐与大数组 | 取消、会话与恢复 | 部署与调试 | 适合场景 | Alpha 判断 |
| --- | --- | --- | --- | --- | --- | --- | --- |
| 每次调用启动 Python 子进程 | 隔离强；杀进程即可回收 | 启动高，单次低到中 | 吞吐低；JSON/文件复制明显 | 取消直接；无会话；失败恢复简单 | 部署最简单，日志直观 | 偶发、昂贵、必须强隔离的任务；原型 | **可做基线，不作为默认** |
| 长驻 stdin/stdout JSONL Worker | 每个 Worker 独立；崩溃不拖垮 Go | 启动一次，单次低 | 小数据足够；JSON 不适合大数组 | 需要 request ID、cancel、超时和重启；支持 session | 依赖少，协议易抓包和复现 | 本机科学计算、模型复用、第一版实现 | **P1 推荐** |
| Unix socket / gRPC 长驻 Worker | 进程隔离；可做多路复用和明确 schema | 启动一次；socket 低，gRPC 有少量序列化成本 | 可并发；仍需独立数据面处理大数组 | deadline/cancel 标准；session 和重连可定义 | gRPC 依赖和版本管理更复杂；跨机更方便 | 多请求并发、服务化、需要强 schema 的团队 | **P2，先有基准再升级** |
| HTTP/REST Python 服务 | 服务进程隔离；网络边界清晰 | 服务预热；HTTP 额外开销较高 | JSON/HTTP 对数组不理想，可配对象存储 | 超时和重试成熟；会话需显式 token；恢复由服务负责 | 运维、观测和权限工具多；本地调试方便 | 已有 FastAPI/模型服务、跨机或云部署 | **外部服务场景可选** |
| 独立数据面：Arrow IPC / Flight、memory-mapped 文件、共享内存 | 数据面本身不提供调用隔离 | 建立缓冲区有成本；大数组摊销好 | 大数组吞吐高，复制少；生命周期复杂 | 取消仍由控制面完成；句柄泄漏需回收 | 工具链和诊断复杂；需校验 dtype/shape/版本 | 批量矩阵、DataFrame、模型权重 | **与 P1/P2 组合的 P2 数据面** |
| 嵌入 CPython（cgo/C API、cffi） | Python 崩溃、GIL 或扩展问题可能影响 Go 进程 | 启动可低；调用额外开销低 | 可减少复制；线程和内存模型最复杂 | 取消常不能强制中断；解释器重置困难 | 构建、虚拟环境、ABI 和调试成本最高 | 已有严格基准证明 IPC 不可接受的单机热路径 | **P4 备选，不进核心路径** |
| 独立模型/计算服务（Ray、专用推理服务等） | 通常有自己的进程/容器边界 | 依赖服务预热；网络开销取决于部署 | 可用服务原生批处理和 GPU 数据面 | 任务、重试、会话由服务协议定义 | 运维面最大；版本和权限需单独治理 | 团队已有平台，LIP 只需调用能力 | **作为外部 Host，不在 LIP 内建** |

几个结论需要特别保留：

- “每次启动进程”适合做正确性和故障语义的基线，但不能用它的性能推断长驻
  Worker 的性能。
- JSONL、Unix socket、gRPC 和 HTTP 是互相竞争的**调用通道**；Arrow、映射文件
  和共享内存是可叠加的**数据通道**。
- 嵌入 CPython 的低延迟只有在真实的数组大小、调用频率、并发度和部署方式下
  才有意义。没有基准就不引入它。
- 已有 Python 服务时，LIP 应调用其稳定 API；不应为“统一语法”把服务重新搬
  进编译器或 Runtime。

## 推荐的保守架构

第一版采用本机长驻 Worker，控制面先用 framed JSON Lines：

```text
LIP Graph
   ↓ Host.Call(ctx, operation, args)
Go Python Adapter
   ↓ request/response JSONL（可替换为 socket/gRPC）
长期运行的 Python Worker
   ↓
NumPy / SciPy / Pandas / PyTorch
```

Go Adapter 是边界的唯一所有者：它编码 `runtime.Value`，维护 request ID，施加
队列和并发上限，传递 deadline/cancellation，把 Worker 退出、协议损坏和 Python
异常转换为可追踪的 `runtime.Result`。编译器不解析 Python，也不需要 Python 才
能完成 `lipc check`。

### 第一版 JSONL 协议

每一行是一个完整消息。请求：

```json
{"id":"42","op":"numpy.linalg.solve","args":[[[3,0],[0,2]],[6,4]],"session":"default","deadline":"2026-10-05T12:00:01Z"}
```

成功响应：

```json
{"id":"42","ok":true,"value":[2,2]}
```

失败响应：

```json
{"id":"42","ok":false,"error":{"type":"ValueError","message":"singular matrix"}}
```

协议必须满足：

- `id` 在一个 Worker 生命周期内唯一，响应必须回到同一个请求；
- stdout 只发送协议消息，日志写 stderr；
- deadline 到期或 Go 取消时发送 cancel frame；不能及时合作取消时回收该 Worker，
  避免一个失控任务污染后续请求；
- 请求队列、单 Worker 并发和 Worker 池大小都有上限，超过上限返回背压错误；
- 启动、退出、协议损坏、JSON 解码和 Python 异常都要带 operation/request ID；
- `session` 只能由 Go Adapter 创建和销毁，不能让 Python 任意改变 LIP State；
- 协议版本、Python 包版本和能力列表可在启动握手中报告。

第一版只传 JSON 标量、列表和对象。大数组不应长期复制成 JSON，而应通过独立的
数据面携带 `dtype`、`shape`、字节数、校验和和释放责任。优先顺序是：

1. 小规模 JSON，作为协议正确性基线；
2. 本机临时 `.npy`/memory-mapped 文件，作为低依赖的大数组方案；
3. Arrow IPC/Flight 或共享内存，只有基准显示文件映射仍不足时再引入。

## LIP 效果、并发和 Python session

Python operation 注册到现有 Host：

```go
host.RegisterPure("solve", pythonSolve)       // 结果只由输入决定
host.RegisterReadOnly("load_model", loadModel) // 外部读取，可并发
host.Register("save_model", saveModel)        // 写入或改变 session，顺序屏障
```

只有在操作确实满足声明时才使用 `RegisterPure`。只读模型或数据源可以使用
`RegisterReadOnly`，但每个 Instance Tick 仍会重新调用；写文件、更新数据库、
改变 Worker session 或无法证明安全的操作使用普通 `Register`，让 Runtime 采用
保守的顺序屏障。

LIP 的并发上限、Go Adapter 的请求队列、Worker 池大小、Python 线程数和 BLAS/GPU
并发是不同层次的限制，必须分别设置。否则“图并发 × Worker 并发 × 数值库线程”
会造成资源失控。session 是资源，不是隐式的全局变量：需要明确初始化、版本、
空闲回收、优雅关闭和故障后的重建。

## 对话、进度和长任务

“进程对话”先定义为有边界的请求/响应会话：

```text
LIP node → request → Python session
LIP node ← progress* / response / error
```

长任务可以发送带 request ID 的 `progress` 事件，但最终必须有且只有一个完成
响应。Python 需要工具或人工确认时，由 Go Adapter 暴露显式 callback operation；
Worker 不得任意执行宿主命令，也不得绕过 Runtime 的取消和权限边界。

## 分阶段实现和退出条件

- **P0：协议基线。** 用 Python 标准库写 echo、失败和延迟 Worker；测试 request
  ID、超时、取消、重启、背压、stderr 日志和协议损坏。记录冷启动与长驻 Worker
  的 p50/p95 延迟。
- **P1：`ProcessHost`。** Go 侧用 `os/exec` 启动长期 Worker，通过 stdin/stdout
  JSONL 提供 operation 注册、Worker 池、有限队列、deadline、协作取消和故障替换；
  默认按 ExternalWrite 处理，只有明确声明的读操作才允许并发，写操作显式标记。
- **P2：数据面。** 先加入 memory-mapped `.npy`；用真实矩阵、DataFrame 和
  模型输入比较 JSON、映射文件与 Arrow 的复制次数、p95 延迟和峰值内存。
- **P3：socket/gRPC 与 session。** 只有当多路复用、跨机部署或 schema 演进
  的需求真实出现时，才把 JSONL 控制面替换或扩展为 Unix socket/gRPC，并加入
  session 初始化、模型复用、能力协商和流式进度。
- **P4：嵌入评估。** 以可重复基准证明 IPC、数据复制或 Worker 数量是瓶颈，且
  能接受 GIL、ABI、崩溃和取消限制后，才做 CPython/cgo 原型。原型必须是可选
  Adapter，不能改变 LIP 编译器的安装和构建要求。

每个阶段都应保留一个可删除的 Adapter 接口；如果 Python 方案被替换，LIP 图、
Effect、Tick 和取消语义不应改变。

## 暂不做的事情

- 不在 LIP 语法中直接 `import` 任意 Python 包；
- 不让 Python Worker 反向驱动隐式 Tick、绕过 State 或持有无界后台任务；
- 不把 JSON 数组协议宣传成高性能张量通道；
- 不以“嵌入更快”代替真实基准、故障测试和部署评估；
- 不把特定框架（Ray、FastAPI、PyTorch Server）写进 LIP 核心。

这条路线先获得 Python 生态的实际价值，再根据可测量的瓶颈选择数据面或通信
升级，同时保留编译、部署、权限、取消和错误传播的清晰边界。
