# 0.6.4 本地发布验收清单

当前契约：[ALPHA-0.6-SPEC.md](docs/ALPHA-0.6-SPEC.md) 与其规范组成部分
[LIST-LIBRARY.md](docs/LIST-LIBRARY.md) 与 [STRING-LIBRARY.md](docs/STRING-LIBRARY.md)。
参考实现版本为 `0.6.4`，变更见 [CHANGELOG.md](CHANGELOG.md#064--consistent-collection-apis-and-bounded-infinite-loops)。
这是本地实现与验收记录。源码包、工具包和 SHA-256 清单由本地打包脚本生成；
远程发布与 Git 版本标签是独立操作，尚未执行。

## 验证入口

在 Go 1.27 环境执行：

```bash
bash scripts/verify-release.sh
```

脚本验证 tests、race、vet、全部 Go 包构建、版本一致性、全部嵌套 `.lip` 示例
与 conformance 的检查/Go 生成/build/vet，以及 core/range/tree/lists/math/for/lifetimes/可空
参数的独立运行。生成库测试验证输入输出、纯函数组合、递归、回调、缓存与错误边界。
所有生成检查写临时目录，结束时清理，不改写仓库示例。
安装验收将编译器和示例放到仓库外，检查离线 run/build、无 Go 的核心可执行
文件、无模块和无关 Go 模块目录，以及 Bash/Zsh 直接路径调用。
相同源码在两个不同临时模块中构建的二进制逐字相同，且均实际执行验证。

自动一致性检查覆盖：

- VERSION、Runtime、CLI、生成文件版本与全部当前文档；0.6.4 规范、README、教程和课程使用现行规则。
- 全部文档 LIP 代码块的检查与生成格式；表达式和练习模板使用明确的包装规则。
- 预期失败代码及其真实诊断 JSON；标准库目录、课程编号/ID 和本地链接。
- 47 个文档 LIP 代码块、19 处标注源码与 `.lip` 文件核对；全部 12 份生成示例与当前编译器逐字核对。
- 26 节课程的示例/解答实际执行；教程包含 27 个实际 CLI 结果/错误用例。
- 编辑器词库逐字检查；可用 Vim/Neovim/Emacs 的原生插件实际加载、缩进、补全和 JSON 诊断检查。

三编辑器完整验收另运行 `python3 scripts/verify-editors.py --require-all`，要求
Vim 9.0+、Neovim 0.10+ 与 Emacs 27.1+。主脚本明确记录缺少的编辑器；
三者均缺失时仍检查生成词库，并明确跳过原生测试。

## 本地发行包

Python 3.11+ 与 Go 1.27+ 可以生成本机发行包：

```bash
python3 scripts/package-release.py
python3 scripts/verify-package.py
```

产物位于 `dist/`：`lip-0.6.4-source.tar.gz`、当前平台工具包、
`lip-0.6.4-manifest.json` 和 `lip-0.6.4-SHA256SUMS`。工具包内含编译器、课程、
Runtime、规范、示例与三套编辑器插件，安装见 [EDITORS.md](docs/EDITORS.md)；
源码包含实现、测试和验证脚本。manifest 记录源码与产物
SHA-256、Go 工具链和目标平台；验证脚本检查完整性并在仓库外离线 run/build。
它检查构建输入与本地包的一致性，不提供发布者签名。

可指定输出目录和重复目标平台，例如：

```bash
python3 scripts/package-release.py --output dist --target linux/amd64 --target windows/amd64
```

Windows 工具包采用 zip，其他平台和源码使用 tar.gz。相同源码、Go 工具链和
构建环境产生一致字节；tar 时间戳默认 0，可通过 SOURCE_DATE_EPOCH 指定，
zip 使用固定的 1980 时间戳。工具包不包含 Git、个人配置、缓存或其他构建产物。
只含交叉目标的包会检查完整性，但原生执行须在目标平台验收。
`check/inspect` 无需 Go；`run/build/repl/learn` 的代码执行需要 Go；构建好的
核心程序独立运行。Host/Go 适配器需在宿主注册，Python 能力另需对应环境。

## 0.6.4 验收（2026-10-09）

- [x] 完整 verify-release.sh：tests/race/vet/build、安装模拟与 56 份 LIP 源程序的 check/生成/build/vet。
- [x] 值生命周期、异步/跳过/别名/失败/取消、State/纯缓存与生成组合的回归和 race 检查。
- [x] 16 MiB 缓冲区的执行中存活堆、标量链分配与计数成本基准，详见 [VALUE-LIFETIMES.md](docs/VALUE-LIFETIMES.md)。
- [x] 规范、教程、课程、版本、生命周期示例与全部 12 份生成文件同步检查。
- [x] Go 定义标量、map 索引/Unicode 字段、空语句、别名调用形式、未使用导入与 Python 控制操作的回归通过；生成组合另以 GOFLAGS=-race 执行。
- [x] 编译器 fuzz：10 秒设置、2 worker、121,843 次执行通过，包含空语句和同名别名 seed。
- [x] 本地源码包及 linux/amd64、windows/amd64、darwin/arm64 工具包交叉构建；同一输入重复打包的 SHA-256 清单逐字相同。
- [x] SHA-256 与源码清单校验、Linux 工具包解压后仓库外离线 run/build，以及构建产物无 Go 运行通过；Windows/macOS 已交叉构建，未在目标系统执行。
- [x] 本轮编辑器扩展：完整发行检查与三套原生编辑器验收通过（Vim 9.2、Neovim 0.12.5、Emacs 31.1）；含插件的三平台包重新校验通过，Linux 离线 run/build 通过。
- [ ] 在远程发布仓库创建版本标签并上传经校验的发行产物（独立发布操作）。

## 0.6.2 验收（2026-10-09）

- [x] 完整 verify-release.sh：tests/race/vet/build、安装模拟与 55 份 LIP 源程序的 check/生成/build/vet。
- [x] 文档、规范、课程、版本与 12 份生成文件的自动同步检查通过。
- [x] match、八种算术、for/break/continue、三种 import/as、词法遮蔽、可空边界、外部表达式效果、State/Retry/Feedback 通过回归。
- [x] REPL 和 learn 的编辑、历史、补全、粘贴、失败恢复、课程进度和导出通过回归。
- [x] 36 个 list 与 19 个 string 操作、纯递归、Host/Python 及 blob/句柄边界通过回归。
- [ ] 在发布仓库创建并核对版本标签与发布产物（独立发布操作）。

## 先前验收记录

0.6.1 初始完整验收覆盖 51 份 LIP 源程序。随后的语言扩展完整验收覆盖 55 份，
新增审计生成程序还在 GOFLAGS=-race 下验证调度、Await、错误停止、惰性分支、
Tick 缓存、只读重读与 State 初始化。编译器短时 fuzz 通过 157,376 次执行
（10 秒设置、2 worker，包含 match/for/可空类型/外部组合/别名 seed）；这是随机
无崩溃检查，不是正确性证明。版本行为历史以 CHANGELOG 为准。

## 可手工运行的入口

在仓库根目录安装，安装位置跟随现有 Go 配置。文中的 `lipc` 可用 PATH 中的
命令名或实际安装路径调用，见[快速入门](docs/QUICKSTART.md#安装)：

```bash
go install ./cmd/lipc
lipc learn
lipc run examples/core.lip '[1,2,3]'
lipc run examples/math.lip
lipc run examples/for.lip 8
lipc run examples/tutorial/12_optional.lip 3 '"null"'
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
