# Alpha 0.6.4 实现一致性审计

当前契约为 [ALPHA-0.6-SPEC.md](ALPHA-0.6-SPEC.md) 与
[LIST-LIBRARY.md](LIST-LIBRARY.md)、[STRING-LIBRARY.md](STRING-LIBRARY.md)。本次审计把核心、已有 Runtime policy 和
外部 adapter 的规则与实现对应起来。

## 核心闭环与证据

| 契约 | 实现与验证 |
| --- | --- |
| 构造数据与完整边界 | ObjectExpr/UnaryExpr/null，list/object 参数与 T? 输入、fn/回调/Flow 结果及 CLI null；三条 CLI 路径 conformance |
| Rust 优先的表达式风格 | 唯一块式 if、# 行注释、显式 Flow 边界；拒绝旧语法的回归测试 |
| range/Map/fold | 半开区间、数量上限、稳定 Map、顺序归约、空输入、错误索引和取消测试 |
| 语句式 for/break/continue | 有限集合、顺序等待、局部作用域与捕获、嵌套控制跳转、首次错误/取消停止、每 Tick 重跑及三种调度/REPL 验证 |
| 可组合推导式 | Flow source/元素可组合外部调用，fn/回调保持纯；调用参数、多层嵌套；变量遮蔽、惰性分支、错误顺序、取消、增量复用与真实 Python/REPL 验证 |
| 统一集合回调 | map/filter/fold/scan/group_by/split_by/sort_by/any/all 内联纯 fn；捕获依赖、参数遮蔽、短路、类型/元素错误与 REPL 验证 |
| 纯递归 | 递归环结果类型验证、尾调用/线性累积循环化、每次调用的运行上下文/1024 深度检查；直接/间接及经 list.map 的递归 conformance |
| 36 个纯 list 操作 | internal/listops 共用签名，全部目录项正面测试；空输入、分组/排序稳定性、形状、取消、错误与不修改输入 |
| 19 个纯 string 操作 | internal/stringops 共用签名；Unicode、空项、字面匹配、解析、资源/错误与组合验证 |
| 明确失败与类型 | fail 不产值，内部 never 保留成功分支类型，Host 不可覆盖；编译/生成/惰性分支验证 |
| 统一资源约束 | 字符串边界/len/索引/+/*/库统一 UTF-8/16 MiB；Map/fold 处理前拒绝超量 |
| 嵌套组合与真实依赖 | callback 名不产生数据边；对象值与数据参数产生真实边；生成库的分组→映射→聚合通过三种调度路径 |
| 整表达式效果与分派 | 汇总参数、Map source/元素、state 初始化、retry/feedback；三种调度、Await、错误顺序、惰性分支、纯缓存与只读/写入每 Tick 重跑；本地 fn 与 Host 注册隔离 |
| Incremental 保持身份 | 纯列表节点未变化时复用，State/Tick 和现有取消/效果测试继续通过 |
| 计算生命周期与 Go GC | 整数索引计划、最后消费者/跳过解除引用、并行依赖快照、迟到结果丢弃；weak.Pointer 验证异步/别名/失效缓存/State/null，生成 Map/回调/for/Retry 与 race 验证 |
| 独立观察 | inspect graph.v1 与 trace.v1；成功/执行失败/写入失败/输入失败及 stdout/退出码检查 |
| 忠实生成与清理 | 失败时执行 Worker defer；Host/Go 独立入口只生成明确报错路径，不残留不可达执行代码 |
| 临时构建可复用 | 内置 Runtime 使用 trimpath；相同生成源码在两个独立临时模块中的二进制逐字相同并实际运行，错误仍保留 LIP 源位置 |
| 可读可写可验证 | 21 节教程、19 处逐字核对文档源码、27 个实际结果/错误样例、完整宿主程序 |
| 文档与实现自动同步 | README/规范/文档的全部 lip 代码块实际检查及生成格式校验；错误示例 JSON、库目录、课程索引、版本及所有编译生成输出的逐字核对 |
| Go/Python/库自然组合 | 同一 Flow 的调用/native 值，三种调度一致；所有外部调用遵循相同纯性规则 |
| Python 数据完整性 | 非有限、键碰撞、无序 set 不隐式丢信息；失败转换回收新句柄 |
| AI 修复接口 | diagnostics.v1、位置/源行/hints/退出码实际 CLI 测试，仍检查首个错误 |
| 交互输入编辑 | 光标/历史/草稿恢复、中文/组合字符/emoji、删除/取消/多行粘贴和补全回归 |
| 导入名称归属 | Python/Host/Go 的 as；命名空间与裸调用分别解析，内置/本地 fn 同名别名、单操作别名优先、词法遮蔽、后端消歧、AST 不变、循环/retry/feedback 与 REPL 验证 |
| 使用能力与声明分离 | 未使用的导入仅保留元数据；实际操作、属性、反馈回调和嵌套循环决定适配器与 Worker 要求，缺注册仍失败 |
| Host 标量和索引安全 | Go 定义的 number/bool/string 与其底层类型一致；json.Number 分类、UTF-8、非有限、map 不可比较键和 Unicode 字段测试；生成 Run/Sequential/Parallel/Tick 验证 |
| 空语句一致性 | 文件、fn、Flow/match/for 与 REPL 接受连续或独立分号，缺分隔符仍失败 |
| 本地发行与版本来源 | Runtime/CLI 共用版本，生成头写版本；明确项目源码打包，确定性归档、SHA-256、解压后仓库外离线执行验证 |
| 诊断与显示一致性 | 参数数量独立分类，拼写建议与具体类型/回调修法；print/str/REPL 使用相同核心值格式 |
| 原生编辑器与诊断定位 | Lua/Vim9script/Elisp 共用生成词库；真实编辑器的中文/组合字符/Tab 字节换算、缩进、补全与异步过期结果/清理测试，见 [EDITORS.md](EDITORS.md) |

list 标准库不是新增控制流或 Host 操作集合；核心纯调用由生成代码直接分派，
仅注册同名 Host operation 不会改变它们；显式外部 import 的调用保留后端身份。list.map 和嵌入表达式的推导式顺序执行，独立
Map 推导式保留 Runtime 展开和并行能力。大区间、大组合结果、非矩形转置和过深递归均明确失败。

## 收敛决定

- 版本、规范入口、README、教程与示例使用同一套现行规则。
- Python adapter 统一使用 PythonWorker/PythonWorkerConfig，配置入口为 Python/Script。
- Runtime 节点效果统一使用 Effect，Map 从节点和 Host 注册获取效果分类。
- 所有 CLI 工具选项使用长形式并位于入口文件前，文件后全部是程序输入。
- 删除旧语法解析、迁移入口、隐式源码生成与过期规范，版本历史集中在 CHANGELOG。
- 新的数据操作进入独立纯标准库和统一目录，语法不为每个列表操作增加关键字。
- 有限集合遍历支持顺序 for/break/continue；可变循环变量、循环内 return、通用函数值/闭包、泛型 iterator 与新后端延后；内联集合回调已支持不可变捕获。
- 字符串索引改为 Unicode 字符字符串，与 len 一致；这是显式记录的 Alpha 语义变更。

## 验证入口

```bash
bash scripts/verify-release.sh
```

脚本执行 tests/race/vet/build、全部示例与 conformance 的检查/Go 生成/构建/vet，
以及 core/range/tree/lists 的独立运行验收。最新结果见 [RELEASE.md](../RELEASE.md)。
0.6.0 完整验收为 33 份源程序；0.6.1 最初扩至 47 份，后续审计扩至 55 份。
此前 0.6.1 与语言扩展的完整验收分别覆盖 51 与 55 份源程序。
0.6.2 完整验收于 2026-10-09 通过：55 份源程序、46 个文档 LIP 代码块、18 处标注源码、
12 份生成文件、26 节课程和 27 个教程 CLI 用例；包含 tests/race/vet/build、独立安装
以及 math/for/可空 CLI 的独立运行。
0.6.4 完整验收于 2026-10-09 通过：56 份源程序、47 个文档 LIP 代码块、19 处标注源码，
保留全部 12 份生成文件、26 节课程和 27 个教程 CLI 用例的同步检查。
值生命周期回归覆盖最后消费者/跳过、嵌套 Await、无关 worker、返回/Host 别名、
失效缓存、State/null、失败/取消和乱序节点；Runtime race 重复验证 10 次，
生成 Map/回调/for/Retry/异步/Tick 组合另以 GOFLAGS=-race 验证。
存活堆、重复执行与首次构图的成本测量见 [VALUE-LIFETIMES.md](VALUE-LIFETIMES.md)。
本轮发行收敛再次完整通过 tests/race/vet/build、56 份源程序与全部文档/课程同步检查；
新增 Go 定义标量、map 索引/Unicode 字段、空语句、同名别名、未使用导入与 Python
控制操作回归。生成的标量/Host/别名/Run/Tick 组合另以 GOFLAGS=-race 验证。
最终编译器 fuzz 设置 10 秒、2 worker，通过 121,843 次执行。
本地源码与 Linux/Windows/macOS 工具包使用 manifest/SHA-256 校验；相同输入
重复打包逐字相同，Linux 解压后仓库外离线 run/build 和无 Go 的产物执行通过。
Windows/macOS 只验证交叉构建，原生执行需对应环境。
新增审计回归位于 compiler/audit_test.go、tests/audit_test.go、REPL 与可空类型教程用例。
编辑器扩展保持 0.6.4：完整发行检查与 Vim 9.2 / Neovim 0.12.5 / Emacs 31.1
原生测试通过；覆盖中文/组合字符/Tab、带空格路径、注释/字符串内的运算符和
调用名、`=>` 换行与嵌套缩进、实际检查命令、过期结果、取消与资源清理。
Emacs 在临时安装目录中以警告为错误做字节编译，再运行 5 个 ERT 用例。
三平台工具包与源码包均包含插件及安装指南，校验器逐项核对每个工具包的
文档/示例/插件内容与源码哈希；Linux 离线运行验收通过。
此前 compiler fuzz 含 match、for、可空类型、外部组合和别名 seed，设置 10 秒、2 worker，
通过 157,376 次执行，
检查随机输入无崩溃、接受的程序能生成可格式化 Go。

实际修正：字符串 * 仅防整数溢出仍可巨量分配；Map/fold 缺输入上限；文件
读取误注册 Pure；Python 非有限静默变 null、键碰撞覆盖、set 无序变 list、
失败转换泄漏句柄；测试把 Worker 启动故障当缺环境跳过。修正都有失败或
组合验证，没有减少检查来掩盖问题。

list/object 在边界检查外层形状，动态元素在运算时检查。Host 通过运行上下文（Go 的 `context`）
协作取消并保护共享资源，外部写入完成后保留其结果。

生成代码的 vet 曾发现 Host/Go 入口提前返回后的不可达分支，现已通过生成器
结构修正，而不是跳过 vet。Python 科学包缺失时对应环境测试明确跳过；独立核心
不以科学包为前提。
