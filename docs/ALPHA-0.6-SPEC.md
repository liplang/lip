# LIP 0.6.3 语言与执行规范（0.6 系列）

参考实现 `0.6.3`。本文统一定义当前语言与执行规则，标准库详见
[列表库](LIST-LIBRARY.md)与[字符串库](STRING-LIBRARY.md)。
验证入口见 [RELEASE.md](../RELEASE.md)。

本文覆盖当前 `0.6.3` 实现，后续 0.6.x 的规则继续在此同步；小版本的行为变更
见 [CHANGELOG.md](../CHANGELOG.md)，不另保留相互竞争的现行规范。

## 1. 设计

LIP 以依赖关系描述计算，核心程序完成 **声明输入 → 构造数据 →
选择、变换与聚合 → 输出结果 → 观察执行**。Go Host 和 Python Worker
提供外部能力，共用同一套调用与值依赖。

| 层次 | 内容 | 用途 |
| --- | --- | --- |
| 语言核心 | Flow、纯 fn、单赋值、基本数据、if/match、Map、顺序 for、break/continue、str/len/range/fold/fail、纯递归 | 能组合出独立的小程序 |
| 纯标准库 | 34 个 list.*、19 个 string.* 操作 | 方便处理实际数据，不新增语法 |
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
分隔符。连续或独立分号表示空语句，可以出现在文件和块内；不产生图节点。
块末和 EOF 前的最后一个分号可省略；fn 的结果表达式后也允许末尾分号。
分号不用于分隔函数参数、列表元素或对象字段；这些位置仍使用逗号，允许尾逗号。
行注释使用 `#`。15 个关键字为 `flow`、`fn`、`return`、`match`、`for`、`in`、
`break`、`continue`、`if`、`else`、`import`、`as`、`true`、`false`、`null`。
依赖关键字只有 `import`；种类为 `host`、`go`、`python`，声明不安装包。
每个外部操作必须匹配 Host 声明、Python 导入根或 Go 包路径下的操作命名空间；Go 声明不能替代 Host 注册。
重复声明、无效声明和未声明操作在 check 阶段失败。未使用的合法导入只保留为元数据，
不要求适配器，也不启动 Python。运行入口检查程序中实际出现的外部操作，包括
未选中的分支和空循环体；不把运行输入当作静态移除依赖的依据。Host 通配声明只
允许该命名空间的调用，运行时只要求实际使用的成员注册。

三种 import 都支持 `as name`。Python 与 Go 是命名空间别名，例如
`import python "math" as m` / `m.sqrt(...)`、`import go "fmt" as f` / `f.Println(...)`。
Host 单操作别名直接调用：`import host "service.fetch" as fetch` / `fetch(...)`；
Host 通配命名空间使用成员调用：`import host "service.*" as s` / `s.emit(...)`。
别名解析保留依赖种类，Host/Go 不会被当成 Python 或纯标准库操作。
Go 操作注册名为完整包路径加 `.操作名`，如 `fmt.Println`、`example.com/adapter.Transform`。
真实 Go 函数由宿主注册，别名不引入自动导入、安装或通用函数值。
参数、绑定、回调与循环变量按词法作用域遮蔽导入名；绑定的初值在引入新名字前求值。
各 match 分支与 for 迭代的局部名字不会影响外层或其他分支。跨种类的重复别名报错；
无别名调用若同时匹配不同后端，必须用不同 as 别名消除歧义。
同一依赖可声明多个不同别名，原声明和别名保留在 check/inspect/RequiredDependencies 中。

Python 声明使用模块的真实导入名，支持点分路径和可选版本范围。
不写别名就保留原名；`import python "numpy" as np` 使 `np.mean(...)` 调用
`numpy.mean(...)`。别名只改本地名称，不改安装包名或 Worker 的模块策略。
例如 `sklearn` 是原始模块名，`scikit-learn` 是安装名，不做隐式映射。
`as` 适用于三种导入。命名空间别名可以与内置操作或具名 fn 同名：
`import python "math" as print` 下，`print(...)` 仍是核心打印，`print.sqrt(...)`
是 Python 调用；具名 fn 的裸调用与同名命名空间的成员调用也分别解析。
单操作别名按显式声明解析，`import host "emit" as print` 的 `print(...)` 调用 emit，
`as state` / `as retry` 也不会被误认为核心控制操作。单操作别名 `as string`
只接管 `string(...)`，`string.trim(...)` 等核心成员仍可调用；补全使用相同规则。
单操作别名与具名 fn 同名
仍会造成裸调用歧义，必须改名。重复别名、关键字、python 控制命名空间和生成
节点前缀仍保留；类型名可作别名。
本地 fn 与 Host 操作的内部注册名隔离；同名 Host 操作可用独立别名调用，不会
被本地 fn 覆盖。本地 fn 的裸调用优先，canonical 外部名有后端歧义时仍需别名。
显式导入同名外部模块或操作时，调用归属于声明的后端；例如 `import python "string"`
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
Go 定义的数值、bool 和 string 类型与其底层标量遵循同一套边界和运算规则；
json.Number 属于 number，不能因其 Go 底层为 string 而进入字符串运算。
Go 边界接受 slice/array 作为 list，字符串键 map 作为 object；宿主必须把已
交给 Flow 的数据视为只读，不得在 Tick 之间原地修改缓存所引用的数据。

Flow 和具名 fn 的参数必须写类型；内联集合回调的参数类型可省略，默认 any。
有返回值的 Flow 必须写输出类型，以一个结果出口收束：可以用一个 return 返回表达式，
也可以在同一个 match 的互斥分支中分别 return。同一块的 return 后不能有语句。
没有返回值的 Flow 省略输出声明和 return，例如 `flow main() { print(79 / 134) }`，
也可显式写 `-> void`。void 只用于 Flow 输出，不能作为参数、fn 返回或 `void?`。
无返回值的 Flow 可在块末尾写一个裸 return；它不取消其他图节点或充当进程退出码。
顶层语句可省略整个 Flow 外壳，例如 `a = 2; b = 3; print(a / (a + b))` 输出 `0.4`。
fn 只含一个结果表达式，return 可省略，返回类型可以推导；例如
`fn square(x: number) { x * x }` 与写 return 等价。Flow 输出 `T?` 接受 T
或 null，并允许门控关闭时无值完成。参数、fn 与内联回调的输入/结果同样支持
`T?`，表示 T 或 null；它不表示参数可省略。match 的字面量分支收窄匹配变量；
无守卫的 null 分支之后，通配分支中的可空变量收窄为 T。有守卫的 null 分支不排除 null。any 本身包含 null，
所以 any? 在值类型上与 any 等价；Flow 的 any? 输出仍允许无值完成。
CLI 的可空参数用 `null` 表示空值；string? 也接受 JSON 引号字符串，传递文本 null
时写 `'"null"'`。普通 string 的 null 仍是原样文本。

绑定不可重复或前向引用，match 分支和 for 迭代内绑定不能逃出作用域；不同分支或循环可独立使用同名绑定。fn 可调用其他纯 fn，
但不能隐含外部工作。直接或间接递归环内的每个函数必须显式声明返回类型，
不推导循环类型。每次本地函数调用检查取消；最大嵌套调用深度为 256，超出
产生普通运行错误。函数名不能覆盖内置操作；生成节点前缀保留。
普通值不构成一等函数；导入名被值遮蔽后不能再通过该名字调用模块操作。集合回调接受本地纯函数名或内联 fn，
不引入可保存、传给外部操作或作为 fn 返回结果的一等函数值。

## 3. 数据与表达式

值包括 null、bool、number、string、list、object。支持嵌套构造：

<!-- lip-check: expression -->
```lip
{name: "Ada", "two words": [1, null, {active: true}]}
```

对象键可以是普通标识符或字符串；关键字键需要引号。对象键唯一，重复键
在编译时报告。键不形成依赖，值中的引用形成依赖。对象和列表的
元素表达式按书写顺序计算，错误停止当前节点；没有可变对象或字段赋值语法。

读取使用 `value.field` 和 `value[index]`。列表索引必须是非负整数且在范围内；
字符串按 Unicode 字符索引并返回一个字符串，len 采用相同的字符计数；对象
索引必须是字符串，缺失键报错，不与显式 null 混同。Go struct 的字段读取保留
为 Host 互操作能力，支持按 Unicode 首字母大写读取导出字段，但 struct 不自动
成为 object 边界类型。Host map 不可比较的索引键返回错误，不触发 Go panic。

运算包括 `+ - * / // % ** */`、`== != > >= < <=`、`&& ||` 和一元 `+ - !`。
数值不隐式与字符串转换；字符串支持连接及整数次数重复。
数字支持 `+ - * / // % ** */`：`/` 返回商，`//` 对商向下取整，`%` 返回符号跟随除数的余数，
`**` 是乘方，`x */ y` 是以 y 为底的对数。例如 `7 // 2 == 3`、`-7 // 2 == -4`、
`-7 % 2 == 1`、`8 */ 2 == 3`。所有操作数都是 number，允许小数；结果仍为有限浮点 number。
对数要求 x > 0、y > 0 且 y != 1；除法、整除和取余的除数不能是零。
乘方不产生复数，非法实数结果和溢出报错；对数等运算遵循浮点精度。

优先级从高到低：调用/索引、`**`、`!`/正负号、`* / // % */`、`+ -`、大小比较、
`== !=`、`&&`、`||`。`**` 右结合，其他二元运算左结合；`2 ** 3 ** 2` 为 512，
`-2 ** 2` 为 -4，`(-2) ** 2` 为 4，`2 ** -2` 为 0.25。
`*/` 与乘除同级，所以 `16 */ 2 ** 2` 为 2；需要其他分组时写括号。

条件必须是 bool，
`if condition { a } else { b }` 只计算所选表达式，每个块写一个值表达式。
与 match 一样，分支可返回不同类型，不可统一时推导 any，fn/Flow 返回边界检查声明类型。
`&&`/`||` 短路，`!` 只接受 bool。静态已知的类型矛盾在 check 失败；动态值在运行时检查。
除 null 比较外，静态已知的跨类型相等比较在 check 被拒绝；动态跨类型比较为
false。数值比较统一 Go 数值表示；集合比较沿用 Go 深相等。
除零、非有限数值结果、非法索引和缺失字段都是错误。

`match value { pattern => result, _ => fallback }` 按源码顺序选择第一个匹配分支，
匹配值只求值一次。模式支持 number、string、bool、null 字面量和通配符 `_`；
可写 `pattern if condition => ...`，仅在模式匹配后求值 bool 守卫。无守卫的 true/false
分支可完整覆盖 bool，bool? 还需无守卫的 null 分支；已知 null 只需 null 分支。
其他类型需要无守卫的 `_` 覆盖所有可能值。有守卫的分支不计入完整覆盖。
同一字面量在无守卫分支后再次出现、或无守卫 `_` 后还有分支，均报不可达错误。
模式不引入变量；解构、范围模式和多个模式的 `|` 组合尚未实现。

值表达式分支之间必须用逗号分隔，末尾逗号可省略；使用块的分支可省略分支间
逗号，与 Rust 的块分支一致。块内语句仍用换行或分号分隔。match 语句的单条
绑定、调用、return、break 或 continue 也可省略块，此时分支之间必须使用逗号。

match 表达式的分支是值表达式，可选 `{ expression }` 外壳，可用于绑定、return、fn、
集合回调和嵌套表达式。分支结果可以是不同类型，match 不要求它们一致；结果类型不能统一时
推导为 any。函数和 Flow 在各自的返回边界检查声明类型，例如 `-> any` 可接受数字或字符串，
`-> number` 则要求实际返回数字。

Flow 或顶层的 match 语句使用 `pattern => { statements }`，分支可绑定、调用外部操作、
嵌套 match、for 或 return；循环内的分支也可 break/continue。只执行所选分支，其他分支节点 Skipped。分支可以为空；如果某些分支
不返回值，Flow 输出声明 `T?`。if/match 表达式内的外部调用也只在所选分支执行；
多语句操作使用 match 语句分支。
图依赖包含表达式的所有自由变量。

例如，不同类型的分支可以直接成为函数结果；Flow 的 match 语句则控制操作和输出：

```lip
fn describe(value: any) -> any {
    match value { null => "empty", 0 => false, _ => value }
}

flow Gated(enabled: bool) -> number? {
    match enabled {
        true => {
            print("运行")
            return 7
        },
        false => {}
    }
}
```

语句式 `for item in source { statements }` 可用于 Flow、顶层和 REPL，遍历有限 list，
最多 1,000,000 项。source 在引入循环变量前求值一次，可使用 range、列表表达式或
外部调用或组合表达式的结果；空列表不执行循环体。每次迭代按书写顺序执行绑定、调用、
match 和嵌套 for，并等待异步调用完成后继续，不构造结果列表。

循环变量遮蔽同名外层值；每次迭代有独立作用域，循环变量和体内绑定不能逃出，
也不能重新赋值。`break` 结束最近一层循环，继续执行该循环之后的语句；
`continue` 跳过本次迭代剩余语句，从下一项继续。两者可出现在循环体或其中的
match 分支，不能用于循环之外、纯 fn 或推导式。同一块的无条件 break/continue 后
不能再有语句；嵌套循环的控制跳转不影响外层循环。

每次迭代检查 Context，取消或首个错误停止后续工作；错误包含从 0 开始的迭代索引
和内层语句位置。for 不支持体内 return 或持久 state；返回写在循环之后，持久状态
保留在 Flow 作用域。转换使用推导式，聚合使用 fold；for 用于顺序执行操作。

以下四个基础纯操作具有固定的核心含义；仅注册同名 Host 操作不会替换它们。
显式外部 import 接管名字时，调用保持所声明的后端身份：

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

未被显式外部 import 接管的 `list.*` 为纯标准库，提供 34 个操作，完整签名、顺序、空输入、
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
或产生列表的表达式。Flow 中 source 和元素均可组合外部调用；纯 fn/回调中的
推导式仍保持纯计算。推导式可放入调用参数、对象/列表、if 分支和纯 fn，
也可互相嵌套，例如 `np.mean([x for x in range(1, 19)])` 与
`[[x + y for y in range(0, x)] for x in range(1, 4)]`。
循环变量只在元素表达式中有效；source 在引入循环变量前求值，所以内层 source
可以引用外层同名变量，元素表达式则使用内层变量。
Map/fold 的输入列表最多 1,000,000 项，超量在展开/调用 callback 前失败。
独立绑定或返回的 Map 是一个静态节点，在依赖与门控就绪后计算一次 source，
然后动态展开；整个 Map（含 source）的效果为纯/只读时允许有界并行，
包含写入或未知效果时顺序执行。嵌入其他表达式
或 fn 的推导式按输入顺序计算，不额外启动并行 worker。两者都保持结果顺序，
空输入产生空列表，失败指出元素索引，并检查取消。if 与 &&/|| 未选择的部分
不求值；元素表达式中的嵌套推导式在每次迭代内求值，不提升为提前执行的图节点。
Flow/顶层/REPL 的外部调用可出现在参数、对象/列表、推导式来源与元素、if/match
等表达式中，按表达式书写顺序等待结果，失败后不计算后续操作。过滤语法与
Map 内 State/Retry/Feedback 不在本版。

## 4. 图与执行

`fail(message)` 是固定纯失败表达式，接一个 string，没有正常结果；可在
if、fn、Map 和 callback 中使用，未选择的分支不求值。不新增 error 值或
try/catch，错误传播为 Runtime Failed。内部 never 记录无正常结果，保留
成功分支的已知类型，不因 fail 将条件变成 any；never 不可由用户声明。
仅注册同名 Host 操作不能改变核心 fail；显式外部 import 调用保留所声明的后端。

未被显式外部 import 接管的 `string.*` 为纯标准库，19 项签名和 Unicode、空输入、转换、限制与错误
规则见 [STRING-LIBRARY.md](STRING-LIBRARY.md)，是本规范组成部分。未知操作
check 失败，不回退外部调用。

所有库使用相同调用表达式与值依赖。Go/Python 外部调用可在 Flow 表达式中组合，
不能藏进纯 fn 或集合回调；普通 Python 有限标量/list/tuple/object 自动进入
相同值域。NaN/Inf、字符串化键碰撞明确错误；无序 set 保留句柄而不变列表，
显式 to_json set 错误，需要顺序时调用 sorted。Python 句柄、blob 与外部资源生命周期仍显式管理。

绑定、return、effect-only call 和必要的 gate 表达式形成节点；fn 内纯表达式不
额外形成图节点。变量引用产生数据依赖；match 语句求值一个分支选择节点，再为各分支生成 gate。
未选中分支及缺失的门控数据使相关节点 Skipped。match 表达式在所在节点内只求值所选分支。
for 在外层图中是一个顺序屏障节点，依赖 source 和实际捕获的外层值。
每次迭代创建新的循环体图并顺序执行，Pure/ReadOnly Host 注册也不改变循环内的顺序。
循环每 Tick 重新执行，迭代值和控制跳转不缓存；外层 trace 记录 for 节点，错误保留
内层语句位置。break/continue 作为成功的控制跳转处理，不产生外层 Error 事件。

默认 Run 自动调度，有界 RunParallel 可指定并发上限，RunSequential 提供顺序
参考路径。三者遵循同一输入、结果和效果契约；并发只是一种机会。Host 的 Pure
可缓存，ReadOnly 可并行且每 Tick 重读，ExternalWrite 按顺序形成屏障；未知
效果保守按 ExternalWrite。生成节点汇总整个表达式的调用效果，包括 Map source、state 初始化、
retry 的被重试表达式，以及 feedback 的 initial/step/verify；任一写入或未知调用
形成屏障，任一只读调用使节点每 Tick 重读。未选分支也参与保守效果分类，但不执行。
NodeSpec.After 是宿主 Ordering API，不增加语言语法。

生成图按 Deps/Gates 管理计算生命周期：最后一个消费者完成或跳过后解除执行值表引用。
Map、for、Retry/Feedback 与捕获计入依赖；异步节点以整个 Await 链完成为准。
After 不保留数据，未消费的结果可立即移出值表。并行 worker 仅复制自身依赖。
输出在执行中单独保留，返回后 Graph 不额外保存；Instance 的输入、State、有效纯缓存
仍有后续用途。缓存重算前解除旧快照；副作用结果不缓存，State 不额外缓存 initializer 依赖。
这不修改对象或共享底层存储、不保证立即回收、不自动关闭外部资源，真正回收由 Go GC 决定。
手写 Go 图的 NewGraph 保持完整值表行为；可在读取均已声明、名称唯一、值表只读且不在
最终异步结果完成后继续访问值表时，通过 GraphOptions.ReleaseIntermediates 显式启用。
详见[值生命周期](VALUE-LIFETIMES.md)。

Result 支持 Ready、Failed、Await。Context 取消传播至 Await、Map、for、fold、Retry
和 Feedback。Host callback 通过 Context 协作取消。
执行 fail-fast：错误停止新的独立工作并阻止下游，未执行节点为 Skipped；并行
执行中已经开始的外部写入不会回滚。

State 由 `name = state(initial)` 声明，只能直接作为 Flow 绑定（也可在 match
语句分支中声明），不能嵌入其他表达式、纯 fn 或 for。初值首次成功计算后保存。初始化可组合外部调用，
首次执行遵循整个初始化表达式的效果规则；保存后 Tick 不重复初始化。持久 Instance
通过 SetState 和 Tick 显式推进，不隐式形成图循环。Tick 接受部分参数更新，
拒绝未知字段和错误类型；首次创建需要完整输入。取消的 Tick 也推进逻辑时钟，
非法输入更新在进入 Tick 前失败。未变化的纯依赖允许复用，外部读写重新执行。
同一 Graph/Instance 的公共执行与观察方法串行化；Host callback 不能重入同一个
锁定 Instance。多实例之间共享 Host 的内部状态由 adapter 自行保护。

`retry(operation(...), attempts)` 和 `feedback(initial(...), step, verify, attempts)`
只能直接作为 Flow 节点的绑定、返回或调用语句，包括 match 分支和 for 体；
不能嵌入运算、集合、调用参数、纯 fn 或推导式。initial/operation 必须是普通
调用，可以是本地纯函数、核心库或声明的外部操作；其参数仍可组合普通表达式。
step/verify 是操作名称而非函数调用或内联回调，各接收一个候选值。
attempts 包含第一次调用/验证，只接受不超过 9007199254740991 且能由 Go int
表示的正整数字面量，防止浮点到整数转换溢出。Retry 等待异步结果，普通失败
才重试，取消直接停止；无退避和隐式幂等性，重试会重新求值整个被重试调用及参数。
Feedback 先计算一次 initial，再验证候选；verifier 必须返回 bool，失败/取消立即
传播，false 才调用 step。最多验证 attempts 次、修订 attempts−1 次，到达上限
未通过是失败。它们属于现有 Runtime 策略，不推广成无界语言循环。

## 5. CLI 与观察

- `lipc check file.lip`：统一检查语法、名称、类型和依赖，失败非零退出。
- `lipc check --json file.lip`：`lip.diagnostics.v1` 结果、首个诊断、源位置/源行与
  修复指导。源码/读取失败为 1，成功 0；用法错误为 2，向 stderr 输出。
  line/column 从 1 开始，column 计 Unicode 字符（含组合字符），Tab 计一个字符。
- `lipc inspect file.lip`：执行相同检查，输出 JSON 依赖图，不执行 Host。
- `lipc run [--trace path.json] file.lip [inputs...]`：构建临时程序并执行。
- `lipc build [--output executable] file.lip`：独立可执行文件。
- `lipc build --emit-go ...`：普通 Go 源码；`--no-main [--package name]` 直接选择库源码模式。
- `lipc version` 查看版本，`lipc help [命令]` 查看帮助。
- `lipc repl [--quiet]`：交互式表达式、绑定和 fn；会话值与声明保留，历史效果不重放。
- `lipc learn [--list] [--lesson 编号或ID] [--progress 文件 | --no-progress]`：
  26 节内置交互课，用真实编译器和 Runtime 验证示例及提交。

REPL 也接收 import、match 与 for；最后一个值表达式自动显示，print 不额外显示
null。成功单元提交值和声明，失败单元保留先前会话，但已完成的外部效果不会回滚。
绑定不可变；`:reset` 清空会话，未闭合输入自动续行，`:cancel` 丢弃草稿。
每单元使用独立 Worker，Python 对象句柄不跨单元保留；模块/函数具名引用可重新
解析。Host/Go adapter 和持久 State/Tick 使用生成库。REPL 不直接接收 Flow 文件。
learn 的课程、命令、进度及集成契约见 [INTERACTIVE-LEARNING.md](INTERACTIVE-LEARNING.md)。
Neovim Lua、Vim9script 与 Emacs Elisp 插件共用生成词库，检查均读取上述 JSON；
安装与编辑器字节/字符位置换算见 [EDITORS.md](EDITORS.md)。

所有命令的工具选项使用 `--` 长形式，放在入口文件前。`run` 把文件后的
全部文本作为程序输入，包括 `--help`、`--trace`、负数和 `--`。
`run --help` 查看工具帮助，各命令都支持 `--help`。直接运行 `lipc` 也显示帮助。
帮助写 stdout，退出 0；用法错误写 stderr，退出 2。
显式命令读取给定的源文件路径。构建输出目录按 Flow 名生成文件，Windows
默认可执行文件带 `.exe`；源码由 `--emit-go` 或 `--no-main` 明确选择。
构建输出和 trace 使用独立路径，保护源文件及其符号链接、硬链接别名。

位置参数严格按 Flow 声明解析：string 原样，number 有限十进制，bool 只接受
true/false，any/list/object 使用 JSON；list/object 额外检查形状。
可空参数的 `null` 表示空值，string? 还可用 JSON 引号字符串区分文本 `"null"`；
其余字符串输入规则同第 2 节。T? 参数仍是必填的位置输入。
`name=值` 对 string 输入也是原样文本。
`run` 在调用 Go 前完成相同的输入校验；缺少输入指出名称与类型，提示实际命令。
结果 string 直接打印，其余 JSON；无返回值的 Flow 不自动输出 null。
print 接受任意类型和任意数量的参数，空格分隔后换行，调用结果为 null；它是显式控制台副作用。
纯核心的三条 CLI 路径结果一致，程序退出码由 run 原样传播。

inspect JSON 的 schema 为 `lip.graph.v1`，包含 flow、参数及类型、output_type、
依赖声明和源序节点。节点含名称、数据依赖、gates、类型、kind、output、纯计算
标记、调用名与源行列；kind 是 value/map/for/state/retry/feedback。不输出 AST 内部
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
   fold 取消和失败索引有测试，Host 注册不替换核心纯操作或本地 reducer；显式外部 import 保留后端身份。
5. inspect 输出稳定并保留对象值/Map/fold 中的真实依赖；trace 成功/失败均可
   解析，stdout 保持结果，run 保留输入失败与执行失败退出码。
6. 既有 0.5 conformance 和 Python adapter 测试继续通过；所有示例可检查并生成
   Go；`go test ./...`、`go test -race ./...`、`go vet ./...`、build 全部通过。
7. VERSION、Runtime、CLI、生成文件版本、README、规范索引、路线图和发布清单一致；有一条命令复现核心
   发布验收。标签与远程发布是独立的发布操作，不由本地验收冒充。
8. list 标准库所有目录项都有正面语义测试；覆盖空输入、非矩形转置、窗口步长、
   分组/排序稳定性、不修改输入、callback 类型/错误/取消及组合式生成库执行。
9. string 全目录语义、Unicode/空值/上限/错误、fail 类型与惰性分支、JSON
   诊断均可验；教程源码与示例文件一致，实际输入输出和错误受自动测试验证。
10. Go 读取→纯库解析→Python 计算→纯库汇总在三种调度模式一致；Python
    非有限、键碰撞、无序集合不隐式丢信息；编译器短时 fuzz 检查无崩溃。
11. match 的覆盖、守卫、惰性、异类结果及返回边界；八种算术的优先级、定义域和
    有限结果；for/break/continue 的顺序、作用域、异步、取消与每 Tick 重跑均可验。
12. 三种 import/as 的后端身份、遮蔽与消歧、外部表达式效果和可空签名一致；
    所有文档 LIP 代码块按完整程序、表达式或练习模板检查，错误示例的诊断也受验证；
    课程索引、库目录与所有跟踪的编译生成文件由自动测试同步核对。
13. 最后消费者完成后解除中间值引用；异步、跳过、错误/取消、输出与 Host 别名、
    State 与有效纯缓存保留有可达性回归和 race 验证；记录大对象收益与计数成本。
14. 空语句、命名空间与裸调用别名、未使用导入、Python 控制操作及 Go 定义标量
    在 CLI、生成库、REPL 与课程执行器一致；Host map 无效索引不能触发 Go panic。
    本地源码/工具包可校验，固定工具链和输入下可复现，并在仓库外离线运行与构建。

新能力由实际程序的需求推动，同时定义语法、组合方式、错误语义和验证。
后续计划见[路线图](ROADMAP.md)。
