# 从 Hello World 到 Flow 组合、并行与外部调用

[English](README.en.md) | [简体中文](README.md)

所有示例都使用 `Main` 作为默认入口，辅助 Flow 按职责命名。

这些可执行示例逐步展开 LIP 1.2 的同一计算模型：用纯函数计算值，用 Flow 组织依赖，用 Claim 汇聚事实，用 effect / perform 执行外部动作。`#` 开始行注释，每个文件都用英文注释在代码旁解释语法。先在项目根目录运行 `make build`；示例可由解释器运行，也可生成 Go 程序。

第一次接触可运行 `./lipc learn`，跟随 40 课与 80 个小例子填写和验证代码；课程还包括 `@{...}` 原生纯性承诺以及 `nan`/`inf` 的最新数值写法。随时用 `./lipc repl` 试算表达式。想先看组合能力，可直接阅读 [26_flows](advanced/26_flows.lip)，再回到基础语法。REPL 的 `:run 文件路径` 执行完整 Flow；需要选择其他入口时使用 `-entry`，输入用 `-input` 或 `-entry-input`。

REPL 支持表达式、语句与声明，Flow 可跨 cell 调用和用作管道阶段。输入完整脚本或前向声明时，使用 `:begin` / `:end` 作为一个 cell 提交；`-project DIR` 指定静态导入根目录。

[30_native_purity](ecosystem/30_native_purity.lip) 展示普通外层 fn 调用带标记的内层原生函数，以及 NaN/无穷的处理。运行 `./lipc run examples/ecosystem/30_native_purity.lip` 即可，只需 Python 3 标准库 math。`@{...}` 是作者对计算纯性的承诺。示例用 `str` 显示 `[3,nan,4,inf]`；直接返回非有限数值时选择 `-data-encoding c-hex`，默认 JSON 继续严格拒绝非有限数。

## 先记住五条规则

1. `flow Name(...) -> Type { ... }` 声明可复用的图；默认选择根模块的 `Main`；没有 `Main` 时选择唯一根 Flow，多个时用 `-entry Name`。显式入口覆盖默认，名称区分大小写。有值的 Flow 由末尾的 `return` 给出结果，void 没有返回值。
2. `name = value` 创建不可变绑定。同一作用域不能给这个名字重复赋值。
3. `fn` 保持计算结果纯性，允许 print/log/telemetry 单向诊断；函数体和值块的末尾表达式是结果。函数尾部可以加 `return`。Flow 不是函数值；子 Flow 与管道用于组合图，同一 run 的参数和成功结果可为任意普通值，包括函数与 effect。
4. `effect op(args)` 只构造请求，`perform` 才执行已经注册的操作。
5. `claim()` 创建事实汇聚点，`<+` 追加贡献；完整读取得到带身份的记录列表。

返回 string、number、list 或 object 都会被 CLI 编码为 JSON。控制块使用大括号，不依赖缩进。`any` 允许动态值，实际使用时仍要满足操作的类型要求。

## 基础语法

运行方式：`./lipc run examples/basics/文件名.lip`。

| 文件 | 学习内容 | 关键结果 |
| --- | --- | --- |
| [01_hello](basics/01_hello.lip) | Flow 入口、返回注解、最终结果 | `"Hello, LIP!"` |
| [02_bindings](basics/02_bindings.lip) | 不可变绑定，用新名字表达新结果 | `subtotal=36, total=31` |
| [03_arithmetic](basics/03_arithmetic.lip) | 优先级、幂、对数、除法、整商、取模、逻辑运算 | `2**3=8, 8*/2=3` |
| [04_containers](basics/04_containers.lip) | list/object、负索引、索引列表、切片、has/keys/put/concat | 原对象 role 仍为 `reader` |
| [05_strings_numbers](basics/05_strings_numbers.lip) | Unicode、slice/split、数字解析、string 排序 | `score=0.8, middle="𐀀"` |
| [22_multiline_strings](basics/22_multiline_strings.lip) | 三引号、缩进、转义与物理换行 | HTML 与 prompt 均为普通 string |

`**` 是幂，右结合：`2 ** 3 ** 2 = 512`。一元负号在幂之后：`-3 ** 2 = -9`，对负数整体求平方写 `(-3) ** 2 = 9`；负指数写 `2 ** -3 = 0.125`。`*/` 以右边为底：`8 */ 2 = 3`，它与乘除同级、左结合。

`number` 不分整数和小数类型，`3` 与 `3.0` 相同，JSON 输出为 `3`。`3 / 0` 为 `inf`，`0 / 0` 为 `nan`，`222 ** 8888` 溢出为 `inf`，`nan * inf` 为 `nan`；`0 ** -1` 为 `inf`。对数真数可为 0、底数必须大于 0 且不能为 1；非法定义域和负底数非整数幂得到 `nan`，零值与溢出可得到带符号 `inf`。幂和对数是固定算法的近似结果。

`learn -lesson power` 与 `learn -lesson logarithm` 提供这两项练习。REPL 和 learn 都可用左右方向键修改输入，单行用上下键取回历史，多行用上下键移动；Ctrl-P/N 随时取回历史，Alt-Enter 换行，Tab 补全、Ctrl-R 搜索历史；完整快捷键见 [README](../README.md#交互计算和学习)。

REPL 也会显示库函数的返回值。`fmt.Print/Printf/Println` 的返回值包含写入字节数，因此 `fmt.Print("hello")` 显示为 `hello5`；它不是额外的打印动作。完整说明见 [README](../README.md#为什么-fmtprint-后面会显示数字)。

## 程序结构

运行方式：`./lipc run examples/structures/文件名.lip`。

| 文件 | 学习内容 | 关键结果 |
| --- | --- | --- |
| [06_conditions](structures/06_conditions.lip) | if 表达式、局部绑定、短路求值 | `grade="ready", safe=false` |
| [07_match](structures/07_match.lip) | 字面量/类型/通配模式与 guard | `["empty","nonnegative","other","note","other"]` |
| [08_functions_closures](structures/08_functions_closures.lip) | 函数值、闭包、命名参数 | `mapped=[11,12,13], product=20` |
| [09_map_fold](structures/09_map_fold.lip) | 纯回调、有限列表转换与有序归约 | `total=30` |
| [10_comprehensions](structures/10_comprehensions.lip) | 推导式、过滤与 guard 先于结果求值 | `reciprocals=[0.5,0.25]` |
| [11_loops](structures/11_loops.lip) | 普通 for、break/continue、Claim 完整读取 | `[1,3]` |
| [12_carry](structures/12_carry.lip) | 有结果的迭代、next/break、列表追加与对象更新 | `early=6, complete=6`，两批工具与名称索引，原列表仍为 1 项 |

普通 for 内的绑定每轮独立。需要携带上一轮结果时用 carry；需要收集独立贡献时用 Claim。普通循环的 `break` 与 carry 的 `break value` 各自作用于自己的构造。

`for ... with s = init` 可以持续构造容器：每轮用 `concat(s.tools, [tool])` 追加列表，用 `put(s.by_name, name, tool)` 增加或覆盖对象字段，再用 `next e` 携带表达式 `e` 作为整个新状态；`break e` 返回最终值。结果还能作为下一批循环的初始状态；空输入保留初始值，旧容器保持不变。在 Flow 中可先 `value = perform request`，再将 value 加入状态；`next` 本身保持纯计算。

## 高级计算与并发

| 文件 | 学习内容 | 运行方式或结果 |
| --- | --- | --- |
| [13_tail_calls](advanced/13_tail_calls.lip) | 十万次直接、互递归、间接尾调用 | `sum=5000050000, even=true, indirect=0` |
| [14_parallel_claim](advanced/14_parallel_claim.lip) | parallel、逻辑位置、事实投影 | `-parallel 4`；结果按输入顺序排列 |
| [15_inputs](advanced/15_inputs.lip) | 类型化 Flow 参数与 JSON 输入 | 使用下方带 `-input` 的命令 |
| [16_snapshots](advanced/16_snapshots.lip) | 即时观察与完整读取的区别 | 完整数为 3，即时观察数允许 0–3 |
| [21_business_failures](advanced/21_business_failures.lip) | 用 Data 表达失败、第三次失败停止 | `failures=3, visited=4` |
| [27_linear_model](advanced/27_linear_model.lip) | 中心化最小二乘、训练/预测 Flow、MSE、奇异输入 | 默认 Main; slope=2, intercept=1, predictions=[9,11] |
| [29_diagnostics](advanced/29_diagnostics.lip) | fn 中的 print/log/telemetry 单向诊断 | 结果 `6`；诊断到 stderr，`-trace=false` 关闭投递 |
| [refine](refine.lip) | 无 source 的 carry，迭代改进直到阈值 | `rounds=4, score=0.8` |
| [evidence](evidence.lip) | 并行证据汇聚、完整报告 | `missing=["b","c"]` |

```sh
./lipc run examples/advanced/13_tail_calls.lip
./lipc run -parallel 4 examples/advanced/14_parallel_claim.lip
./lipc run -input '{"name":"Ada","scores":[0.6,0.9]}' examples/advanced/15_inputs.lip
./lipc run -parallel 4 examples/advanced/16_snapshots.lip
```

live snapshot 的观察差异是语义的一部分。需要最终完整结论时直接读取 Claim，不要用即时观察猜测所有任务已经完成。`fail(...)` 是不可捕获的程序错误；可恢复业务失败需要显式的 `{ok:false,...}` 数据。

## 声明、入口与复用

[26_flows](advanced/26_flows.lip) 将静态 Data、私有纯函数、导出的子 Flow、管道和并行 Claim 放在同一文件中：

```sh
./lipc run -input '{"xs":[3,1,2]}' examples/advanced/26_flows.lip
# {"count":3,"values":[5,3,4]}
./lipc run -entry Adjust -entry-input '{"positional":[3],"named":{}}' -entry-output examples/advanced/26_flows.lip
# {"kind":"value","value":5}
```

`x |> Adjust` 先固定 x，再完整完成子 Flow；具名纯函数和核心函数也能作为阶段。Flow 显式实参按源码顺序求值，允许嵌套调用，也可传递函数或 effect。原生库函数与普通函数值使用普通调用。`OFFSET:number=2` 是静态字面量，`export` 使其可被其他模块读取。入口 JSON 的重复键、非法 Unicode、格式或参数不符均在运行前以 E-SCHEMA 拒绝。

将下面的 `reuse.lip` 放在项目根目录，便可复用同一示例，无需复制实现：

```lip
import lip "./examples/advanced/26_flows.lip" as adjust
flow Main(xs:list)->list {return [x |> adjust.Adjust for x in xs]}
```

`./lipc run -input '{"xs":[3,1,2]}' reuse.lip` 得到 `[5,3,4]`。模块只能访问 export；私有 `add` 不可访问。编译先冻结完整依赖和版本，运行与生成程序不会重新读取模块。`-project` 可显式指定项目根；所有声明都要合法，不能靠选择另一个入口绕过检查。

`learn -lesson entries`、`flows`、`modules` 分别练习入口、组合和模块；课程依赖已内置。REPL 中可输入 `:run -input '{"xs":[3,1,2]}' examples/advanced/26_flows.lip`，文件运行独立于会话绑定。

## Go / Python 生态

下面的例子使用 `-host demo`，显式启用项目的教学合约。Python 例子需要 Python 3，未涉及额外 package 安装。

| 文件 | 学习内容 | 结果 |
| --- | --- | --- |
| [17_go_python](ecosystem/17_go_python.lip) | 在同一 Flow 中调用两个生态 | Python 均值为 2，Go 返回 SHA-256 字符串 |
| [18_proposals](ecosystem/18_proposals.lip) | 纯函数构造/传递请求、请求链、不同结果类型 | `first="HELLO", second="HELLO, LIP", average=2` |
| [19_parallel_read_write](ecosystem/19_parallel_read_write.lip) | 保留函数返回摘要的并行 ReadOnly、完整收集、顺序 Write | `["ADA","LIN","SAM"]`；日志写 stderr |
| [20_recorded_iteration](ecosystem/20_recorded_iteration.lip) | 记录结果进入下一轮外部请求 | `rounds=4, score=0.8` |
| [21_summary_recovery](ecosystem/21_summary_recovery.lip) | 闭合容器、条件/返回函数和递归参数保留请求摘要 | 每个名字得到四个大写结果 |

```sh
./lipc run -host demo examples/ecosystem/17_go_python.lip
./lipc run -host demo examples/ecosystem/18_proposals.lip
./lipc run -host demo -parallel 4 examples/ecosystem/19_parallel_read_write.lip
./lipc run -host demo examples/ecosystem/20_recorded_iteration.lip
./lipc run -host demo -parallel 4 examples/ecosystem/21_summary_recovery.lip
```

这些教学示例的显式操作合约、schema 和 bytes 编码集中在 [适配器与教学 Host](../script/adapters.go) 的 `DemoHost` 中。CLI 默认还提供通用 Go/Python 桥接，可以直接调用导入库的函数、读取模块属性和操作原生对象，例如 `import go "strings"; strings.ToUpper("hello")` 或 `import python "math"; math.sqrt(3)`。直接调用和属性访问会降为 `perform effect`；数组等原生对象以资源引用保留，使用 `native_data(value)` 显式取得普通数据快照。通用配置、转换规则和运行条件见 [README](../README.md#接入-go--python)。

读 effect 示例时，只需分清三件事：

1. `upper_request("hello")` 得到请求，没有调用外部库。`perform request` 才执行，并返回合约规定的 Data。
2. `18_proposals` 的 `forward(request:effect)->effect` 展示 fn 参数与返回值能传请求；Prepare/Execute 展示 Flow 返回和接收请求。Host 参数/结果、入口协议和 Claim 仍要求 Data。
3. `19_parallel_read_write` 直接执行已知构造函数的结果，保留并行资格；固定字段、下标和切片投影也保留已知只读请求。`21_summary_recovery` 进一步展示闭合容器、条件/返回函数和递归 effect 参数的转发。真正未知的形参或开放来源仍可顺序执行，但不能证明并行安全。

项目自定义操作可用普通 `policy` 参数携带 token、金额与时限等选项，具体限制由 Host 实施；权限需求由合约声明并由 Host 授予。短例子与当前边界见 [README 的 effect 说明](../README.md#所有外部动作都走同一条边界)。

[28_flow_values](advanced/28_flow_values.lip) 是纯核心的小例子：Offset 返回捕获 delta 的函数，Apply 接收并调用它，同时展示嵌套实参和管道命名参数。Flow 本身始终不是值。

## 本地 Ollama 聊天、记录与提醒

[tmp_ollama_chat.lip](tmp_ollama_chat.lip) 在一个 LIP 文件内实现多轮流式聊天、工具参数检查、计算记录和提醒。模型选择工具，LIP 按明确的工具名执行，再把结果传回模型继续决策。模型接入使用官方 Python `ollama` 客户端，终端编辑使用 `prompt_toolkit`，存储与计时直接调用标准库。

1.2 将模型和配置捕获在一个纯请求构造函数中，传给 `RunAgent`；每轮由 `StreamReply(request(s.messages, tools), terminal)` 接收 effect 并执行。模型在启动时选定，替换构造函数可接入兼容 Ollama 分块接口的实现。`Query(request:effect)` 返回行列表，`Write(request:effect)` 返回影响行数；请求须产生 SQLite 游标，两个 Flow 都负责关闭和释放它。SQL 仍使用独立参数。动态请求结果的原生属性使用 `py.getattr(cursor, "rowcount")`，方法使用显式 `effect cursor.method(...)`；已知请求则可沿有限描述穿过函数、容器和 Flow 返回值。

工具定义用纯 `Tool(name, description, properties, required)` 统一结构。此应用选择由 `BuildTools()` 在启动时构造一次；也可用 `concat(base, [Tool(...)])` 追加，再由 carry 将新列表带入下一轮，见 [12_carry](structures/12_carry.lip)。按条件逐项构造也可使用推导式。需要随对话更新时，将工具列表与 messages 一起放入 carry 状态。`Tool` 只生成模型看到的 schema，新增工具仍须同步业务校验和 `DispatchTool` 分派。顶层静态声明只接受字面量，构造函数应在 fn/Flow 内调用。

参数校验中的 `strings.TrimSpace` 使用 `fn TrimSpace(...) @{...}` 表达确定性的原生计算；`@{...}` 是作者对计算纯性的显式承诺，实际工具请求仍由 Flow 中的 `perform` 执行。

业务逻辑集中在一个文件中：`Main` 负责启动、会话与关闭，`OpenModel` 返回包含请求闭包的普通对象，`Conversation` 管理输入循环，`RunCommand` 处理命令，`ChatTurn` 准备上下文，`RunTools` 反馈观察。流式代码分成原生分块解码、纯 `MergeChunk` 累积和终端显示。工具构造复用纯函数；请求函数、effect 和嵌套容器可以跨 Flow 传递，有限描述让已知只读请求保留并行资格，动态原生属性仍通过显式调用取得。

先在 `lipc` 使用的 Python 3.9+ 环境中安装客户端，并启动本地 Ollama、下载支持工具调用的模型。以下命令从项目根目录运行，默认模型为 `llama3.1:8b`：

```sh
python3 -m pip install 'ollama>=0.6.3' 'prompt_toolkit>=3.0'
ollama pull llama3.1:8b
make build
LIP_CHAT_TZ=Asia/Taipei ./lipc run examples/tmp_ollama_chat.lip
```

已安装 `lipc` 时，也可以从上级目录运行 `~/go/bin/lipc run ./lipv1/examples/tmp_ollama_chat.lip`。程序直接使用这一个 `.lip` 文件，无需其他业务源码文件或 `PYTHONPATH` 配置。`LIP_CHAT_MODEL` 可覆盖默认模型；服务地址和超时在文件顶部配置。时间默认跟随系统本地时区，可通过 `LIP_CHAT_TZ` 指定 IANA 时区（例如 `Asia/Taipei`、`Asia/Shanghai`）；启动时显示实际日期和 UTC 偏移。

可以输入“计算 123 * 456，备注采购预算”“查询采购预算的历史计算”“10 秒后提醒我喝水”“查看待办提醒”“取消编号为 1 的提醒”“从现在再过 31 小时 52 分钟是几点”。成功的计算自动保存操作数、结果、备注和时间；时间偏移使用专用工具，返回含日期、时区的本地时间。提醒支持倒计时或带时区的 RFC 3339 时间。

- `/time`、`/after 31h52m`（也支持 `24m`、`-1h`、`30s`）直接计算时间，无需等待模型。
- `/calculations`、`/reminders` 直接读取最近记录；`/new` 清空聊天上下文，保留计算和提醒；`/help` 显示帮助。
- 终端支持中文退格、Delete、方向键、历史上下翻阅、Ctrl-R 搜索、Ctrl-A/E 行首/尾、Ctrl-U 删除至行首和整段粘贴。Ctrl-C 丢弃当前输入，Ctrl-D 或 `/exit` 退出；缺少 `prompt_toolkit` 时回退为逐行输入。管道输入仍按普通文本读取。
- 工具默认输出简短结果，`LIP_CHAT_DEBUG=1` 显示完整参数和 JSON 结果。明确的身份询问和编辑/速度反馈不提供业务工具。

SQLite 数据库默认保存在**当前工作目录**的 `.ollama_chat/state.sqlite3`，首次运行自动创建；环境变量 `LIP_CHAT_DB` 可指定路径。换目录运行时如需复用记录，应将它设为同一个绝对路径。提醒在等待输入或模型生成时也会主动显示，编辑中的草稿会保留；程序退出期间不会通知，重启读取同一个数据库时会补发到期提醒。聊天上下文保留最近 12 个完整用户轮次，并按约 24000 字符预算裁剪完整轮次，至少保留最新轮次；重启不恢复聊天，但可通过工具查询保存的记录。每次输入最多请求模型 8 轮，参数错误会反馈给模型修正。模型请求使用低温度、1024 token 输出上限和 30 分钟驻留设置，终端显示等待提示并流式输出正文；实际速度取决于模型和硬件。连接、数据库等运行错误仍会终止程序。

## 网站入口

这些入口通过 HTTP Host 接收 request 并返回响应对象：

| 文件 | 路由 | 能力 |
| --- | --- | --- |
| [23_home](web/23_home.lip) | GET /{$} | 三引号 HTML 页面 |
| [24_echo](web/24_echo.lip) | POST /api/echo | 严格 JSON 输入与重复查询值 |
| [25_greeting](web/25_greeting.lip) | GET /api/hello/{name} | 路径参数与 JSON 响应 |

```sh
./lipc serve examples/web/23_home.lip
./lipc serve -route 'GET /api/hello/{name}' examples/web/25_greeting.lip
```

完整请求/响应约定、多路由清单、执行预算及当前范围见 [README](../README.md#网站与-http-接入)。
HTTP 入口不能使用会话级通用 bridge；外部操作由显式 Host 合约提供。

## 同一个例子编译执行

```sh
make compile SOURCE=examples/advanced/13_tail_calls.lip OUTPUT=.build/tail
./.build/tail
make compile SOURCE=examples/ecosystem/17_go_python.lip HOST=demo OUTPUT=.build/interop
./.build/interop
make compile SOURCE=examples/advanced/26_flows.lip ENTRY=Main OUTPUT=.build/flows
./.build/flows -input '{"xs":[3,1,2]}'
```

生成程序固定入口和模块快照，不再接收 `.lip` 文件；直接接受 `-input` / `-entry-input` / `-entry-output` / `-parallel` 等运行参数。`make compile` 可传 ENTRY、PROJECT 和 HOST；输入与输出封装规则见 [README](../README.md#语言核心与程序结构)。

`make examples` 会输出各个示例的验收结果；`make clean` 删除编译产物与缓存。

`a[-1]` 取末项，`a[[2,0,-1]]` 按列表顺序取值，`a[1::2]` 间隔取值，`a[::-1]` 反转。切片边界裁剪；字符串按 Unicode 标量切片。`learn -lesson slicing` 用非空与空输入练习这些操作。
