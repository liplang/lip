# LIP beta 0.9：小计算核心与统一调用边界

日期：2026-10-14。

## 0. 范围、术语与阅读约定

本文件完整定义 LIP beta 0.9 的语言与执行合约，是实现与一致性判定的唯一规范依据。
第 1–9、12–15 节及第 16 节附录定义 core（基本合规层）；第 10 节定义 core 必须遵守的
Host 边界；第 11 节定义 durable profile（可选的崩溃恢复合规层）。
未提供持久恢复的实现仍可符合 core。理解语义、身份编码和核心一致性用例只需本文件。

核心包括不可变值、表达式、纯 fn、if/match/for、effect/perform、Flow 依赖图和 join-only
Claim store。Go/Python 使用同一调用协议。持久状态、事务、重试、业务汇聚和证据展示可由
库或 Host 提供，其行为须满足本文件规定的调用与值边界。

“必须”和“不得”表示合规要求；“可”表示实现选择。示例说明规则，一致性用例给出
必须满足的输入/结果关系；是否已经实现这些规则，由具体实现的合规声明说明。

| 术语 | 含义 |
|---|---|
| Runtime / scheduler | 执行引擎 / 从已满足条件的图节点中选择工作的调度器 |
| Host / adapter / provider | 管理外部操作及权限的宿主 / 转换原生参数与结果的适配层 / 实际执行外部操作的服务或函数 |
| operation / descriptor / registry | 可调用的外部操作 / 该操作的注册合约 / 本次运行使用的合约登记表 |
| schema / capability | 参数或结果的数据约束 / Host 对操作与资源范围授予的权限 |
| run / binding | 一次 Flow 执行 / 名称与不可变值的绑定 |
| node / graph | 一个可独立调度的计算实例 / 节点及其数据、控制和执行顺序依赖 |
| gate / selector | 决定节点是否被选中的控制条件 / 计算分支或循环控制选择的节点 |
| region / producer / writer | 源码中分支或循环的结构区域 / 可能产生节点的语句或区域 / 向指定 Claim store 提交贡献的节点 |
| proposal / perform | operation 与固定参数组成的请求值 / 对该请求执行准入、调用和记录的语言操作 |
| Claim / Claim store / join | 带贡献身份的事实 / 一次 run 内的事实集合 / 用 `<+` 追加贡献的操作 |
| snapshot / projection | Claim store 在某次读取时的不可变记录列表 / 从该列表计算普通业务值的纯函数 |
| journal / replay / reconcile | 调用记录 / 复用已记录观察 / 根据可信外部证据解决未决调用 |
| terminal outcome | 外部事实及验证结果已确定的调用记录；不同于计算节点的终态 |

正文中的 `fn`、`effect`、`perform` 等代码 token 属于 LIP 源语法；`carry` 是
第 6.1 节携带值迭代表达式的文档简称，不是额外关键字。`Recorded` 表示观察已进入
调用记录，不是源类型。`arity` 表示函数接收的实参数量。
`canonical` 表示本规范确定的规范编码表示；`wire` 表示协议中的传输结构表示。

语法块采用 EBNF：`::=` 定义产生式，`|` 表示选择，`[...]` 表示可选，`*` 表示零次
或多次，引号内是源 token。形式规则和协议结构是描述记号，不是可直接执行的 LIP；
其中 `{field:type}` 表示记录字段约束，字段名后的 `?` 表示字段可缺省。代码块中作为
结构占位的 `...` 表示按相同规则重复，不属于源语法。各节在使用数学记号前定义其含义。

## 1. 核心对象

| 对象 | 规则 |
|---|---|
| Data | null、bool、有限 binary64 number、UTF-8 string、list、string-keyed object |
| fn | 捕获不可变值的纯闭包；支持递归、互递归和函数值 |
| effect | 已解析 operation 与已固定 Data 参数组成的不可变 proposal |
| Flow | 不可变绑定和控制结构降低得到的依赖图 |
| Claim store | 一次 Flow run 内只通过 join 增长的信息槽位 |

普通值 V 递归定义为 null、bool、number、string、list<V>、object<string,V>、fn 或 effect。
Data 是其中递归不含 fn/effect 的子域。普通容器可以存 fn/effect；跨 Host 边界、Claim
贡献、Flow 输入和成功输出必须是 Data。Claim store 不是普通值，不存在可任意传递的
store handle。变量只绑定一次；内层可遮蔽普通值，但不得遮蔽 Claim store 名称。

`effect` 构造请求，`perform` 解释请求。所有 Go/Python 调用、外部属性读取、随机、时钟、
日志、存储和网络访问都经 perform；没有第二条即时 Host 调用通道。纯核心操作与本地 fn
可直接调用，外部 operation 不可直接调用。第 10 节的通用库适配配置可在核心检查前
把直接库调用和属性访问降为 perform；降级后仍使用这一条外部通道。

## 2. 源文件和语法

文件为无 BOM 的有效 UTF-8，允许 LF/CRLF、空格/tab、`#` 行注释、分号和物理换行
分隔语句；其他裸控制字符非法。行末反斜杠可以显式
续行，之后不得有其他字符。括号、参数、list、object 内允许自然换行；block 内换行分隔
语句。lexer 保留换行，parser 按上下文处理；不根据下一行运算符自动续行。

标识符为 `[A-Za-z_][A-Za-z0-9_]*`。保留词为 `import as flow fn return if else match
for in parallel break continue with next effect perform true false null void`。`go/python/host` 在
import 后为上下文关键字；类型名只在类型/pattern 位置有专用含义。`claim`、import alias
与第 3 节核心函数名不得重定义。其他同一作用域的重复声明为静态错误。
string 使用双引号，裸换行非法，只支持 `\"`、`\\`、`\n`、`\r`、`\t`、`\u{HEX}`
转义；HEX 为 1–6 位十六进制且必须为 Unicode 标量，拒绝 surrogate 和超过 U+10FFFF
的值。不进行 Unicode normalization。number 的词法形式为
`(0|[1-9][0-9]*)(\.[0-9]+)?([eE][+-]?[0-9]+)?`，按 nearest/ties-to-even 转为
有限 binary64；溢出非法，下溢必须按 nearest/ties-to-even 舍入为次正规数或 0，不能
任选报错/flush-to-zero。正负号是 unary operator，不存在隐式八进制。
运算符按最长匹配识别；`**`、`*/`、`//` 各是一个 token，两个字符之间不能有空白。

以下是规范语法；`sep` 为一个或多个分号/换行，`padding` 为零个或多个；Data/type 和
operator 的静态约束见后文。`id` 为标识符，`string`/`number` 为字面量；EOF 表示文件
结束，NEWLINE 表示物理换行 token。

```ebnf
file       ::= padding import* fn_decl* (flow_decl | statements) EOF
import     ::= "import" ("go" | "python" | "host") string ["as" id] sep
fn_decl    ::= "fn" id params "->" type fn_body padding
flow_decl  ::= "flow" id params "->" (type | "void") block padding
params     ::= "(" [parameter ("," parameter)* [","]] ")"
parameter  ::= id ":" type
type       ::= "null" | type_name ["?"]
type_name  ::= "any" | "bool" | "number" | "string" | "list" | "object"
             | "function" | "effect"

statements ::= padding [stmt (sep stmt)* padding]
block      ::= "{" statements "}"
stmt       ::= id "=" expr | id "=" "claim" "(" ")" | id "<+" expr
             | "return" [expr] | "break" | "continue"
             | if_stmt | match_stmt | for_stmt | call | perform
if_stmt    ::= "if" expr block ["else" (block | if_stmt)]
for_stmt   ::= "for" id "in" expr ["parallel"] block | "for" block
match_stmt ::= "match" expr "{" padding arm_stmt (arm_sep arm_stmt)*
               [arm_sep] "}"
arm_stmt   ::= patterns ["if" expr] "=>" (block | stmt)
arm_sep    ::= "," | sep
patterns   ::= pattern ("," pattern)*
pattern    ::= literal | ["+" | "-"] number | concrete_type | "_"
concrete_type ::= "bool" | "number" | "string" | "list" | "object"
                | "function" | "effect"

expr       ::= or
or         ::= and ("||" and)*
and        ::= equality ("&&" equality)*
equality   ::= compare [("==" | "!=") compare]
compare    ::= add [("<" | "<=" | ">" | ">=") add]
add        ::= multiply (("+" | "-") multiply)*
multiply   ::= unary (("*" | "/" | "//" | "%" | "*/") unary)*
unary      ::= ("!" | "-" | "+") unary | power
power      ::= power_base ["**" unary]
power_base ::= perform | proposal | postfix
postfix    ::= primary suffix*
suffix     ::= "(" [arguments [","]] ")" | "." id
             | "[" expr "]"
arguments  ::= positional_args ["," named_args] | named_args
positional_args ::= expr ("," expr)*
named_args ::= named_arg ("," named_arg)*
named_arg  ::= id "=" expr
primary    ::= literal | id | "(" expr ")" | list | object | value_block
             | "fn" params "->" type fn_body
             | if_expr
             | "match" expr "{" padding arm_expr (arm_sep arm_expr)*
               [arm_sep] "}"
             | carry_expr
if_expr    ::= "if" expr value_block "else" (value_block | if_expr)
proposal   ::= "effect" operation "(" [arguments [","]] ")"
perform    ::= "perform" (proposal | postfix)
operation  ::= id ("." id)*
call       ::= postfix                 # last suffix must be an argument list
fn_body    ::= "{" padding (pure_stmt sep)* fn_tail padding "}"
fn_tail    ::= ["return"] expr
pure_stmt  ::= id "=" expr | call | if_stmt | match_stmt | for_stmt
             | "break" | "continue"
local_stmt ::= id "=" expr | call | perform | if_stmt | match_stmt | for_stmt
value_block ::= "{" padding (local_stmt sep)* expr padding "}"
carry_expr ::= "for" id "in" expr "with" id "=" expr carry_block
             | "for" "with" id "=" expr carry_block
carry_block ::= "{" padding (local_stmt sep)* carry_exit padding "}"
carry_exit ::= "next" expr | "break" expr | carry_if
             | "match" expr "{" padding carry_arm (arm_sep carry_arm)*
               [arm_sep] "}"
carry_if   ::= "if" expr carry_block "else" (carry_block | carry_if)
carry_arm  ::= patterns ["if" expr] "=>" carry_block
arm_expr   ::= patterns ["if" expr] "=>" expr
list       ::= "[" [expr ("," expr)* [","]] "]"
             | "[" expr "for" id "in" expr ["if" expr] "]"
object     ::= "{" [field ("," field)* [","]] "}"
field      ::= (id | string) ":" expr
literal    ::= number | string | "true" | "false" | "null"
```

`pure_stmt` 中嵌套 block 的语句也必须属于 pure_stmt，不允许 return。fn 的末尾 return
是唯一结果出口，expression 简写等价于末尾 return。fn 不允许 claim 声明/读取/join 或
perform，但允许纯粹构造 effect。结果类型为 function/effect 时也须满足纯性。
fn_body 由局部纯语句加 fn_tail 组成；尾部 return 可省略。
具名 fn 的签名先统一收集，所以递归/互递归可以引用同文件的 fn；普通值不能前向引用。
签名参数、局部 binding 和循环变量在本作用域中唯一。遮蔽绑定 RHS 先在外层环境求值，
新名只对后续语句可见；循环体每次重新建立作用域。fn 中 for 顺序执行，parallel 非法；
循环中的 binding 不会更新外层值，也不会泄漏到循环之后。

Flow 有返回值时必须恰好有一个顶层最后 return；void 可没有 return 或用最后的无值
return。return 不提前跳过其他节点。分支/循环体不得 return。顶层 statements 是无参数
void `main` Flow；文件不能同时包含显式 Flow 与顶层 statements，也不能声明多个 Flow。
Flow 接口注解 `function`/`effect` 及其可选形式为 `E-TYPE`；list/object/any 的实际输入/
输出递归验证为 Data。fn 接口没有这项 Data 限制。fn 不支持 void；丢弃返回值的纯调用
可用于验证并传播 Error，需要没有结果的外部动作由 perform 表达。

`{}` 在表达式位置是空 object。非空花括号在一般表达式位置跳过换行后，以字段冒号前瞻
区分 object 与 value block；if 分支显式要求 value_block，返回 object 时使用 `{{a: 1}}`。
statement block 不按字段前瞻解释。match arm 的 expr 可以直接为 object。`}` 后换行紧接
`else` 时，if 语句和 if 表达式都不断开。多字符 token 必须最长匹配；`<+` 只用于完整
join statement，在普通表达式中不拆成 `<`、`+`。
binding RHS 为 `claim()` 时优先识别 store 声明；claim 不是可以别名/传递的函数，也
不能作为普通 expression 调用。join 左侧在名称解析阶段必须是已声明的 Claim store。

proposal 不属于 postfix primary，不能直接追加字段、索引或调用后缀。
`effect op(args)` 以自身右括号结束；读取 perform 结果的字段使用
`(perform effect op(args)).field`，避免把字段访问写进 perform 的操作数。
空语句通过 padding/sep 表示，连续空行与注释行不产生节点。parser 在括号、参数、list、
object 内忽略 NEWLINE；在 block 中保留分隔；紧接 else 前的 NEWLINE 属于 if 结构。
value_block 的局部 binding 对后续 local_stmt/末尾表达式可见，不向外泄漏；Flow 中允许
perform，但不得声明/join store，所有嵌套 block 也遵守此限制。完整读取按其所属图节点
的位置检查第 9 节约束；fn 中引用活动 store 仍非法。local_stmt 顺序执行，不展开为
独立图节点，未选块不求值；局部普通 for 也在该节点内顺序执行，不允许 parallel。
在 fn_body/value_block/carry_block 中，先忽略闭合 `}` 前的尾部 padding，再确定最后一项：
分别按 fn_tail、expr、carry_exit 解析。该尾部规则优先于语句位置对 if/match/for 的识别；
尾 if/match 的分支按对应的值块/携带值块解释，不能先按 statement block 固定分类。
其余项须有 sep 并按语句解释。尾部换行或分号不把尾表达式变成丢弃结果的语句，
所以 `{ f(); }` 的值是 f 的结果；需要丢弃再返回 null 时写 `{ f(); null }`。
普通 statement block 始终按语句解释。parser 可以延迟分类共享结构，不要求无界回溯。
`for … with` 是表达式形式，使用于 binding/参数/return/块尾等表达式位置。
carry_exit 仅在 carry_block 尾位置有效，不跨函数或 carry 转移；局部普通 for 的无值
break/continue 按最近普通循环解释，不属于 carry_exit。
位置参数必须全部位于命名参数之前，`f(x=1,2)` 为 E-PARSE；重复命名参数在名称/参数
检查阶段静态报 E-ARITY，即使 callee 是动态函数值。普通调用、核心调用和 proposal 共用
上述实参语法；核心函数的命名参数另按第 3.1 节拒绝。

## 3. 值和核心操作

Data 是有限、无环、按值的数据树。object 不得有重复键，按 UTF-8 键序定义规范比较与
编码；list 保留顺序。函数、effect、原生指针和活动资源不属于 Data。adapter 返回的原生
mutable 对象必须复制为 Data，或转为第 10 节的 Host 资源引用，不能直接共享。

`==`/`!=` 按类型和递归结构比较，object 忽略存储顺序；function/effect 不支持比较。
递归比较遇到 function/effect 也报 `E-TYPE`，不使用指针地址判等。null 只等于 null，
bool 与 number 不相等。对象字段/索引不产生隐式绑定，重复 object 字段报静态错误。
大小比较接受两个 number 或两个 string；string 按 UTF-8 bytes 字典序（前缀较短者在前），
与 keys 一致，不使用 locale 或 Unicode normalization；混合类型报 E-TYPE。
逻辑操作只接受 bool。`+` 接受两个 number 或两个 string，
没有其他隐式类型转换。参数、字段、元素、算术子表达式从左到右求值；Error 后不继续
求值右侧。`&&`、`||`、if/match 未选分支不求值。

number 为有限 IEEE 754 binary64；`-0` 规范化为 `0`。基本四则按 binary64 的 round-to-
nearest/ties-to-even 求值，禁止浮点重结合或影响结果的隐式 FMA。除零、非有限结果报
`E-NUMBER`。`a // b` 为精确数学商向负无穷取整后的 binary64 值；`a % b` 是与该取整
对应的数学余数，舍入后必须满足 b>0 时 `[0,b)`、b<0 时 `(b,0]`。若舍入等于排除的
边界 b，取 `nextafter(b,0)`；零规范化为 0。这里 a、b 是两个 number 操作数，
`nextafter(b,0)` 表示从 b 向零移动一个 binary64 可表示值，是数值规则的辅助记号，
不是 LIP 内建函数。实现不得使用截断除法代替 floor 除法。

`a ** b` 为实数幂，`a */ b` 为以右操作数 b 为底的实数对数。两者只接受两个 number，
类型不符报 E-TYPE。幂的约定为 `0 ** 0 = 1`；零底数的负指数、负底数的非整数指数报
E-NUMBER。这里“整数指数”指存储的 binary64 值精确为整数，不受索引安全整数范围限制。
对数要求 a>0、b>0 且 b!=1，否则报 E-NUMBER；合法输入中 `1 */ b = 0`、`a */ a = 1`。
超出有限 number 范围的结果报 E-NUMBER；允许次正规结果和下溢为零，并规范化负零。
两者使用第 16.5 节固定的 binary64 参考算法，近似实数运算，不承诺正确舍入；
不得直接替换为随平台或库版本变化的 pow/log。数学上相等的不同表达式未必逐位相等。
领域数值库、复数和其他超越函数由 Go/Python operation 提供。

`**` 右结合，优先于左侧的一元正负号，也允许右侧带一元正负号；`*/` 与 `* / // %`
同级，从左向右结合。括号明确底数或改变结合次序。以下关系属于语法及数值一致性要求：

| 表达式 | 分组 | 结果 |
|---|---|---|
| `2 ** 3 ** 2` | `2 ** (3 ** 2)` | 512 |
| `-3 ** 2` | `-(3 ** 2)` | -9 |
| `(-3) ** 2` | 负数整体作为底数 | 9 |
| `2 ** -3` | `2 ** (-3)` | 0.125 |
| `8 */ 2` | 以 2 为底求 8 的对数 | 3 |
| `8 */ 2 * 4` | `(8 */ 2) * 4` | 12 |
| `256 */ 2 ** 2` | `256 */ (2 ** 2)` | 4 |

number 不区分整数和浮点数类型；结果 3 与 3.0 是同一个 binary64 值，规范 JSON 输出为 `3`。

list/string 的索引为安全非负整数，string 索引按 Unicode 标量值；object 索引为 string
键，`.name` 等价于该键查找。越界、缺键、类型不符报 Error。安全整数的绝对值不超过
`2^53-1`；Host 原生整数超出范围时必须拒绝，或按显式 schema 编码为 string/bytes。

### 3.1. 核心纯函数表（beta 0.9）

不可覆写的核心函数只有下表所列；全部纯、接收 V、返回新值，不修改输入。

| 签名 | arity | 参数/结果与规则 |
|---|---|---|
| len(value) | 1 | string/list/object → number 长度；string 按 Unicode 标量计数 |
| str(value) | 1 | Data → string；string 原值，其余为无空白规范 JSON |
| range(end) / range(start,end) / range(start,end,step) | 1/2/3 | 安全整数、非零 step → 半开 number list；默认 start=0、step=1 |
| map(xs,f) | 2 | list、纯一元 function → 按输入顺序的新 list |
| fold(xs,seed,f) | 3 | list、V、纯二元 function → V；严格左折叠，空 list 返回 seed |
| fail(message) | 1 | string → E-FAIL，没有成功值 |
| concat(a,b) | 2 | 两个 list → 顺序拼接的新 list；append 用 concat(a,[v]) |
| slice(v,start,end) | 3 | list/string、安全整数 → 同类型半开片段；string 按 Unicode 标量 |
| keys(o) | 1 | object → UTF-8 bytes 排序的 string list |
| has(o,k) | 2 | object、string → bool；缺键返回 false |
| put(o,k,v) | 3 | object、string、V → 增加/替换该键的新 object |
| split(s,separator) | 2 | string、非空 string → string list；左到右非重叠，保留空段 |
| parse_number(s) | 1 | string → 有限 number；完整文本转换，详见下文 |

核心函数不接受命名参数；直接可知时静态 E-ARITY，动态核心函数值调用时同样验证。
它们可以作为纯 function 值传递。map 的 f 接受一个元素，fold 的 f 接受
`(accumulator, element)`；实际 callable/arity/类型按第 5 节验证。推导式没有 guard 时
与顺序 map 等价；可选 guard 按第 6.2 节过滤。Flow 中允许 perform 的推导式依次执行，
与 list literal 一样保留元素顺序。range 在分配前检查长度预算。
len 的返回数必须是安全整数，超过范围须明确失败；不得舍入成错误长度。range 使用
精确整数计算其长度和元素，再转成 number，不用浮点循环判断制造重复/漏项。
slice 的 start/end 为安全非负整数，须满足 `0≤start≤end≤len(v)`，否则 `E-INDEX`，
不截断、不使用负索引。has/put 不接受非 string 键；缺键 has 返回 false，put 添加字段。
split 的 separator 为空时报 `E-VALUE`；`split("",",")` 为 `[""]`，
`split("a,,b,",",")` 为 `["a","","b",""]`。这些操作按声明 arity 检查，没有隐藏参数。
parse_number 接受整段 ASCII 文本：可选一个 `+`/`-`，随后必须恰为第 2 节 number 词法。
不接受空白、前导零、hex、NaN/Infinity 或尾部字符；非法文本/溢出报 E-VALUE，类型不符
报 E-TYPE。nearest/ties-to-even、次正规数/下溢和零规范化与字面量一致，不使用 Host 的
宽松数字 parser。`parse_number(str(n)) == n` 对所有核心 number 成立。

列表过滤、缺键默认值、对象合并、字符串拼接及包含判断由纯函数组合这些操作实现。
例如下列 filter/get/merge_objects 普通函数同时适用于 Flow 与其他 fn：

```text
fn filter(xs: list, predicate: function) -> list {
    [x for x in xs if predicate(x)]
}
fn get(o: object, key: string, fallback: any) -> any {
    if has(o, key) { o[key] } else { fallback }
}
fn merge_objects(a: object, b: object) -> object {
    fold(keys(b), a, fn(acc: object, key: string) -> object { put(acc, key, b[key]) })
}
```

规范 JSON number 按下述独立规则显示，而不使用 Host 的默认打印：零为 `0`。对非零
binary64 的精确数学值 v，在能 round-trip 为 v 的十进制数 `m×10^e` 中选最少有效数字
的 m（m 不含前导/末尾零）；再选与 v 精确距离最小者；仍平局则选绝对值较小者。
设该十进制数的科学指数为 k：`-6≤k<21` 时输出无指数的普通十进制，否则输出一位整数
加可选小数、小写 `e` 和指数。指数无正号/前导零；普通小数必须有整数部分、不得有
无意义末尾零。负数前置 `-`。例如 `str(1e3)` 为 `1000`，`str(1e21)` 为 `1e21`。
JSON string 仅转义引号、反斜杠、U+0000–001F，其余 UTF-8 原样；控制字符统一
`\u00xx` 小写编码。list/object 递归使用此规则，字段按 UTF-8 bytes 排序。
`str(function/effect)` 及递归含它们的容器非法。
这是本版的规范显示，不是 RFC 8785/JCS（JSON Canonicalization Scheme），也不要求
与 JavaScript 的 JSON.stringify 字节相同。str 仅用于显示，`str("1")==str(1)` 合法；
fingerprint（参数摘要）与身份使用第 16.1 节定义的带类型编码函数 C，不使用 str。
JSON 输入若含重复 object 键（包括解码后相同键）必须拒绝，不能由 parser 静默覆盖。

fn 的 environment 只捕获不可变快照；可以结构共享不可变值，不要求每次调用物理深拷贝。
创建闭包不得把 Claim store 或外部原生 callable 隐藏进去。Flow 可以把读出的 Data 快照
传入 fn；fn 内不访问活跃 store。
`g = fn() -> list { facts }` 若 facts 是 store 名，静态报 `E-PURITY`，不在闭包创建时
执行完整读取。捕获快照须先 `captured = facts`，再由闭包引用 captured；该 binding
受完整读取的依赖和位置约束。

## 4. 函数的计算能力与执行机

语言不规定 fn 的最大迭代数或最大递归次数。递归终止性不属于静态类型判定；不终止的
纯函数表示发散，不得伪装成成功、返回 null 或被静默截断。

实现必须支持 proper tail calls：包括直接、互递归和 function 值的间接尾调用。尾调用
复用 continuation，不因尾调用次数累计栈帧。`if`/`match` 选中分支的最终调用也是尾位置。
proper tail calls 是执行语义要求，不得因尾调用次数而累计栈帧或触发递归深度限制。
解释器和 Go 后端可使用 trampoline（循环驱动的调用跳转）、显式 continuation（后续
计算描述）或 CPS（延续传递形式）。每条提供的执行路径均须满足相同尾调用规则。

```text
fn sum_to(n: number, acc: number) -> number {
    if n == 0 { acc } else { sum_to(n - 1, acc + n) }
}

flow Main(n: number) -> number {
    return sum_to(n, 0)
}
```

非尾递归可以使用显式 continuation/运行栈，内存仍随计算增长；不得宣称任意递归都是
常量空间。Host 可以设置已公布的内存、指令数、时间和输出大小预算。预算耗尽报
`E-BUDGET`；取消报 Cancelled。语义不限制次数不等于承诺无限物理资源。执行机必须在
循环/递归安全点检查取消；并行模式下，不得因长纯计算长期占用所有执行机会。

## 5. 类型和纯求值

静态判断写作 `Γ ; S ⊢ e : τ ! ε`，表示在普通名称环境 Γ 和 Claim store 环境 S 中，
表达式 e 的类型为 τ、求值效果为 ε。Γ 保存普通值/函数名的类型和可用分析信息，
S 保存已声明 store 的身份；`Γ(x)=τ` 表示普通名称 x 的类型为 τ。编码函数 C
只用于第 16.1 节的数据编码，不用作环境记号。

| 效果 ε | 含义 |
|---|---|
| Pure | 仅使用纯核心计算，不执行外部操作 |
| ReadOnly | 外部读取；注册合约保证没有外部写入 |
| Write | 可能改变外部状态的操作 |
| Unknown | 无法证明实际操作集合或效果；按写入限制准入 |

Recorded 是 perform 观察的 journal 元数据，不是独立类型维度或传播到普通引用的标签。
Unknown 的启动/并发限制等同 Write；幂等性是 Host 合约，不是单独的语言效果等级。
Concurrent 是注册合约的 bool 标记；true 表示该 operation 允许多个调用重叠执行，
false 表示按第 7 节的 barrier 规则串行。ReadOnly 本身不隐含 Concurrent=true。
判定隐含一个本 run 固定的 operation registry Σ；registry 的 name/version/schema/
效果合约在 run 内不可变，执行时 capability 的撤销可导致明确拒绝，不改变既有 proposal。
`any` 为 V 的顶类型；`τ?` 表示 τ 或 null，`any?` 等于 any。list/object 注解约束外形，
不隐含所有内容均是 Data。源类型只有第 2 节列出的裸类型和可选类型；`list<object>` 等
表示数学描述，不是源语法。闭包值携带实际参数名/类型/arity/返回注解，裸 function
参数不静态声明这些信息；所有语言 fn 天生纯。无法静态检查的 arity/参数/结果必须在
调用时检查，不要求编译器从任意高阶函数推导完整签名。
普通函数参数按声明名称和源码参数顺序绑定，先位置后命名；重复、缺少、未知参数及 arity
不符报错。没有默认参数、隐式展开或可变参数。核心 range 的 arity 为 1/2/3。
静态可知的类型错误拒绝编译，any 带来的动态不符报 `E-TYPE`；Flow/adapter schema 不符
报 `E-SCHEMA`。没有隐式 string/number/bool 互转。

可静态确定的 callable、arity、参数名和声明类型错误必须在运行前拒绝；仅动态函数值/
any 遮蔽的信息留到运行时。动态普通调用统一按以下顺序执行：

1. 求值 callee，验证为语言闭包或第 3.1 节核心纯函数；原生 callable 报 E-TYPE。
2. 用实际签名检查实参结构、参数名、重复绑定、缺参和 arity；不符报 E-ARITY，尚不求值
   实参表达式。核心函数拒绝命名参数。
3. 按源码顺序从左到右求值实参，再按声明检查全部参数类型；失败时函数体不执行。
4. 执行函数体；正常结果通过返回注解检查后才发布。返回类型失败时函数体已经执行，
   结果不作为成功值发布。第 3 步已经发生的 perform 不因后续类型失败回滚。

map/fold 逐次回调采用相同规则。proposal 按 operation schema 绑定参数，不把 Host
operation 当作普通 callable；构造请求和真正执行的阶段按第 10 节区分。

下列规则中 op 是已解析 operation，args 是实参表达式集合，p 是 proposal 表达式。
`args : Data ! Pure` 表示每个实参均纯求值且结果属于 Data。横线以上为前提，以下为
结论。`Ops(p)` 是 p 可能指向的非空有限 operation 集合或 Unknown，其推导规则见本节下表。
`EffectOf(Ops(p))` 在已知集合上取注册效果的最大值，Unknown 时为 Unknown；
`ResultType(Ops(p))` 合并这些合约的结果类型，Unknown 时为 any。效果次序和类型
合并规则在本节后文定义。这些辅助函数不是源函数，也不是 Claim join。

```text
Γ(x) = τ
──────────────────────────────────
Γ ; S ⊢ x : τ ! Pure

Γ ; S ⊢ args : Data ! Pure
─────────────────────────────────────────────────
Γ ; S ⊢ effect op(args) : effect ! Pure

Γ ; S ⊢ p : effect ! Pure
─────────────────────────────────────────────────────
Γ ; S ⊢ perform p : ResultType(Ops(p)) ! EffectOf(Ops(p))
```

ε 在复合表达式中取上确界：Pure<ReadOnly<Write<Unknown。引用一个已完成 binding 是
读固定值，不重新观察其来源。因此一次 perform 的结果可以进入下一次 proposal。
proposal 内不能再次 perform、查询即时状态或执行随机；这些值须先单独绑定。
普通引用读取的是已固定值；S 中 store 名不是此标识符规则的对象。完整读取在所有 producer
成功终结后成为 Pure Data 节点，带第 9 节的隐式依赖；live snapshot 为
Recorded perform。这不把活动 store 误当成随时固定的变量。

Ops 是 compiler 的辅助信息，不是 effect[Ops] 源类型，取非空有限 operation 集合或 Unknown。
为保持独立实现对 parallel 的接受条件一致，本版仅使用下表的规范分析，不以私有优化
或更强高阶推导放行规范分析得到 Unknown 的 perform。

| proposal 值的产生路径 | Ops |
|---|---|
| 直接 effect op(args) | 已解析 op 的单元素集合 |
| 已分析 binding 的引用、括号 | 保留该值的 Ops；闭包捕获值也只保留已有信息 |
| if/match 的结果 | 所有候选结果集合并；任一 Unknown 则 Unknown，不按常量条件删分支 |
| value_block 的结果 | 在顺序局部环境中分析尾表达式 |
| list/object 的索引或字段投影 | Unknown，包括字面量容器投影 |
| fn/核心/高阶函数调用的返回值、carry 结果 | Unknown，不分析函数返回或跨轮状态 |
| 无以上信息的参数/输入/any 值 | Unknown；any 注解本身不抹去已有 binding 信息 |

容器保存 proposal 不会执行它；但投影出的实际 proposal 仍须在 perform 处分析。
例如令 x 为已绑定的 number，worker 是 `import python "example.worker" as worker`
声明的 alias，注册 `worker.read(number)->number`、version="1"、Concurrent ReadOnly。
片段 `p=[effect worker.read(x)]; q=p[0]; r=perform q` 合法顺序执行，q 的 Ops 为 Unknown，
perform 所属节点按 barrier 处理；放入 parallel body 则静态 E-PARALLEL。构造参数中的效果和节点内
各实际 perform site 另按 ε 合并，不能用结果 Ops 掩盖其中的执行。Unknown 集合的效果
为 Unknown，不要求全程序高阶推导。
闭包传回的实际 proposal 仍包含已解析 operation/version/参数，执行时按注册合约验证。
所有 operation 均在 perform 时授权；`any` 不豁免 Data 边界，也不豁免 fn 纯性。
类型判断不能把 Pure 的 function 参数当作原生 callable；所有语言 function 本身纯。
perform 的操作数须是 proposal，不能调用任意 callable。proposal 构造只解析命名空间/
operation 合约并固定参数；构造时的注册/schema 错误不表示已经调用 provider。
分支类型相同则保留；τ 与 null 合成 τ?；其他不同裸类型合成 any。fn/Flow 声明的结果
注解静态能验证时提前检查，否则运行时验证；不引入可书写 union 类型。不同 operation
的结果 schema 同样按此保守合并；Ops 为 Unknown 时静态结果为 any，执行仍验证实际
operation 的结果 schema。parallel 中每个 perform 必须按以上 Ops 规则证明
所有 operation 为 Concurrent ReadOnly，否则静态 E-PARALLEL；不以“也许运行时安全”
放行。普通顺序/图节点允许 Unknown，在运行时注册、授权、schema 检查。

对封闭纯环境使用小步机 `⟨e,ρ,κ⟩ → ⟨e',ρ',κ'⟩`：e 为当前表达式，ρ 为运行时的
不可变名称/值环境，κ 为 continuation，即当前结果之后还需进行的计算和返回检查。
`→` 表示一步归约，带撇号的量表示归约后的状态。操作数顺序、匹配次序和基础操作
都已固定，所以纯小步关系是确定的。

尾调用规则中的 f 是语言闭包，vs 是按声明参数顺序绑定完成的实参值序列；body(f)、
captures(f)、params(f) 分别为函数体、捕获环境和参数名序列，`params(f)↦vs` 表示
把参数与值对应绑定。尾调用归约为：

```text
⟨call f(vs), ρ, κ⟩ → ⟨body(f), captures(f)[params(f)↦vs], κ⟩
```

这里不追加 return continuation。复用 continuation 不能省略调用链中仍有效的返回
注解检查；实现可合并有限裸类型的约束，不按尾调用次数累积帧。只有确实需要在调用后
继续运算的非尾调用保留对应帧。

## 6. Flow、循环与位置

普通 binding 形成节点，引用已绑定值形成数据依赖；普通绑定不可赋值更新。fn 内的局部
求值不展开成全局图。if/match selector 是控制依赖，未选节点不执行；所有节点都必须
等自己的 gate 打开才进行语义求值，包括纯节点，不允许未开门的推测求值。
call/perform 语句、join、selector、循环 source 和 return 也各有对应节点；结构 block
不是独立计算节点，但有完成条件：其中所有选中节点均为 Completed 或正常控制 Skipped。
循环有入口、逐迭代入口和整体完成条件；分支值只在自己的作用域内可见。实现可融合节点，
但不得改变节点身份、依赖、record、外部顺序或错误边界。

break/continue 必须在顺序 for 的词法 body 中，作用于最近的该循环。可能执行控制转移
的更早 selector 未解决前，该迭代后面的节点不得开始语义求值。不适用的分支为正常
gate Skipped，不是 Error，不产生根错误，不计未执行求值的指令/调用预算；图构建、静态
检查及其真实内存成本仍可计入已公布预算，不承诺免费资源。静态语法/名称/类型检查仍
覆盖未选代码；动态域错误（如除零/fail）不能因常量折叠升级为未选路径的无条件错误。
优化不得改变错误位置、gate 或已公布预算的可观察规则。

match 按源码顺序选择第一个 pattern 匹配且 guard 为 true 的 arm。guard 必须为 Pure
bool，false 表示未选，Error 使整个 match 出错。type pattern 只使用非可选具体类型，
`_` 必须是该 arm 唯一的 pattern，但可有 guard。expression 无匹配报
`E-MATCH-NO-ARM`；statement 无匹配为空操作。不提供对象部分模式、绑定或解构；
安全的对象分派写法见第 12 节，guard 的缺键错误不会被当成“不匹配”。

`for x in source` 的 source 只接受 list，先求值一次；每次迭代是新作用域，上一轮全部
节点成功终态或正常控制 Skipped 后才启动下一轮。Error/Cancelled/失败级联 Skipped
终止循环。`continue` 关闭当前轮剩余未启动节点；`break` 还关闭最近 for 的后续入口。
已启动动作不回滚。`for { ... }` 为无穷顺序循环，可 break/取消/预算终止。

`for x in source parallel` 按 Host 并发上限展开。body 只能为 Pure、join 或 descriptor
声明 Concurrent 的 ReadOnly perform；禁止 Write、Unknown、转移外层图循环的
break/continue 和跨迭代普通 binding 引用。允许节点内 carry 及其 break e/next e，
也允许以节点内普通顺序 for 为目标的 break/continue，它们不转移 parallel 图循环。
其中所有 perform 仍须证明 Concurrent ReadOnly；不能嵌套局部 parallel。
多个迭代以自己的局部快照工作，不共享可变原生对象。

### 6.1. 携带值的顺序迭代

`for x in source with s = init { ... }` 是有结果的顺序表达式。先在外层求值 source 一次
并验证为 list，再求值 init 一次。每轮 x/s 是新的不可变 binding，s 初值为 init；
body 的局部 binding 不泄漏。正常路径必须恰好以 next e、break e 或有这些出口的
if/match 结束。next e 先完成当前迭代的全部局部语句，再以 e 为下一轮 s；break e 结束迭代
并返回 e；source 耗尽返回最后 s，空 source 返回 init。carry match 无 arm 为 Error。
`for with s = init { ... }` 无 source、无 x，持续 next，直到 break/发散/取消/预算。

next/break 的 e 必须 Pure，可引用已完成 perform 值；未选出口不求值。Error/Cancelled
停止迭代，不进入下一轮。普通 for 的 break 仍无值，continue 只用于普通 for；carry
用 next s 表示保持状态继续，二者不混用。s 的源类型为 any，转移值仍是普通 V，实际
操作及 fn/Flow 结果按类型规则检查；没有共享可变变量。

该表达式在当前图节点内由顺序执行机求值，和列表推导式一样不创建新的 global 节点。
body 可局部绑定/if/match/顺序循环/perform，但不得声明或 join store，不得包含局部
parallel。carry 可以出现在外层 parallel body 的节点内，合法性按第 6 节检查。
在纯 fn 内执行时 perform 非法；在 Flow 内允许。节点效果仍取全部可能 perform 的
上界，内部循环索引加入第 6.2 节定义的 EvalPath。fn 和 fold 回调保持纯性，外部操作
只在 Flow 的节点局部执行。跨迭代的证据可携带为 s 中的普通 Data，再由独立语句 join。

```text
import python "application.llm" as llm

flow Refine(initial: string) -> object {
    final = for round in range(10) with s = {draft: initial, score: 0} {
        out = perform effect llm.refine(s.draft)
        revised = {draft: out.text, score: out.score}
        if revised.score >= 0.8 { break revised } else { next revised }
    }
    return final
}
```

本例的注册合约为 `llm.refine(draft:string)` 返回含 text:string、score:number 的
Data object，version 固定为 "1"，descriptor 为 ReadOnly、Concurrent=true。每轮的 draft
来自前一轮的固定结果。效果类别和 Concurrent 标记由此合约声明，不能由 llm 名称推定。

### 6.2. 位置与调用身份

独立节点可以并行；同一节点内表达式、参数和多个 perform 从左到右执行。列表推导式的
source 为 list，元素在当前节点中按顺序求值；fn/map/fold 回调必须纯，不出现 perform。
列表推导式在 Flow 中可以含 perform，但并发只由图和显式 parallel for 引入。
推导式先在外层环境求值 source 一次，再以每个元素建立作用域。可选 `if p` 对每个
原始元素恰求值一次，必须为 Pure bool；false 跳过 body，true 才求值 body 并追加结果，
guard Error 终止推导式。即使 source 在文本上靠后，也先于 guard/body 执行。
EvalPath 在 guard/body 求值期间追加原始 source 的零基索引，不使用过滤后的结果序号；
source 求值不追加自己的元素索引。嵌套推导式按实际包围的 guard/body 从外到内追加。
推导式结果构造的额外工作须为 O(n+k)，n 为 source 长度、k 为保留项数，不计 guard/body
计算及值本身成本；可以使用不泄漏的内部 builder，最终值仍不可变。本版不要求 concat
具有摊还 O(1) 成本，fold+concat 可能二次复制，不推荐用它替代大列表的过滤推导式。

使用规范 token 的零基 ordinal 作为 StaticSite：去除注释/空白，保留换行分隔符后，从
文件首个 token 依次编号；没有私有 AST child 编号。显式 run_id 在 run 开始时固定。
token 化遵守第 2 节最长匹配，保留每个物理 NEWLINE（CRLF 为一个）；显式反斜杠续行
的反斜杠与对应换行均不产生 token，EOF 不编号。NEWLINE 即使被 parser 忽略也参与编号。
statement 的 node_site 为首 token；循环锚点为 for token；推导式锚点为它的 for token。
run_id 为 Host 分配的非空 UTF-8 string，同一 Host 身份域内不同 run 不得复用。

以下身份结构中的字段含义为：enclosing_loop_site 是包围图节点的普通 for 锚点，
index 是该循环的零基迭代索引；node_site 是该节点对应语句的首 token；
node_local_loop_site 是节点内携带值循环、普通顺序循环或推导式的锚点，
iteration_or_element_index 是它的零基迭代/元素索引；perform_site 是 perform token
的 StaticSite。Position 标识图节点，EvalPath 标识该节点内的动态求值路径，
JoinID 标识贡献，CallID 标识一次外部观察。所有 site/index 均为非负整数。

```text
Position = [(enclosing_loop_site, index), ..., (node_site, 0)]
EvalPath = [(node_local_loop_site, iteration_or_element_index), ...]
JoinID   = (run_id, Position)
CallID   = (run_id, Position, EvalPath, perform_site)
```

循环/推导式元素索引从零开始；锚点位于索引之前。Position/EvalPath 各用非负整数 pair
序列的字典序，pair 的两个分量都按任意精度非负整数比较，不按 wire string 或 binary64
比较；相同前缀时较短者在前。Position 从外向内记录每个包围循环，最后记录
自身 statement。循环 selector 的 Position 只含外层循环和自身锚点；它比子节点短。
例如两个兄弟循环锚点 2/5，body 节点 3/6，则 `[(2,3),(3,0)] < [(5,0),(6,0)]`，
前一个循环的所有迭代都在后一个循环之前。
EvalPath 从外到内记录包围当前求值点的节点内循环/推导式索引；没有这些结构时为空。
perform 使用其执行点的 EvalPath，节点内错误使用其发生点的 EvalPath。
重放执行相同静态 perform 及相同动态元素使用
相同 CallID；同一节点的不同 perform_site 不会冲突。纯 fn 内不存在 perform site。
两个相同参数的调用不会因此成为同一个 CallID；值 fingerprint 从不属于身份。
StaticSite/Position 是 revision 内的身份；空行/注释编辑可移动后续 ordinal，新 revision
不能用旧 snapshot id 对齐业务实体。跨 run 的业务 key 须另用显式 Data 字段。
这里的程序 revision 是将 CRLF 归一化为 LF 后，对完整 UTF-8 源字节计算 SHA-256
得到的源码摘要；注释也参与摘要。第 11 节恢复使用同一 revision 定义。

实现必须以相同源 token/run 输入产生相同 Position。取消后的恢复保留 run_id；程序改变
则不能把未完成旧 run 按新位置恢复。新 run 与旧 run 是不同命令，不能隐式代替恢复。

## 7. 图调度和错误

节点状态只有 Pending（待调度）、Running（正在求值）、Completed（成功且有结果）、
Skipped（未执行或剩余求值被关闭）、Error（本次求值失败）、Cancelled（被外部取消）。
后四种是当前执行尝试的节点终态。Skipped 的原因区分正常 gate/break/continue 与依赖
失败/取消。普通数据依赖必须为 Completed 才有值；终态本身不产生 null 值。

对待调度节点 n，Ready(n) 表示下列条件全部成立。表中的条件名称是调度谓词，不是
源函数；没有相应依赖或约束时，该条件为 true。公式中的 ∧ 表示逻辑“且”。

| 条件 | 含义 |
|---|---|
| DataDepsCompleted(n) | n 的全部普通数据依赖已 Completed，所需值可用 |
| GatesOpen(n) | n 的控制选择已确定为选中，且未被 break/continue 关闭 |
| ContextActive(n) | n 所属执行上下文未取消，n 未被当前错误截断排除 |
| AfterSatisfied(n) | Host 追加的完成约束满足，判定见本节后文 |
| OrderAllows(n) | 源顺序及 barrier/region 完成条件允许启动，判定见本节后文 |
| WorkerAdmission(n) | scheduler 有可用工作额度；不等同于第 10 节的 provider 资源许可 |

```text
Ready(n) = DataDepsCompleted(n) ∧ GatesOpen(n) ∧ ContextActive(n)
           ∧ AfterSatisfied(n) ∧ OrderAllows(n) ∧ WorkerAdmission(n)
```

运行时间与相同 Ready 节点的并行选择不属于语言值。无错误/预算/取消且资源最终可用
时，scheduler 与 provider permit 分配必须公平，不能永久饿死一个持续可运行节点。

顺序模式一次只运行一个节点，选择满足 Ready 的最小 Position。所有模式都保留 source
order：节点的效果为其全部可能 perform 的上界；只有每个可能 operation 都声明
ReadOnly + Concurrent 时，节点才是 Concurrent ReadOnly。Write、Unknown 和不可并发
ReadOnly 节点形成 barrier。barrier 等待所有更早的含 perform 节点成功或正常控制
Skipped；后续含 perform 节点不得越过仍未终结的更早 barrier。Concurrent ReadOnly
节点可以相互重叠；Pure/join 节点不因外部 barrier 本身被阻塞。

OrderAllows 必须包含尚未实例化的更早循环/分支可能产生的调用，不能只扫描当前 Ready
或已经创建的节点。跨 producer 循环，barrier 等该循环完成、或被正常 gate 关闭；若循环
尚有未开始迭代，就视为更早调用尚未终结。更早 barrier 之后的 parallel body 也受该
barrier 约束。判定使用 Position 的结构前缀和静态 producer region，适用于所有模式。
内部多个 perform 左到右执行、逐次准入；节点的保守上界可能减少并发，不改变外部次序。

Write 不越过更早的 Concurrent ReadOnly；不从 ReadOnly 名称推断跨调用独立性。
join 必须遵守自己的数据/control 依赖。循环或分支中的源顺序通过整体入口/完成约束
保守保持，不能因不同“区域”就越过更早外部动作。无限调用生产者后的 barrier 可永远
不就绪；需要这种跨越的程序应改变图结构，而不是由 scheduler 默默放宽。

Host 可增加 After 约束，目标必须是本 run 已解析的静态 site/动态实例，不接受不存在的
对象；对动态循环可约束整个 region 的完成。展开前后均检查环。After 目标 Completed 满足；同一关闭 gate 的
正常 Skipped 也满足；Error、Cancelled、其他 Skipped 使依赖节点
`Skipped(cause=after_failure)`。闭合控制门优先使本门下节点 Skipped，不伪造可用值。
静态检查必须拒绝数据、控制、完整 Claim 读取和隐式 barrier 合成后的必然依赖环。
运行时新增 After 或动态展开导致的环报 `E-AFTER-CYCLE`/`E-GRAPH-CYCLE`，不无限 Pending。
依赖 Error/Cancelled/失败 Skipped 的节点最终为对应原因 Skipped；正常控制 Skipped 的
数据目标没有值，若消费者未被同一个门关闭则报 `E-DEPENDENCY`。错误写入 cause 链。

根错误是在选中节点内实际求值产生的 Error；依赖失败传播和 region 失败汇总保留其
源 cause，不创建新的根错误。设 err 为一个根错误，err.node 是其所属图节点，
Position(err.node) 取该节点的位置，EvalPath(err) 取错误发生时的动态求值路径，
err.error_site 是产生错误的 token StaticSite；perform 错误使用 perform_site。
排序键定义为：

```text
ErrorKey(err) = (Position(err.node), EvalPath(err), err.error_site)
```

排序依次比较 Position、EvalPath、error_site，前两项使用
第 6.2 节的结构字典序，最后一项按非负整数比较。主错误取 ErrorKey 最小的根错误。

F 表示当前已知活动根错误的最小 ErrorKey，F.Position 表示该键的 Position 分量；没有
活动根错误时 F 为空，不做错误截断。只有所有可能 Position≤F.Position 的节点及其
必要前置依赖已经终结，F 对应的根错误才能确认为最终主错误。前缀中无穷计算可能
延迟确认，不能凭超时宣称较早节点不会失败；预算/外部取消有独立终结规则。

某节点集合的前置依赖闭包，是反复加入其成员执行所需的全部依赖，直到不再新增所得
的集合。K 为 Position≤F.Position 的结构前缀及其完整前置依赖闭包，包括数据、控制、
After、完整读取 producer 和 barrier/循环入口完成条件；未展开 region 的入口与未来
实例也必须保留。F 为空时 K 不限制调度。即使某前置依赖的位置大于 F.Position，也不得剪掉。
非 K 的 Pending 节点为 Skipped(error_cut)，不再准入新调用；K 中节点继续按 Ready
执行以判定更早错误。已经开始的 provider 调用接收/记录 outcome，不由内部错误自动
中断；非 K 的节点在当前调用完成后停止剩余求值。其他无外部动作的工作可协作终止。
再次出现更小错误时更新 F/K；trace 保留观察到的全部错误和实际调用。
error_cut 是当前活动根错误推导出的决定，不是独立永久事实。core 的一次执行中普通
根错误不撤销；durable 恢复中 E-UNCERTAIN 的活动性与 F/K 重算按第 11.1 节处理。

失败 run 的实际调用集合、部分 store 和完整 trace 可以随调度不同：较晚 ReadOnly
可能在较早错误被观察前已经启动。外部取消是协作请求，不承诺中断第三方动作。
Flow 成功需要所有选中的节点成功或正常控制 Skipped，输出类型正确；最后 return
不丢弃未使用的 perform。

### 7.1. 必须保留的调度信息与参考算法

compiler 除普通依赖边外，须输出 region 树：分支候选、循环入口/body/completion、
source 范围、可能的 perform/barrier/writer sites。树的父子关系对应源码中区域的词法
包围关系。整个 region 只有选定工作成功/正常 Skipped 且不再有未来迭代时才完成；
失败携带根 cause，不能当正常完成。不要求某种
具体 ready queue，但必须与以下算法的可观察规则等价：

```text
1. 建立静态 region summary、完整读 producer、数据/control/After/order 条件；拒绝环。
2. 开门/展开循环时加入实例和 Position，保留未展开 region 的完成 token。
3. 先传播关门与失败 Skipped；有根 Error 时更新 F 和前置依赖闭包 K。
4. 仅从允许的 Ready 实例选择工作；顺序模式选最小 Position，并行模式公平选择。
5. live perform 逐次准入并记录，join 原子 union；完成后更新 region token 和条件。
6. 重复；所有选中工作成功则输出；F 前缀终结则发布主错误并排空已启动调用记录。
   durable 的暂定错误只终结本尝试，最终失败确认还须满足第 11.1 节的恢复条件。
```

barrier 的更早 region 失败使相关调用 Skipped(order_failure)，不一直 Pending。
After 目标来自更后位置时，K 必须保留它；省略前置依赖闭包的位置剪枝算法
不是合规实现。等待依赖或许可的节点分别保持 Pending 或 Running，不使用额外生命周期状态。

## 8. Claim store：只 join 的信息结构

本节的 ∅ 表示空集。带底的 join-semilattice（join 半格）是具有最小元素、且任意两个
元素都有最小上界的偏序结构；此处最小元素是空贡献集合，join 是集合并。

`facts = claim()` 只能在 Flow 顶层声明。本规范中的 Claim 是信息记录，Claim store 才
是带底 join-semilattice，不承诺任意无限 join。一次成功的 `facts <+ value` 创建一个
原子记录 `(JoinID, C(value))`；C 为第 16 节规范编码。value
必须为 Pure RHS 的 Data，允许引用已经 Completed 的 Recorded binding。
声明在本 run 创建底值 ∅；所有 join/read/snapshot 均依赖声明完成，并且只能使用
源码中已声明的 store。普通变量名不能作为 join 目标，store 也不得重新声明或赋值。

核心不提供覆盖、删除、撤回、按最后抵达值覆盖或用户自定义 merge 函数。信息修订通过
新 Claim 表达，例如贡献一个包含修订类别、被修订贡献的 snapshot.id 和替代数据的
普通 object。业务当前值由明确的纯投影或 Host 合约选择，投影不必保持单调。

设 A 为所有可规范编码的 Claim 原子的集合，每个原子是一个 (JoinID,C(value)) 对。
L 是由 A 的全部有限子集组成的集合，写作 `L=𝒫_fin(A)`；store 状态属于 L。
下式中 x、y 是 store 状态，a 是 A 中的原子；∅ 为空集，⊆ 为子集关系，∪ 为集合并，
⊑ 为信息偏序，⊔ 为两个状态的最小上界；iff 表示“当且仅当”。更新式中的 := 表示
Runtime 内部状态更新，不是源变量赋值：

```text
bottom = ∅
x ⊑ y iff x ⊆ y
x ⊔ y = x ∪ y
join(store, a): store := store ⊔ {a}
```

L 在有限 join 下是带底的 join-semilattice。结合、交换、幂等以及最小上界由集合并直接
成立。无限执行不要求 store 在有限时间内达到最终值；本规范不要求物理内存容纳无限集合。

同一 JoinID/同一 value 是重复投递，只保留一项；相同 value/不同 JoinID 是两个逻辑
事件。相同 JoinID/不同 value 是重放身份冲突。为使 join 仍是总函数，底层保留双方，
冲突标记只增不减；引入冲突的 join 节点及此后的 snapshot 投影报 `E-CLAIM-CONFLICT`，
不得由第一个抵达者胜出。已 Running 的其他贡献可以继续留下记录；Flow 无法成功。
业务矛盾（不同 JoinID 给出不同观察）只是两个事实，不等于身份冲突。
原子交付分为新原子、相同原子确认、变值冲突登记。前两者成功；第三者在同一线性化点
保留变值与冲突标记、使触发交付失败。较早交付可能已经成功，不能改写其历史 receipt；
运行错误账本在该 JoinID 的 source 登记根错误。完整读与此后 live 读均报冲突；过去
成功快照仍是原固定值。terminal snapshot replay 复用其历史值，不重新观察 store。

store 的作用域为本次 run。成功 join 可以立刻被 live snapshot 看到，writer 随后的
Error 不回滚已加入信息；这不代表 Flow 可以成功。跨 run 持久化是 Host storage 合约，
跨 run 的旧信息必须以显式 Data 输入或 effect 结果进入，不能自动污染本 run 的 store。

## 9. 随时读取和完整读取

store 只增长，并不意味着读取结果与调度无关。核心定义两种读取，不能隐式混用：

| 形式 | 返回值 | 图和重放语义 |
|---|---|---|
| 普通表达式中的 `facts` | 全部生产者完成后的 Data snapshot | 加入完整依赖；成功后固定 |
| `perform effect claim.snapshot("facts")` | 原子读取当前已提交原子的 Data snapshot | ReadOnly + Recorded；不等未来 writer |

snapshot 是 list，每项为 `{id:string, run_id:string, position:list, value:Data}`，按
Position/JoinID 排序。position 为第 16 节 wire pairs，因而可结合 run 的源码/revision
定位贡献。id 是第 16 节 JoinID 编码的 SHA-256 十六进制，排序用原身份 tuple，不能
用哈希或到达时间替代源位置排序。返回值不可变，不是随 store 增长而改变的 live handle。
构造 snapshot 或按摘要索引 journal 时，若检测到不同 canonical 身份摘要相同，报
`E-ID-COLLISION`，保留原身份，不合并、不任选其一。正式存储的比较键不能只保存摘要。

`claim.snapshot` 的第一个参数必须是当前 Flow 顶层 store 名的 string literal；compiler
解析为当前 run 的 store identity。操作不允许猜测其他 run/store，不能按任意 string
查询其他 Session。这项 Runtime operation 随语言提供，但使用普通 effect/perform 通道。
claim.snapshot 无需 import，是保留内建 operation 命名空间，直接解析为
`["lip","claim","snapshot"]`；claim 不属于 import alias。不存在可导入的 lip 模块类型。
其唯一参数名为 `store`，result 为上述 list，descriptor 为 ReadOnly、Concurrent=true、
Recorded；无额外 provider 资源。纯 fn 不能构造指向 Flow store 的 snapshot proposal。
snapshot 与 join 各自在 store 上有原子线性化点，不能返回半条记录或半次 union。

完整读取必须位于所有可能 writer 的外层 producer statement 之后，并且不位于包含
writer 的循环体内；否则报 `E-CLAIM-ORDER`。空 store 的完整值为 `[]`。未选 writer 或
break 跳过的迭代合法地没有贡献；writer Error/取消/失败级联 Skipped 使完整读取和 Flow
失败，不能把失败当空集合。
判断以 store 的所有静态 writer 和包围 writer 的最外层 producer statement 为准，
分支互斥不会绕过此限制。读取可以位于后续顺序循环内，只要所有 writer 在该循环之外
且源位置更早。生产者集合含其依赖失败的原因，不能把未实例化的失败 region 当作空。
在表达式、list/object、fn 参数和 return 中使用 store 名均表示这种 Data 完整读取；
`<+` 左侧、claim 声明和 snapshot 的专用 string 参数位置才解析为 store 身份。

即时 snapshot 可以进入普通 if/match、fn、proposal 和后续 join；其来源 journal 必须
保留。它可能改变执行了哪些分支、调用或贡献，因此这种程序具有显式记录的时序观察。
重放恢复的是已记录观察，不承诺不同 live 调度得到相同输出。
即时 snapshot 不提供按源码前缀隔离的读取合约。即使读取位于顺序循环内，较后位置的
join 也不受外部 barrier 本身阻塞，可能在读取之前抵达。第 13 节的确定性命题排除
所有即时 snapshot；局部迭代状态由 carry 显式携带。

```text
import python "application" as worker

flow Collect(items: list) -> object {
    facts = claim()
    for item in items parallel {
        result = perform effect worker.process(item)
        facts <+ {input: item, output: result}
    }
    seen = perform effect claim.snapshot("facts")
    return {complete: facts, seen: seen}
}
```

本例注册 `worker.process(item:Data)->Data`、version="1"、ReadOnly、Concurrent=true。
`seen` 可以先于全部 writer 结束；`complete` 必须等待全部 writer。

即时 snapshot 只观察其线性化点已提交的原子，不能判断未来是否还会产生贡献，也不会
等待某个业务谓词变为 true 或自动重新求值。完整读取提供等待全部 producer 的合约；
核心不提供额外的条件等待或隐式固定点求值机制。

## 10. Go/Python 调用协议

`import` 声明依赖命名空间/alias，不执行模块代码、不联网安装、不授予权限。省略 alias
时取路径最后一个 `/` 或 `.` 后的片段；若不是合法 id 则必须显式 as。alias 在文件中
唯一。核心中的 alias 只用于 operation 解析；本节的通用适配配置可将其值用法降为
模块资源引用的 perform，不能将原生模块本体直接传入 fn。
Go operation 由 Host 注册函数/method 或生成的 adapter 提供；Python 的 module/path/
method 通道可通用映射环境中安装的包，不把包名单写进编译器。

请求中的 CallID 是第 6.2 节的调用身份，operation 是本节定义的规范 operation 名，
contract_version 是注册合约版本；positional 是固定 Data 实参的有序 list，named 是
命名实参到固定 Data 值的 object。响应中的 ErrorData 结构为
`{code:string,message:string,details:Data}`。所有调用使用以下协议结构：

```text
Request  = {CallID, operation, contract_version, positional: list<Data>, named: object}
Response = Result(Data) | Rejected(ErrorData) | Uncertain(ErrorData)
```

operation 合约至少声明 name/version、参数/结果 schema、ReadOnly/Write/Unknown、
Concurrent、capabilities 和资源需求。调用前验证，不能从名称推断纯性、并发、幂等性或
授权。命名参数在位置参数之后、每名一次，并按 schema 解析；固定 proposal 不保留原生
对象本体；需要对象身份时只保留经声明的资源引用。构造 effect 不调用 provider、不写
intent、不消耗调用预算。
规范 operation 名由 `[language, import_path, member_path]` 唯一解析，其中 member_path
是 alias 后以 `.` 连接的 ASCII 成员名。内建 snapshot 名为 `["lip","claim","snapshot"]`。
这里 language 取 import 的 go/python/host 标记，import_path 是解码后的路径 string，
member_path 是 alias 之后全部成员名的点分路径。普通 operation 必须是已导入 alias
加至少一个成员名；若 worker 是已导入 alias，`effect worker(0)` 为静态 E-OPERATION。
模块本身不能被隐式调用。未声明 alias、空/未知 member、未注册
version、错误内建成员均静态 E-OPERATION；直接 operation 合约由本 run 固定 registry
解析，动态 proposal 执行时仍验证实际完整身份。claim 只提供 snapshot，不能被 alias
覆盖，也不能任意扩展内建命名空间。
name/version 是 Data string 组成的合约身份，不以 Go 函数地址或 Python repr 代替。
schema 至少能表达 Data 子类型、命名字段、位置参数顺序、required/optional 与返回外形；
没有注册 schema 的调用拒绝。adapter 若无法分析 native signature，可显式注册通用
Data 参数/结果 schema 并声明 Unknown；不能默认为 Pure。
contract_version 为 Host 声明的非空 string，任一影响调用/恢复语义的变化必须换版本。
具体 SDK、传输、package 安装与 schema 文件格式由 Host 实现，Request/Response 的
语义、Data 约束和身份编码是跨实现共有合约。合规测试使用同一注册合约和输入。

perform 的 live 流程为：解析 proposal → 注册/schema 检查 → capability/审批/预算/取消
检查 → 原子 Acquire provider 资源 → 写 intent（恢复合约启用时持久化）→ 调用 provider
→ 记录 provider 外部事实并复制/验证结果 → 记录 terminal outcome 或 uncertainty
→ 释放资源 → 成功时对依赖发布 Completed 值。
每次调用需有独占的 CallID journal 执行权，相同身份并发投递只能有一个执行者，其余
等待或读已记录 outcome。准入拒绝不得调用 provider；若审批需等待，节点保持 Running，
cause 标记等待原因，取消仍可结束。Host 授权覆盖 operation 和资源 scope，不只覆盖包名。

多 perform 节点仍从左到右执行；每次真实 provider 调用单独准入，不让节点提前占用后续
call 的全部 provider 资源。所有资源一次原子取得，成功/失败/取消均释放；terminal replay
不获取 provider permit。worker 调度额度与 provider 资源不同，不能因等待 permit 耗尽
worker 而阻止释放许可所需的工作。
资源需求是本次调用的固定 `(resource_name,nonnegative_units)` 向量，必须整体满足或
整体等待/拒绝，不能部分占用后等待其余资源。预算/并发额度由 Host 公布；释放与
journal 清理不能依赖已经关闭的 Flow control gate。无资源需求的调用不申请虚假许可。

合约声明为失败通道的 Go error/Python exception 为节点 Error；Go panic/worker crash 由 adapter 分类为
明确失败或 Uncertain。普通 provider Rejected 为 E-EFFECT-REJECTED；Host 确认外部动作
完成后的转换/schema fault 保留 E-VALUE/E-SCHEMA，Uncertain 为 E-UNCERTAIN。
结构化 `{ok: false, ...}` 若属于返回 schema，则仍是普通 Result，可做业务分支。
terminal rejection 只说明操作
以已知失败结束，不代表此前没有产生外部变化。Write 调用中 crash、取消失联、超时或
返回转换失败，如无法证明外部 outcome，必须标为 Uncertain；不得记成“从未执行”。
若已知外部动作完成但返回值不符合 schema，必须把“provider 事实”和“LIP 验证”分层
记录；不能把 provider_fact 记成 Unknown 或没有动作，也不能发布不合法值。Host 内部
记录至少区分：

```text
ProviderFact = KnownSucceeded(receipt:Data) | KnownRejected(receipt:Data)
             | Unknown(details:Data)
Validation   = Passed(Data) | Failed(ErrorData, raw_data?:Data) | NotApplicable
TerminalKind = result | rejected | known_fault
```

| provider 事实 / 验证 | 对语言的结果 | journal |
|---|---|---|
| KnownSucceeded / Passed | Result，节点 Completed | terminal result |
| KnownRejected / NotApplicable | Rejected，节点 E-EFFECT-REJECTED | terminal rejected，保留可能已有的外部变化 |
| KnownSucceeded / Failed | Rejected，节点 E-SCHEMA 或转换 E-VALUE | terminal known_fault，保留完成 receipt 和验证错误 |
| Unknown / NotApplicable | Uncertain，节点 E-UNCERTAIN | 非 terminal intent + uncertainty |

Response 的三种结果中，known_fault 用 Rejected(ErrorData) 承载；上述分类是可信 Host
验证记录，不能仅凭 provider 自称的错误码推断。receipt 至少保留动作已完成的证据描述，
不要求 provider 必有交易号；raw_data 仅在已转换为合法 Data 时可选保存，禁止存 native
指针/可变对象。known_fault 重放同一验证错误、零新增 provider 调用；不可对账为成功，
也不可重做原写入来修复返回格式。业务补偿或修正必须是显式的新操作。

Go adapter 处理 context、有限值、struct/string-keyed map/list、声明的多返回值和 error；
Python worker 处理相同 Data、exception、call ID、deadline、取消和有限队列。
协议的 number 始终是 binary64；传输即使用 JSON，也按第 3 节解码，不能依赖 Python
默认 int/float 猜测。默认 native 参数映射为 Go float64/Python float；需要 Go int 或
Python 整数参数时，adapter 合约必须声明并验证安全整数转换规则。bytes 不属于 Data，必须通过
声明的 string 编码（如 base64/hex）或资源引用传输，不能隐式采用平台字符集。
Python tuple 可以按声明映射为 list；set、native callable、array/DataFrame 等必须由
adapter 给出显式 Data/资源引用转换，不能靠宿主默认 str 或不可检验 pickle 混入 Data。

长期对象/大数组可以用 Host 管理的资源引用：引用是 Data object 中的不可猜测 token，
只由认可该 schema 的 operation 解释，且绑定 Host/worker generation、类型、寿命和
授权 scope。Host 必须声明寿命为单次 run 或显式会话；会话允许引用跨成功 cell 使用，
不回放旧 cell 以重建对象。纯计算只能处理这个 object，不能访问原生对象。引用不等于
权限，scope 不符、过期、worker 重启或未迁移时必须拒绝；Host 提供 release 和生命周期
结束清理。Go 包初始化状态可具有进程寿命，不等于资源引用仍然有效。
持久重放不得把已失效原生对象 token 当作可用结果，须重建、拒绝或显式 reconcile。

```text
import go "crypto/sha256" as sha
import python "numpy" as np

flow Statistics(xs: list) -> object {
    mean = perform effect np.mean(xs)
    digest = perform effect sha.Sum256(str(xs))
    return {mean: mean, digest: digest}
}
```

本例的 Host 合约固定 version="1"：np.mean 接收有限 number list，返回 number，并
声明 Concurrent ReadOnly；sha.Sum256 接收 string，由 adapter 将 UTF-8 bytes 传入
Go 函数，把 32-byte 返回数组编码为 64 位小写 hex string，并声明 Concurrent ReadOnly。
这些映射是本例的显式注册合约；通用 adapter 不得将它们作为其他操作的默认转换规则。
第三方 callback、stream、事务和大型对象由 adapter 合约承接，不增加第二套 LIP 计算语义。

工具可提供通用库适配配置，例如 `NewNativeHost`。该配置通过 Go 导出类型信息生成
adapter，或通过持久 Python worker 解析模块与对象，不限制包名单。它在核心检查前将
直接调用、属性读取、方法、索引及资源转换降为注册的 proposal/perform，并声明 Unknown；
不把 native callable 变成纯 fn，不绕过授权，也不推导 Concurrent。callee 解析和可获得的
签名/arity 检查先于实参求值；无法获得原生签名时由实际调用检查，adapter 须说明这一
限制。仅构造 effect 不执行这些原生检查或加载代码。

这一配置可把 import alias 的值用法降为取模块资源引用的 perform，核心中的 alias
仍只用于 operation 解析。import/补全不执行包初始化；Go 导出发现允许使用工具链与
module resolver，Python importlib 在首次授权的实际操作时运行。CLI、交互会话与生成
程序必须使用同一转换和调用边界，不能只在 REPL 中模拟库行为。

本实现通用配置的转换合约为：Go number 按目标签名验证整数范围与精确转换；string
映射到 Go `[]byte` 时明确采用 UTF-8。Python number 的安全整数值转为 int，其他值转
为 float；这不改变 LIP number 的 binary64 身份。普通 Python list/tuple/字符串键 dict
映射为 Data；Go 容器/结构体和其他原生对象保留为资源引用。`native_data` 经 perform
取得有限无环 Data 快照，`native_release` 释放引用；不能以 release 代替对象自身的
外部关闭方法。跨运行时的数据须显式取快照或编码，不能直接解释另一运行时的引用。
进程取消或失联不得自动重试可能已完成的写入；重置或关闭须使原有引用失效。
Go 多返回值中的末尾 error 按失败通道解释；唯一的 error 返回值保留为原生资源（nil
为 null），使错误构造与比较操作可以组合。应用可通过显式合约声明其他错误映射。

授权和数据边界不等于执行沙箱。进程内 Go、Python worker 仍具有其 OS 权限；不受信任
库须由 Host 使用进程/容器隔离。effect/perform 不会使任意 native 代码自动变安全。

## 11. 记录与可选持久恢复

每个 run 至少在本次运行中记录所有 perform 的参数 fingerprint、result/rejection/
uncertain，尤其是 Claim snapshot、时钟与随机输出。Recorded binding 只有记录就绪后
才能对依赖可见。核心不承诺进程崩溃后的恢复；启用 durable profile 时，必须实现本节的
全部持久规则，不能只持久化外部写入而漏掉上游观察。

核心要求的 C/wire identity 在第 16 节定义，即使不启用 durable profile 也须遵守。

本节使用以下恢复术语：intent 是调用 provider 前登记的请求意图；checkpoint 是可恢复
节点、控制门、贡献和对应 journal 边界的完整快照；journal 高水位标识该快照已包含的
记录边界。恢复 epoch 是本 run 每次取得独占执行权的尝试编号，fencing 是拒绝失去
该执行权的执行者继续提交的检查机制。epoch 不加入逻辑 Position/CallID。
Provider DedupKey 是由 provider 合约认可的稳定去重键，不由 LIP 参数相等推导。

durable profile 固定并持久保存 run_id、输入、精确程序 revision 与所用 Host contract
version。程序 revision 使用第 6.2 节的完整源码摘要定义；包括注释编辑也算新 revision。
run 输入/程序/合约登记必须先于任何 durable intent。一个 run 同时只有一个受 fencing
保护的恢复执行者；旧 owner 不能在新 owner 已恢复时继续发布 journal/outcome。
不兼容 revision/version 的恢复报 `E-REPLAY-DIVERGENCE`。已完成 run 的普通 Data 存档
不因新源码失效；新 run 使用新 revision，Host 持久 state 的 schema/迁移由 Host 负责。

Journal 以完整 CallID 为键。version 对应 Request.contract_version；canonical_arguments
保存 C([positional,named]) 的完整字节；provider_fact、validation、terminal_kind 使用
第 10 节的定义。result_or_rejection 保存已决响应，uncertainty 保存未决事实说明：

```text
Journal[CallID] = {operation, version, argument_fingerprint,
                   canonical_arguments, intent, provider_fact?, validation?,
                   terminal_kind?, result_or_rejection?, uncertainty?}
```

| 旧记录 | 恢复行为 |
|---|---|
| 参数/operation/version 不同 | `E-REPLAY-DIVERGENCE`；禁止创建新身份掩盖变化 |
| 已有 terminal result | 复用已记录值，provider 不再调用 |
| 已有 terminal rejection | 再次呈现同一节点 Error，provider 不再调用 |
| 已有 terminal known_fault | 复用完成事实和同一 E-SCHEMA/E-VALUE，provider 不再调用 |
| 只有 intent，ReadOnly 且 contract 允许 | 可重新读取，再记录；旧调用没有已发布的结果 |
| 只有 intent，Write 有 provider 支持的稳定 DedupKey | contract 明确证明安全时可恢复调用 |
| 只有 intent，其余 Write/Unknown | fail closed，先查询外部事实 reconcile；不盲目重试 |

此处恢复调用复用原 CallID。结果必须持久记录后才发布，所以一个尚无结果记录的读取
不可能合法地被下游已持久写入消费。Provider DedupKey 不替换 CallID，也不由参数相等
自动推导。Exactly-once 的外部世界不是语言保证。
Uncertain 保持非 terminal，ReadOnly 自动恢复同样须有明确的无外部写入和可重执行合约。
含失效资源引用的 terminal result 可以再次呈现记录，但不得通过验证为有效原生资源；
需要资源时由 Host 显式恢复同一资源身份/修改 recovery 合约，或终止该 run。

### 11.1. 节点状态和调用记录是两个状态机

| 当前执行尝试中的节点 | journal 状态 | 含义 |
|---|---|---|
| Pending/Running | absent 或 intent | 尚无发布的结果，不能从 Running 推断 provider 未执行 |
| Completed | terminal Result | 成功值已记录，允许依赖消费 |
| Error(E-EFFECT-REJECTED) | terminal Rejected | 已知失败，重放同一 Error |
| Error(E-SCHEMA/E-VALUE) | terminal known_fault | 外部动作已完成，结果验证失败；重放错误 |
| Error(E-UNCERTAIN) | 非 terminal intent + uncertainty | 本次求值失败，外部事实尚不确定 |

Uncertain 非 terminal 指 journal，不是否认节点 Error 在本执行尝试中终结。Host 可报告
该尝试失败且有未决调用；reconcile 后 resume 建立新的 Pending/Running 节点实例，
保留逻辑 Position/CallID，按记录结果求值，不把旧终态节点原地改为 Completed。
durable 必须保存根错误账本与尝试历史，区分永久根错误和
关联未决 CallID 的暂定 E-UNCERTAIN；status 区分“最终失败”与“尝试失败、仍可恢复”。

恢复的活动错误与截断按以下规则计算：

1. 普通根错误、terminal Rejected 和 known_fault 为永久事实，不因恢复遗忘；每个错误
   保留完整 ErrorKey/类别/details。永久错误存在时 run 不可能恢复为成功。
2. E-UNCERTAIN 仅在对应 outcome 未决时是活动根错误。可信 Result 使它退出活动集合；
   可信 Rejected/known_fault 在原调用身份和错误位置登记相应永久根错误。历史仍保留。
3. 由活动根错误重新计算 F 和前置依赖闭包 K，不把旧 error_cut 当永久墓碑。
   仅因已解除暂定错误而被 cut 或依赖失败跳过的工作，可以在新恢复 epoch 建立 Pending
   实例，复用原 Position/CallID；其他活动错误仍限制它。正常未选 gate 不因此重开。
4. 所有已记录 terminal outcome 及已开始调用的外部事实保留；较晚调用已记下的 Rejected
   仍是永久根错误。恢复不改写旧节点终态，也不抹去真实发生的调用。
5. 当前 F 的前缀或必要依赖若仍可能因暂定错误解决而重新求值，不能确认最终主错误。
   永久错误只保证最终失败，不提前保证主错误已定；已确认的最终失败不得恢复为成功，
   其无关未决动作仍须排空/对账。

以下恢复例是一个抽象图，不是 LIP 源程序。四个节点在同一 run 内，位置使用第 6.2 节
的整数 pair 结构；uncertain_call 和 later_call 的 operation 均为 Concurrent ReadOnly，
check_a/check_b 为独立纯计算。没有额外依赖或 barrier，其他更早节点均成功：

| 节点名称 | Position | 当前尝试的状态 |
|---|---|---|
| uncertain_call | [(5,0)] | Error(E-UNCERTAIN)，调用 outcome 未决 |
| check_a | [(6,0)] | Skipped(error_cut)，尚未求值 |
| check_b | [(7,0)] | Skipped(error_cut)，尚未求值 |
| later_call | [(8,0)] | 调用在截断前已开始，随后记录 terminal Rejected 和永久根错误 |

对账 uncertain_call 为 Result 后，其暂定根错误退出活动集合，later_call 的永久根错误
保留；重算 F/K 使 check_a/check_b 可在新 epoch 求值。两者和更早前缀均成功时，
later_call 是主错误；若 check_a 产生 E-FAIL，check_a 是主错误。两次恢复均复用
已记录调用的 outcome，不重做 provider 调用。
checkpoint 必须保留这些根错误和关联 CallID；从旧 cut 恢复时按活动账本重算，不能
把被暂定错误跳过的节点当作永久完成或把永久根错误丢掉。

启用 durable profile 的 Host API 最小契约为：

| API | 最小行为 |
|---|---|
| begin | 分配唯一 run_id，持久固定程序、输入、contracts，再允许执行 |
| resume | 在同一 run_id 上取得独占恢复权，复用观察与请求身份 |
| status | 可区分 Running/成功/失败/有未决 intent/已放弃，并列出每个非 terminal intent |
| reconcile | 在授权与审计下，把未决 CallID 变成可信 provider 事实和验证记录，得到 Result/Rejected/known_fault |
| abandon | 仅当全部 intent 为 terminal 才记录放弃；不删除 journal 或宣称动作已回滚 |

begin/resume/reconcile/abandon 对重复相同请求必须幂等，对并发互相冲突的请求必须有
持久 compare-and-set/fencing；status 在 crash 后仍能判断已落盘 outcome。reconcile
不得覆盖任何 terminal result/rejected/known_fault；相矛盾的恢复证据报错，不能让两个
结果分别被消费。相同证据重复提交只确认原记录；known_fault 是已决 outcome，不能
因为未发布成功值而被当作可重试 intent。abandon 将它计为 terminal。

Claim store 从已记录的输入/观察重建：要么从空 store 重算，要么按完整 checkpoint 恢复
且不重算已恢复 contribution；不得把残缺旧 store 与重新计算的重复事实错误相加。
集合 join 去重仍适用于正常重复投递；同一 JoinID 的变值必须失败，不能静默删记录。
Claim 贡献记录不必逐条持久，未成功 run 的 partial store 不是已提交事务。可丢弃整个
partial store 并确定性重算；若使用 checkpoint，则必须同时保存所完成节点/门和与之
对应的 journal 高水位、根错误账本和 epoch；错误 cut 按第 11.1 节重新推导，不能只
保存 cut 后的残缺 producer 集合。journal 本身不得随 partial store 丢弃。
完整读取的缓存同样须基于重建后的完整 producer 状态，不能复用残缺快照。

replay divergence/未决 intent 不提交成功输出，既有外部动作不回滚。Host 只有在所有
intent 已有可信 terminal outcome 后才能 abandon 旧 run；重新发起同一业务命令必须由
Host 显式处理旧外部事实/DedupKey，不能靠换 run_id 规避未决写入。

## 12. 核心范围与库、Host 合约

核心语法在第 2 节，核心纯函数表在第 3.1 节。LIP 源码按单文件组织纯 fn；业务投影
通过同文件函数定义或显式源代码打包共享，不提供 LIP 模块加载。扩展能力分工如下：

| 能力 | 在本规范中的位置 |
|---|---|
| 持久状态、Session 与状态事务 | Host storage 合约；语言传入/输出普通 Data |
| retry | Host operation 的显式重试合约；业务重试可用 for 和 `{ok:false}` 数据 |
| 多轮计算 | 纯递归/尾调用/fold；有外部操作时用普通 for 或携带值 for 表达式 |
| max/min/set/any/all/count/bag/log/latest | 对完整 Claim snapshot 的纯函数/库投影 |
| 撤回、active view | 新的修订事实 + 明确投影，或 Host 持久 store |
| 阈值等待 | 不属于核心；即时 snapshot 只读取一次，完整读取等待全部 producer |
| trace、provenance UI、缓存、审批、限流 | Runtime 产品/策略，仍不得违背核心边界 |
| 所有外部调用 | 统一 effect/perform；通用库适配配置先把直接库调用与属性访问降为 perform（第 10 节） |

count/bag 的事件 identity 必须保留；完整 snapshot 投影中不同 id 的相同 payload 分别
计数，同一 id 重投只算一次。log 使用 Position 排序；浮点求和使用明确的左 fold，
不对普通 binary64 加法假定结合律。不同业务 reducer 无须成为不同可变槽位。
latest 必须按明确规则定义：完整 snapshot 源顺序的最后一项，或业务时间/字段和总
平局规则；不能指“最后到达”。统一 UTC、同精度的 ISO 文本可按 string 顺序比较；
任意时区/精度的日期文本不自动获得时间先后语义。对象/集合投影可用 keys/has/put/fold。

对象分派使用类型 pattern 和短路 has guard 验证字段，再显式取值：

```text
fn payload(x: any) -> any {
    match x {
        object if has(x,"kind") && x.kind == "a" && has(x,"payload") => x.payload,
        _ => null
    }
}
```

has 不证明字段值符合业务 schema，后续调用仍按声明检查。普通纯 for 保留用于逐项
校验和传播被丢弃调用的错误；需要累积值时用 fold/carry，不能靠遮蔽 binding 更新状态。

实现可以采用解释器或代码生成，并配置并发上限 1 或更高。durable profile 是独立的
合规选项。并发上限 1 仍须实现全部 core 语义：parallel 静态检查、region/
依赖/barrier、最小错误与 K、Claim 和身份，不可把 parallel 改写成遇错即 break 的普通
顺序 for。Host After 默认可为空；一旦接受约束就必须执行本规范的检查和调度规则。
每条提供的执行路径都须验收相同 core 规则。

核心不提供确定性的并行提前熔断或用户定义的格容器。需要累计三次业务失败后不再启动
下一项时，用顺序 carry 计数，并把业务失败定义为 schema 中的普通 Result 数据。
并行版本完整收集后判定，或使用具有时序观察语义的即时 snapshot。

parallel body 不允许 Write。推荐并行 ReadOnly → join 收集 → 完整读取 → 顺序 Write：

```text
import python "application" as worker
import go "application/storage" as storage
flow Save(items: list) -> object {
    facts = claim()
    for item in items parallel {
        result = perform effect worker.compute(item)
        facts <+ result
    }
    results = facts
    for record in results { perform effect storage.save(record.value) }
    return {saved: len(results)}
}
```

本例注册 `worker.compute(item:Data)->Data`、version="1"、Concurrent ReadOnly；
`storage.save(value:Data)->null`、version="1"、Write。业务失败若需要参与计数/分支，
合约须将其编码为例如 `{ok:false}` 的合法 Result。运行时 Error 不自动变成 Claim，
也不被隐式捕获；adapter 将原生异常转换为业务 Result 时，须由 Host 合约明确声明。

## 13. 确定性与保证边界

在基础纯操作正确、adapter 合约诚实、值边界被执行的条件下：

**固定外部应答下的逻辑结果确定性。** 固定 run_id、输入、源码、registry/schema/
授权、After 约束与初始 journal；不含 live claim.snapshot；无外部取消、时限/预算失败、
crash、失效资源或变值重复投递。每个实际选中节点/调用有限终结，scheduler 公平。
令 R 为一个固定的响应映射：键为完整 CallID 和 canonical 请求，值为该调用的 Host
应答；所有正常重复投递携带相同值。
在这些前提下，所有合法调度得到相同成功 Data 输出，或相同 ErrorKey/类别/语义 details
的主错误。成功执行时所求值的 CallID 集合相同；失败执行时**不保证**实际已发出调用
集合相同，也不保证 partial store、错误之外的 trace 或动作时间相同。

固定 R 是比较执行的数学前提，不声称远程服务实际永远返回同值。若不满足有限终结
前提，较早发散节点可能使错误前缀无法确认。Host 引入的新 cancel/budget/版本变化是
新的输入条件，不归入此定理。依赖错误传播不构造新的更小根错误。
未开 gate 的节点不求值，因此不产生动态 Error，不更新 F/K 或触发 error_cut；未选
if/match 表达式分支同样不求值。编译期错误不属于本定理对合法程序的运行结论。

1. 纯表达式的小步归约至多有一个下一状态；若终止，结果唯一。无限递归可能发散。
2. 固定输入的有限无环纯图，在公平执行且无预算/取消干扰时，所有合法调度得到同一值。
3. 对某个 store，令 B 为本次 run 中成功贡献组成的有限原子集合。B 固定时，重复投递
   和不同交付次序均得到最终 store 状态 B，因此最终 snapshot 和确定性纯投影相同。
   相同 identity 的变值呈现相同冲突，不选择抵达者。
4. 无 perform 的源程序不能调用 Host；没有完成准入的 perform 不能调用 provider。
5. durable profile 在固定 journal 下复用原观察；未决 Write 不会因恢复自动再次执行。

即时 snapshot 参与控制选择或下游 join 时，产生的贡献集合 B 也可能随调度变化。
journal 保存并重放实际观察，不消除不同 live 执行之间的差异。store 只增长不保证
业务投影单调，也不提供整个程序的事务性。

失败时的调用集合差异可由两个独立节点说明：将位置较早的节点命名为 early，较晚的
命名为 late；两者均执行 Concurrent ReadOnly，early 固定返回 Rejected，late 固定
返回 Result。early 先完成且 late 未启动时，late 被 error_cut；late 已启动后才观察到
early 错误时，两个调用都已发生。两种调度的主错误均为 early，调用集合分别只含 early
的 CallID 或同时包含两个 CallID。错误排序不撤销已经发出的调用。

## 14. 一致性验证义务

每条提供的执行路径（解释器/生成 Go）须验证下列行为；同时提供时共享用例。
第 16.4 节给出核心一致性用例。实现还须覆盖下列规则，并扰动合法调度次序：

- immutable capture、参数/返回值复制、结构比较、严格求值/短路和数值边界；
- 一万/十万次直接、互递归、function 值间接尾调用；非尾递归预算和发散取消；
- for 顺序/并行、break/continue、match first-arm 和 guard Error；
- carry 空源/next/exhaustion/break/无限源预算，迭代参数源自上一轮，恢复复用 EvalPath；
- concat/slice/keys/has/put/split/parse_number、string 顺序、带 guard 推导式及线性构造；
- sum_to、fold 回调尾 if、尾 match/换行/分号、carry else-if；块局部 binding 与参数验证顺序；
- 命名参数顺序、静态/动态 arity、保守 Ops 的容器/函数/分支路径与 parallel 判定；
- gate 未开/关闭时无动态错误，常量折叠不引入未选路径错误或预算；
- 不同 Ready 次序、跨 parallel 区域的 barrier、After 终态和失败级联；
- 多错误最小位置、较晚观察先到、较早 After 依赖在前缀之外仍保留；失败调用集合反例；
- 兄弟/嵌套循环与推导式的 Position/CallID/JoinID 唯一和顺序；
- 同原子重投、相同 id 变值、相同 value 不同 id、并行 join 与完整读取；
- live snapshot 多种结果都合法，但各自 journal replay 相同；完整读取等待/失败正确；
- Go/Python 操作同 schema/同输入产生同边界 Data；异常/取消/native 对象没有泄漏；
- effect 构造零调用、授权前零调用、terminal replay 零新增调用、intent-only crash/reconcile；
- 已完成 Write 的 schema/conversion fault 保留事实、重放错误、零重调用；
- Uncertain-only 与混合永久错误恢复：重算 F/K、同逻辑身份、后续较早错误可改变主错误；
- 宿主资源引用失效和 worker 重启不能被误认作有效持久值。

应用场景的验收规则如下。业务失败为 schema 中的普通 Result 数据，provider Error
仍按第 7 节传播；含恢复的用例仅用于声明 durable profile 的实现。

| 目标 | 本版方式与验收条件 |
|---|---|
| 并行搜索/三次失败 | 并行完整收集后判定；精确提前熔断用顺序 carry。并行确定提前取消不属于 core |
| 多 agent 缺失证据报告 | 完整 snapshot → 带 guard 推导式/has/put，调度扰动成功输出一致 |
| 迭代改进到 score≥0.8 | carry + perform，下一轮参数来自前一轮，命中后不再调用 |
| crash 恢复 | durable profile，重放固定结果、不重复未决写入、未完成不能 abandon |
| 事实来源定位 | snapshot 的 run_id/Position 可结合对应源码 revision 定位 join；完整因果 UI 由 Runtime 提供 |

在第 13 节确定性命题的前提下，调度扰动须保持成功输出或主错误相同；只对成功执行
要求 CallID 集合相同。有限状态模型可以辅助检查转移规则，但不代替 parser、求值机、
Host 数据边界和持久 journal 的实现验收。

## 15. 诊断与合规标记

错误对象与 Skipped cause 分开。编译期已知的无效程序必须拒绝，运行期 Error 不得
转换成 null/false/空 snapshot；Error 也不是可供普通 match 捕获的语言值。Host 若希望
业务可恢复失败成为值，须在 result schema 中显式声明 `{ok:false,...}`。

| 静态诊断 | 条件 |
|---|---|
| E-LEX / E-PARSE | 无效字面量/转义/token 或语法结构 |
| E-NAME / E-DUPLICATE | 未声明名、前向值引用、重复声明/对象字段 |
| E-SHADOW-CLAIM / E-RESERVED | 遮蔽 Claim 身份或重定义保留名称 |
| E-TYPE / E-ARITY | 可静态确定的类型/参数不符 |
| E-PURITY | fn 中的外部执行/活动 store 引用，或非纯 proposal/join RHS |
| E-RETURN / E-CONTROL | 无效结果出口、return 位置、break/continue 位置 |
| E-PARALLEL | parallel body 含写入、Unknown 或转移图循环的控制操作 |
| E-CLAIM-ORDER | 完整读取与全部 producer 的源位置/循环范围不符 |
| E-GRAPH-CYCLE / E-AFTER-CYCLE | 静态可确定的依赖环/After 环 |
| E-OPERATION / E-SCHEMA | 未注册 operation/版本或可确定的合约不符 |

| 运行时 Error | 条件 |
|---|---|
| E-TYPE / E-ARITY / E-SCHEMA | 动态值/参数/输入/结果不符 |
| E-VALUE / E-NUMBER / E-INDEX | 非法 native 数据、非有限数/除零、越界/缺键 |
| E-FAIL / E-MATCH-NO-ARM | fail 调用或表达式 match 无 arm |
| E-BUDGET / E-CAPABILITY / E-OPERATION | 明确资源预算、授权或注册失败 |
| E-CLAIM-CONFLICT / E-DEPENDENCY | 同 JoinID 变值或不可用数据依赖 |
| E-GRAPH-CYCLE / E-AFTER-CYCLE | 动态新增约束/展开形成环 |
| E-EFFECT-REJECTED / E-UNCERTAIN | terminal provider 失败或未知外部 outcome |
| E-REPLAY-DIVERGENCE / E-RESOURCE | 恢复请求变化或失效/越域 native 引用 |
| E-ID-COLLISION | 不同 canonical 身份/参数产生可检测的相同索引摘要 |

cause 取 `unselected_gate`、`break`、`continue`、`dependency_failure`、`after_failure`、
`order_failure`、`error_cut`、`cancelled` 等明确原因，并可链接源错误；它们不是静态
诊断码。遮蔽普通循环外绑定且
RHS 引用同名旧值时，推荐 `W-SHADOW-LOOP`，语义仍是新 binding，不是累加赋值。

实现须声明 `LIP beta 0.9 core` 或 `LIP beta 0.9 core + durable profile`，并公布支持的
Go/Python transports、注册 contracts、资源预算和取消粒度。模型检验通过、某个 adapter
可用或部分示例运行成功，均不能替代第 14 节的一致性验收。

## 16. Core 附录：编码、显示与数值向量

### 16.1. 规范数据与身份编码

C(v)：null/false/true 为单字节 0x00/0x01/0x02；number 为 0x03 加 -0 归零后的八字节
大端 binary64；string 为 0x04、八字节大端无符号 UTF-8 长度与内容；list 为 0x05、
八字节大端无符号元素数与各 C(element)；object 为 0x06、八字节大端无符号字段数与
按 UTF-8 bytes 键序的 C(key),C(value)。大小超过编码/Host 预算必须失败。
tuple 转成 Data list，不用 Host 自定义 repr；位置 ordinal/index 使用十进制 string：

以下 wire 结构用于传输和编码。PositionPairs 和 EvalPathPairs 分别由 Position 和
EvalPath 的每个整数 pair 转成两个十进制 string 组成；anchor/index 为对应 pair 的
两个分量。decimal 将非负整数转为 ASCII 十进制，无符号、无前导零，零为 "0"。
SHA256 返回 SHA-256 的 32-byte 摘要，lowerhex 将每个字节转为两个小写十六进制字符。
positional/named 是第 10 节 Request 的实参字段。

```text
JoinID_wire = ["join", run_id, [[decimal(anchor), decimal(index)], ...]]
CallID_wire = ["call", run_id, PositionPairs, EvalPathPairs, decimal(perform_site)]
JoinDigest = lowerhex(SHA256(C(JoinID_wire)))
CallDigest = lowerhex(SHA256(C(CallID_wire)))   # 可选的索引摘要
ArgFingerprint = lowerhex(SHA256(C([positional, named])))
```

字段数、tag 和 string 类型必须精确符合 wire 结构；tag 是首项的 "join" 或 "call"。
pair 排序先解码为任意精度自然数，或用等价的长度/数字串比较；`"9"` 在位置排序中
先于 `"10"`，不能用 string 的字典序。ordinal/index 任意精度或明确预算检查，不能
wrap；身份不因 binary64 安全整数范围给迭代次数设上限。

第 9 节 snapshot.id 就是 JoinDigest；第 10/11 节 argument_fingerprint 就是
ArgFingerprint。CallID 的传输结构为 CallID_wire，若计算摘要只使用上述 C 算法，不能
使用 JSON/repr/私有拼接。逻辑身份相等比较完整规范身份结构（等价地比较 C 字节），
不是 CallDigest；参数一致性比较完整 C([positional,named])，不是 ArgFingerprint。
operation/version 单独比较。没有摘要索引需求的实现不必计算 CallDigest。
若使用任一摘要作索引，检测到同一摘要对应不同 canonical 身份/参数时必须
E-ID-COLLISION，保留原字节，不合并或任选；摘要不替代身份和请求原文。

| v | C(v) 的 hex |
|---|---|
| null / false / true | 00 / 01 / 02 |
| 1 | 033ff0000000000000 |
| "a" | 04000000000000000161 |
| [] / {} | 050000000000000000 / 060000000000000000 |

### 16.2. 显示向量与 JCS 的区别

第 3 节算法是规范。以下都是必须的结果，不是 Host 自选格式：

| 输入 | str 结果/排序 |
|---|---|
| -0 / 1e3 / 1e21 | `0` / `1000` / `1e21` |
| 1e-6 / 1e-7 / 5e-324 | `0.000001` / `1e-7` / `5e-324` |
| `["\n","\t"]` | `["\u000a","\u0009"]` |
| object 键 U+E000、U+10000 | U+E000 在前；UTF-16/JCS 顺序相反 |
| 顶层 string "1" 与 number 1 | str 均为 `1`；C 不同，不能由 str 推导身份相等 |

普通 JSON decoder 可解码这些输出。规范显示使用 UTF-8 键序和无正号的指数，控制字符
使用统一的长转义；JCS/JSON.stringify 默认输出不能作为规范字节结果的判定依据。

### 16.3. 数值向量

下表的 a、b 是按第 2 节字面量规则转换并存储的 binary64 操作数。`//` 先计算精确
数学商的 floor（不超过该商的最大整数），再转换 binary64；`%` 先计算对应的精确余数，按
nearest/ties-to-even 舍入后实施区间修正。该区间约束不等于 Python float `%` 或
IEEE fmod；区间修正后不再保证浮点恒等式
`a == (a//b)*b + a%b`。实现可用已证明等价的整数/浮点快路径，边界 fallback 使用
binary64 的 dyadic 整数表示/大整数；不得以普通浮点 `floor(a/b)` 替代精确商。
本版不承诺该操作与硬件一次除法具有相同成本，预算与性能测试应计入 fallback。
表中的十六进制浮点记号用于精确表示 binary64 结果，不是 LIP 字面量语法；
bits=1 表示八字节 binary64 编码的无符号整数值为 1，即最小正次正规数。

| a | b | a // b | a % b |
|---|---|---|---|
| -5 | 3 | -2 | 1 |
| 5 | -3 | -2 | -1 |
| 1 | 0.1 | 9 | binary64 hex `0x1.9999999999996p-4` |
| -1e-17 | 1 | -1 | nextafter(1,0) |
| 1e-17 | -1 | -1 | nextafter(-1,0) |
| 5e-324 | 1 | 0 | 5e-324 |
| -5e-324 | 5e-324 | -1 | 0 |

字面量 `2e-324` 舍入为 0，`3e-324` 舍入为最小正次正规值（bits=1）。四则运算不能
flush subnormal；number 的四则/literal 转换结果均按第 3 节规范化零。除数为零和非有限
商报 E-NUMBER。以上向量必须在解释器、生成 Go 和边界编解码中一致。

### 16.4. 核心一致性用例

下列完整源文件和片段的判定属于规范。完整源文件独立解析；表达式片段表示在明示
环境中的单节点求值；静态片段表示编译检查，不因 callee 动态而跳过语法或规范 Ops
分析。core/durable 指适用合规层，不要求实现暴露内部测试 API。

尾 if 和尾 match 必须能解析以下完整文件（含末尾换行），输出为 `{sum:55,odd:[1,3],tag:1}`：

```text
fn sum_to(n: number, acc: number) -> number {
    if n == 0 { acc } else { sum_to(n - 1, acc + n) }
}
fn filter_fold(xs: list, p: function) -> list {
    fold(xs, [], fn(acc: list, x: any) -> list {
        if p(x) { concat(acc, [x]) } else { acc }
    })
}
fn tag(x: any) -> number {
    match x { number => 1, _ => 0 }
}
flow Main() -> object {
    odd = filter_fold([1,2,3], fn(x: number) -> bool { x % 2 == 1 })
    return {sum:sum_to(10,0), odd:odd, tag:tag(0)}
}
```

carry 的尾部分类、else-if 和局部 break 必须能解析以下完整文件，输出为 6：

```text
flow Main() -> number {
    answer = for x in [1,2,3,4] with s = 0 {
        for unused in [0] { break }
        if x == 3 { break s + x }
        else if x == 4 { break 999 }
        else { next s + x }
    }
    return answer
}
```

以下片段使用同一局部测试环境；每行单独求值，不继承上一行的 binding 或 journal：

- worker 是 `import python "example.worker" as worker` 声明的 alias。registry 注册
  `worker.read(value:number)->number`，固定返回 value；注册 `worker.text()->string`，
  固定返回 "a"。两者均为 version="1"、Concurrent ReadOnly；worker.unknown 未注册。
- 动态函数值 f 的静态信息仅为 function；实际值是语言闭包
  `fn(x:number)->number { x }`。直接具名函数的用例使用单独名称 known_fn。
- store 是 `import host "example.store" as store` 声明的 alias。registry 注册
  `store.commit()->number`、version="1"、Write；该故障用例的 provider 完成外部写入，
  返回合法 Data string "invalid"，完成 receipt 为 `{done:true}`。
- “恢复图”指第 11.1 节的四节点表。另定义一个“前置依赖图”：earlier_node 的位置为
  [(1,0)]、failed_node 为 [(5,0)]、dependency_node 为 [(9,0)]；earlier_node 是纯节点，
  Host After 要求它等待 dependency_node。failed_node 已产生永久 E-FAIL，
  dependency_node 的 Concurrent ReadOnly 调用为暂定 E-UNCERTAIN。其他前缀工作成功，
  没有额外依赖或 barrier。这些名字仅标识抽象图节点，不是源变量或错误优先级。

| 输入/固定环境 | 预期判定 | 层/模式 |
|---|---|---|
| `{ len([0]); }` / `{ len([0]); null }` | 分别为 1 / null，末尾分号是 padding | core 求值 |
| `if false { 1 / 0 } else { 7 }` | 7；无 E-NUMBER，未选分支不消耗执行预算 | core 求值/不同 Ready 次序 |
| `flow Main()->number { if false { x=1/0 }; return 7 }` | x 为 unselected_gate；无根错误，F 为空，不截断，输出 7 | core 调度 |
| `[1 / x for x in [0,2] if x != 0]` | `[0.5]`；x=0 的 body 不求值 | core 求值 |
| `[x for x in [1] if 1 / 0 == 0]` | guard E-NUMBER，不能当成 false | core 求值 |
| `[perform effect worker.read(x) for x in [0,1,2] if x>0]` | 输出 [1,2]；两个 CallID 的该 EvalPath 索引为 1/2，不是 0/1 | core 身份 |
| `"10" < "9"` / `"a" < "aa"` / `1 < "2"` | true / true / E-TYPE（直接已知时静态拒绝） | core 比较 |
| `parse_number("+1e-3")` / `parse_number("-0")` / `parse_number("3e-324")` | 0.001 / 0 / bits=1 | core 求值 |
| parse_number 的输入 `" 1"`、`"01"`、`"1x"`、`"NaN"`、`"1e309"` | 均为 E-VALUE | core 求值 |
| `f(x=1,2)` / `f(x=1,x=2)` | 分别静态 E-PARSE / E-ARITY | core 编译 |
| 声明 `fn known_fn(x:number)->number {x}`，调用 `known_fn(y=1)` / `len(value=[])` | 静态 E-ARITY | core 编译 |
| `f(perform effect worker.read(0), perform effect worker.read(1))` | arity 先失败，两个实参的 provider 调用均为零 | core 动态调用 |
| `f(perform effect worker.text())` | 实参调用一次，参数 E-TYPE，f body 不执行 | core 动态调用 |
| `p=[effect worker.read(0)]; q=p[0]; r=perform q` | 普通图允许，r 为 0，其 binding 节点按 Unknown barrier 调度；parallel body 静态 E-PARALLEL | core Ops |
| `for item in [0] parallel { out=for x in [item] with s=0 { v=perform effect worker.read(x); break v } }` | 合法；carry 的 break v 不退出图循环 | core 编译/调度 |
| `effect worker(0)` / `effect worker.unknown(0)` | 静态 E-OPERATION；provider 调用零次 | core 编译 |
| 第 12 节 payload 对 `{}` / `{kind:"a",payload:5}` / 0 | null / 5 / null | core 匹配 |
| Position wire `[["9","0"]]` 与 `[["10","0"]]` | 前者先于后者；snapshot 依此排序 | core 身份 |
| 两条不同完整身份被摘要索引检测为同 digest | E-ID-COLLISION，不合并；保留原 canonical 身份 | core 索引故障注入 |
| `perform effect store.commit()`，使用上述完成后非法输出的故障结果 | terminal known_fault、E-SCHEMA；保存 receipt/validation，没有成功值 | core Host / durable |
| 重放 commit 的同一 CallID；另提交将该 terminal known_fault 改为 Result 的 reconcile | 重放同一 E-SCHEMA，零 provider 调用；覆盖 terminal 被拒绝 | durable |
| 恢复图只取 uncertain_call/check_a 两个节点；对账 uncertain_call 为合法 Result | 移除暂定活动错误，check_a 可恢复为 Pending；逻辑 Position/CallID 不变 | durable |
| 完整恢复图；对账 uncertain_call 为合法 Result，check_a/check_b 成功 | 保留 later_call 永久错误，恢复两个纯节点，主错误为 later_call；已决调用不重做 | durable |
| 完整恢复图；对账 uncertain_call 为合法 Result，check_a 产生 E-FAIL | 主错误为 check_a，later_call 的永久错误仍保留 | durable |
| 前置依赖图；dependency_node 未决，随后对账为合法 Result | K 始终保留 dependency_node；对账前不能确认主错误，对账后继续判定 earlier_node | durable / 前置闭包 |

完整恢复图的 later_call 在首次截断前已启动并得到 terminal Rejected，这是用例的固定
历史输入，不要求所有初次调度都启动它。ErrorData 判定比较错误类别和语义 details，
不比较耗时或 trace 文本；回放测试须单独统计 provider 的实际调用次数。

### 16.5. 幂与对数的固定数值算法

本节是规范性参考算法，供解释器、生成程序和独立实现使用；不是对某个 Go 版本的
依赖。以下代码使用 Go 风格的描述记号，并非 LIP 源码。`float64` 为 binary64；
每次 numericAdd/Sub/Mul/Div 都独立进行 nearest/ties-to-even 舍入，禁止重结合和 FMA。
十六进制常量指定精确 binary64 值。赋值、比较、布尔判断和循环按代码顺序执行。
`int` 表示足以存储本算法指数的有符号整数；`uint64` 是 64 位无符号整数；
`>>`、`&` 是描述算法所用的整数移位和位与，不是新增的 LIP 运算符。

辅助名字 `math.Abs(x)` 为绝对值，`math.Floor(x)` 为不大于 x 的最大整数，
`math.Frexp(x)` 返回精确的 `(m,k)`，满足 x=m·2^k、非零时 0.5≤|m|<1；
`math.Sqrt(x)` 为非负 x 的平方根，按 nearest/ties-to-even 正确舍入；
`math.Ldexp(m,k)` 返回 m·2^k 经 nearest/ties-to-even 舍入的 binary64，允许次正规数、
零和溢出；`math.Inf(1)` 为内部正无穷哨兵，最终必须转为 E-NUMBER，不能成为 LIP 值。
整数/浮点转换遵循代码，涉及的循环索引可精确表示。符合本节结果的等价优化可使用；
不得借由误差容限接受不同的结果位模式。

调用前先执行第 3 节定义域检查；corePower 接受检查通过的有限操作数，coreLog 只接受
正有限操作数，coreExp 只由 corePower 调用。`**` 取 corePower(a,b)；`*/` 先处理
第 3 节的精确特例，再取 numericDiv(coreLog(a),coreLog(b))。最终拒绝非有限值并规范化零。
自然对数使用固定 24 项级数及指数分解；指数使用固定 24 项级数和 2 的幂缩放；
幂的整数部分使用带缩放的平方求幂，±0.5 指数使用平方根及其倒数。这些辅助函数不加入核心函数表。

```go
func numericAdd(a, b float64) float64 { return float64(a + b) }
func numericSub(a, b float64) float64 { return float64(a - b) }
func numericMul(a, b float64) float64 { return float64(a * b) }
func numericDiv(a, b float64) float64 { return float64(a / b) }

const numericLn2Hi = 0x1.62e42fee00000p-1
const numericLn2Lo = 0x1.a39ef35793c76p-33

func coreLog(x float64) float64 {
	m, k := math.Frexp(x)
	if m < 0x1.6a09e667f3bcdp-1 {
		m = numericMul(m, 2)
		k--
	}
	z := numericDiv(numericSub(m, 1), numericAdd(m, 1))
	z2 := numericMul(z, z)
	term, sum := z, z
	for j := 1; j < 24; j++ {
		term = numericMul(term, z2)
		sum = numericAdd(sum, numericDiv(term, float64(2*j+1)))
	}
	return numericAdd(numericMul(float64(k), numericLn2Hi),
		numericAdd(numericMul(2, sum), numericMul(float64(k), numericLn2Lo)))
}

func coreExp(x float64) float64 {
	if x > 0x1.62e42fefa39efp+9 {
		return math.Inf(1)
	}
	if x < -0x1.74910d52d3052p+9 {
		return 0
	}
	ln2 := numericAdd(numericLn2Hi, numericLn2Lo)
	k := int(math.Floor(numericAdd(numericDiv(x, ln2), 0.5)))
	r := numericSub(numericSub(x, numericMul(float64(k), numericLn2Hi)),
		numericMul(float64(k), numericLn2Lo))
	term, sum := 1.0, 1.0
	for j := 1; j <= 24; j++ {
		term = numericDiv(numericMul(term, r), float64(j))
		sum = numericAdd(sum, term)
	}
	return math.Ldexp(sum, k)
}

func corePower(x, y float64) float64 {
	if y == 0 || x == 1 {
		return 1
	}
	if y == 1 || x == 0 {
		return x
	}
	if y == 0.5 {
		return math.Sqrt(x)
	}
	if y == -0.5 {
		return numericDiv(1, math.Sqrt(x))
	}
	negative := x < 0 && math.Abs(y) < 0x1p53 && int64(math.Abs(y))&1 != 0
	x = math.Abs(x)
	if math.Abs(y) >= 0x1p63 {
		if x == 1 {
			return 1
		}
		if (x < 1) == (y > 0) {
			return 0
		}
		return math.Inf(1)
	}
	whole := math.Floor(math.Abs(y))
	fraction := numericSub(math.Abs(y), whole)
	if fraction > 0.5 {
		fraction = numericSub(fraction, 1)
		whole = numericAdd(whole, 1)
	}
	a, scale := 1.0, 0
	if fraction != 0 {
		a = coreExp(numericMul(fraction, coreLog(x)))
	}
	m, e := math.Frexp(x)
	for n := uint64(whole); n != 0; n >>= 1 {
		if e < -8192 || e > 8192 {
			scale += e
			break
		}
		if n&1 != 0 {
			a = numericMul(a, m)
			scale += e
		}
		m = numericMul(m, m)
		e *= 2
		if m < 0.5 {
			m = numericMul(m, 2)
			e--
		}
	}
	if y < 0 {
		a = numericDiv(1, a)
		scale = -scale
	}
	a = math.Ldexp(a, scale)
	if negative {
		a = -a
	}
	return a
}
```

以下用例在解释器及生成程序中必须一致；表中的 bits 是规范化结果的 binary64
无符号 64 位编码，十六进制写法仅用于说明结果。定义域错误不产生成功值。

| 表达式 | 预期结果 |
|---|---|
| `0 ** 0`、`(-1) ** 9007199254740992` | 1 |
| `(-2) ** 3` | -8 |
| `2 ** -1074` | bits=1，最小正次正规数 |
| `2 ** -1075`、`(-2) ** -1075` | 0 |
| `2 ** 1024`、`0 ** -1`、`(-2) ** 0.5` | E-NUMBER |
| `0 */ 2`、`2 */ 1`、`2 */ 0`、`2 */ -2` | E-NUMBER |
| `0.25 */ 2`、`8 */ 0.5` | -2、-3 |
| `2 ** 0.5`、`4 ** -0.5` | bits=`0x3ff6a09e667f3bcd`、0.5 |
| `5e-324 ** 0.5` | 精确 2^-537，bits=`0x1e60000000000000` |
| `27 ** (1/3)` | 3 |
| `2 */ 3` | bits=`0x3fe430939835353d` |
| `10 */ 2` | bits=`0x400a934f0979a372` |
| `"2" ** 3`、`8 */ true` | 静态 E-TYPE；动态 any 同样报 E-TYPE |
| `if false { (-2) ** 0.5 } else { 7 }` | 7，未选分支不求值 |
