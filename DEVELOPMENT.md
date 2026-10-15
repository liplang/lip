# LIP 开发指南

[English](DEVELOPMENT.en.md) | [简体中文](DEVELOPMENT.md)

本指南与仓库的[开发技能](.agents/skills/lip-development/SKILL.md)配套，适用于 LIP 1.0 规范、编译器、Go 后端、Go/Python Host 边界、示例和交互工具的开发与审查。先定位用户指定、包含 `spec/LIP-1.0-SPEC.md`、`script/` 和 `go.mod` 的项目根目录。受限环境不能写目标目录时，使用已经授权的本地副本。以下路径均相对根目录。

## 设计依据

[LIP 1.0 规范](spec/LIP-1.0-SPEC.md)是唯一语言依据，另有[英文版](spec/LIP-1.0-SPEC.en.md)。先读相关章节，再追踪实现。规范应独立完整，先定义符号、状态、身份与错误，再使用；将接受的结论融入设计，不携带审查优先级、问题编号或聊天称谓。拒绝设计建议时，交付说明应给出具体理由。

除非任务涉及规范或发现矛盾，优先修实现。区分规范审查和实现审查，只报告实际完成的检查。

核心保持小：不可变值、纯函数、控制结构、图依赖、追加式 Claim、effect/perform。普通数据操作由既有纯操作组合，主要库来自 Go/Python。不增加业务 reducer 语法、任意可变格、自动订阅、并行写或阈值等待，也避免不必要的文件。

## 实现边界

- `script/compiler.go` 与 `checker.go` 负责语法和静态保证；隐藏在 function/any 后的签名仍需动态检查。
- `values.go` 统一 Data、数值、编码、身份与诊断。any? 等价于 any；各路径一致处理可空类型、非有限数、重复字段与不支持的原生值。
- `**` 右结合，优先于左侧负号，右侧可带符号；`*/` 以右操作数为底，与乘除同级且左结合。遵守 §16.5 固定算法，不替换为平台 pow/log；同步规范参考代码、错误和解释/生成向量。
- `evaluator.go`/`scheduler.go` 负责纯函数、闭包、proper tail calls、carry、gate、barrier、主错误与 Claim。未选分支不产生动态错误或调用；保留节点位置和求值路径。
- `host.go`/`adapters.go` 是唯一外部边界。构造 proposal 不调用 provider；参数、授权和资源验证先于动作。区分已知完成但验证失败与 Uncertain，重试不得隐藏写入事实。
- Ops 保守传播：容器投影、调用结果及 carry 结果为 Unknown。不能用更强的私有推导放行 parallel；记录来源不是纯性类型。
- `backend.go` 生成实际 Go 求值代码，不把源码包入解释器。新增 helper 时检查嵌入声明筛选，避免生成代码遗漏符号。
- 生成程序的静态元数据只初始化一次，之后只读共享。并发构造/运行 Program 不得改写活动 AST；native fixture 同样启用 race。
- `session.go` 通过相同 checker 和图引擎保存成功 cell 的不可变值与闭包，不重跑历史。失败 cell 不提交绑定，已执行动作仍存在；完整 Claim 程序有明确运行生命周期。
- REPL/learn 共用 `cmd/lipc/editor.go`。raw 模式仅覆盖输入，执行 provider 前恢复；取消、EOF、错误也恢复。终端不后台预读。通过真实 PTY 验证快捷键、Unicode 和括号粘贴，管道保持普通文本；补全不执行源码或原生调用。

## 调用一致性

普通动态调用依次：求值 callee → 验证 callable、实际 arity/命名参数 → 按源码顺序求值实参 → 检查参数类型 → 执行函数 → 验证返回注解。arity 错误先于实参中的 perform，类型错误先于函数体；map/fold 回调遵守相同规则。

覆盖直接函数、别名、匿名函数、容器取出的动态函数、闭包、高阶回调和命名参数。静态已知错误提前拒绝，包括空列表上的已知回调 arity 错误。解释与生成程序的动态错误一致。

Claim 追加带身份的事实；完整读取等待全部 producer，live snapshot 是单次外部观察。position 使用十进制字符串传输并按数值排序。身份比较完整规范结构，摘要仅作索引，冲突不能先到先得。

## 文档与目录

普通提示、帮助、诊断、课程和示例注释以英文为主，可辅中文；篇幅有限时优先英文。公共文档提供互相链接的中英文版本，同步合约、示例、章节号与导航。代码、注释和正常交互文字使用英文。

README 先解释图并发、Claim 和 effect 解决的问题，再介绍用法。ReAct 解释为思考 → 调工具 → 观察 → 再决定。区分 run 内依赖图与 Host 长期事件管理，如实说明小核心、Go/Python 库及未实现的持久恢复。

`examples/` 提供有注释、可执行的 `.lip` 程序，从 Hello World、绑定、算术、分支、函数逐步展开到 carry、Claim 与外部调用。教学注册集中在 `script/adapters.go`，由 DemoHost 显式启用；`backend.go` 从同一注册函数导出 DemoHostGoSource。测试统一放 `tests/`。按语义合并代码，新文件应承担完整职责。

最低 Go 版本为 1.25，允许更高版本，不固定补丁或 toolchain；根据用户要求和真实依赖调整。

## 验证与交付

针对真实语义风险添加回归，不重复实现。有效检查包括数值/编码向量、value block 尾部 if/match、错误参数与 malformed source；实际生成程序及诊断位置；合法调度变化、未选分支零调用、前置闭包/barrier/Claim；Go/Python 一致的 Data 边界、授权前零调用、已知故障重放不重跑；REPL 多行、错误恢复、闭包、历史动作只执行一次；各课程的多输入验收。

运行 `make test`、`make race`、`make vet`；race 同时实际构建并运行带竞争检测的生成程序。缓存与临时文件放 `.build`。验证后执行 `make clean`，不交付 lipc、`.build`、Python 缓存或其他生成物；只清理项目产物。说明具体改动、实际检查和限制，不宣称实现所有可选配置。
