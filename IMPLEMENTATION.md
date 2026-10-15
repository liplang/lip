# LIP 1.0 实现与验证

[English](IMPLEMENTATION.en.md) | [简体中文](IMPLEMENTATION.md)

唯一语言依据为 [LIP 1.0 规范](spec/LIP-1.0-SPEC.md)。本仓库已实现 core 的入口、值与纯函数、控制结构、结构化 Flow、静态模块、Claim、图调度和统一 Host 边界，并提供解释执行与真正生成 Go 两条执行路径。以下按规范章节列出实现与可重复的回归证据，供使用、复核与后续开发。HTTP 是可选接入；可选 durable profile 未实现。

## 规范覆盖

测试集中在现有 `tests/` 文件；表中省略共同的 `Test` 前缀。

| 规范 | 实现与检查重点 | 主要证据 |
|---|---|---|
| §2、2.3 文件、声明、入口 | 文件模式唯一选择；末行无换行；未选声明检查；EntryInput 先于 run_id/journal；CLI 与生成程序的两种 JSON 输入统一报 E-SCHEMA；EntryOutput 区分 null/void，等待全图成功 | `DeclarationFilesAndEntrySelection`、`EntryInputBoundary`、`EntryOutputCompletion`、`GoBackendDeclarationsAndEntryProtocol`、`InteractiveCLI/entry-selection-and-input` |
| §2.1、6.3 子 Flow/管道 | 静态目标、Flow 非值、调用环、Pure Data 参数；初始值只求值一次；局部游标等待子图，保留原始错误；父子共用预算和 journal | `StructuredFlowAndPipeline`、`FlowBoundaries`、`SubFlowErrorPathAndEmptyBudget`、`GoBackendStructuredFlows` |
| §2.2 静态模块 | 冻结完整依赖、版本锁、私有/导出、模块环；模块只使用自身名字与 import，不捕获入口模块环境；路径精确匹配、链接及重复物理文件拒绝 | `StaticModuleSnapshot`、`ModuleLexicalIsolation`、`LocalSourceSnapshot`、`GoBackendStaticModules` |
| §3–5 值、函数、纯性 | 不可变捕获；动态调用顺序；参数/返回注解；十万次尾调用；保守 Ops；严格 JSON、Unicode、数值与 Data 边界 | `PureOperations`、`FunctionCapturesSnapshotNotMutableInput`、`DynamicArityPrecedesEffectfulArguments`、`DirectAndIndirectTailCalls`、`NullableAnyAndCallbackChecks`、`GoBackendNumericErrorsAndBudget` |
| §4 递归与资源失败 | 默认 10,000 个非尾帧、正值可调整、负配置拒绝；尾调用复用帧；E-BUDGET 不伪装为空值成功；REPL/HTTP 恢复 | `NonTailFrameBudget`、`GoBackendNonTailFrameBudget`、`SessionRecoversFromFrameBudget`、`HTTPRecoversFromFrameBudget`、`InteractiveCLI/recursion-budget` |
| §6、16.1 身份与局部迭代 | Position/EvalPath/CallID/JoinID；循环 source 子 Flow 与第 0 轮 body 不碰撞；CRLF 归一；多源根优先 sites；Claim 实例隔离 | `ModuleStaticSiteVector`、`LoopSourceFlowAndBodyPositions`、`CarryPerformFeedsNextProposal`、`ClaimParallelAndReplay`、native Claim fixture |
| §7 调度、Earlier、After、F/K | 等待子图/资源时释放工作额度；祖先豁免不吞掉外部前置；动态目标、空 region 完成集合、错误截断前置闭包；合法调度扰动 | `SubFlowBarrierCompletion`、`PermitWaitYieldsWorker`、`AfterWireAndChildCompletion`、`AfterEmptyFlowCompletion`、`AfterRejectsImpossibleLoopInstances`、`FutureLoopPrerequisiteBeyondErrorPrefix` |
| §8–9 Claim | 追加事实、同身份重投/冲突、完整读取等待 producer、live snapshot 是单次观察；未选 gate 零调用/零错误 | `ClaimParallelAndReplay`、`NativeClaimConflictFixture`、`SkippedGateProducesNeitherCallNorError`、snapshot 示例 |
| §10、11 的 core 调用记录规则 | proposal 零调用；参数、授权和资源准入；Go/Python 共同 Data/schema 边界；terminal 重放零新增调用；已完成 schema fault 与 Uncertain 区分 | `AuthorizationBeforeProvider`、`GoAndPythonUseSameDataBoundary`、`KnownSchemaFaultNeverRetries`、`WriteTimeoutIsUncertainAndNeverRetried`、`JournalDiagnosticsAreImmutableObservations` |
| §10.1–10.2 原生资源与生成程序（边界见下文） | run 清理 hook、原生 owner/generation 检查、Go 静态链接；原生辅助转换尊重词法名称与管道静态目标；真实 Go 求值；启动核对合约；编译后元数据并发只读 | `NativeHostAuthorizationAndRegisteredPrecedence`、`NativeModuleHelpers`、`NativeHelperLexicalScope`、`NativePipelineStagesRemainStatic`、`NativeLibrariesInterpreterAndGeneratedProgram`、`GoHostContractDriftRejectedBeforeCalls`、带 race 的 native fixture |
| §12.1 可选 HTTP | 显式方法、HEAD 不回退、405/Allow；编码路径与 params；请求/响应预算、隔离、取消和不重试 | `HTTPExplicitMethodsAndEscapedPath`、`HTTPConcurrentRunsKeepIdentityAndClaimsSeparate`、`HTTPAdmissionCancellationAndUncertainNeverRetry`、`WebExamplesInterpreterAndGo` |
| §14、16.3–16.5 组合与固定向量 | 同一冻结依赖经两条路径复用；闭包、carry、图循环、管道、void、未选分支及动态 Data 故障；固定 binary64 参考算法 | `CoreCompositionConformance` 在并发额度 1/2/4/7/1238/73295 下对照实际生成程序；`SpecNumericReference`、`GoBackendPowerLogarithm` |

模块/调用实现复用 compiler、checker、evaluator、scheduler 和 backend。源码加载与链接集中在 `script/modules.go`，没有第二套执行机。函数参数和全局数据按词法作用域解析；Claim 读取使用当前环境，不按执行节点的 store 名劫持模块函数里的同名值。`native_data` / `native_release` 仅转换未被声明、参数、binding、Claim 或 import alias 占用的辅助调用；依赖模块的辅助调用在链接时固定其来源。管道阶段保留静态目标供普通 checker 验证，原生调用不会把它转换成另一种 AST 后绕过检查。

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

完成的词法 region 释放节点值、环境和循环 source，同时保留 After 所需状态、错误、身份及 source 长度。子 Flow 完成后释放其 store，不可变 Data 输出保留到交给等待中的调用方。冻结闭包和共享 Data 各自保留引用；顶层 session 结果保留到事务提交。`CompletedScopesReleaseLocalValues` 在 run 尚未结束时，测量子 Flow、分支和循环中大块纯临时数据的活跃堆；`CoreCompositionConformance` 在解释器与生成程序中检查共享输出、闭包和 Claim。尚未实现活跃作用域内每个绑定的精确最后使用清理；图记录、活跃 Claim、输出、原生引用及内存 journal 仍保留必要数据，不保证常量内存，也不保证从致命 OOM 恢复。

具名函数与匿名函数均只冻结自身使用的外部名字，具名函数互递归仍保留原函数身份；不会仅因声明了无关函数，就保留已经解绑的大块 session 数据。管道临时绑定属于当前管道的局部作用域，不会被闭包误当成外部名字。`SessionDeleteReleasesUncapturedValues` 测量解绑后的堆保留；`SessionPipelineClosures` 与 `CoreCompositionConformance/closure_pipeline` 检查 REPL、解释器、实际生成程序中的嵌套管道闭包及名字复用。`FuzzSessionTransactions` 进一步检查任意 cell 的失败事务、错误后恢复与解绑重用。

## 工具、教学与范围

CLI `run/emit-go/build/serve` 都使用同一冻结 SourceSet；构建预算为 4096 来源、64 MiB。`BuildInfo` 公开 SourceTable、DependencyLock 和 revision。SourceTable 按 SourceID 排序，StaticSite 使用根来源优先的独立顺序。生成程序固定入口和源码快照，保留构建元数据但不重新解析/解释 LIP 文本。

`DefaultMaxFrames` 为每个叶子求值器 10,000 个同时活跃的非尾函数帧。`Options.MaxFrames` 以及 run/repl/serve/生成程序的 `-frames` 接受正值覆盖；零使用默认值，负数报配置错误，不能绕过保护。直接、间接、互递归及 map/fold 回调共用调用检查；proper tail calls 复用帧。耗尽时报告带位置的 E-BUDGET，Report 没有 EntryOutput，CLI 不输出成功值，失败 cell 不提交绑定。HTTP 使用现有固定 500 错误响应。REPL 与 handler 可继续处理后续工作。这是提前生效的帧预算，不是按 run 统计内存或恢复原生运行时/操作系统致命资源耗尽；Host 仍应保留合适的指令、分配、并发与时间限制。

REPL cell 保存成功值、fn/Flow 声明与纯闭包，不重跑历史动作；复用声明解析器、checker、linker、原生调用 lowering 和图执行器，支持静态 Data、export、LIP 导入、Claim，以及控制结构的语句和值形式。Flow 调用和管道每次创建独立子图与 store；顶层 Claim 在单个 cell 中完成，仅保留完成后的 Data 快照。`:begin/:end` 将完整脚本或前向声明作为一个 cell 提交；`-project` 指定导入根目录，来源闭包冻结至 reset。`:run` 可带 `-entry/-project/-input/-entry-input/-entry-output`，文件运行独立于会话绑定。`learn` 的 18 课使用同一 CompileSourceSet/Run 路径；入口课检查两种参数绑定，模块课使用内置冻结来源。README 中英文、示例教程、CLI 帮助与课程保持相同的入口和边界描述。

会话命名空间将公开名字映射到各 cell 的不可变符号。`Session.Delete(names...)` 与 CLI 的 `:del name[,name...]` 先验证整批名字，再解除公开绑定，与绑定内容无关。后续 cell 可跨绑定种类复用名字，已有闭包与 Flow 保留原引用、导入含义和源码 token。根据公开绑定及保留 Flow 的依赖可达性，清理不再使用的保存值/定义；导入缓存和原生资源生命周期仍显式管理，解绑不重跑动作也不关闭资源。`SessionDeleteBindings`、`SessionDeleteModuleAlias` 和 `InteractiveCLI/delete-bindings` 覆盖跨种类复用、旧依赖/proposal、缺失或格式错误时整批不变、Claim 快照、别名替换、可见性及 reset。

HTTP 只接受显式 Host 合约，不提供请求级通用 Native bridge。普通文件、REPL 和生成程序可用通用 Go/Python bridge；解释执行的自动 Go 导入仍使用需要 Go 工具链、CGO 和 plugin 的开发 bridge；生成程序的 Go 导出和可推导泛型实例在构建时链接，调用时不需要工具链。构建时仍未知的泛型实参类型需要通过 HostSource 显式注册具体实例。Python 需要运行环境中的相应库。MemoryJournal 仅提供进程内调用记录和重放，不能恢复进程崩溃。durable 的 begin、落盘、crash/reconcile 验收不在当前支持范围。

## 正确性与 Host 集成边界

REPL 保存 proposal 原有的规范 Ops 分析。容器投影、函数返回与 carry 结果跨 cell、别名后仍为 Unknown；不能凭保存结果中实际 operation 的身份放行 parallel。`SessionPreservesProposalOps` 检查拒绝前 provider 零调用，同时确保直接保存的 proposal 仍可在符合条件的 parallel body 中执行。

原生来源按容器中的具体位置记录，与不可变容器本身分开。`native_data` 返回普通 Data；捕获快照、读取资源引用旁的纯字段、选择纯列表元素不会引入 perform。`NativeSnapshotsAndContainerPurity` 对照解释器、实际生成程序与持久 session。已知 Go 标量结果遵循转换后的 Data 表示；保存的函数和 proposal 单独保留结果来源，不改变 Ops。纯数据集不会再分配逐元素来源数组；`SessionNativeLargeDataAllocation` 在保留 250,000 个数值元素时检查小 cell 的分配开销。构建期 initializer 关联放在运行时表达式树之外，lexer token 独立保存文本，不再通过子串保留整个源码缓冲区。`SessionNativeMetadataDoesNotRetainInitializers` 检查只捕获计算后长度的闭包能释放已删除的 12 MiB 字面量。

`Host.BeginRun` 与 `RunLease.End` 提供应用自定义 run 租约，接收 run 身份、结束错误与 journal。清理在已开始动作结算后执行，覆盖取消与预算失败，不撤销写入。`Report.CleanupErrors` 独立保留清理故障，不篡改图根错误。原生引用携带 scope、owner 与 generation；文件 run 的引用结束即撤销，失败 cell 释放新引用而保留原有资源。成功 cell 保留保存值、闭包、proposal 与结果可达的新引用。资源释放只解除技术引用，业务 close/commit/rollback 仍显式执行。`HostRunLifecycle`、`NativeRunOwnershipAndFailedCellCleanup`、`NativePythonFailedCellCleanup` 与 `DiagnosticPhases` 覆盖这些边界；Python 检查通过 weak reference 验证对象实际回收，并确保保留的闭包继续可用。诊断标明 static、runtime 或 Host 阶段，失效、越域、已释放引用报告 E-RESOURCE。

生成程序静态注册自动 Go 导出，保留命名参数绑定，并根据源码实参类型和 Go 返回签名链接具体泛型实例。`GeneratedGoLibrariesWithoutRuntimeToolchain` 在 PATH 中没有 Go 工具链时运行真实可执行文件；`GoModulePackageDiscoveryWithoutRegistry` 还检查本地第三方模块。构建时仍未知的类型需要通过可信 HostSource 注册具体实例。交互/解释执行的自动 Go 开发 bridge 仍在已授权调用中加载 plugin，尚不满足 §10.2 的静态绑定要求；需要该保证时使用显式静态注册 adapter 或生成程序，没有为此放宽 spec。

一般应用资源的元数据、取消/关闭行为和 worker 租约隔离仍由嵌入 Host 负责。默认 Native Host 用于单个 session 或文件工作流，没有提供共享并发请求的 worker pool 或完整应用资源管理。HTTP 继续要求显式 Host 合约，因此不能将 §10.1–10.2 的测试覆盖视为全面合规。

## 实际应用检查

`PracticalNativeWorkflows` 检查参数化 SQLite 聚合与注入输入、pandas CSV 清理/分组、scikit-learn 线性回归与标准化/分类 pipeline、NumPy 线性求解、SciPy 积分/优化，以及 Go HTML 模板转义和危险 URL 过滤。每个用例均以并发 1 和 7 解释运行，再运行实际生成的可执行文件和持久 REPL。科学计算用例需要已安装的库，缺少时明确跳过；本地环境提供 NumPy 2.3.2、pandas 2.3.2、SciPy 1.16.2 和 scikit-learn 1.7.2。

`NativeReceiverProposals` 检查 Flow/函数参数、嵌套 Go 资源、保存资源/可调用对象的 Data 包装、已保存的 REPL receiver、删除/复用名字和零调用构造。`NativeModuleReceiverProposals` 在解释器、生成程序和 REPL 中检查导入 LIP helper 的词法隔离与对象方法请求。显式 receiver 请求不依赖静态记录的原生来源；原生 adapter 仅在 perform 时解析嵌套成员。普通纯对象调用保持原有语义。

`HTTPPracticalCRUDApplication` 运行共享存储的网站，包含独立的列表/读取/创建/更新/删除/页面入口、业务验证、400/404/409 结果、Go HTML 模板转义和 24 个并发创建/读取客户端。非法输入零写入；重复请求保留存储而不泄漏请求局部值。HTTP 存储与渲染使用应用显式 Host 合约。纯核心线性模型示例还通过全部示例执行路径检查中心化最小二乘、预测、均方误差和奇异输入拒绝。

合成的模块读取/调用准备操作使用源码保留关键字前缀，与 get.once、prepare.once 等真实 Python 路径分离。adapter 仅移除当前动作对应的一层前缀。`NativePythonStateAttributesAndCallOrder` 覆盖这些名字、嵌套 prepare 字段、补全，以及解释器/生成程序/REPL 执行路径。

## 验证方法与环境边界

```sh
make test       # Public API, lessons/examples, actual PTY, actual generated programs
make race       # Interpreter and generated programs, including shared static metadata
make vet
make clean
```

数值逐位表由 `go run ./tests/reference_numeric -check` 从规范参考算法独立核对，正常包含在 `make test`。`CoreCompositionConformance` 除成功输出还比较原始模块诊断；错误不会改写为调用方包装错误。After、barrier 和资源等待的专项测试继续检查完成集合及前置依赖，而非仅比较最终值。原生组合用例让 Go/Python 对象经过合法纯 fn 管道后继续调用方法/索引，再对照实际生成程序。

README 中英文的三个完整程序块保持一致，已实际验证命名/位置输入、null/void 协议、模块复用与并发额度 1/2/4；命令验收还包括 emit-go/build 的可执行结果、Go/Python REPL、全部脚本示例的 session 执行、Flow 跨 cell 事务、模块来源冻结、多行语句、真实 PTY 下的 Flow 补全与粘贴、含空格路径的 :run、仓库外的内置模块课与 18 课目录。文档相对路径、章节链接和表中的测试名一并核对。

2026-10-15 在 Go 1.27、Python 3.13、Linux 环境完成 `make test`、`make race`、`make vet`，均通过。真实 TCP 监听在受限沙箱中不可用时，`InteractiveCLI/web-server` 明确跳过；内存 HTTP 请求/响应与 handler 用例继续执行。依赖平台 plugin、已安装第三方库的用例按前提标记跳过，不能将跳过算作通过。

验收使用项目内缓存与临时目录。目录清理只删除项目生成物，不提交 `.build`、可执行文件或 Python 缓存。
