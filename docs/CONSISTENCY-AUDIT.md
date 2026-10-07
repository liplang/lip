# Alpha 0.5 实现一致性审计

审计对象：当前 `compiler/`、`runtime/`、`cmd/lipc/`、生成示例、规范文档，
以及 `../playaround/01-TestingExamples.md`、`02-AlphaDesign.md`、
`03-AlphaRoadmap.md` 的设计主线。

## 已对齐

| 设计主张 | 实现证据 | 结论 |
| --- | --- | --- |
| AST 与 Graph 分离 | `compiler/ast` → `compiler/graph` → `NodeSpec` | 一致 |
| 数据依赖表达等待 | `refsOf`、`dependencies`、下游阻塞 | 一致 |
| `if` 与 `when` 不同 | `IfExpr` 值计算、`Gates` 执行资格 | 一致 |
| Static Structure + Dynamic Expansion | `MapSpec`、运行时 slice/array 展开 | 一致 |
| State/Tick/增量重算 | `runtime.Instance`、依赖快照、`SetState` | 一致 |
| Retry/Feedback 有界 | 正整数 attempts 校验、Runtime 上限 | 一致 |
| Cancellation 属于 Runtime | Context、Await、Map、Retry、Feedback | 一致 |
| Effect/Ordering 是轻量 metadata | `Effect`、Host 注册、`NodeSpec.After` | 一致 |
| 并发是机会而非保证 | bounded scheduler、效果屏障、顺序 API | 一致 |
| Go 是 Alpha 后端 | 生成 Go、Host Adapter、标准 Go 工具链 | 一致 |

## Alpha 0.5 契约修正

- 多个条件 return 的“最后一个输出获胜”规则改为单一输出点；条件值用 `if`，门控
  无值完成显式写 `Type?`。这使输出契约与图执行模型一致。
- 边界参数不再省略类型；Flow 必须声明返回类型，动态结果在完成时校验，包括 Await。
  一次性库输入拒绝额外字段，Tick 拒绝未知或错误类型的更新，State 保持基础类型。
- 局部 fn 纯计算且允许组合，外部调用必须可见于图。局部 Host 注册使用副本，避免
  并发运行或多个生成包覆盖调用方操作。
- CLI 的三条路径共用输入解析与 JSON 输出，迁移命令输出通过正常编译检查的程序。
- 删除 `pandas.describe` 替身，使用 Pandas Series 的真实方法；Worker 身份进入句柄，
  重启后旧句柄失败，已打开 blob 的复用也重新校验文件内容。

## 已修复的矛盾或易误读点

- README 过去只列功能，没有说明 LIP 的根本模型；现在明确依赖、Runtime、
  State/Tick、增量和 Host 边界。
- CLI 过去只有 `version/check/build`，且 `build` 只写源文件；现在提供
  `help/run`，`build` 默认产出可执行文件，`-emit-go` 保留源码生成路径。
- 文档过去只把 `RegisterPure` 描述为可并行；当前 `RegisterReadOnly` 也可并行，
  但每个 Instance Tick 仍会重新执行。
- playaround 同时使用“Ready 状态”和 `Result.Ready`；当前实现把 Ready 保留为
  结果构造器，Node `Status` 只记录生命周期状态，避免把调度状态暴露成 LIP 值。
- State 的初值、SetState 的消费时机、Retry/Feedback 的次数含义、ExternalWrite
  的顺序屏障已经分别写入 0.3/0.4 规范。
- `Graph` 和 `Instance` 的公共执行/观察方法已串行化，避免复用对象时的竞态；
  Host 仍须在执行前完成注册，Host operation 自己负责保护内部共享状态。
- 生成入口只接受声明的参数；所有 Flow 显式声明输入/输出类型，恰好一个 `return`，外部示例用 `require
  host`/`require python` 写出 adapter 或 Python 环境边界，未声明的外部调用在
  `check` 失败，缺失时不再由编译器补值。

## 有意保留的边界

- 普通 `for`/`while`、事件/流、detach/background、完整 Effect 类型系统，以及
  直接执行 Python/Go `import` 尚未进入 Alpha 0.5；文件头的 `require` 依赖元数据
  已进入，用于环境声明而不触发安装或导入；通用 dotted Host call
  已经通过 Python Worker 提供。
- Unknown Host effect 按 ExternalWrite 处理，牺牲部分并行换取安全顺序。
- Map 的结果保持输入顺序；Map 元素可以并行，但 effectful Map 按顺序执行。
- 当前一次 `Run`/`Tick` 是 fail-fast 的：节点错误会停止新的独立工作，未开始
  的节点记为 `Skipped`。这比“只传播到数据下游”更保守，是 Alpha 的明确执行策略。
- Instance 的并发调用会串行化；Host callback 不应重入同一个 Instance 的锁定方法。
- Python 集成先走独立进程协议，不把解释器、GIL 或 Python 包管理引入 LIP 编译核心；
  `require python "..."` 只记录包/版本要求，实际模块仍由 Worker 环境解析。
- Python 文档把调用通道（JSONL、Unix socket/gRPC、HTTP）和数据通道（映射文件、
  Arrow、共享内存）分开；当前 `ProcessHost` 已实现 P0/P1 的 JSONL 控制面，P2
  只读 blob/mmap 数据面已落地，P3 传输升级仍保持为后续阶段。
- Python 路线的默认形态已经明确为 `os/exec` 启动一次、Worker 常驻、stdin/stdout
  JSONL；Unix Domain Socket 是后续可替换传输，Gob 不作为跨语言默认协议，大数组
  走独立数据面。
- 中文 README 和 `README.en.md` 共享同一 Alpha 0.5 能力边界；Python Worker 的
  进程 Adapter 和通用 dotted call 已加入实现能力，直接 LIP `import`、事件/流和
  完整 Effect 类型系统仍标为后续候选；依赖元数据通过 `require` 声明。

## 验证

以下检查在本次审计中通过：

```bash
go test ./...
go test -race ./...
go vet ./...
go build -buildvcs=false ./...
for f in examples/*.lip tests/conformance/*.lip; do lipc check "$f"; done
lipc build -o /tmp/lip-example examples/hello.lip
lipc run examples/hello.lip -- Alice
```
