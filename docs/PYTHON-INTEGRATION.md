# LIP 0.6.4 Python 科学计算与进程集成设计

Go/Python/纯库组合从[教程](TUTORIAL.md)第 19 节的 mixed Flow 开始。
有限标量、list/tuple/object 自动进入 LIP 值域，长期对象使用句柄。
set/frozenset 保留句柄，需要有序列表时调用 builtins.sorted。
非有限数值和字符串化后冲突的对象键会报告错误。
未使用的 Python 导入保留元数据，不强制启动 Worker；实际使用模块属性、调用、
feedback 回调或循环中的 Python 操作才需要 Worker。命名空间别名可与内置操作
或本地 fn 同名，裸调用与成员调用分别解析。

Python 调用参数可以直接组合 LIP 的纯列表表达式，在文件和 REPL 中均可使用：

```lip
import python "numpy" as np

result = np.mean([x for x in range(1, 19)])
print(result) # 9.5，range 包含 1 到 18。
```

推导式在 LIP 中生成列表，再传给 Python；不用为范围和变换各写一个中间变量。
嵌套推导式顺序求值，独立 Map 仍使用现有有界调度。

LIP 负责依赖图、Logical Tick、取消和调度，Python 提供 Worker 环境中
已安装的科学计算、数据处理、机器学习和自定义库。
`runtime.PythonWorker` 用 `os/exec` 管理常驻进程，通过
`runtime.NewPythonHost` 或 `Host.RegisterPython*` 接入图。

当前协议使用 stdin/stdout JSONL，包含握手、request ID、deadline、有限队列、
协作取消、异常返回和进程回收/重启。较大数据通过只读文件 blob 传递，支持
校验和、大小与数量配额。下面的阶段表区分已实现能力与后续评估方向，
语言的整体计划见[路线图](ROADMAP.md)。

路线可以按四个阶段阅读：

| 阶段 | 状态 | 主要工作 | 进入下一阶段的依据 |
| --- | --- | --- | --- |
| P0 | 已完成 | JSONL 握手、request ID、错误、超时、取消、重启、背压和协议损坏处理 | 控制面测试稳定通过 |
| P1 | 已完成 | `PythonWorker`、通用 dotted call、动态导入、效果映射、示例和基准入口 | 能在真实 Flow 中调用已安装库并取回结果 |
| P2 | 进行中 | 文件支持的只读 blob、`.npy` 映射、校验和、大小/映射/对象句柄配额；随后补 Arrow/共享内存评估、空闲回收、泄漏诊断和复杂对象生命周期 | JSON 复制成为 p95 或峰值内存瓶颈，且数据面基准可复现 |
| P3 | 后续 | Worker pool、session 版本与故障重建、并发配额、Unix socket/gRPC 和流式进度 | 单 Worker 排队或跨机/协议演进需求被真实工作负载证实 |
| P4 | 评估项 | cgo/嵌入 CPython 的性能、取消、崩溃隔离和 ABI 对比 | 基准证明进程边界是主要瓶颈，并接受更高耦合成本 |

P2 的对象句柄基础和大小/数量配额已经落地，但不代表 P2 完成：目前句柄保存在
单个 Worker session 中，仍需要空闲超时、主动回收、泄漏诊断和故障重建策略。

### P2 当前选择：文件支持的只读数据句柄

P2 不直接把 POSIX shared memory 当作默认方案。共享内存的吞吐很好，但跨平台
清理、崩溃后的孤儿段、所有权和同步协议都更复杂；Arrow 适合有稳定 schema 的
表格和列式数据，但会引入更大的 Go/Python 依赖面。当前先采用本机文件支持的
只读数据句柄作为可靠基线，再用基准决定是否增加 Arrow 或共享内存：

```go
blob, err := worker.PutBlob(ctx, bytes, runtime.PythonBlobMetadata{
    DType: "<f8", Shape: []int64{rows, columns},
})
result := worker.Call(ctx, "numpy.sum", []runtime.Value{blob})
err = worker.ReleaseBlob(ctx, blob)
```

`PutBlob`/`PutFile` 使用 Worker 专属目录、0600 临时文件、写入后的 `fsync`、同目录
原子重命名和 SHA-256 描述符。Python 首次打开时检查路径不能逃出数据目录、文件大小
和摘要，然后以只读 mmap 建立 NumPy view；控制面只发送文件名、大小、摘要、格式、
dtype、shape 和 order。`Format: "npy"` 使用 `numpy.load(..., mmap_mode="r",
allow_pickle=False)`，适合已有 `.npy` 文件；默认 `raw` 格式适合 Go 直接产生的
二进制数组。句柄释放会关闭映射并删除文件，Worker 关闭时会清理它自己创建的目录。

这个基线的可靠性边界是明确的：它要求 Go 和 Python Worker 位于同一台机器并共享
文件系统，首次打开需要一次 SHA-256 扫描，数据文件不会通过 JSONL 传输。它不承诺
跨机器、跨容器挂载或无限期缓存；这正是 P3 传输升级和 P2 句柄治理的输入。只有在
真实基准显示文件映射仍是瓶颈时，才增加 Arrow/共享内存，并保持同一个逻辑句柄协议。

## 当前可运行的 P1 入口

Go Host 启用 Python fallback，LIP 中的 dotted operation 原样传到 Worker；需要
稳定领域接口时仍可以额外映射 Host 名称。编译器不需要安装 Python，也不解析
Python 代码：

```go
worker, err := runtime.NewPythonWorker(ctx, runtime.PythonWorkerConfig{})
if err != nil { /* Python 不可用或握手失败 */ }
defer worker.Close()

host := runtime.NewPythonHost(worker)
value, trace, err := scientific.Run(ctx, host, map[string]runtime.Value{
    "values": []runtime.Value{1, 2, 3, 4, 5},
})
```

对应的 LIP 仍是普通 Host 调用：

```lip
import python "numpy>=1.26"
import python "pandas"

flow Scientific(values: any) -> any {
    total = numpy.sum(values)
    average = numpy.mean(values)
    series = pandas.Series(values)
    raw_stats = python.call(series, "describe", [])
    stats = python.to_json(raw_stats)
    python.release(raw_stats)
    python.release(series)
    return [total, average, stats]
}
```

仓库中的完整可运行样例是 [`examples/python`](../examples/python)，执行
`GOCACHE=/tmp/lip-gocache go run ./examples/python` 会启动真实 Python 子进程，
调用 NumPy/Pandas，并把 JSON 可表示的结果和 Trace 取回 Go。缺少 NumPy/Pandas 时，
这个 Flow 明确失败。Worker 的 `sum`、`mean` 等工具操作只用于协议基线测试，
不替代真实库调用；部署程序应确认所需依赖。内置 Worker 默认支持动态导入所有已安装的 Python
模块，不维护一个需要逐项更新的库名单；部署方若需要权限收紧，可以配置
`AllowedModules`/`DeniedModules`，让文件、网络和命令类模块由显式策略决定。

这里的“所有库”指 Worker 所使用的 Python 环境中已安装、并且能通过模块路径取得
的 Python API。LIP 不负责替用户安装依赖，也不会把每个包复制成一层 Go 绑定；升级
或替换库只需要更新该环境。`python.call`、`python.to_json`、`python.release` 和
`python.module_available` 属于 Worker 控制面，在设置 `AllowedModules` 后仍然可用，
因此受限部署仍能管理句柄和检查可选依赖。默认允许所有安装模块意味着 Python 子进程
拥有自身的操作系统权限；生产环境应使用专用虚拟环境、容器或外部 sandbox，并按需
设置模块策略。

文件头的 `import python "导入根或版本范围"` 是显式的依赖声明（例如 `sklearn`
是 `scikit-learn` 的导入根，编译器不改名）。可以显式写
`import python "sklearn" as ml`，随后调用 `ml.preprocessing.scale(...)`；
不写 `as` 则仍用 `sklearn.preprocessing.scale(...)`。
模块字符串也支持点分路径，如 `import python "xml.etree.ElementTree" as et`。
别名只影响源码名称，不修改真实 Python 路径、安装包名或模块访问策略。
`as` 增加一个可用名称，原模块路径仍可引用；同一模块也可以同时声明原名
和多个不同别名。在 REPL 中重复输入相同声明不会报错，别名冲突仍会报错。
模块与 LIP 标准库同名时，也可以用显式别名区分，如
`import python "string" as text`，同时保留 LIP 的 `string.*` 操作。
Host/Go 同样支持 as：`import go "fmt" as f` 使 `f.Println(...)` 使用注册名
`fmt.Println`；`import host "service.*" as s` 使 `s.fetch(...)` 使用 `service.fetch`；
`import host "fetch" as f` 则直接写 `f(...)`。Go 包路径完整保留，操作由宿主适配器注册。
`import go "包路径"` 声明由宿主适配器提供的 Go 包命名空间，`import host "操作名"` 用于
说明必须由宿主注册哪个 Host operation。`lipc check` 会打印这些声明，库模式的
`RequiredDependencies()` 会把它们返回给部署代码；声明不会联网安装、不会自动
修改解释器，也不会把一个固定库白名单写进编译器。dotted
Python operation 要有匹配的 `import python`（或明确的 `import host`），bare Host
operation 有 `import host`；缺声明在 `lipc check` 阶段失败。这样通用库调用不受
白名单限制，但源文件仍然完整地说明运行环境。

导入的模块还支持属性读取，例如 `version = np.__version__`、
`pi = math.pi`。可传输的属性直接成为 LIP 值；模块、函数和类返回具名引用，
显示为 `<Python module numpy>` 或
`<Python function numpy.mean; call with (...)>`，读取函数不会执行它。
具名引用可保存在 REPL 中跨单元查看；它记录路径，在接收它的 Worker 中
重新解析并检查模块访问策略。它不是 LIP 的纯 `fn`，可用原模块路径调用，
也可以通过 `python.call(reference, "__call__", [arguments])` 调用。
`python.getattr("numpy.__version__")` 提供对应的 Worker 控制操作。
文件、Flow 与 REPL 均支持组合外部读取和调用，例如
`print(np.__version__)` 或 `print(np.mean([1,2,3,4]))`。参数按书写顺序等待结果，
if/match/布尔短路只执行所选部分；纯 fn 和集合回调仍不允许外部调用。

### 通用库调用，而不是逐个内置库

Worker 的调用格式是 `module.submodule.callable`，所以新增库不需要修改 Go 或
Worker 的代码：

```go
host.RegisterPythonPure("relu", worker, "torch.nn.functional.relu")
host.RegisterPythonPure("standardize", worker, "sklearn.preprocessing.scale")
host.RegisterPythonPure("random_sample", worker, "random.random")
host.RegisterPythonReadOnly("transformer_log_level", worker, "transformers.utils.logging.get_verbosity")
```

同样的方式可以直接调用 `networkx.*`、`sympy.*`、`statsmodels.*`、
`QuantLib.*`、`zipline.*` 以及用户自己的包；Worker 不需要知道这些包的名字。

这些 Python 包的导入名分别是 `matplotlib`、`sklearn`、`xgboost`、`torch`、`jax`、
`equinox`、`keras`、`cv2`、`transformers` 和 `pytorch_lightning`（新版 Lightning
也可能使用 `lightning`）。只要包安装在 Worker 使用的同一个 Python 环境中，动态
调用即可工作；包不存在时，结果会返回带 operation 名的 Python 异常。

部署方可以收紧模块策略，而不改变 LIP Flow：

```go
worker, err := runtime.NewPythonWorker(ctx, runtime.PythonWorkerConfig{
    AllowedModules: []string{"torch", "transformers", "sklearn"},
})
```

`AllowedModules` 为空表示允许安装的模块，`DeniedModules` 可按部署需要增加拒绝
规则；填写 `AllowedModules` 后则切换为 allowlist。`AllowAnyModule` 用于明确表达
不应用任何模块规则的专用 Worker。这样库的扩展速度和权限边界可以分别治理。

通用 dotted call 解决的是“调用函数并取回 JSON 结果”。不可直接 JSON 化的对象会
留在 Worker 的 session 中并返回不透明句柄（含 Worker 身份，重启后失效）；可以用 `python.call(handle, method, args)`
继续调用，用 `python.to_json(value)` 显式序列化，最后用 `python.release(handle)`
释放。大 Tensor、权重和图像仍需要独立数据面；句柄的池化、空闲回收和跨 Worker
重建属于 P2/P3，不会把每个 Python 库逐个写进 LIP 核心。

生成图的[值生命周期优化](VALUE-LIFETIMES.md)只解除 Go 执行值表中的引用。
它不自动释放 Worker 中的句柄、blob 文件或 mmap；`python.release`、
`python.release_blob` 和 Worker.Close 继续遵循各自的资源契约。

常见库的导入根和第一阶段适配边界如下：

| 能力 | Python 导入根 | 直接可取回的结果 | 后续需要句柄/数据面的对象 |
| --- | --- | --- | --- |
| 数值与随机 | `math`、`random`、`statistics` | 标量、列表、统计量 | 随机生成器状态 |
| 符号与量化 | `sympy`、`statsmodels`、`QuantLib`、`zipline` | 表达式序列化结果、统计表 | 定价器、回测器、长生命周期模型 |
| 图与数据 | `pandas`、`matplotlib` | `python.to_json` 后的 records、描述统计、图表数据 | DataFrame、Figure、Axes |
| 传统机器学习 | `sklearn`、`xgboost` | 预测数组、指标、特征表 | estimator、Booster、训练缓存 |
| 深度学习 | `torch`、`jax`、`equinox`、`keras`、`lightning` | `python.to_json` 后的标量、数组、预测结果 | Tensor、Module、Optimizer、Checkpoint |
| 视觉与语言 | `cv2`、`transformers`、`pytorch_lightning` | 关键点、标签、token、分类结果 | 图像 buffer、Tokenizer、模型权重 |

表中的“后续”描述的是值传输方式，不是库白名单。库本身已经可以通过 dotted
operation 导入；不可 JSON 化的返回值会变成显式句柄，而不是失效的 Python 指针。

Go 侧还可以调用 `CallSession` 或使用 `RegisterPythonSession` 显式传递 session
标签。session 是 Adapter 资源元数据，不会自动变成 LIP State。

例如，Tensor 计算可以保持在 Python Worker 内，只在最后一步取回 JSON：

```lip
import python "torch"

flow Relu(values: list) -> list {
    tensor = torch.tensor(values)
    relu = torch.nn.functional.relu(tensor)
    return python.to_json(relu)
}
```

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

对象句柄是普通 JSON 对象，不能脱离对应 session 使用：

```json
{"id":"43","op":"torch.tensor","args":[[[-1,2]]],"session":"default"}
{"id":"44","op":"python.call","args":[{"$python_handle":"worker-id:0"},"tolist",[]],"session":"default"}
{"id":"45","op":"python.release","args":[{"$python_handle":"worker-id:0"}],"session":"default"}
```

协议必须满足：

- `id` 在一个 Worker 生命周期内唯一，响应必须回到同一个请求；
- stdout 只发送协议消息，日志写 stderr；
- deadline 到期或 Go 取消时发送 cancel frame；不能及时合作取消时回收该 Worker，
  避免一个失控任务污染后续请求；
- 请求队列、单 Worker 并发和 Worker 池大小都有上限，超过上限返回背压错误；
- 启动、退出、协议损坏、JSON 解码和 Python 异常都要带 operation/request ID；
- `session` 标签只能由 Go Adapter 传递，不能让 Python 任意改变 LIP State；完整的
  session 创建、回收和对象句柄生命周期属于 P2/P3；
- 协议版本、Python 包版本和能力列表可在启动握手中报告。

第一版控制面传 JSON 标量、列表、对象和 session 句柄。大数组不应长期复制成 JSON，
而应通过独立的数据面携带 `dtype`、`shape`、字节数、校验和和释放责任。优先顺序是：

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

- **P0/P1（已完成控制面）：** 内置标准库 Worker 提供 echo、失败、延迟和数值
  基线；测试覆盖 request ID、超时、取消、重启、有限队列、协议握手错误和异常
  返回。`PythonWorker` 通过 `os/exec` 长期运行 stdin/stdout JSONL Worker，Host
  注册时显式声明 Pure/ReadOnly/ExternalWrite。`runtime/python_benchmark_test.go`
  提供 cold-start 与 warm-call 基准；发布前需要用目标机器运行它并记录 p50/p95，
  不用未经测量的数字作承诺。
- **P2（进行中）：数据面和句柄治理。** 文件支持的只读 raw blob、typed NumPy view、
  `.npy` 映射和大小/数量配额已经有可靠性基线；下一步用真实矩阵、DataFrame 和模型
  输入比较 JSON、文件映射与 Arrow/共享内存的复制次数、p95 延迟和峰值内存，同时补齐
  空闲回收、泄漏诊断和跨 Worker 重建。
- **P3：多 Worker、session 与传输升级。** 先测量单 Worker 排队，再决定 Worker
  pool；为 session 增加版本和故障重建语义。只有多路复用、跨机部署或 schema 演进
  成为真实需求时，才引入 Unix Domain Socket/gRPC 和流式进度；Gob 不作为跨语言默认
  协议。
- **P4：嵌入评估。** 以可重复基准证明 IPC、数据复制或 Worker 数量是瓶颈，且
  能接受 GIL、ABI、崩溃和取消限制后，才做 CPython/cgo 原型。原型必须是可选
  Adapter，不能改变 LIP 编译器的安装和构建要求。

每个阶段都应保留一个可删除的 Adapter 接口；如果 Python 方案被替换，LIP 图、
Effect、Tick 和取消语义不应改变。

## 暂不做的事情

- 不把 Python/Go `import` 执行语义和包管理塞进 LIP 核心；`import` 只提供可审计
  的依赖元数据，库调用通过 Python Host 的通用 dotted operation 完成；
- 不让 Python Worker 反向驱动隐式 Tick、绕过 State 或持有无界后台任务；
- 不把 JSON 数组协议宣传成高性能张量通道；
- 不以“嵌入更快”代替真实基准、故障测试和部署评估；
- 不把特定框架（Ray、FastAPI、PyTorch Server）写进 LIP 核心。

这条路线先获得 Python 生态的实际价值，再根据可测量的瓶颈选择数据面或通信
升级，同时保留编译、部署、权限、取消和错误传播的清晰边界。
