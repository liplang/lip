# LIP 0.6：小而完备的核心

参考实现 `0.6.1`。本文统一定义当前语言与执行规则，标准库详见
[列表库](LIST-LIBRARY.md)与[字符串库](STRING-LIBRARY.md)。
验证入口见 [RELEASE.md](../RELEASE.md)。

## 1. 设计

LIP 以依赖关系描述计算，核心程序完成 **声明输入 → 构造数据 →
选择、变换与聚合 → 输出结果 → 观察执行**。Go Host 和 Python Worker
提供外部能力，共用同一套调用与值依赖。

| 层次 | 内容 | 用途 |
| --- | --- | --- |
| 语言核心 | Flow、纯 fn、单赋值、基本数据、if/when、Map、str/len/range/fold/fail、纯递归 | 能组合出独立的小程序 |
| 纯标准库 | 34 个 list.*、19 个 string.* 操作 | 无循环时方便处理实际数据，不新增语法 |
| 执行核心 | 有界调度、Host effect、取消、State/Tick、增量复用 | 保持 Logical / Incremental / Parallel 的身份 |
| 已有执行策略 | 有界 Retry/Feedback、Await、Host Ordering | 保留已有可验证能力，不添加新语法 |
| 外部适配器 | Go Host、现有 Python Worker | 外部能力有明确入口，不决定核心能否工作 |

风格参考 Rust 的 fn、显式边界、块式 if 和不可变绑定；区间采用 Python 的
半开规则。裸绑定直接命名计算结果，集合处理由 Map/fold 和纯标准库组合完成。

## 2. 文件、名字与类型

文件顺序为零个或多个 `import`、零个或多个 `fn`，然后唯一显式 `flow` 或一组
顶层执行语句，再到 EOF。顶层语句隐式组成 `flow main() { ... }`，没有参数或返回值；
与显式 Flow 使用相同的作用域、不可变绑定和依赖图规则。两种入口形式不能混写。
执行语句之间用换行或 `;` 分隔，同一行的多条语句必须使用 `;`，单独空格不构成
分隔符。块末和 EOF 前的最后一个分号可省略；fn 的结果表达式后也允许末尾分号。
分号不用于分隔函数参数、列表元素或对象字段；这些位置仍使用逗号，允许尾逗号。
行注释使用 `//`。13 个关键字为 `flow`、`fn`、`return`、`when`、`for`、`in`、
`if`、`else`、`import`、`as`、`true`、`false`、`null`。
依赖关键字只有 `import`；种类为 `host`、`go`、`python`，声明不安装包。
每个外部操作必须匹配 Host 声明或 Python 导入根；Go 声明不能替代 Host 注册。
重复声明、无效声明和未声明操作在 check 阶段失败。

Python 声明使用模块的真实导入名，支持点分路径和可选版本范围。
不写别名就保留原名；`import python "numpy" as np` 使 `np.mean(...)` 调用
`numpy.mean(...)`。别名只改本地名称，不改安装包名或 Worker 的模块策略。
例如 `sklearn` 是原始模块名，`scikit-learn` 是安装名，不做隐式映射。
`as` 只用于 Python 模块；别名不能与内置名字、其他模块别名、函数或局部绑定冲突。
显式导入同名 Python 模块时，调用归属于该模块；例如 `import python "string"`
之后的 `string.capwords(...)` 是 Python 调用。用 `import python "string" as text`
可以同时使用 `text.capwords(...)` 和 LIP 的 `string.trim(...)`，编译器不会暗中改名。

```lip
fn add(total: number, value: number) -> number { return total + value }

flow Report(values: list) -> object {
    doubled = [x * 2 for x in values]
    total = fold(doubled, 0, add)
    return {count: len(values), values: doubled, total: total}
}
```

边界类型固定为 `any`、`number`、`bool`、`string`、`list`、`object`。
`number` 是有限浮点数；`list` 是序列，`object` 是字符串键对象；集合类型只检查
外层形状，不引入泛型或 schema。集合元素中的动态错误仍在实际运算时失败。
string 边界要求有效 UTF-8，单字符串最多 16 MiB；字符串 +/*、len、索引与
string.* 遵循相同规则，扩张前检查数量，不容许巨量重复分配。
Go 边界接受 slice/array 作为 list，字符串键 map 作为 object；宿主必须把已
交给 Flow 的数据视为只读，不得在 Tick 之间原地修改缓存所引用的数据。

Flow 和具名 fn 的参数必须写类型；内联集合回调的参数类型可省略，默认 any。
有返回值的 Flow 必须写输出类型，恰好一个 return，return 后
不能有语句。没有返回值的 Flow 省略输出声明和 return，例如 `flow main() { print(79 / 134) }`，
也可显式写 `-> void`。void 只用于 Flow 输出，不能作为参数、fn 返回或 `void?`。
无返回值的 Flow 可在块末尾写一个裸 return；它不取消其他图节点或充当进程退出码。
顶层语句可省略整个 Flow 外壳，例如 `a = 2; b = 3; print(a / (a + b))` 输出 `0.4`。
fn 只含一个结果表达式，return 可省略，返回类型可以推导；例如
`fn square(x: number) { x * x }` 与写 return 等价。Flow 输出 `T?` 接受 T
或 null，并允许门控关闭时无值完成；参数和 fn 暂不支持 `?`。

绑定不可重复或前向引用，when 内绑定不能逃出作用域。fn 可调用其他纯 fn，
但不能隐含外部工作。直接或间接递归环内的每个函数必须显式声明返回类型，
不推导循环类型。每次本地函数调用检查取消；最大嵌套调用深度为 256，超出
产生普通运行错误。函数名不能覆盖内置操作；生成节点前缀保留。
Flow 的变量名不会改变调用名的解析。集合回调接受本地纯函数名或内联 fn，
不引入可保存、传给外部操作或作为 fn 返回结果的一等函数值。

## 3. 数据与表达式

值包括 null、bool、number、string、list、object。支持嵌套构造：

```lip
{name: "Ada", "two words": [1, null, {active: true}]}
```

对象键可以是普通标识符或字符串；关键字键需要引号。对象键唯一，重复键
在编译时报告。键不形成依赖，值中的引用形成依赖。对象和列表的
元素表达式按书写顺序计算，错误停止当前节点；没有可变对象或字段赋值语法。

读取使用 `value.field` 和 `value[index]`。列表索引必须是非负整数且在范围内；
字符串按 Unicode 字符索引并返回一个字符串，len 采用相同的字符计数；对象
索引必须是字符串，缺失键报错，不与显式 null 混同。Go struct 的字段读取保留
为 Host 互操作能力，但 struct 不自动成为 object 边界类型。

运算包括 `+ - * /`、`== != > >= < <=`、`&& ||` 和一元 `+ - !`。
数值不隐式与字符串转换；字符串支持连接及整数次数重复。条件必须是 bool，
`if condition { a } else { b }` 只计算所选表达式，每个块写一个值表达式。
`&&`/`||` 短路，`!` 只接受 bool。静态已知的类型矛盾在 check 失败；动态值在运行时检查。
除 null 比较外，静态已知的跨类型相等比较在 check 被拒绝；动态跨类型比较为
false。数值比较统一 Go 数值表示；集合比较沿用 Go 深相等。
除零、非有限数值结果、非法索引和缺失字段都是错误。

`if` 选择表达式的值，`when` 控制图节点的执行资格。图依赖包含表达式的
所有自由变量；把外部操作放进 `when`，即可控制它是否执行。

基础纯操作只有以下四个，它们不能被 Host 重定义：

| 操作 | 契约 |
| --- | --- |
| `str(value)` | 与 print、REPL 和入口输出一致：字符串原样，核心其他值用 JSON，null 为 `null`；不可 JSON 编码的原生 Host 值使用宿主显示形式 |
| `len(value)` | list 的元素数、object 的键数、string 的 Unicode 字符数；其他值报错 |
| `range(end)` / `range(start, end[, step])` | 有限安全整数的半开区间；单参数时 start=0；默认 step=1，允许负步长，0 步长报错，方向不匹配为空列表；最多构造 1,000,000 个元素，每个元素检查取消 |
| `fold(source, seed, reducer)` | source 必须是 list；reducer 为本地或内联二参数纯 fn；从左到右调用 `(accumulator, item)`；空列表返回 seed |

fold 每个元素前检查取消，次数由输入长度限定，失败指出元素索引。编译器验证
reducer 名称、参数数量、已知 seed/返回类型；动态元素由 fn 参数检查。
range 暂时构造列表，不引入惰性 iterator 协议；数字限制为可精确表示的整数
（绝对值不超过 2^53−1），超量在分配前失败。Map、fold 与 list.* 都可以直接使用
range 等纯列表表达式，无需先绑定中间变量。
fold 不展开静态图、不并行归约，不通过外部 Host operation 注入 reducer，
不能把 effectful 工作藏进 fn。大规模科学计算继续交给显式适配器。

`list.*` 是保留的纯标准库命名空间，提供 34 个操作，完整签名、顺序、空输入、
分组键、转置形状和数量限制见 [LIST-LIBRARY.md](LIST-LIBRARY.md)，该文档是
本规范的一部分。callback 接受本地纯 fn 名或内联 `fn(x) { expression }`，
例如 `list.map(range(5), fn(x) { x * x })`、
`fold(range(5), 0, fn(total, x) { total + x })`。内联 fn 的参数类型可显式写出，
省略时为 any；结果可推断或用 -> 声明，单表达式前的 return 可省略。
内联回调可以捕获所在表达式的不可变值，捕获成为图依赖；参数只在回调体内
有效并遮蔽同名外层值。回调保持纯计算，不隐藏 Python/Host 调用、print 或控制操作。
每次调用检查取消、参数/结果边界与深度限制。具名回调不捕获 Flow 值，按原规则
显式传参。callback 函数名不形成数据边，但全部数据参数与内联捕获都形成依赖。
可在 fn、if、Map 元素内组合。未知 list 操作在 check 失败，不回退为 Python 或 Host。list.map 是顺序
纯表达式；独立的 Map 推导式允许 Runtime 动态并行展开。

Map 形式为 `[expression for item in source]`，source 可以是已有绑定/参数，
或产生列表的纯表达式。推导式可放入调用参数、对象/列表、if 分支和纯 fn，
也可互相嵌套，例如 `np.mean([x for x in range(1, 19)])` 与
`[[x + y for y in range(0, x)] for x in range(1, 4)]`。
循环变量只在元素表达式中有效；source 在引入循环变量前求值，所以内层 source
可以引用外层同名变量，元素表达式则使用内层变量。
Map/fold 的输入列表最多 1,000,000 项，超量在展开/调用 callback 前失败。
独立绑定或返回的 Map 是一个静态节点，在依赖与门控就绪后计算一次 source，
然后动态展开；纯/只读元素允许有界并行，写入元素顺序执行。嵌入其他表达式
或 fn 的推导式按输入顺序计算，不额外启动并行 worker。两者都保持结果顺序，
空输入产生空列表，失败指出元素索引，并检查取消。if 与 &&/|| 未选择的部分
不求值；元素表达式中的嵌套推导式在每次迭代内求值，不提升为提前执行的图节点。
外部调用仍须作为独立 Flow 或 Map 操作；调用参数里的推导式和 source 只组合
纯计算。过滤语法与 Map 内 State/Retry/Feedback 不在本版。

## 4. 图与执行

`fail(message)` 是固定纯失败表达式，接一个 string，没有正常结果；可在
if、fn、Map 和 callback 中使用，未选择的分支不求值。不新增 error 值或
try/catch，错误传播为 Runtime Failed。内部 never 记录无正常结果，保留
成功分支的已知类型，不因 fail 将条件变成 any；never 不可由用户声明。
Host 无法覆盖 fail。

`string.*` 为保留纯标准库，19 项签名和 Unicode、空输入、转换、限制与错误
规则见 [STRING-LIBRARY.md](STRING-LIBRARY.md)，是本规范组成部分。未知操作
check 失败，不回退外部调用。

所有库使用相同调用表达式与值依赖。Go/Python 外部调用在节点或 Map 元素
显式调度，不能藏进纯 fn；普通 Python 有限标量/list/tuple/object 自动进入
相同值域。NaN/Inf、字符串化键碰撞明确错误；无序 set 保留句柄而不变列表，
显式 to_json set 错误，需要顺序时调用 sorted。复杂对象生命周期仍显式管理。

绑定、return、effect-only call 和必要的 gate 表达式形成节点；fn 内纯表达式不
额外形成图节点。变量引用产生数据依赖，when 产生 gate。false gate 及缺失的
门控数据使相关节点 Skipped。when true 不强制添加虚假的运行依赖。

默认 Run 自动调度，有界 RunParallel 可指定并发上限，RunSequential 提供顺序
参考路径。三者遵循同一输入、结果和效果契约；并发只是一种机会。Host 的 Pure
可缓存，ReadOnly 可并行且每 Tick 重读，ExternalWrite 按顺序形成屏障；未知
效果保守按 ExternalWrite。NodeSpec.After 是宿主 Ordering API，不增加语言语法。

Result 支持 Ready、Failed、Await。Context 取消传播至 Await、Map、fold、Retry
和 Feedback。Host callback 通过 Context 协作取消。
执行 fail-fast：错误停止新的独立工作并阻止下游，未执行节点为 Skipped；并行
执行中已经开始的外部写入不会回滚。

State 由 `name = state(initial)` 声明，初值首次成功计算后保存。持久 Instance
通过 SetState 和 Tick 显式推进，不隐式形成图循环。Tick 接受部分参数更新，
拒绝未知字段和错误类型；首次创建需要完整输入。取消的 Tick 也推进逻辑时钟，
非法输入更新在进入 Tick 前失败。未变化的纯依赖允许复用，外部读写重新执行。
同一 Graph/Instance 的公共执行与观察方法串行化；Host callback 不能重入同一个
锁定 Instance。多实例之间共享 Host 的内部状态由 adapter 自行保护。

Retry 的 attempts 包含第一次调用，只接受正整数上限；无退避和隐式幂等性。
Feedback 保留有界 initial/step/verify policy，verifier 必须返回 bool；到达上限
未通过是失败。它们属于现有 Runtime 策略，不推广成无界语言循环。

## 5. CLI 与观察

- `lipc check file.lip`：统一检查语法、名称、类型和依赖，失败非零退出。
- `lipc check --json file.lip`：`lip.diagnostics.v1` 结果、首个诊断、源位置/源行与
  修复指导。源码/读取失败为 1，成功 0；用法错误为 2，向 stderr 输出。
- `lipc inspect file.lip`：执行相同检查，输出 JSON 依赖图，不执行 Host。
- `lipc run [--trace path.json] file.lip [inputs...]`：构建临时程序并执行。
- `lipc build [--output executable] file.lip`：独立可执行文件。
- `lipc build --emit-go ...`：普通 Go 源码；`--no-main [--package name]` 直接选择库源码模式。
- `lipc version` 查看版本，`lipc help [命令]` 查看帮助。
- `lipc repl [--quiet]`：交互式表达式、绑定和 fn；会话值与声明保留，历史效果不重放。

所有命令的工具选项使用 `--` 长形式，放在入口文件前。`run` 把文件后的
全部文本作为程序输入，包括 `--help`、`--trace`、负数和 `--`。
`run --help` 查看工具帮助，各命令都支持 `--help`。直接运行 `lipc` 也显示帮助。
帮助写 stdout，退出 0；用法错误写 stderr，退出 2。
显式命令读取给定的源文件路径。构建输出目录按 Flow 名生成文件，Windows
默认可执行文件带 `.exe`；源码由 `--emit-go` 或 `--no-main` 明确选择。
构建输出和 trace 使用独立路径，保护源文件及其符号链接、硬链接别名。

位置参数严格按 Flow 声明解析：string 原样，number 有限十进制，bool 只接受
true/false，any/list/object 使用 JSON；list/object 额外检查形状。
`name=值` 对 string 输入也是原样文本。
`run` 在调用 Go 前完成相同的输入校验；缺少输入指出名称与类型，提示实际命令。
结果 string 直接打印，其余 JSON；无返回值的 Flow 不自动输出 null。
print 接受任意类型和任意数量的参数，空格分隔后换行，调用结果为 null；它是显式控制台副作用。
纯核心的三条 CLI 路径结果一致，程序退出码由 run 原样传播。

inspect JSON 的 schema 为 `lip.graph.v1`，包含 flow、参数及类型、output_type、
依赖声明和源序节点。节点含名称、数据依赖、gates、类型、kind、output、纯计算
标记、调用名与源行列；kind 是 value/map/state/retry/feedback。不输出 AST 内部
表示；同一源程序输出稳定。

trace 自动创建所需父目录，仅在进入执行后写出；写入失败不会隐藏同时发生的
程序错误。JSON 的 schema 为 `lip.trace.v1`，包含 events 和可选 error。每个 event 含
tick、node、status、reason；status 使用名称而非 Go 枚举整数。trace 写入独立
文件，成功与执行失败都保留，写入失败本身非零退出。不另行序列化节点输入、
输出或 Host 参数；reason/error 保留原诊断文字。事件记录节点状态与原因，独立节点按实际调度顺序记录。
进入图执行后写入 trace；检查、输入或启动失败时保留已有文件。

独立核心不依赖 Python。Python 声明启用已有独立 Worker；生成库由宿主控制
Worker 生命周期。Host/Go adapter 程序使用库模式。外部包版本由部署管理。

## 6. 验证

本版完成需要同时满足：

1. 零外部依赖的 Report 示例，空列表、嵌套对象、null、Unicode 都能 check/run；
   可执行文件、生成 Go 和生成库结果相同。
2. len/range/fold 可组合于 fn、if、Map 元素及 Flow 表达式，类型错误、重复键、缺失
   reducer、非列表、越界与 reducer 错误有可断言的失败；空 fold 不调用 reducer。
3. 块式 if 的选择与短路可验证；注释和语法规则唯一；直接/间接纯递归可运行，缺失返回类型在 check
   失败，深度超限为错误而非崩溃；range 步长、方向、上限、取消有验证。
4. 顺序、自动及有限并行返回同一核心结果；纯节点在未变化的 Tick 被复用；
   fold 取消和失败索引有测试，Host 不能重定义纯操作或本地 reducer。
5. inspect 输出稳定并保留对象值/Map/fold 中的真实依赖；trace 成功/失败均可
   解析，stdout 保持结果，run 保留输入失败与执行失败退出码。
6. 既有 0.5 conformance 和 Python adapter 测试继续通过；所有示例可检查并生成
   Go；`go test ./...`、`go test -race ./...`、`go vet ./...`、build 全部通过。
7. VERSION、CLI、README、规范索引、路线图和发布清单一致；有一条命令复现核心
   发布验收。标签与远程发布是独立的发布操作，不由本地验收冒充。
8. list 标准库所有目录项都有正面语义测试；覆盖空输入、非矩形转置、窗口步长、
   分组/排序稳定性、不修改输入、callback 类型/错误/取消及组合式生成库执行。
9. string 全目录语义、Unicode/空值/上限/错误、fail 类型与惰性分支、JSON
   诊断均可验；教程源码与示例文件一致，实际输入输出和错误受自动测试验证。
10. Go 读取→纯库解析→Python 计算→纯库汇总在三种调度模式一致；Python
    非有限、键碰撞、无序集合不隐式丢信息；编译器短时 fuzz 检查无崩溃。

新能力由实际程序的需求推动，同时定义语法、组合方式、错误语义和验证。
后续计划见[路线图](ROADMAP.md)。
