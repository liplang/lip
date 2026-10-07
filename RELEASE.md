# 0.6.1 发布清单

当前契约：[ALPHA-0.6-SPEC.md](docs/ALPHA-0.6-SPEC.md) 与其规范组成部分
[LIST-LIBRARY.md](docs/LIST-LIBRARY.md) 与 [STRING-LIBRARY.md](docs/STRING-LIBRARY.md)。
参考实现版本为 `0.6.1`。

## 本地发布验收

在 Go 1.27 环境执行：

```bash
bash scripts/verify-release.sh
```

脚本验证 tests、race、vet、全部 Go 包构建、版本一致性、全部嵌套 `.lip` 示例
与 conformance 检查/生成/编译，以及无 adapter 的 core/range/tree/lists 程序。
生成库的输入输出、纯函数组合、递归、list callback、缓存与错误边界由测试覆盖。
所有生成检查写临时目录，结束时清理，不改写仓库示例。
安装验收将编译器和示例放到仓库外，检查离线 run/build、无 Go 的核心可执行
文件、无模块和无关 Go 模块目录，以及 Bash/Zsh 直接路径调用。

- [x] 完整 0.6.1 验证脚本通过（2026-10-07，47 份 LIP 源程序，包含每份生成 Go 的 vet）。
- [x] VERSION 和 lipc version 同为 0.6.1。
- [x] 中文/英文 README、规范索引、路线图和当前教程一致，旧规范合并至当前入口。
- [x] // 注释、块式 if、长选项与文件参数边界统一；旧语法、迁移和运行库别名已移除。
- [x] 34 个 list 目录项均有语义测试，已有 adapter 测试无回归。
- [x] 19 个 string 目录项有语义、错误、Unicode、数量验证；fail 保留成功分支类型。
- [x] 教程源码与文件一致，23 个正常/空/失败样例实际编译运行；完整 State/策略宿主例子。
- [x] Go→纯库→Python→纯库混合例子三种调度一致，含空输入与错误。
- [x] 编译器短时 fuzz 通过（2 worker，10 秒设置，240,582 次执行）；不是正确性证明。
- [x] 已跟踪的生成便利输出与当前编译器一致。
- [ ] 在发布仓库创建并核对版本标签与发布产物（独立发布操作，未由本地测试代替）。

## 可手工玩用的入口

在仓库根目录安装，安装位置跟随现有 Go 配置。文中的 `lipc` 可用 PATH 中的
命令名或实际安装路径调用，见[快速入门](docs/QUICKSTART.md#安装)：

```bash
go install ./cmd/lipc
lipc run examples/core.lip '[1,2,3]'
lipc run examples/range.lip 5
lipc run examples/tree.lip '{"value":1,"left":null,"right":null}'
lipc run examples/lists.lip '[{"department":"A","amount":3},{"department":"B","amount":2}]'
lipc run examples/strings.lip ' Rust, LIP, rust, ,你好 '
lipc check --json examples/strings.lip
go run -buildvcs=false ./examples/tutorial/host_demo
go run -buildvcs=false ./examples/tutorial/mixed_demo
lipc inspect examples/core.lip
lipc run --trace core-trace.json examples/core.lip '[1,2,3]'
```

Python 科学计算示例：

```bash
go run ./examples/python
```

本例需要相应 Python 包。测试按环境执行，缺少解释器或科学包时记录跳过。

## 执行规则

list/Map/fold 处理有界数据，递归支持取消与 256 层调用深度。字符串使用有效
UTF-8，长度最多 16 MiB；Map/fold 输入最多 1,000,000 项，物化列表还有元素槽位
上限。Python 非有限数值和冲突对象键报告错误，无序集合通过显式排序转成列表。

Host/Go 程序通过生成库接入，Python 使用常驻 Worker。跟踪的生成示例随编译器
更新，验证仍从 LIP 源码开始。变更沿革见 [CHANGELOG.md](CHANGELOG.md)。
