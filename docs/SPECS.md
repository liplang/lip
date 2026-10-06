# LIP 规范索引

这些文档定义各个 Alpha 里程碑的语言与 Runtime 边界。后一个版本继承前一个
版本的行为，并在文档中标明新增规则。

| 规范 | 主要边界 |
| --- | --- |
| [Alpha 0.1](ALPHA-0.1-SPEC.md) | 基础语法、依赖图、Host 边界与确定性调度 |
| [Alpha 0.2](ALPHA-0.2-SPEC.md) | Dynamic Map 与有界并行 |
| [Alpha 0.3](ALPHA-0.3-SPEC.md) | 持久 Instance、State、Logical Tick 与增量重算 |
| [Alpha 0.4](ALPHA-0.4-SPEC.md) | Retry、Feedback、取消、Effect/Ordering、严格输入与依赖声明 |
| [Alpha 0.5（设计中）](ALPHA-0.5-SPEC.md) | 完整程序契约、`require` 依赖头、显式输入/输出、忠实翻译与 Python 边界 |

当前参考实现版本为 `0.4.0`，版本源文件见仓库根目录的 [VERSION](../VERSION)。
发布检查项见 [RELEASE.md](../RELEASE.md)，版本变更见
[CHANGELOG.md](../CHANGELOG.md)。
