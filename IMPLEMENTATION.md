# LIP 实现与验证

[English](IMPLEMENTATION.en.md) | [简体中文](IMPLEMENTATION.md)

语言设计与实现以独立完整的 [LIP 1.2 规范](spec/LIP-1.2-SPEC.md)为准；[1.0](spec/LIP-1.0-SPEC.md) 与 [1.1](spec/LIP-1.1-SPEC.md) 保留为历史版本。本页记录 1.2 的实现范围和可重复验收证据：有限容器/参数描述传播、递归请求摘要、统一调用边界、非有限数值、解释器、生成 Go 与 session 已贯通。

本仓库已实现入口、值与纯函数、控制结构、结构化 Flow、静态模块、Claim、图调度和统一 Host 边界，并提供解释执行与真正生成 Go 两条执行路径。以下按 1.2 规范章节列出可重复的回归证据。HTTP 是可选接入；可选 durable profile 未实现。

1.1 的纯性承诺与 IEEE-754 NaN/无穷扩展复用解释器、Go 生成和 REPL 路径。`Spec11PurityScopesAndCallIDs` 检查嵌套标记、回调、递归、独立调用身份和重放；`Spec11NonConcurrentAdmission` 检查跨 run 串行许可及等待让出工作额度。`Spec11NonFiniteSemanticsInterpreterAndGo`、`TestSpec11NonFiniteArithmeticInterpreterAndGo`、`Spec11CanonicalAndTransport`、`Spec11NativeAdaptersAndGeneratedEntry`、`Spec11BoundariesReplayAndErrors` 覆盖数值、规范身份、严格 c-hex、Python/Go 通道、入口协议、HTTP 拒绝和保存的闭包。默认 JSON 的有限 Data 字节不变，CLI/生成程序以 `-data-encoding c-hex` 显式选择无损通道；不声明原生纯性证明或持久恢复。

## v1.2 推进安排

本轮完成规范边界审查、组合闭包向量（§16.7–16.9）、IEEE-754 非有限数值向量、有限描述传播及独立数值模型检查。语言规则统一以 1.2 正文为准，不另设 direction；每一阶段都以正反用例、调用轨迹和解释器/生成 Go/session 对照验收。

| 阶段 | 交付范围与现有落点 | 进入下一阶段的条件 |
|---|---|---|
| 1：描述与合约基础 | 在 `compiler.go`、`checker.go`、`host.go` 的现有模型中明确 Info、NoResult、Union、Callable/captures、registry 快照和原因结构；复用现有 schema/参数绑定，不新建接口文件或执行机 | 联合结合/交换/幂等、不同 captures 不误合并、快照隔离、静态零调用和预算明确失败有针对性回归；尚不据不完整模型增强准入 |
| 2：容器与循环传递 | 在 checker 中完成固定/动态投影、逐候选负下标、gather/slice、put/concat、普通 for、推导式与 map 容器外形；回调参数到返回增强留到阶段 3，原生来源继续独立 | §16.7–16.9 中适用本阶段的空输入、开放 rest、覆盖、异长列表、读写混合及字面量边界有正反用例；解释器与真实生成程序在 1/2/7 额度下保持调用数及 barrier 次序 |
| 3：调用、递归与保存值 | 完成实参到返回位置、候选签名/类型、闭包、Flow 返回、BaseOps/Active 和 `session.go` 的描述保存；`backend.go` 携带检查结果，不在运行时反推 | 多层/互递归/高阶/跨模块/跨 cell 结果一致；Active/缓存访问顺序不改接受条件；实参动作不被 Pure fn 或返回摘要隐藏；Flow 执行 Summary 不按实参专门化 |
| 4：契约、诊断与适配 | 从同一源码/registry 向嵌入 API、CLI/REPL、帮助与补全提供只读视图；在 `values.go`、`host.go`、`adapters.go` 贯通结构化 reason、部分候选、边界 path 与完成事实 | 未知与禁止明确区分；签名/可空/可缺省一致；多错顺序稳定；静态查看零新增执行；权限撤销、过期引用、known_fault 重放零重复写入；展示开关不改预算/身份/主错误 |
| 5：组合验收与发布准备 | 将全部规范向量和既有应用回归贯通解释器、实际 Go 程序与适用 REPL；同步示例、帮助、课程、README 和本页证据 | 相关回归及 `make test`、`make race`、`make vet` 通过，明确环境跳过项；清理生成物并完成 core 声明审查后再决定发布版本号 |

已将原有“容器投影/参数转发必为 Unknown”的回归迁移为 1.2 正向用例；真正未知、写入、不可并发读取和一般 carry 结果继续作为拒绝用例，容器 slot、空 concat、Prefix slice、fold accumulator identity、推导式 identity 和 carry state identity 作为正向用例。每个正向用例都同时验证静态零调用、实际 provider 行为和生成程序结果。

每阶段的验收记录须注明规范条款、执行路径、具体输出/错误归属、provider 调用次数及必需先后关系。并行重叠用会合/事件证明；声明置换与动作源码重排分别检查。独立模型只验证半格、投影、候选笛卡尔积、错误阶段、Claim 冲突和递归固定点的规范代数，不能替代解释器/生成程序验收。已有 1.1 回归和文档/数值附录通过只说明基线仍有效，不能代替新能力验收。可选 durable 的实现及并行写、共享可变状态不纳入这条主线。

## 1.2 实现覆盖

测试集中在现有 `tests/` 文件；表中省略共同的 `Test` 前缀。

| 规范 | 实现与检查重点 | 主要证据 |
|---|---|---|
| §2、2.3 文件、声明、入口 | 文件模式唯一选择；默认根 Main、显式覆盖与唯一根 Flow 回退；末行无换行；未选声明检查；EntryInput 先于 run_id/journal；CLI 与生成程序的两种 JSON 输入统一报 E-SCHEMA；EntryOutput 区分 null/void，等待全图成功 | `MainDefaultEntrySelection`、`MainDefaultEntryInterpreterAndGo`、`DeclarationFilesAndEntrySelection`、`EntryInputBoundary`、`EntryOutputCompletion`、`GoBackendDeclarationsAndEntryProtocol`、`InteractiveCLI/entry-selection-and-input` |
| §2.1、6.3 子 Flow/管道 | 静态目标、Flow 非值、调用环、普通值参数与返回值、实参按源码顺序求值；初始值只求值一次；局部游标等待子图，保留原始错误；父子共用预算和 journal | `StructuredFlowAndPipeline`、`FlowBoundaries`、`SubFlowErrorPathAndEmptyBudget`、`GoBackendStructuredFlows` |
| §2.2 静态模块 | 冻结完整依赖、版本锁、私有/导出、模块环；模块只使用自身名字与 import，不捕获入口模块环境；路径精确匹配、链接及重复物理文件拒绝 | `StaticModuleSnapshot`、`ModuleLexicalIsolation`、`LocalSourceSnapshot`、`GoBackendStaticModules` |
| §3–5 值、函数、纯性 | 不可变捕获；动态调用顺序；参数/返回注解；十万次尾调用；保守 Ops；严格 JSON、Unicode、数值与 Data 边界；有限描述的投影、切片、concat、put、map 与可空调用组合 | `PureOperations`、`FunctionCapturesSnapshotNotMutableInput`、`DynamicArityPrecedesEffectfulArguments`、`DirectAndIndirectTailCalls`、`NullableAnyAndCallbackChecks`、`StaticDescriptionCompositionBoundaries`、`GoBackendNumericBoundariesAndBudget` |
| §4 递归与资源失败 | 默认 10,000 个非尾帧、正值可调整、负配置拒绝；尾调用复用帧；E-BUDGET 不伪装为空值成功；REPL/HTTP 恢复 | `NonTailFrameBudget`、`GoBackendNonTailFrameBudget`、`SessionRecoversFromFrameBudget`、`HTTPRecoversFromFrameBudget`、`InteractiveCLI/recursion-budget` |
| §6、16.1 身份与局部迭代 | Position/EvalPath/CallID/JoinID；循环 source 子 Flow 与第 0 轮 body 不碰撞；CRLF 归一；多源根优先 sites；Claim 实例隔离 | `ModuleStaticSiteVector`、`LoopSourceFlowAndBodyPositions`、`CarryPerformFeedsNextProposal`、`ClaimParallelAndReplay`、native Claim fixture |
| §7 调度、Earlier、After、F/K | 等待子图/资源时释放工作额度；祖先豁免不吞掉外部前置；动态目标、空 region 完成集合、错误截断前置闭包；合法调度扰动 | `SubFlowBarrierCompletion`、`PermitWaitYieldsWorker`、`AfterWireAndChildCompletion`、`AfterEmptyFlowCompletion`、`AfterRejectsImpossibleLoopInstances`、`FutureLoopPrerequisiteBeyondErrorPrefix` |
| §8–9 Claim | 追加事实、同身份重投/冲突、完整读取等待 producer、live snapshot 是单次观察；未选 gate 零调用/零错误 | `ClaimParallelAndReplay`、`NativeClaimConflictFixture`、`SkippedGateProducesNeitherCallNorError`、snapshot 示例 |
| §10、11 的 core 调用记录规则 | proposal 零调用；参数、授权和资源准入；Go/Python 共同 Data/schema 边界；terminal 重放零新增调用；已完成 schema fault 与 Uncertain 区分 | `AuthorizationBeforeProvider`、`GoAndPythonUseSameDataBoundary`、`KnownSchemaFaultNeverRetries`、`WriteTimeoutIsUncertainAndNeverRetried`、`JournalDiagnosticsAreImmutableObservations` |
| §10.1–10.2 原生资源与生成程序（边界见下文） | run 清理 hook、原生 owner/generation 检查、Go 静态链接；原生辅助转换尊重词法名称与管道静态目标；真实 Go 求值；启动核对合约；编译后元数据并发只读 | `NativeHostAuthorizationAndRegisteredPrecedence`、`NativeModuleHelpers`、`NativeHelperLexicalScope`、`NativePipelineStagesRemainStatic`、`NativeLibrariesInterpreterAndGeneratedProgram`、`GoHostContractDriftRejectedBeforeCalls`、带 race 的 native fixture |
| §12.1 可选 HTTP | 显式方法、HEAD 不回退、405/Allow；编码路径与 params；请求/响应预算、隔离、取消和不重试 | `HTTPExplicitMethodsAndEscapedPath`、`HTTPConcurrentRunsKeepIdentityAndClaimsSeparate`、`HTTPAdmissionCancellationAndUncertainNeverRetry`、`WebExamplesInterpreterAndGo` |
| §14、16.3–16.5 组合与固定向量 | 同一冻结依赖经两条路径复用；闭包、carry、图循环、管道、void、未选分支及动态 Data 故障；固定 binary64 参考算法 | `CoreCompositionConformance` 在并发额度 1/2/4/7/1238/73295 下对照实际生成程序；`SpecNumericReference`、`GoBackendPowerLogarithm` |
| §6.3、8–10、12.3 证据与决策组合 | 跨模块子 Flow、纯闭包、父子同名 Claim、完整汇聚与条件顺序写；业务拒绝、空输入、类型与 Host schema 故障；记录重放 | `EvidenceDecisionComposition` 对照解释器、实际生成程序与 REPL |

§3.2 的单向诊断由 `script/trace.go` 的有界缓冲实现，事件脱离业务值与 AST，不执行用户 sink 回调。`DiagnosticComposition` 对照解释器、生成程序和 session，覆盖闭包、map/fold、guard、carry、管道、尾递归与未选分支；禁用、满队列和关闭时结果、Steps 与 journal 相同。`DiagnosticArgumentsAndEffects` 检查实参错误、签名顺序和 Pure/parallel 边界；`DiagnosticRequestSummariesAndReplay` 用会合证明带诊断的读取实际重叠，检查重放零新增 provider 调用。`DiagnosticModulesAndNativeValues` 检查模块返回函数/容器与原生引用释放后的不透明显示；`DiagnosticReceiverIsolation`、`DiagnosticBlockedReceiverAndBudgets`、`DiagnosticSessionFailureAndCaptures` 和 `DiagnosticCancellation` 检查接收端故障/阻塞、共享关闭、预算/取消、失败 cell 与旧捕获。`HTTPDiagnosticsStayOutsideResponse` 用内存 HTTP 并发请求核对独立 run、无日志混入响应及关闭接收端不影响状态；`InteractiveCLI/diagnostics` 核对 stdout/stderr、开关、`:run` 继承和新课程。终端诊断排空最多等待 100ms；嵌入者的阻塞 Writer 须自行解除阻塞以回收接收协程，求值本身不等待它。

模块/调用实现复用 compiler、checker、evaluator、scheduler 和 backend。源码加载与链接集中在 `script/modules.go`，没有第二套执行机。函数参数和全局数据按词法作用域解析；Claim 读取使用当前环境，不按执行节点的 store 名劫持模块函数里的同名值。`native_data` / `native_release` 仅转换未被声明、参数、binding、Claim 或 import alias 占用的辅助调用；依赖模块的辅助调用在链接时固定其来源。管道阶段保留静态目标供普通 checker 验证，原生调用不会把它转换成另一种 AST 后绕过检查。

`EvidenceDecisionComposition` 使用显式 Host 合约验证“并行检查 → 子 Flow 内归类与 Claim → 父 Claim 完整读取 → 纯函数判断 → 条件写入”。检查结果的完成时间被扰动；并发额度 1、2、7 下，重复值仍按不同贡献保留，写入保持输入顺序，Host 在每次写入时核对全部检查已经完成。七种输入覆盖通过、分数不足、业务失败、只检查不发布、空集合、Host 返回不符 schema 和子 Flow 参数类型错误。解释器每次用同一 journal 重放，核对零新增读取/写入及稳定记录数；生成程序检查真实输出和写入轨迹；持久 REPL 加载同一模块并核对结果和调用数。该测试的 Host 是本地受控检查器，不依赖真实模型或网络服务。

同一测试通过纯请求构造函数执行跨模块子 Flow 的并行读取，并检查 Write 和容器投影产生的 Unknown 请求在编译阶段拒绝并行，Host 零调用。Flow 不作为普通值传给 map/fold，活动 Claim 不跨子 Flow 传递，完整快照通过 Data 组合。静态已知函数返回请求的 Ops 按规范传播；通用原生操作的合约仍为 Unknown。业务 Result 由显式控制门解释；多步 Flow 不自动获得事务或崩溃恢复。

纯函数返回请求的分析使用独立的有限 operation 联合依赖图，具名函数预先分配摘要节点，函数尾表达式连接返回来源；可达性计算得到递归联合方程的最小固定点。摘要不保留源码树、环境或业务值，不运行函数，也不按实参专门化。参数来源以有限 `Param/ParamOps` 节点保留，调用点再替换为实际请求摘要；尚未完成的递归/前向边若需要未建模的参数位置置换则保守回退 Unknown，避免把错误位置当成已知请求。描述合并和摘要缓存 key 必须区分 exact/unknown list、open/closed object、参数 operation 和映射实参，不能因结构碰撞改变结果。`FunctionProposalSummaries` 覆盖具名/匿名函数、简单别名、不可变捕获、管道、分支、多层前向调用、互递归、条件/返回函数和闭合容器取出的函数；`FunctionProposalUnknownAndUnsafe` 保留开放容器、一般 carry、动态函数和未知形参的保守边界，并检查写入、不可并发读取及实参效果不会被返回摘要掩盖。`FunctionProposalClosedValuesAndRecursiveParameters` 额外验证 `[request][0]`、条件/返回函数和递归 effect 参数在 parallel 中保留已知只读请求。`FunctionProposalSchedulingAndGo` 用 provider 会合验证普通图及显式 parallel 的真实读取重叠和前后写入 barrier，对照解释器与实际生成的可执行文件。Go 后端携带检查后的效果/并发元数据，运行时不重新分析函数。

`FunctionProposalRuntimeBoundaries` 检查构造函数封装后的授权拒绝仍为零 provider 调用，已完成 Write 的 schema fault 重放不重试，以及跨 cell 捕获的旧 snapshot 请求仍受原 run 的资源生命周期限制。生成 Go 的调度回归同时覆盖容器取函数、条件函数、返回函数和递归参数摘要，确保解释器与生成程序使用同一准入结论。

`NativeRequestBuilderComposition` 交叉检查多层请求构造、模块、别名、闭包、管道、互递归、容器投影及普通参数转发。函数声明反序和内部重命名均得到相同结果；构造与编译零 provider 调用，执行后可继续调用原生对象方法。原生来源先按函数依赖收集，再用现有静态返回摘要补齐递归请求，并刷新外围闭包与容器；不重复转换 proposal，不用原生来源增强 Ops。测试对照并发额度 1/2/7 的解释执行、实际生成程序和跨 cell 保存的请求构造函数，同时确认普通 Host object 字段仍可并行纯读取。

### 1.1 普通值与调用组合

同一 run 的 Flow 参数与返回值允许 function、effect、可空形式和嵌套容器，Flow 定义本身仍不是值。入口 JSON、Host 和 Claim 保留递归 Data 边界。显式实参、管道额外参数和 perform 的请求表达式在调用方游标中按顺序完整求值，其效果与目标 Flow/实际操作分别合并。Flow 的 RetOps 只描述最后返回请求，执行 Summary 独立；动态 effect 参数不按调用者实参专门化。

`FlowOrdinaryValues`、`FlowEffectValuesAndNestedArguments` 对照解释器、实际生成程序及跨 cell 会话，覆盖闭包清理后的捕获、请求构造零调用、传递、执行和并发额度 1/2/7。`GoBackendStaticModules` 与 `CoreCompositionConformance` 检查模块内返回函数和携带函数的容器、动态参数注解及根输出 Data 故障。`FlowArgumentOrderAndBoundaries` 检查源码次序、失败后不执行后续实参、写入效果不被纯目标掩盖及 Flow 非值；`FlowValueDataAndSnapshotBoundaries` 检查 Host/Claim/入口注解和 snapshot 实例归属。

`NativeReturnProvenance` 检查未使用的原生实参不污染纯结果，同时保留真正返回的参数、对象位置、map/concat/put 结果、闭包、高阶调用、子 Flow 和请求结果。用符号参数身份及投影表达返回来源，不把所有输入混入返回值，不保存 AST 或业务值；跨 cell 的返回闭包按其冻结捕获补齐原生来源，Ops 仍由 checker 保存。Flow 依赖次序与已知返回注解防止普通列表/标量被误当成原生接收者。互递归参数交换使用递归分量内同时求解的有限结构固定点，未使用的资源参数不污染递归纯结果；递归对象中的纯字段保持纯。put 的固定键更新精确替换对应字段来源；动态键先合并可能来源，后续固定键覆盖仍恢复该字段的纯结果。无法有限记录的层层包装/投影仍支持显式 receiver 请求，不无界展开来源或反复增长高阶调用项。测试同时交换函数名称与声明顺序，并对照解释器、生成程序和 REPL。


## 并发与值保留

叶子额度接受 Go `int` 范围内任意正整数；普通 Run 的零值表示一个额度，负数返回 E-VALUE。固定事件存储、有界完成批次、仅排序活跃候选节点、按 effect/barrier 建索引，避免按所配置的额度预分配内存，也避免反复排序已完成历史。worker 只随实际工作创建。节点预算耗尽立即停止建图。父子调用共享额度，等待子图或资源时让出额度；ResourcePool 限制独立于调度并发额度。

证据包括 `ArbitraryParallelismOverlap`、`ParallelismConfiguration`、`GoBackendArbitraryParallelism`、`LargeParallelNestedFlowsAndPermits`、`LargeParallelCancellationReleasesPermits` 和 `GraphConstructionStopsAtNodeBudget`。重叠测试检查 provider 实际同时进入并最终退出，而不只是检查数字参数能否通过；常规矩阵覆盖非二次幂额度和不足一整批的尾部工作。下面的可选模式让指定数量的调用全部重叠。2026-10-15，注册为 ReadOnly 的阻塞 provider 达到 73,295 个同时活跃调用；这证明调度重叠能力，不表示相同 CPU 吞吐量或任意外部服务容量。

```sh
make prepare
env GOCACHE="$PWD/.build/cache" GOTMPDIR="$PWD/.build/tmp" \
    TMPDIR="$PWD/.build/tmp" GOPATH="$PWD/.build/gopath" \
    LIP_STRESS_PARALLELISM=73295 \
    go test ./tests -run '^TestArbitraryParallelismOverlap/73295$' -count=1 -v
```

完成的词法 region 释放节点值、环境和循环 source，同时保留 After 所需状态、错误、身份及 source 长度。子 Flow 完成后释放其 store，不可变普通值输出保留到交给等待中的调用方。冻结闭包和共享 Data 各自保留引用；顶层 session 结果保留到事务提交。`CompletedScopesReleaseLocalValues` 在 run 尚未结束时，测量子 Flow、分支和循环中大块纯临时数据的活跃堆；`CoreCompositionConformance` 在解释器与生成程序中检查共享输出、闭包和 Claim。尚未实现活跃作用域内每个绑定的精确最后使用清理；图记录、活跃 Claim、输出、原生引用及内存 journal 仍保留必要数据，不保证常量内存，也不保证从致命 OOM 恢复。

具名函数与匿名函数均只冻结自身使用的外部名字，具名函数互递归仍保留原函数身份；不会仅因声明了无关函数，就保留已经解绑的大块 session 数据。管道临时绑定属于当前管道的局部作用域，不会被闭包误当成外部名字。`SessionDeleteReleasesUncapturedValues` 测量解绑后的堆保留；`SessionPipelineClosures` 与 `CoreCompositionConformance/closure_pipeline` 检查 REPL、解释器、实际生成程序中的嵌套管道闭包及名字复用。`FuzzSessionTransactions` 进一步检查任意 cell 的失败事务、错误后恢复与解绑重用。

## 工具、教学与范围

CLI `run/emit-go/build/serve` 都使用同一冻结 SourceSet；构建预算为 4096 来源、64 MiB。`BuildInfo` 公开 SourceTable、DependencyLock 和 revision。SourceTable 按 SourceID 排序，StaticSite 使用根来源优先的独立顺序。生成程序固定入口和源码快照，保留构建元数据但不重新解析/解释 LIP 文本。

`DefaultMaxFrames` 为每个叶子求值器 10,000 个同时活跃的非尾函数帧。`Options.MaxFrames` 以及 run/repl/serve/生成程序的 `-frames` 接受正值覆盖；零使用默认值，负数报配置错误，不能绕过保护。直接、间接、互递归及 map/fold 回调共用调用检查；proper tail calls 复用帧。耗尽时报告带位置的 E-BUDGET，Report 没有 EntryOutput，CLI 不输出成功值，失败 cell 不提交绑定。HTTP 使用现有固定 500 错误响应。REPL 与 handler 可继续处理后续工作。这是提前生效的帧预算，不是按 run 统计内存或恢复原生运行时/操作系统致命资源耗尽；Host 仍应保留合适的指令、分配、并发与时间限制。

REPL cell 保存成功值、fn/Flow 声明与纯闭包，不重跑历史动作；复用声明解析器、checker、linker、原生调用 lowering 和图执行器，支持静态 Data、export、LIP 导入、Claim，以及控制结构的语句和值形式。Flow 调用和管道每次创建独立子图与 store；顶层 Claim 在单个 cell 中完成，仅保留完成后的 Data 快照。`:begin/:end` 将完整脚本或前向声明作为一个 cell 提交；`-project` 指定导入根目录，来源闭包冻结至 reset。`:run` 可带 `-entry/-project/-input/-entry-input/-entry-output`，文件运行独立于会话绑定。`learn` 的 38 课与 76 个小例子使用同一 CompileSourceSet/Run 路径；入口课检查两种参数绑定，模块课使用内置冻结来源。README 中英文、示例教程、CLI 帮助与课程保持相同的入口和边界描述。

课程与学习交互集中在 [learn.go](cmd/lipc/learn.go)，按单个操作、组合、额外练习逐步展开。每课显示输入与预期结果，提供三级提示、可执行小例子与完整演示；演示不标记完成。答案按课程暂存，`:retry` 通过共享编辑器载入原答案，完整程序失败后同样可修改；进度文件仍按原有版本和课程 ID 保存完成标记，不保存草稿。`InteractiveCLI/all-lessons` 执行全部 76 个例子及参考答案，其他学习用例覆盖多输入拒绝、草稿恢复、完整程序修改、提示不影响进度、旧进度恢复、完整程序与匿名函数表达式的区分，以及下一行的 else。课程列表不依赖进度文件有效。真实 PTY 验证多行上下移动、Home/End、缩进换行、混合 Tab/空格的列对齐、Unicode、参数/请求构造函数补全及预填答案修改；终端恢复和纯文本管道规则保持一致。

会话命名空间将公开名字映射到各 cell 的不可变符号。`Session.Delete(names...)` 与 CLI 的 `:del name[,name...]` 先验证整批名字，再解除公开绑定，与绑定内容无关。后续 cell 可跨绑定种类复用名字，已有闭包与 Flow 保留原引用、导入含义和源码 token。根据公开绑定及保留 Flow 的依赖可达性，清理不再使用的保存值/定义；导入缓存和原生资源生命周期仍显式管理，解绑不重跑动作也不关闭资源。`SessionDeleteBindings`、`SessionDeleteModuleAlias` 和 `InteractiveCLI/delete-bindings` 覆盖跨种类复用、旧依赖/proposal、缺失或格式错误时整批不变、Claim 快照、别名替换、可见性及 reset。

HTTP 只接受显式 Host 合约，不提供请求级通用 Native bridge。普通文件、REPL 和生成程序可用通用 Go/Python bridge；解释执行的自动 Go 导入仍使用需要 Go 工具链、CGO 和 plugin 的开发 bridge；生成程序的 Go 导出和可推导泛型实例在构建时链接，调用时不需要工具链。构建时仍未知的泛型实参类型需要通过 HostSource 显式注册具体实例。Python 需要运行环境中的相应库。MemoryJournal 仅提供进程内调用记录和重放，不能恢复进程崩溃。durable 的 begin、落盘、crash/reconcile 验收不在当前支持范围。

## 正确性与 Host 集成边界

REPL 保存 proposal 原有的规范 Ops 和静态已知函数的返回摘要。能够证明的固定字段/下标投影、条件函数、返回函数和 state-identity carry 跨 cell 与别名继续保留摘要；无法证明的开放投影、动态调用和一般 carry 结果仍为 Unknown，不能凭保存值的实际函数或 operation 身份补强摘要。成功函数声明、别名和捕获保留摘要，失败 cell 不提交新摘要，删除或复用名字不改变旧闭包。`SessionPreservesProposalOps` 与 `SessionFunctionProposalSummaries` 检查上述边界和 provider 调用次数。

list/string 单值索引支持负安全整数，list 的索引列表保留顺序与重复项。方括号切片与 `slice(v,start,stop[,step])` 共用精确整数归一化、边界裁剪、null 默认值和负步长；这取代此前只接受非负且严格边界的 slice 规则。核心求值与原生 Data 投影共用 helper，Go 资源支持单值/列表索引和切片，Python 资源收到原生 slice。新 list 在分配前检查预算，循环检查取消与指令预算；选出的 function/effect 保持原值，已证明的闭合请求来源保留规范 Ops，开放或未追踪来源仍为 Unknown。

`IndexAndSliceConformance` 对照解释器和实际生成程序的成功结果、错误诊断及分配预算；`IndexAndSliceSyntaxAndComposition` 检查语法、Unicode 标量、闭包、carry、操作数顺序、未选操作数及保守 Ops；`NativeIndexAndSliceComposition` 覆盖 Go 资源、Python 数组、原生 Data 回退、容器纯成员和保存视图；`SessionIndexAndSlice` 复现多 cell 索引、错误恢复、帮助与补全。容器示例、38 节课程中的 `slice`、`reverse` 与 `slicing`、REPL 帮助及中英文规范/README 同步说明这些规则。

`claim.snapshot` proposal 只保存构造时的 run 和 Flow 实例身份，不保留活动 store 或整个图。它可在同一实例内经过闭包、map、容器和 carry 后执行；进入后续 cell 或另一实例时，以运行时 `E-RESOURCE` 拒绝，早于 Host 授权，不会重新解析为新 store 或导致进程崩溃。`SessionSnapshotProposalLifetime` 检查错误后恢复、失败 cell 不提交与删除/复用 store 名；`SnapshotProposalComposition` 对照解释器、实际生成程序及同身份 journal 重放，检查两次子 Flow 调用的隔离。

Go Data 转换与 `native_data` 快照按类型、地址及切片长度区分访问中的对象，允许同一底层数组的有限切片视图；`native_data` 还允许地址重叠的不同类型指针，同时拒绝真实的 slice/map/pointer 混合循环。`NativeSharedDataSnapshots` 在解释器、实际生成程序与持久 session 中检查这些组合及未选分支零调用。`GoDataSharedSlicesAndInvalidFieldNames` 还检查显式 Go adapter：非法 UTF-8 struct 字段映射明确报 `E-VALUE`，不会静默变为 null；已完成调用的转换故障保留 known_fault，重放零新增调用。

原生来源按容器中的具体位置记录，与不可变容器本身分开。`native_data` 返回普通 Data；捕获快照、读取资源引用旁的纯字段、选择纯列表元素不会引入 perform。`NativeSnapshotsAndContainerPurity` 对照解释器、实际生成程序与持久 session。已知 Go 标量结果遵循转换后的 Data 表示；保存的函数和 proposal 单独保留结果来源，不改变 Ops。纯数据集不会再分配逐元素来源数组；`SessionNativeLargeDataAllocation` 在保留 250,000 个数值元素时检查小 cell 的分配开销。构建期 initializer 关联放在运行时表达式树之外，lexer token 独立保存文本，不再通过子串保留整个源码缓冲区。`SessionNativeMetadataDoesNotRetainInitializers` 检查只捕获计算后长度的闭包能释放已删除的 12 MiB 字面量。

`Host.BeginRun` 与 `RunLease.End` 提供应用自定义 run 租约，接收 run 身份、结束错误与 journal。清理在已开始动作结算后执行，覆盖取消与预算失败，不撤销写入。`Report.CleanupErrors` 独立保留清理故障，不篡改图根错误。原生引用携带 scope、owner 与 generation；文件 run 的引用结束即撤销，失败 cell 释放新引用而保留原有资源。成功 cell 保留保存值、闭包、proposal 与结果可达的新引用。资源释放只解除技术引用，业务 close/commit/rollback 仍显式执行。`HostRunLifecycle`、`NativeRunOwnershipAndFailedCellCleanup`、`NativePythonFailedCellCleanup` 与 `DiagnosticPhases` 覆盖这些边界；Python 检查通过 weak reference 验证对象实际回收，并确保保留的闭包继续可用。诊断标明 static、runtime 或 Host 阶段，失效、越域、已释放引用报告 E-RESOURCE。

生成程序静态注册自动 Go 导出，保留命名参数绑定，并根据源码实参类型和 Go 返回签名链接具体泛型实例。`GeneratedGoLibrariesWithoutRuntimeToolchain` 在 PATH 中没有 Go 工具链时运行真实可执行文件；`GoModulePackageDiscoveryWithoutRegistry` 还检查本地第三方模块。构建时仍未知的类型需要通过可信 HostSource 注册具体实例。交互/解释执行的自动 Go 开发 bridge 仍在已授权调用中加载 plugin，尚不满足 §10.2 的静态绑定要求；需要该保证时使用显式静态注册 adapter 或生成程序，没有为此放宽 spec。

一般应用资源的元数据、取消/关闭行为和 worker 租约隔离仍由嵌入 Host 负责。默认 Native Host 用于单个 session 或文件工作流，没有提供共享并发请求的 worker pool 或完整应用资源管理。HTTP 继续要求显式 Host 合约，因此不能将 §10.1–10.2 的测试覆盖视为全面合规。

## 实际应用检查

`NativeHigherOrderAndBranchProvenance` 覆盖返回闭包中的外层捕获、不同分支的参数名/个数、固定列表位置与递归列表的纯元素；按实际签名的符号参数分别保留来源。`FlowValueControlAndFailureComposition` 对照具体输出、动作轨迹和错误归属，覆盖命名实参、动态失败顺序、短路/空循环、carry 传递函数与请求、尾调用、跨子 Flow 闭包及 journal 重放。两者均运行实际生成程序，原生用例另检查跨 cell 捕获。

`AfterNestedCursorCompletion` 将 After 与同一局部游标内的嵌套实参、条件、短路、局部语句/循环相交叉，覆盖静态调用集合与动态实例、额度 1/7、反向约束和批量事件。已关闭路径及时发布完成，不等待仍在执行的 owner；活动循环不会因单轮完成被提前关闭。生成程序通过嵌入 Host 的公开 Run API 接受相同 After 约束。

`OllamaChatRequestValues` 直接复用实际单文件 agent 的 StreamReply/RunAgent，并注入兼容流式接口的另一请求构造函数。额度 1/7 下，解释器与生成程序都检查构造零调用、同一请求两次 perform 产生两次调用、每个流关闭、thinking 保留和 agent 历史。`OllamaChatTools` 检查参数校验、SQL 参数绑定、计算及提醒持久化；`OllamaChatInterpreterAndGo` 用离线 SDK 替身验证模型配置、多轮工具、历史裁剪、重启、空闲提醒和真实 PTY 编辑。编译与各执行模式分别计时，避免竞争检测下前面的编译耗尽后续终端测试的预算。真实 Ollama 模型交互仍依赖本地服务。

`PracticalNativeWorkflows` 检查参数化 SQLite 聚合与注入输入、pandas CSV 清理/分组、scikit-learn 线性回归与标准化/分类 pipeline、NumPy 线性求解、SciPy 积分/优化，以及 Go HTML 模板转义和危险 URL 过滤。每个用例均以并发 1 和 7 解释运行，再运行实际生成的可执行文件和持久 REPL。科学计算用例需要已安装的库，缺少时明确跳过；本地环境提供 NumPy 2.3.2、pandas 2.3.2、SciPy 1.16.2 和 scikit-learn 1.7.2。

`NativeReceiverProposals` 检查 Flow/函数参数、嵌套 Go 资源、保存资源/可调用对象的 Data 包装、已保存的 REPL receiver、删除/复用名字和零调用构造。`NativeModuleReceiverProposals` 在解释器、生成程序和 REPL 中检查导入 LIP helper 的词法隔离与对象方法请求。显式 receiver 请求不依赖静态记录的原生来源；原生 adapter 仅在 perform 时解析嵌套成员。普通纯对象调用保持原有语义。

`HTTPPracticalCRUDApplication` 运行共享存储的网站，包含独立的列表/读取/创建/更新/删除/页面入口、业务验证、400/404/409 结果、Go HTML 模板转义和 24 个并发创建/读取客户端。非法输入零写入；重复请求保留存储而不泄漏请求局部值。HTTP 存储与渲染使用应用显式 Host 合约。纯核心线性模型示例还通过全部示例执行路径检查中心化最小二乘、预测、均方误差和奇异输入拒绝。

合成的模块读取/调用准备操作使用源码保留关键字前缀，与 get.once、prepare.once 等真实 Python 路径分离。adapter 仅移除当前动作对应的一层前缀。`NativePythonStateAttributesAndCallOrder` 覆盖这些名字、嵌套 prepare 字段、补全，以及解释器/生成程序/REPL 执行路径。

显式原生 receiver 请求只固定实际成员路径所需的普通包装字段，避免把含函数/effect 的整个外层对象送过 Data 边界；原生成员解析仍延迟到 perform，实参不获 Data 豁免。`NativeExplicitReceiverMixedContainers` 覆盖请求构造函数、跨子 Flow 的混合容器、动态别名、未选缺失路径、原对象不变及非法实参，对照解释器、生成程序和 session；聊天 Agent 的 OpenModel 返回对象是实际应用回归。

## 验证方法与环境边界

```sh
make test       # Public API, lessons/examples, actual PTY, actual generated programs
make race       # Interpreter and generated programs, including shared static metadata
make vet
make clean
```

数值逐位表由 `go run ./tests/reference_numeric -check` 从规范参考算法独立核对，正常包含在 `make test`。`CoreCompositionConformance` 除成功输出还比较原始模块诊断；错误不会改写为调用方包装错误。After、barrier 和资源等待的专项测试继续检查完成集合及前置依赖，而非仅比较最终值。原生组合用例让 Go/Python 对象经过合法纯 fn 管道后继续调用方法/索引，再对照实际生成程序。

README 中英文的对应程序块保持一致；入口、Claim 和模块示例已实际验证命名/位置输入、null/void 协议、模块复用与并发额度 1/2/4。`Examples/ecosystem/18_proposals.lip` 与 `SessionScriptExamples` 验证 effect 的纯函数构造、参数传递、请求链和不同结果类型，`19_parallel_read_write` 验证保留操作摘要后的并行读取与顺序写入，`21_summary_recovery.lip` 展示闭合容器、条件/返回函数和递归参数的摘要恢复。项目 policy 片段明确要求应用注册与实施限制，不作为 demo 内建能力。命令验收还包括 emit-go/build 的可执行结果、Go/Python REPL、全部脚本示例的 session 执行、Flow 跨 cell 事务、模块来源冻结、多行语句、真实 PTY 下的 Flow 补全与粘贴、含空格路径的 :run、仓库外的内置模块课与 38 课目录。文档相对路径、章节链接和表中的测试名一并核对。

2026-10-15 在 Go 1.27、Python 3.13、Linux 环境完成 `make test`、`make race`、`make vet`，均通过。真实 TCP 监听在受限沙箱中不可用时，`InteractiveCLI/web-server` 明确跳过；内存 HTTP 请求/响应与 handler 用例继续执行。依赖平台 plugin、已安装第三方库的用例按前提标记跳过，不能将跳过算作通过。

2026-10-17 的补充检查包括两轮各 40 秒的编译/会话模糊测试，以及竞争检测版 CLI 的真实 PTY、全部课程和 README 原生请求例子的实际输出检查。模糊测试未启用 NativeHost，不能据此声称覆盖原生降低。另以固定种子 11017 完成 4,000 次启用原生适配的递归组合编译，交叉变换参数、容器、投影、高阶返回和名称/声明顺序；它验证编译终止和接受，不替代业务运行断言。新增示例经 CLI 实跑与独立程序验证，原生返回来源另有参数交换、递归结构、固定/动态键 put、纯字段、跨模块及跨 cell 闭包的实际运行和竞争检测回归。

验收使用项目内缓存与临时目录。目录清理只删除项目生成物，不提交 `.build`、可执行文件或 Python 缓存。
