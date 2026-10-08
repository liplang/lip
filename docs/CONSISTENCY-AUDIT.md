# Alpha 0.6.1 实现一致性审计

当前契约为 [ALPHA-0.6-SPEC.md](ALPHA-0.6-SPEC.md) 与
[LIST-LIBRARY.md](LIST-LIBRARY.md)、[STRING-LIBRARY.md](STRING-LIBRARY.md)。本次审计把核心、已有 Runtime policy 和
外部 adapter 的规则与实现对应起来。

## 核心闭环与证据

| 契约 | 实现与验证 |
| --- | --- |
| 构造数据与完整边界 | ObjectExpr/UnaryExpr/null，list/object 参数与可选 null 输出；三条 CLI 路径 conformance |
| Rust 优先的表达式风格 | 唯一块式 if、// 行注释、显式 Flow 边界；拒绝旧语法的回归测试 |
| range/Map/fold | 半开区间、数量上限、稳定 Map、顺序归约、空输入、错误索引和取消测试 |
| 可组合推导式 | 纯 source 表达式、调用参数、fn、多层嵌套；变量遮蔽、惰性分支、错误顺序、取消、增量复用与真实 Python/REPL 验证 |
| 统一集合回调 | map/filter/fold/scan/group_by/split_by/sort_by/any/all 内联纯 fn；捕获依赖、参数遮蔽、短路、类型/元素错误与 REPL 验证 |
| 纯递归 | 递归环结果类型验证、每次调用的 Context/256 深度检查；直接/间接及经 list.map 的递归 conformance |
| 34 个纯 list 操作 | internal/listops 共用签名，全部目录项正面测试；空输入、分组/排序稳定性、形状、取消、错误与不修改输入 |
| 19 个纯 string 操作 | internal/stringops 共用签名；Unicode、空项、字面匹配、解析、资源/错误与组合验证 |
| 明确失败与类型 | fail 不产值，内部 never 保留成功分支类型，Host 不可覆盖；编译/生成/惰性分支验证 |
| 统一资源约束 | 字符串边界/len/索引/+/*/库统一 UTF-8/16 MiB；Map/fold 处理前拒绝超量 |
| 嵌套组合与真实依赖 | callback 名不产生数据边；对象值与数据参数产生真实边；生成库的分组→映射→聚合通过三种调度路径 |
| Incremental 保持身份 | 纯列表节点未变化时复用，State/Tick 和现有取消/效果测试继续通过 |
| 独立观察 | inspect graph.v1 与 trace.v1；成功/执行失败/写入失败/输入失败及 stdout/退出码检查 |
| 忠实生成与清理 | 失败时执行 Worker defer；Host/Go 独立入口只生成明确报错路径，不残留不可达执行代码 |
| 可读可写可验证 | 21 节教程、14 份逐字核对源码、23 个实际结果/错误样例、完整宿主程序 |
| Go/Python/库自然组合 | 同一 Flow 的调用/native 值，三种调度一致；所有外部调用遵循相同纯性规则 |
| Python 数据完整性 | 非有限、键碰撞、无序 set 不隐式丢信息；失败转换回收新句柄 |
| AI 修复接口 | diagnostics.v1、位置/源行/hints/退出码实际 CLI 测试，仍检查首个错误 |
| 交互输入编辑 | 光标/历史/草稿恢复、中文/组合字符/emoji、删除/取消/多行粘贴和补全回归 |
| 导入名称归属 | import 与 Python as；保留真实模块名，别名冲突、点分模块、AST 不变与 REPL 持续声明验证 |
| 诊断与显示一致性 | 参数数量独立分类，拼写建议与具体类型/回调修法；print/str/REPL 使用相同核心值格式 |

list 标准库不是新增控制流或 Host 操作集合；纯调用由生成代码直接分派，不能
被宿主同名 operation 重定义。list.map 和嵌入表达式的推导式顺序执行，独立
Map 推导式保留 Runtime 展开和并行能力。大区间、大组合结果、非矩形转置和过深递归均明确失败。

## 收敛决定

- 版本、规范入口、README、教程与示例使用同一套现行规则。
- Python adapter 统一使用 PythonWorker/PythonWorkerConfig，配置入口为 Python/Script。
- Runtime 节点效果统一使用 Effect，Map 从节点和 Host 注册获取效果分类。
- 所有 CLI 工具选项使用长形式并位于入口文件前，文件后全部是程序输入。
- 删除旧语法解析、迁移入口、隐式源码生成与过期规范，版本历史集中在 CHANGELOG。
- 新的数据操作进入独立纯标准库和统一目录，语法不为每个列表操作增加关键字。
- 普通循环、通用函数值/闭包、泛型 iterator 与新后端延后；内联集合回调已支持不可变捕获。
- 字符串索引改为 Unicode 字符字符串，与 len 一致；这是显式记录的 Alpha 语义变更。

## 验证入口

```bash
bash scripts/verify-release.sh
```

脚本执行 tests/race/vet/build、全部示例与 conformance 的检查/Go 生成/构建/vet，
以及 core/range/tree/lists 的独立运行验收。最新结果见 [RELEASE.md](../RELEASE.md)。
0.6.0 完整验收为 33 份源程序；0.6.1 最初扩至 47 份，现为 51 份。
最新完整执行结果记录在 RELEASE：2026-10-08 tests/race/vet/build 与 51 份源程序全部通过。
额外 compiler fuzz 含嵌套推导式与内联回调 seed，设置 10 秒、2 worker，通过 75,461 次执行，
检查随机输入无崩溃、接受的程序能生成可格式化 Go。

实际修正：字符串 * 仅防整数溢出仍可巨量分配；Map/fold 缺输入上限；文件
读取误注册 Pure；Python 非有限静默变 null、键碰撞覆盖、set 无序变 list、
失败转换泄漏句柄；测试把 Worker 启动故障当缺环境跳过。修正都有失败或
组合验证，没有减少检查来掩盖问题。

list/object 在边界检查外层形状，动态元素在运算时检查。Host 通过 Context
协作取消并保护共享资源，外部写入完成后保留其结果。

生成代码的 vet 曾发现 Host/Go 入口提前返回后的不可达分支，现已通过生成器
结构修正，而不是跳过 vet。Python 科学包缺失时对应环境测试明确跳过；独立核心
不以科学包为前提。
