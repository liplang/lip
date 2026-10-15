# LIP 1.0 实现与验证

唯一语言依据为 [LIP 1.0 规范](spec/LIP-1.0-SPEC.md)。本仓库已实现 core 的入口、值与纯函数、控制结构、结构化 Flow、静态模块、Claim、图调度和统一 Host 边界，并提供解释执行与真正生成 Go 两条执行路径。以下按规范章节列出实现与可重复的回归证据，供使用、复核与后续开发。HTTP 是可选接入；可选 durable profile 未实现。

## 规范覆盖

测试集中在现有 `tests/` 文件；表中省略共同的 `Test` 前缀。

| 规范 | 实现与检查重点 | 主要证据 |
|---|---|---|
| §2、2.3 文件、声明、入口 | 文件模式唯一选择；末行无换行；未选声明检查；EntryInput 先于 run_id/journal；CLI 与生成程序的两种 JSON 输入统一报 E-SCHEMA；EntryOutput 区分 null/void，等待全图成功 | `DeclarationFilesAndEntrySelection`、`EntryInputBoundary`、`EntryOutputCompletion`、`GoBackendDeclarationsAndEntryProtocol`、`InteractiveCLI/entry-selection-and-input` |
| §2.1、6.3 子 Flow/管道 | 静态目标、Flow 非值、调用环、Pure Data 参数；初始值只求值一次；局部游标等待子图，保留原始错误；父子共用预算和 journal | `StructuredFlowAndPipeline`、`FlowBoundaries`、`SubFlowErrorPathAndEmptyBudget`、`GoBackendStructuredFlows` |
| §2.2 静态模块 | 冻结完整依赖、版本锁、私有/导出、模块环；模块只使用自身名字与 import，不捕获入口模块环境；路径精确匹配、链接及重复物理文件拒绝 | `StaticModuleSnapshot`、`ModuleLexicalIsolation`、`LocalSourceSnapshot`、`GoBackendStaticModules` |
| §3–5 值、函数、纯性 | 不可变捕获；动态调用顺序；参数/返回注解；十万次尾调用；保守 Ops；严格 JSON、Unicode、数值与 Data 边界 | `PureOperations`、`FunctionCapturesSnapshotNotMutableInput`、`DynamicArityPrecedesEffectfulArguments`、`DirectAndIndirectTailCalls`、`NullableAnyAndCallbackChecks`、`GoBackendNumericErrorsAndBudget` |
| §6、16.1 身份与局部迭代 | Position/EvalPath/CallID/JoinID；循环 source 子 Flow 与第 0 轮 body 不碰撞；CRLF 归一；多源根优先 sites；Claim 实例隔离 | `ModuleStaticSiteVector`、`LoopSourceFlowAndBodyPositions`、`CarryPerformFeedsNextProposal`、`ClaimParallelAndReplay`、native Claim fixture |
| §7 调度、Earlier、After、F/K | 等待子图/资源时释放工作额度；祖先豁免不吞掉外部前置；动态目标、空 region 完成集合、错误截断前置闭包；合法调度扰动 | `SubFlowBarrierCompletion`、`PermitWaitYieldsWorker`、`AfterWireAndChildCompletion`、`AfterEmptyFlowCompletion`、`AfterRejectsImpossibleLoopInstances`、`FutureLoopPrerequisiteBeyondErrorPrefix` |
| §8–9 Claim | 追加事实、同身份重投/冲突、完整读取等待 producer、live snapshot 是单次观察；未选 gate 零调用/零错误 | `ClaimParallelAndReplay`、`NativeClaimConflictFixture`、`SkippedGateProducesNeitherCallNorError`、snapshot 示例 |
| §10、11 的 core 调用记录规则 | proposal 零调用；参数、授权和资源准入；Go/Python 共同 Data/schema 边界；terminal 重放零新增调用；已完成 schema fault 与 Uncertain 区分 | `AuthorizationBeforeProvider`、`GoAndPythonUseSameDataBoundary`、`KnownSchemaFaultNeverRetries`、`WriteTimeoutIsUncertainAndNeverRetried`、`JournalDiagnosticsAreImmutableObservations` |
| §10.1–10.2 原生资源与生成程序 | Host 所有权、失效引用、worker 生命周期；原生辅助转换尊重词法名称与管道静态目标；真实 Go 求值；启动核对合约；编译后元数据并发只读 | `NativeHostAuthorizationAndRegisteredPrecedence`、`NativeModuleHelpers`、`NativeHelperLexicalScope`、`NativePipelineStagesRemainStatic`、`NativeLibrariesInterpreterAndGeneratedProgram`、`GoHostContractDriftRejectedBeforeCalls`、带 race 的 native fixture |
| §12.1 可选 HTTP | 显式方法、HEAD 不回退、405/Allow；编码路径与 params；请求/响应预算、隔离、取消和不重试 | `HTTPExplicitMethodsAndEscapedPath`、`HTTPConcurrentRunsKeepIdentityAndClaimsSeparate`、`HTTPAdmissionCancellationAndUncertainNeverRetry`、`WebExamplesInterpreterAndGo` |
| §14、16.3–16.5 组合与固定向量 | 同一冻结依赖经两条路径复用；闭包、carry、图循环、管道、void、未选分支及动态 Data 故障；固定 binary64 参考算法 | `CoreCompositionConformance` 在并发额度 1/2/4 下对照实际生成程序；`SpecNumericReference`、`GoBackendPowerLogarithm` |

模块/调用实现复用 compiler、checker、evaluator、scheduler 和 backend。源码加载与链接集中在 `script/modules.go`，没有第二套执行机。函数参数和全局数据按词法作用域解析；Claim 读取使用当前环境，不按执行节点的 store 名劫持模块函数里的同名值。`native_data` / `native_release` 仅转换未被声明、参数、binding、Claim 或 import alias 占用的辅助调用；依赖模块的辅助调用在链接时固定其来源。管道阶段保留静态目标供普通 checker 验证，原生调用不会把它转换成另一种 AST 后绕过检查。

## 工具、教学与范围

CLI `run/emit-go/build/serve` 都使用同一冻结 SourceSet；构建预算为 4096 来源、64 MiB。`BuildInfo` 公开 SourceTable、DependencyLock 和 revision。SourceTable 按 SourceID 排序，StaticSite 使用根来源优先的独立顺序。生成程序固定入口和源码快照，保留构建元数据但不重新解析/解释 LIP 文本。

REPL cell 保存成功值与纯闭包，不重跑历史动作；完整 Flow、静态 Data 和模块通过 `:run`，可带 `-entry/-project/-input/-entry-input/-entry-output`。文件运行独立于会话绑定。`learn` 的 18 课使用同一 CompileSourceSet/Run 路径；入口课检查两种参数绑定，模块课使用内置冻结来源。README 中英文、示例教程、CLI 帮助与课程保持相同的入口和边界描述。

HTTP 只接受显式 Host 合约，不提供请求级通用 Native bridge。普通文件、REPL 和生成程序可用通用 Go/Python bridge；自动 Go adapter 需要 Go 工具链、CGO 和 plugin 支持，Python 需要运行环境中的相应库。MemoryJournal 仅提供进程内调用记录和重放，不能恢复进程崩溃。durable 的 begin、落盘、crash/reconcile 验收不在当前支持范围。

## 验证方法与环境边界

```sh
make test       # 公共 API、所有课程/示例、真实 PTY、实际生成的 Go 程序
make race       # 解释器和实际生成程序均启用 race，含并发共享静态元数据
make vet
make clean
```

数值逐位表由 `go run ./tests/reference_numeric -check` 从规范参考算法独立核对，正常包含在 `make test`。`CoreCompositionConformance` 除成功输出还比较原始模块诊断；错误不会改写为调用方包装错误。After、barrier 和资源等待的专项测试继续检查完成集合及前置依赖，而非仅比较最终值。原生组合用例让 Go/Python 对象经过合法纯 fn 管道后继续调用方法/索引，再对照实际生成程序。

README 中英文的三个完整程序块保持一致，已实际验证命名/位置输入、null/void 协议、模块复用与并发额度 1/2/4；命令验收还包括 emit-go/build 的可执行结果、Go/Python REPL、含空格路径的 :run、仓库外的内置模块课与 18 课目录。文档相对路径、章节链接和表中的测试名一并核对。

2026-10-15 在 Go 1.27、Python 3.13、Linux 环境完成 `make test`、`make race`、`make vet`，均通过。真实 TCP 监听在受限沙箱中不可用时，`InteractiveCLI/web-server` 明确跳过；内存 HTTP 请求/响应与 handler 用例继续执行。依赖平台 plugin、已安装第三方库的用例按前提标记跳过，不能将跳过算作通过。

验收使用项目内缓存与临时目录。目录清理只删除项目生成物，不提交 `.build`、可执行文件或 Python 缓存。
