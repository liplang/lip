# LIP 1.2 语言规范：小计算核心与统一调用边界

[English](LIP-1.2-SPEC.en.md) | [简体中文](LIP-1.2-SPEC.md)

版本：1.2 工作草案。日期：2026-10-18。

## 0. 范围、术语与阅读约定

本文件完整定义 LIP 1.2 的语言与执行合约，是实现与一致性判定的唯一规范依据。
第 1–9、12–15 节及第 16 节附录定义 core（基本合规层），其中第 12.1 节是可选的 HTTP
Host 接入配置；第 10 节定义 core 必须遵守的 Host 边界；第 11 节定义 durable profile
（可选的崩溃恢复合规层）。未提供 HTTP 接入不影响 core 合规。
未提供持久恢复的实现仍可符合 core。理解语义、身份编码和核心一致性用例只需本文件。

核心包括不可变值、表达式、纯 fn、静态模块与导出、if/match/for、effect/perform、Flow
依赖图、结构化子 Flow 调用和 join-only Claim store。`|>` 在所属节点内依次固定中间值并
调用下一阶段，采用第 2.1 节的顺序绑定语义，不等价于把调用直接嵌套进实参。
Go/Python 使用同一调用协议。持久状态、事务、重试、业务汇聚和证据展示可由
库或 Host 提供，其行为须满足本文件规定的调用与值边界。

“必须”和“不得”表示合规要求；“可”表示实现选择。示例说明规则，一致性用例给出
必须满足的输入/结果关系；是否已经实现这些规则，由具体实现的合规声明说明。

既有单源、无子 Flow 程序的身份编码保持不变；产品部署、认证组件、长连接和流式库
的开发计划不属于本文件。仓库实现状态集中列于第 17 节非规范性附注。

| 术语 | 含义 |
|---|---|
| Runtime / scheduler | 执行引擎 / 从已满足条件的图节点中选择工作的调度器 |
| Host / adapter / provider | 管理外部操作及权限的宿主 / 转换原生参数与结果的适配层 / 实际执行外部操作的服务或函数 |
| operation / descriptor / registry | 可调用的外部操作 / 该操作的注册合约 / 本次运行使用的合约登记表 |
| schema / capability | 参数或结果的数据约束 / Host 对操作与资源范围授予的权限 |
| run / binding | 一次选定入口的执行，包含其全部子 Flow / 名称与不可变值的绑定 |
| entry / sub Flow / invocation | Host 选定的根 Flow / 在父图中实例化的 Flow / 一次有结构身份的调用 |
| module / export / source set | 构建时解析的 LIP 源模块 / 显式公开的声明 / 根源码及其完整已锁定依赖闭包 |
| node / graph | 一个可独立调度的计算实例 / 节点及其数据、控制和执行顺序依赖 |
| gate / selector | 决定节点是否被选中的控制条件 / 计算分支或循环控制选择的节点 |
| region / producer / writer | 分支、循环或子 Flow 调用的结构区域 / 可能产生节点的语句或区域 / 向指定 Claim store 提交贡献的节点 |
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

### 0.1. 保证、描述与实现

本规范独立定义完整的语法、值、计算、图、身份、外部协议、错误和编码，不要求读取
其他版本规范。普通类型与可调用签名、请求候选、执行效果和原生来源是不同的信息维度。
第 5 节规定它们在封装中的有限传播，使用静态实参描述而不试跑程序；第 10.4–10.5 节
规定只读接口契约和适配边界；第 15.1–15.2 节规定可解释的分析与数据边界诊断。

静态推导、Host 合约、作者的 @{...} 纯性承诺分别说明保证依据，不互相代替。
Flow 本身不是值，调用图无环，活动 store 不跨实例传递；请求的返回信息不等于
执行它的资格。不引入 effect<T>、自定义源类型、共享可变状态或另一条外部调用通道。
源码重构不保证结构身份或旧 journal 的可恢复性；完整来源、语言版本和合约仍固定于 run。

规范版本与工程发布版本分开管理。第 17 节明确记录实现状态，未实现的规范要求不能
由已有示例、先前版本的检查或更详细的工具说明替代。实现只有满足全部 core 要求后
才能声明本版本 core 合规；可选 HTTP 与 durable 另按各自条款声明。

## 1. 核心对象

| 对象 | 规则 |
|---|---|
| Data | null、bool、number（完整 binary64，包括正负无穷与规范 NaN）、Unicode scalar char、UTF-8 string、list、string-keyed object |
| fn | 捕获不可变值的纯闭包；允许第 3.2 节单向诊断和第 10.3 节 @{...} 显式承诺纯性的原生调用；支持递归、互递归和函数值 |
| effect | 已解析 operation 与已固定 Data 参数组成的不可变 proposal |
| Flow | 具名图定义；不可变绑定、控制结构和子 Flow 调用降低得到依赖图；不是普通值 |
| Claim store | 一次 Flow run 内只通过 join 增长的信息槽位 |

普通值 V 递归定义为 null、bool、number、char、string、list<V>、object<string,V>、fn 或 effect。
Data 是其中递归不含 fn/effect 的子域。普通容器和同一 run 内的 Flow 输入/输出可以包含任意 V，包括 fn、effect 及其嵌套容器。
跨 Host 的参数/结果、入口协议输入/输出及 Claim 贡献必须是 Data。Claim store 不是普通值，不存在可任意传递的
store handle。变量只绑定一次；内层可遮蔽普通值，但不得遮蔽 Claim store 名称。
Flow 定义不属于 V 或 Data，也不是 function。不能把 Flow 放入容器、捕获为闭包值、
绑定为别名、作为参数/返回值传递或交给 map/fold；调用目标按第 2.1 节静态解析。

`effect` 构造请求，`perform` 解释请求；`str(effect)` 可把请求的固定 operation、contract
和 Data 参数变成可审阅的稳定字符串，但不执行请求。所有 Go/Python 调用、外部属性读取、随机、时钟、
业务日志、存储和网络访问都经 perform；没有第二条即时 Host 调用通道。第 3.2 节核心诊断
只单向输出，不属于业务 operation。纯核心操作与本地 fn
可直接调用，外部 operation 不可直接调用。第 10 节的通用库适配配置可在核心检查前
把直接库调用和属性访问降为 perform；降级后仍使用这一条外部通道。

## 2. 源文件和语法

文件为无 BOM 的有效 UTF-8，允许 LF/CRLF、空格/tab、`#` 行注释、分号和物理换行
分隔语句；三引号字符串内部的 LF/CRLF 与 tab 按下文处理，其他裸控制字符非法。行末反斜杠可以显式
续行，之后不得有其他字符。括号、参数、list、object 内允许自然换行；block 内换行分隔
语句。lexer 保留换行，parser 按上下文处理；不根据下一行运算符自动续行。

标识符为 `[A-Za-z_][A-Za-z0-9_]*`。保留词为 `import as flow fn return if else match
for in parallel break continue with next effect perform export true false null nan inf void`。`go/python/host/lip` 在
import 后为上下文关键字；类型名只在类型/pattern 位置有专用含义。`claim`、import alias
与第 3 节核心函数名不得重定义。其他同一作用域的重复声明为静态错误。
char 使用单引号 `'...'`，必须恰好包含一个 Unicode scalar；支持 `\'`、`\\`、`\n`、`\r`、`\t` 和
`\u{HEX}` 转义，拒绝 surrogate、超过 U+10FFFF 的值和多 scalar 内容。char 是独立类型，按
Unicode code point 比较；`str(char)` 得到包含该 scalar 的 string。char 使用单引号，不能作为
string 的替代，也不与 string/list 隐式混用。string 使用普通双引号 `"..."` 或 ASCII 三引号 `"""..."""`。普通字符串的裸换行与
tab 非法；三引号字符串允许物理换行与 tab，LF/CRLF 均产生一个 LF，裸 CR 非法。
两种形式都只支持 `\"`、`\\`、`\n`、`\r`、`\t`、`\u{HEX}` 转义；HEX 为 1–6 位
十六进制且必须为 Unicode 标量，拒绝 surrogate 和超过 U+10FFFF 的值。不进行 Unicode
normalization。三引号优先于普通引号识别，以首个未转义的连续三个 ASCII 双引号结束；
内部单个或连续两个双引号是正文，`\"` 可用于表达正文中的三引号。正文内 `#`、括号和
运算符没有语法意义；不删除首尾换行、不自动去缩进、不插值、不支持 raw/f-string 前缀。
物理行末反斜杠在字符串内部不是续行转义。未闭合三引号为 E-LEX；交互工具在输入结束前
可以继续收集物理行，字符串正文内的 `:help` 等文本不作为工具命令。
string 仍是第 1 节的同一种 UTF-8 值，规范编码、比较与 Host 边界不因字面量写法变化。
number 的词法形式为
`(0|[1-9][0-9]*)(\.[0-9]+)?([eE][+-]?[0-9]+)?`，按 nearest/ties-to-even 转为
有限 binary64；溢出非法，下溢必须按 nearest/ties-to-even 舍入为次正规数或 0，不能
任选报错/flush-to-zero。正负号是 unary operator，不存在隐式八进制。
运算符按最长匹配识别；`**`、`*/`、`//`、`|>` 各是一个 token，两个字符之间不能有空白。

以下是规范语法；`sep` 为一个或多个分号/换行，`padding` 为零个或多个；Data/type 和
operator 的静态约束见后文。`id` 为标识符，`string`/`number` 为字面量；EOF 是不消耗
token 的文件结束判定，NEWLINE 表示物理换行 token。`decl_end` 要求分隔符或文件结束，
所以最后一个 import/data_decl 不要求末尾换行；后面还有内容时仍必须有 sep。

```ebnf
file       ::= padding import* (declarations | fn_decl* statements) EOF
declarations ::= declaration+
declaration ::= ["export"] (fn_decl | flow_decl | data_decl)
import     ::= "import" ("go" | "python" | "host") string ["as" id] decl_end
             | "import" "lip" string "as" id decl_end
fn_decl    ::= "fn" id params "->" type fn_body padding
flow_decl  ::= "flow" id params "->" (type | "void") block padding
data_decl  ::= id ":" type "=" static_data decl_end
decl_end   ::= sep | EOF
static_data ::= literal | ("+" | "-") (number | "inf")
              | "[" [static_data ("," static_data)* [","]] "]"
              | "{" [static_field ("," static_field)* [","]] "}"
static_field ::= (field_name | string) ":" static_data
params     ::= "(" [parameter ("," parameter)* [","]] ")"
parameter  ::= id ":" type
type       ::= "null" | type_name ["?"]
type_name  ::= "any" | "bool" | "number" | "char" | "string" | "list" | "object"
             | "function" | "effect"

statements ::= padding [stmt (sep stmt)* padding]
block      ::= "{" statements "}"
stmt       ::= id "=" expr | id "=" "claim" "(" ")" | id "<+" expr
             | "return" [expr] | "break" | "continue"
             | if_stmt | match_stmt | for_stmt | call | perform | pipeline_stmt
if_stmt    ::= "if" expr block ["else" (block | if_stmt)]
for_stmt   ::= "for" id "in" expr ["parallel"] block | "for" block
match_stmt ::= "match" expr "{" padding arm_stmt (arm_sep arm_stmt)*
               [arm_sep] "}"
arm_stmt   ::= patterns ["if" expr] "=>" (block | stmt)
arm_sep    ::= "," | sep
patterns   ::= pattern ("," pattern)*
pattern    ::= literal | ["+" | "-"] number | concrete_type | "_"
concrete_type ::= "bool" | "number" | "char" | "string" | "list" | "object"
                | "function" | "effect"

expr       ::= pipeline
pipeline   ::= or ("|>" stage)*
stage      ::= static_target ["(" [named_args [","]] ")"]
static_target ::= id | id "." field_name
pipeline_stmt ::= or "|>" stage ("|>" stage)*
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
suffix     ::= "(" [arguments [","]] ")" | "." field_name
             | "[" (expr | slice_spec) "]"
slice_spec ::= [expr] ":" [expr] [":" [expr]]
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
operation  ::= id ("." field_name)*
field_name ::= id | "nan" | "inf"
call       ::= postfix                 # last suffix must be an argument list
fn_body    ::= ["@"] "{" padding (pure_stmt sep)* fn_tail padding "}"
fn_tail    ::= ["return"] expr
pure_stmt  ::= id "=" expr | call | perform | pipeline_stmt | if_stmt | match_stmt | for_stmt
             | "break" | "continue"
local_stmt ::= id "=" expr | call | perform | pipeline_stmt | if_stmt | match_stmt | for_stmt
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
field      ::= (field_name | string) ":" expr
literal    ::= number | char | string | "true" | "false" | "null" | "nan" | "inf"
```

file 的分支按模块顶层的声明种类唯一选择：出现任一 export、flow 声明或 data_decl
就使用 declarations，其他文件使用 fn_decl* statements。后者允许 statements 为空；
仅含 import/普通 fn 的根文件因此产生隐式空 main，作为依赖时只收集声明、不创建 main。
此选择只看模块顶层，不把 fn/Flow body 内的 token 当作文件模式标记。声明模式不得
混入顶层 statements，旧式 statements 模式不得声明静态数据。所有 import 必须位于
任何声明或 statement 之前，export 数据也不能出现在 import 之前。

`pure_stmt` 中嵌套 block 的语句也必须属于 pure_stmt，不允许 return。fn 的末尾 return
是唯一结果出口，expression 简写等价于末尾 return。fn 不允许 claim 声明/读取/join、
子 Flow 调用；允许构造 effect。普通 `{...}` 函数体不得 perform；只有 `@{...}` 函数体
可按第 5、10.3 节显式承诺原生调用纯性后 perform。即使被调用 Flow 的效果上界为 Pure，
fn 中调用它仍为 E-PURITY；结果类型为 function/effect 时也须满足纯性。
fn_body 由局部纯语句加 fn_tail 组成；尾部 return 可省略。
具名 fn/Flow 的签名先统一收集，声明顺序不限制具名调用；fn 的递归/互递归保持原规则，
Flow 的调用环按第 6.3 节拒绝。普通值不能前向引用；静态数据按第 2.2 节解析。
同模块的静态数据/fn/Flow 共用声明名称空间，同名为 E-DUPLICATE。普通值遮蔽声明
时按词法解析结果处理，不隐式回退到同名 Flow。
签名参数、局部 binding 和循环变量在本作用域中唯一。遮蔽绑定 RHS 先在外层环境求值，
新名只对后续语句可见；循环体每次重新建立作用域。fn 中 for 顺序执行，parallel 非法；
循环中的 binding 不会更新外层值，也不会泄漏到循环之后。

Flow 有返回值时必须恰好有一个顶层最后 return；void 可没有 return 或用最后的无值
return。return 不提前跳过其他节点。分支/循环体不得 return。顶层 statements 是无参数
void `main` Flow；根文件不能同时包含声明模式与顶层 statements，只有原有 fn 前缀可
与 statements 共存；导入模块不得包含顶层 statements。仅有 import/普通 fn 且无
statements 的根文件仍可产生旧式空 main，导入模块不产生隐式 main。显式声明可以有多个
Flow，但一次 run 恰好选择一个入口。Host/工具可在运行前显式选择根模块 Flow 声明名。
未显式选择时，优先选择根模块中名称恰为 `Main` 的 Flow；不存在该 Flow 时，仅有一个
根 Flow 则选择它，否则报 E-ENTRY。名称区分大小写，`main` 没有默认优先级；声明顺序
不影响选择。显式选择覆盖默认规则，未知或非根 Flow 的入口报 E-ENTRY，不回退到默认入口。
源码不增加入口关键字；未选为入口的 Flow
仍是可复用定义，完整 source set 均须通过静态检查。导入模块的 Flow 不参与根入口
数量判断，也不自动成为入口；含静态数据/export 而无根 Flow 的声明文件报 E-ENTRY。
隐式 main 的入口规则不变。
Flow 接口允许 `function`/`effect` 及其可选形式；list/object/any 的内容可为任意普通值。
同一 run 内的调用只验证声明注解，不额外要求 Data；Host 入口协议另遵守第 2.3 节。fn 不支持 void；丢弃返回值的纯调用
可用于验证并传播 Error，需要没有结果的外部动作由 perform 表达。
`null` 是唯一值为 null 的 Data 类型，可作为 Flow 输入/输出；`void` 仅表示没有返回值，
只允许在 Flow 返回注解中使用。`any?` 与 any 相同，不免除外部协议和 Claim 的 Data 检查；可选参数注解
允许传入 null，不使该参数可省略。当 F 有返回值时，`return F(x)` 在非 void Flow 的
最后返回节点内合法；该节点等待 F 完整成功后检查自身返回注解，不增加特殊的提前返回规则。

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
的位置检查第 9 节约束；fn 中引用活动 store 仍非法。local_stmt 本身顺序执行，不展开为
独立图节点，未选块不求值；其中的子 Flow body 按第 6.3 节建立子图并保留局部求值
游标。局部普通 for 仍在所属节点内顺序执行，不允许局部 parallel。
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
上述实参语法；核心函数的命名参数另按第 3.1–3.2 节拒绝。

### 2.1. 子 Flow 调用与管道写法

`F(args)` 的 callee 若词法解析为具名 Flow 声明，表示第 6.3 节的结构化子 Flow 调用，
而不是普通 function 求值。目标必须静态已知，可以是本模块声明或 `alias.Name` 指向
第 2.2 节的已导出 Flow；不接受 Flow 值、动态 callee、普通 object 成员或运行时名称查找。

子 Flow 的显式实参可以是任意普通值表达式，包括函数、effect、包含它们的容器、
perform 或其他子 Flow 调用。实参在调用方的局部游标内按源码顺序求值，各自完整成功
后才求值下一项；全部实参完成且注解验证通过后才实例化子图。未选分支不求值实参。
fn、guard、proposal/join RHS 等纯位置仍不得通过实参隐藏非 Pure perform 或子 Flow 调用。参数仍使用位置在前、命名在后的绑定规则，没有默认值、隐式展开
或可变参数。

`|>` 的优先级低于 `||`，按从左到右的阶段顺序组合。右侧 stage 必须静态解析为具名
Flow、具名 fn、已导出的模块 fn/Flow 或核心纯函数；普通动态函数调用仍使用原有调用语法。
名称按词法作用域解析，不越过同名局部值查找声明。未声明目标为 E-NAME，未导出成员为
E-EXPORT；局部函数值、普通 object 成员或其他非静态阶段目标为 E-TYPE。
`x |> F` 与 `x |> F()` 都把 x 绑定到 F 的第一个声明参数；
`x |> F(limit=20)` 还绑定显式命名参数。
stage 不接受额外位置参数、占位符或隐式字段展开。没有第一个参数、重复绑定第一个
参数、未知/缺少参数均为 E-ARITY；核心函数仍拒绝命名参数。因此核心阶段只收到一个
位置参数，必须在该实参数量下合法：len/str/range 等可用，必须有多个参数的 map/fold
等不可直接用作阶段；需要时用具名纯 fn 包装。

一条管道先静态检查全部阶段的名称、参数结构和可确定类型，再执行：初始左侧恰求值
一次，每阶段先取得上阶段的完整成功结果，再按源码顺序求值本阶段的命名参数，检查
参数后执行该阶段。后续阶段的实参不提前求值。初始左侧按调用方表达式规则求值并
固定，Flow 阶段接收其普通值结果；额外命名参数遵循调用方上下文，可包含函数、effect、
perform 或子 Flow 调用。fn 阶段保留普通函数的参数/结果规则；fn/Flow 阶段返回 proposal
时沿用第 5 节的返回描述；核心阶段按第 5.2 节表传播。管道本身不增加更强推导。
阶段注入的上一步结果视为第一个位置实参；静态目标若同时显式绑定该参数，或显式
参数重复/未知，先报 E-ARITY，不求值该阶段的其他参数。上一步未完整成功时，当前阶段
的任何命名参数、阶段 body 或子 Flow 均不求值。阶段内部若再包含普通动态调用，则该
调用仍先完成自己的 callee 求值与 callable 检查；其中已经发生的合法动作不会被后续
arity/type 错误回滚。

对 `e |> S1 |> … |> Sn`，在所属节点的局部求值游标中依次执行如下描述步骤；t0…tn 是
不可见的临时绑定，不属于源码名称空间：

```text
t0 = evaluate(e)
ti = invoke_stage(Si, first_argument=ti-1, explicit_named_arguments=Si.args)  # i=1…n
result = tn
```

以上描述有值管道；若最后阶段是 void Flow 且整条管道为独立语句，最后一步只等待
完整完成，不创建 tn 或 result，不以 null 代替没有返回值。
每步必须完整成功才进入下一步，任何步骤失败都停止该游标。传入阶段的是已经固定的
值引用，不重新求值原表达式。`Read(perform effect worker.read(2))`、
`Adjust(Read(2),delta=3)` 与相应的 binding/管道写法都合法；局部求值顺序一致。
嵌套调用与管道具有各自的源码 site 和调用路径，因此不要求改写前后的 CallID 相同。
Flow 的静态目标约束与 fn 的纯性不因允许嵌套实参而改变。

整条管道属于一个调用方节点；在 value_block/carry 等局部表达式中仍属于外层 owner。
临时绑定和阶段不创建独立图节点或新的源码 token/site；阶段中的子 Flow 另按第 6.3 节
实例化子图。降低不能用展开后的行号或临时变量序号替代第 6.2 节规定的身份。改写源码
会改变 revision；顺序绑定的语义等价不承诺跨 revision 的 CallID 相同。
Flow 阶段的完整成功结果还包含第 6.3 节规定的 region 完成条件，不能只传递提前可用的
return 值。`|>` 不解释业务 Result，不自动解包 `{ok:false}`、截断或重试。

管道在语法上可出现于任何 expr 位置，也可作为独立语句；纯性、类型和 Data 约束递归
作用于整条管道。guard、proposal/join RHS、carry 出口及 fn body 中可使用纯 fn/核心阶段，
不得借括号、容器或嵌套 block 引入非 Pure perform 或 Flow 调用。`ok && x |> F` 按
`(ok && x) |> F` 分组；`x |> F == y` 为 E-PARSE，比较阶段结果须写 `(x |> F) == y`。
建议对未加括号的布尔运算与管道混用给出第 15 节的 W-PIPE-PRECEDENCE。
void Flow 只允许作为独立调用
语句或管道语句的最后阶段；它不产生可传递的 null 值，在其他值位置使用为 E-TYPE。
fn 中只允许纯 fn/核心阶段，不允许任何 Flow 阶段。管道是一次求值的组合，不表示流式
传输、长期订阅、后台启动或消息投递；分支、汇聚和循环使用现有 binding/if/match/for。

### 2.2. 静态模块、导出与构建输入

一个 LIP 模块对应一个源文件，模块身份为构建输入中的 SourceID。SourceID 是项目根
内的规范相对路径，使用 `/`，无空段、`.`、`..` 或绝对路径；保留 UTF-8 原字节，不做
Unicode normalization。根源码的 SourceID 由工具/嵌入 Host 固定；内存源码可使用
固定虚拟路径。物理目录和源码缓存位置不属于语言身份。
本地文件解析时，每段路径须与实际目录项的 UTF-8 名称逐字节相同，不做大小写折叠或
NFC/NFD 自动匹配；文件系统无法提供无歧义的精确匹配时报 E-MODULE。路径中的符号链接
一律拒绝，包括指向项目根内的链接；项目根本身先由 Host 固定为解析后的物理目录。
同一构建中不同 SourceID 若指向同一物理文件（包括硬链接），报 E-MODULE。预供源码表
按精确键解析，不借底层文件系统合并名称；这些规则不把物理路径或 inode 写入语言身份。

`import lip "./lib/pricing.lip" as pricing` 在构建时解析 LIP 源模块，必须显式 as。
路径相对导入文件所在目录解析，必须指向项目根内的 `.lip` 文件；规约路径后得到
SourceID。Host 可以从本地文件或预先提供的源码表解析，不得在启动 run、接收请求、
补全或纯求值时联网下载、动态读入依赖或执行模块初始化。缺失、越界、歧义或锁定
内容不符报 E-MODULE。本版不定义包管理器、远程 import 或运行时模块加载。

同一 SourceID 只解析一次，不因多个 alias/导入者复制声明或执行代码。全部静态 import
组成模块依赖图，直接/间接依赖环报 E-MODULE-CYCLE，即使引用仅出现在未选代码中。
依赖模块可以只含 import 和声明，不要求 Flow；依赖模块顶层不得含 statements 或任何
求值动作。perform 可出现在 @{...} fn 或 Flow body 的合法位置，前者适用第 10.3 节的显式承诺；
claim 声明只允许在 Flow body，导出的定义也遵守相同规则。顶层调用不因结果被丢弃而成为声明。根源码仍遵守第 2 节的显式/隐式
入口规则。

声明默认私有；`export` 只修饰模块顶层的 fn、Flow 或静态 Data 声明，不能修饰局部
binding、import 或 namespace。模块成员只通过显式 alias 访问，不提供通配导入、
隐式名字合并、重导出 namespace 或文本拼接。访问未导出成员为 E-EXPORT；alias 本身
不是普通值，不能把模块对象传入函数。模块内私有声明与另一个模块的同名声明不冲突。

`export TAX_RATE:number = 0.1` 声明不可变静态 Data。初始化只接受第 2 节 static_data
字面量树，不含名称引用、函数调用、proposal、Flow 或其他求值操作；依照普通值规则
检查类型、number（含 nan、inf 与 -inf）、Unicode、重复键和 Data 边界。静态数据在模块签名收集后可被本模块
定义前向引用，不能重新赋值。解析/验证与实际内存成本仍受构建预算约束，不表示免费
加载；它不创建 run 或执行 Host。

`pricing.total` 若为已导出 fn，是在定义模块环境中求值的纯函数，可作为 function 值
使用；其 environment 只包含该模块的不可变数据/闭包，不捕获导入者的局部值。
`pricing.Quote(args)` 若为已导出 Flow，是静态子 Flow 调用，管道亦可写
`request |> pricing.Quote`。两者的声明、类型、Data 与纯性规则和本模块调用完全相同。
`pricing.TAX_RATE` 为固定 Data 引用；object 投影和模块成员解析须在检查阶段区分。
Flow 仍不是值，不能因跨模块导出而变成动态 function。跨模块 fn 返回 proposal 时使用第 5 节同一返回摘要规则；摘要中的 operation 按定义模块解析，导入不改变其合约或 parallel 接受条件。

被导入定义使用自己的 import alias、静态数据与私有声明。go/python/host alias 不直接
导出；可通过纯 fn 构造 proposal 或 Flow 复用外部工作。operation 的规范名称仍按定义
模块中的原 import 解析，导入者的 alias 不改写 operation 身份或 Host 合约。

构建输入必须在执行前冻结根源码和完整依赖闭包，包括未使用的 import。每个来源保存
SourceID、非空版本 string（本地未发布源码可用 `local`）、完整源字节及其内容摘要。
工具可从本地项目读取并生成这一快照，但检查、解释、REPL 和 Go 生成必须消费同一个
快照，不能先摘要再重新读盘编译。源码改变必须重新构建；不对运行中的程序热替换。
缺失版本/内容或版本锁与实际源码不一致时，在任何外部动作之前拒绝。

以下规范构建记号不是源语法：Normalize 将 CRLF 变为 LF，SourceHash 是归一化 UTF-8
字节的 SHA-256 小写十六进制；不执行其他空白、注释、Unicode 或字符串转义归一化。
SourceTable、SourceHash 与规范 token 编号均基于同一 Normalize(source)；诊断可映射
回原文件的物理位置，不据原始 CRLF 的额外字节改变 site。版本锁 `DependencyLock`
是每条不同导入边的记录
`[importer_id, decoded_import_path, target_id, target_version, SourceHash(target)]`。
记录按前四项的 UTF-8 bytes 字典序排列；同一 importer/path 不得解析为不同目标，
重复同一导入边只保留一项。`SourceTable` 包含根及全部依赖的
`[SourceID, version, Normalize(source)]`，按 SourceID 的 UTF-8 bytes 顺序排列。
具体工程清单文件格式不属于 core，但必须能还原上述唯一快照，不能影响第 6.2 节身份。

编译错误保留来源模块、原始 token 和源码位置；不得用展开后的行号替代。导出检查、
fn 纯性、Flow 调用图与传递效果分析覆盖整个 source set 的全部声明；非入口定义也
必须合法。Go 生成时按模块限定名隔离符号，生成程序使用静态模块元数据和真实 Go
求值，不在运行中重新解析/解释 LIP 源文。交互工具如支持模块，也不得重跑历史成功
cell 来重建模块环境；已有闭包/绑定保持原冻结版本，失败 cell 不提交新模块绑定。

以下是两份独立文件；pricing 的私有/导出名字按模块隔离，根入口只有 Main：

```lip
# lib/pricing.lip
export TAX_RATE:number = 0.1
export fn total(price:number) -> number { price * (1 + TAX_RATE) }
export flow Quote(request:object) -> object {
    return {total:total(request.price)}
}
```

```lip
# main.lip
import lip "./lib/pricing.lip" as pricing
flow Main(request:object) -> object {
    quote = request |> pricing.Quote
    return {quote:quote, rate:pricing.TAX_RATE}
}
```

### 2.3. 入口输入、输出与运行前验证

Host 向选定入口提交以下唯一的逻辑输入结构；这是协议记号，不是新的源语法：

```text
EntryInput = {positional:list<Data>, named:object<string,Data>}
```

两个字段都必填，不接受其他字段。从 JSON 接收此结构时须拒绝重复 object 键，不得
静默覆盖。先按声明顺序绑定 positional，再按参数名绑定 named；
参数必须恰好绑定一次，不允许重复、未知或缺参，不提供默认值或隐式展开。所有值递归
验证为 Data，再验证参数注解。无参数入口（包括隐式 main）接收
`{positional:[],named:{}}`。例如 `flow Main(x:number,y:string)->object` 可接收
`{positional:[2],named:{y:"a"}}`，也可接收 `{positional:[],named:{x:2,y:"a"}}`。

入口选择按第 2 节在先；输入结构、绑定或注解不符统一作为 Host 运行前拒绝 E-SCHEMA，
不分配 run_id，不建立 run journal、活动图节点、Claim store、根错误或 ErrorKey，不准入
provider。启用 durable profile 时，只有验证通过后才进入 begin；被拒绝的输入不创建
持久 run 或调用记录。这与源码调用处静态 E-ARITY/E-TYPE 或节点内动态 E-SCHEMA 的
诊断阶段不同。成功输出仍在返回节点
验证注解；仅根入口输出还须验证 Data。子 Flow 的成功普通值不做序列化或 Data 检查。
void 没有可验证的返回值。

命令行、SDK 或 HTTP 可提供便捷输入格式，但必须在验证前明确转换为 EntryInput；
不能按运行时值猜测参数映射。第 12.1 节 HTTP 固定转换为
`{positional:[request],named:{}}`。第 11 节持久化的输入就是验证通过的完整 EntryInput，
其规范值与入口选择一并固定，恢复时不得重新解释便捷格式。

Host 在整个入口实例完整成功后收到以下逻辑结果，不能只因 return 值提前就绪而发布：

```text
EntryOutput = {kind:"value", value:Data} | {kind:"void"}
```

有返回值的入口只使用 value 分支，value 是通过返回注解与 Data 检查的结果；void 入口
（包括隐式 main）只使用 void 分支且没有 value 字段。各分支不得含其他字段。
返回 null 的成功结果为 `{kind:"value",value:null}`，与 `{kind:"void"}` 区分。
失败或取消不产生 EntryOutput，按既有诊断与运行状态向 Host 报告。此结构不是新的
LIP 值类型或错误捕获机制；命令行/SDK 可显式解包展示，HTTP 按第 12.1 节使用 value。

## 3. 值和核心操作

Data 是有限、无环、按值的数据树。object 不得有重复键，按 UTF-8 键序定义规范比较与
编码；list 保留顺序。函数、effect、原生指针和活动资源不属于 Data。adapter 返回的原生
mutable 对象必须复制为 Data，或转为第 10 节的 Host 资源引用，不能直接共享。

`==`/`!=` 按类型和递归结构比较，object 忽略存储顺序；function/effect 不支持比较。
递归比较遇到 function/effect 也报 `E-TYPE`，不使用指针地址判等。null 只等于 null，
bool 与 number 不相等。对象字段/索引不产生隐式绑定，重复 object 字段报静态错误。
大小比较接受两个 number、两个 char 或两个 string；char 按 Unicode code point 数值比较，
string 按 UTF-8 bytes 字典序（前缀较短者在前），与 keys 一致，不使用 locale 或 Unicode
normalization；混合类型报 E-TYPE。char 不参与算术或 string/list 的隐式转换。
逻辑操作只接受 bool。`+` 接受两个 number 或两个 string，
没有其他隐式类型转换。参数、字段、元素、算术子表达式从左到右求值；Error 后不继续
求值右侧。`&&`、`||`、if/match 未选分支不求值。

number 为 IEEE 754 binary64，含第 3.3 节的正负无穷与规范 NaN；`-0` 规范化为 `0`。以下数值域规则适用于有限操作数；非有限操作数先按第 3.3 节处理。基本四则按 binary64 的 round-to-
nearest/ties-to-even 求值，禁止浮点重结合或影响结果的隐式 FMA；结果允许为规范 NaN 或正负无穷。
`a / 0` 按 IEEE-754 产生带符号无穷，`0 / 0` 为 NaN；`a // 0` 与 `a % 0` 为 NaN。
`a // b` 为精确数学商向负无穷取整后的 binary64 值；`a % b` 是与该取整
对应的数学余数，舍入后必须满足 b>0 时 `[0,b)`、b<0 时 `(b,0]`。若舍入等于排除的
边界 b，取 `nextafter(b,0)`；零规范化为 0。这里 a、b 是两个 number 操作数，
`nextafter(b,0)` 表示从 b 向零移动一个 binary64 可表示值，是数值规则的辅助记号，
不是 LIP 内建函数。实现不得使用截断除法代替 floor 除法。

`a ** b` 为实数幂，`a */ b` 为以右操作数 b 为底的实数对数。两者只接受两个 number，
类型不符报 E-TYPE。幂的约定为 `0 ** 0 = 1`；零底数的负指数返回 `+inf`；负底数的非整数指数返回
NaN。这里“整数指数”指存储的 binary64 值精确为整数，不受索引安全整数范围限制。
对数要求 a>=0、b>0 且 b!=1；非法定义域返回 NaN。`0 */ b` 在 b>1 时为 -inf，在 0<b<1 时为 +inf；
有限合法输入中 `1 */ b = 0`、`a */ a = 1`。超出有限 number 范围的结果返回带符号无穷；允许次正规结果和下溢为零，并规范化负零。
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

安全整数的绝对值不超过 `2^53-1`；Host 原生整数超出范围时必须拒绝，或按显式 schema
编码为 string/bytes。list/string 的单值索引 `v[i]` 接受安全整数。设长度为 n，负 i
只调整一次为 n+i；调整后的索引必须位于 [0,n)，否则 `E-INDEX`。string 按 Unicode
标量值计数，不按字素簇，不做正规化。object 的索引仍为 string 键，`.name` 等价于
该键查找；缺键报 `E-INDEX`，索引或接收值类型不符报 `E-TYPE`。

list a 还接受索引列表 `a[indices]`：indices 的每项必须为安全整数，逐项遵循单值负索引
规则，返回按索引顺序排列的新 list，保留重复。任一索引非法则整个表达式失败；不隐含
布尔掩码、递归展开或多维索引。空索引列表返回空 list，空源列表同样如此。string 不接受
索引列表。例如 `a=range(5)` 时，`a[[3,-1,3,0]]` 为 `[3,4,3,0]`，
`a[range(3)]` 为 `[0,1,2]`。

list/string 的切片写作 `v[start:stop]` 或 `v[start:stop:step]`，返回同类型值，
不包含 stop。各分量均可省略，显式 null 等同省略。非 null 分量必须为安全整数：
类型不符报 `E-TYPE`，非整数或超安全范围报 `E-INDEX`。step 默认 1，零报 `E-VALUE`。
边界以精确整数运算按以下规则归一化：

- step 为正时，省略 start/stop 分别取 0/n。显式负边界加 n 一次，再分别裁剪到 [0,n]。
- step 为负时，省略 start/stop 分别取 n−1/−1。显式负边界加 n 一次，再分别裁剪到
  [−1,n−1]。因此省略 stop 与显式 −1 不同，后者在裁剪前表示最后一项。
- 从归一化 start 开始按 step 递进；正步长在索引 < stop 时选取，负步长在索引 > stop
  时选取。方向不符返回空 list/string。切片边界越界会裁剪，空源同样适用；单值和列表
  索引仍严格检查越界。

接收值和给出的索引/边界表达式从左到右各求值一次（接收值、start、stop、step），
遇到错误即停止。新 list 在分配前检查长度预算，超出报 `E-BUDGET`，不发布部分结果。
这些操作保留不可变值，包括 function/effect 元素，不调用它们。投影、索引列表与切片的
静态描述按第 5 节传播，缺少依据时为 Unknown。所有执行路径上的核心 Data 遵循以上规则；不透明
Host 资源按其注册的原生索引协议访问。

| 表达式 | 结果 |
|---|---|
| `range(5)[-1]` | `4` |
| `range(5)[::-1]` | `[4,3,2,1,0]` |
| `range(5)[1:5:2]` | `[1,3]` |
| `range(5)[99:-99:-2]` | `[4,2,0]` |
| `range(5)[:-1:-1]` / `range(5)[:null:-1]` | `[]` / `[4,3,2,1,0]` |
| `"a𐀀b"[::-1]` | `"b𐀀a"` |
| `[][:]` / `""[::-1]` | `[]` / `""` |
| `[0][-2]` / `[0][[0,1]]` / `[0][0.5]` | E-INDEX |
| `[0][true]` / `"a"[[0]]` / `[0][:"x"]` | E-TYPE |
| `[0][::0]` | E-VALUE |

### 3.1. 核心纯函数表

不可覆写的核心函数由下表及 §3.2 的诊断函数组成；按 §3.2 的计算纯性规则接收 V、返回值，不修改输入。

| 签名 | arity | 参数/结果与规则 |
|---|---|---|
| len(value) | 1 | string/list/object → number 长度；string 按 Unicode 标量计数 |
| str(value) | 1 | Data 或 effect → string；string 原值，Data 为无空白规范显示，effect 为第 3.1.1 节的稳定审阅摘要 |
| range(end) / range(start,end) / range(start,end,step) | 1/2/3 | 安全整数、非零 step → 半开 number list；默认 start=0、step=1 |
| map(xs,f) | 2 | list、纯一元 function → 按输入顺序的新 list |
| mapcat(xs,f) | 2 | list、纯一元 function（每次返回 list）→ 按输入顺序映射并一层合并为新 list |
| fold(xs,seed,f) | 3 | list、V、纯二元 function → V；严格左折叠，空 list 返回 seed |
| fail(message) | 1 | string → E-FAIL，没有成功值 |
| concat(a,b) | 2 | 两个同类 list 或两个 string → 顺序拼接的新值；append 用 concat(a,[v]) |
| allcat(parts) | 1 | 非空 list 中的一层同类 list 或 string parts → 顺序合并；不递归展开，list/string 不混合 |
| to_chars(s) | 1 | string → 按 Unicode scalar 顺序生成 list<char> |
| from_chars(chars) | 1 | 只含 char 的 list → string；其他元素报 E-TYPE |
| to_codepoint(c) | 1 | char → Unicode code point number |
| from_codepoint(n) | 1 | safe integer number → Unicode scalar char；surrogate、越界和非有限数报 E-VALUE |
| slice(v,start,stop) / slice(v,start,stop,step) | 3/4 | 与方括号切片同规则；null 表示省略分量 |
| keys(o) | 1 | object → UTF-8 bytes 排序的 string list |
| has(o,k) | 2 | object、string → bool；缺键返回 false |
| put(o,k,v) | 3 | object、string、V → 增加/替换该键的新 object |
| split(s,separator) | 2 | string、非空 string → string list；左到右非重叠，保留空段 |
| parse_number(s) | 1 | string → number；有限数文本或恰为 "nan"、"inf"、"+inf"、"-inf"，完整文本转换，详见下文 |

核心函数不接受命名参数；直接可知时静态 E-ARITY，动态核心函数值调用时同样验证。
它们可以作为纯 function 值传递。map 的 f 接受一个元素，fold 的 f 接受
`(accumulator, element)`；实际 callable/arity/类型按第 5 节验证。推导式没有 guard 时
与顺序 map 等价；可选 guard 按第 6.2 节过滤。Flow 中允许 perform 的推导式依次执行，
与 list literal 一样保留元素顺序。range 在分配前检查长度预算。
len 的返回数必须是安全整数，超过范围须明确失败；不得舍入成错误长度。range 使用
精确整数计算其长度和元素，再转成 number，不用浮点循环判断制造重复/漏项。
slice 与上述方括号切片完全同义，支持负边界、裁剪、null 省略和可选非零 step。
`slice(v,null,null,-1)` 等于 `v[::-1]`。has/put 不接受非 string 键；缺键 has 返回 false，put 添加字段。
split 的 separator 为空时报 `E-VALUE`；`split("",",")` 为 `[""]`，
`split("a,,b,",",")` 为 `["a","","b",""]`。这些操作按声明 arity 检查，没有隐藏参数。
parse_number 接受整段 ASCII 文本：恰为小写 `"nan"`、`"inf"`、`"+inf"`、`"-inf"`，或可选一个 `+`/`-` 后恰为第 2 节 number 词法。
不接受空白、前导零、hex、`"NaN"`、`"+nan"`、`"-nan"`、Infinity 或尾部字符；非法文本/溢出报 E-VALUE，类型不符
报 E-TYPE。nearest/ties-to-even、次正规数/下溢和零规范化与字面量一致，不使用 Host 的
宽松数字 parser。`parse_number(str(n)) == n` 对所有非 NaN number 成立；NaN 的往返用 is_nan 验证。

### effect 的审阅摘要

`str(p)` 在 `p` 为未执行 effect 时返回一个紧凑、规范 JSON-like 的字符串。它只读取 proposal
中已经固定的 operation、contract 和 Data 参数，不读取 provider、原生 receiver、CallID、run/
node/path 身份、资源租约或 journal 状态，也不执行 provider、授权、资源准入或参数转换。
因此 `str(p)` 是 Pure；它可以出现在 fn、guard、proposal 参数、Flow 条件及未选分支中，
未选分支不会产生任何外部动作。

摘要的顶层结构固定为：

```text
{
  "arguments": {"named": object, "positional": list},
  "contract": {
    "capabilities": list<string>, "concurrent": bool,
    "effect": "ReadOnly" | "Write" | "Unknown" | "Pure",
    "parameters": [{"name": string, "optional": bool, "schema": SchemaSummary}],
    "resources": object<string, number|string>, "result": SchemaSummary,
    "version": string
  },
  "kind": "effect",
  "operation": {"language": string, "member": string, "path": string}
}
```

`SchemaSummary` 至少含 `{"type":string}`；有 `items`、`fields`、`required` 时递归包含它们，
其中 `fields` 按 UTF-8 键排序，`required` 按 UTF-8 排序。`named` 的键和所有普通 object
字段按 UTF-8 排序；`positional` 与参数声明顺序保持不变；capability 按 UTF-8 排序。
资源值按合约的非负整数以规范 number 显示。字符串转义、数字（包括 `nan`/`inf`）和容器
编码沿用本节规范显示；safe integer 以内的资源单位为 number，超出该范围时为十进制
string 以保持 uint64 精度；参数中的 native reference 递归显示为不含 id/owner 的
`"<language type>"`，不泄漏运行时资源身份；摘要本身不是 fingerprint、身份或 Host 的传输编码。

effect 只有直接作为 `str` 实参时才使用上述摘要；包含 effect 的 list/object 仍不是 Data，
`str` 报 E-TYPE。fn 仍不能 `str`。摘要不是字段投影，effect 仍不可比较；程序应比较摘要
字符串或根据同一份构造 Data 得出审阅决定，再在通过的分支执行 `perform p`。摘要可作为
普通 string 传给 fn/Flow，但不构成跨 Flow 的人工审批消息或授权记录。Host/REPL 可以另外
显示 Go 导出签名或 Python 文档；这些帮助文本不属于 `str(effect)` 的稳定规范内容。

`allcat` 只展开外层 parts：`allcat([[1],[2,3]])` 为 `[1,2,3]`，不会递归展开
嵌套 list。它要求 parts 非空且每一项同为 list 或同为 string；`concat([1],[2])` 与
`concat("a","b")` 保留为两个参数的拼接函数，list 与 string 不能混用。`to_chars` 和
`from_chars` 使用 Unicode scalar（不是 grapheme cluster）；`from_codepoint` 只接受有限
safe integer 且拒绝 surrogate。char 可作为参数、返回值、list 元素和 match pattern，但
不参与 number 算术。普通 JSON 将 char 表示为只含一个 Unicode scalar 的 string；需要保留
char 类型身份时使用第 16.6 节的 c-hex/canonical 编码。
`mapcat(xs,f)` 是 `allcat(map(xs,f))` 的 list 专用组合；回调每次必须返回 list，结果只合并一层，
不把 string 当作 list 片段，也不递归展开嵌套 list。

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

有限 number 的规范 JSON 按下述独立规则显示，而不使用 Host 的默认打印：零为 `0`。对非零
binary64 的精确数学值 v，在能 round-trip 为 v 的十进制数 `m×10^e` 中选最少有效数字
的 m（m 不含前导/末尾零）；再选与 v 精确距离最小者；仍平局则选绝对值较小者。
设该十进制数的科学指数为 k：`-6≤k<21` 时输出无指数的普通十进制，否则输出一位整数
加可选小数、小写 `e` 和指数。指数无正号/前导零；普通小数必须有整数部分、不得有
无意义末尾零。负数前置 `-`。例如 `str(1e3)` 为 `1000`，`str(1e21)` 为 `1e21`。
JSON string 仅转义引号、反斜杠、U+0000–001F，其余 UTF-8 原样；控制字符统一
`\u00xx` 小写编码。list/object 递归使用此规则，字段按 UTF-8 bytes 排序。
`str(function)` 及递归含 fn/effect 的容器非法；直接 `str(effect)` 使用第 3.1.1 节摘要。
这是本版的规范显示，不是 RFC 8785/JCS（JSON Canonicalization Scheme），也不要求
与 JavaScript 的 JSON.stringify 字节相同。str 仅用于显示，`str("1")==str(1)` 合法；
fingerprint（参数摘要）与身份使用第 16.1 节定义的带类型编码函数 C，不使用 str。
JSON 输入若含重复 object 键（包括解码后相同键）必须拒绝，不能由 parser 静默覆盖。

fn 的 environment 只捕获不可变快照；可以结构共享不可变值，不要求每次调用物理深拷贝。
创建闭包不得捕获活动 Claim store，也不得通过包装外部原生 callable 绕过统一 perform 边界；
闭包内的原生调用同样必须使用第 10.3 节 @{...} 函数体显式承诺纯性。
不可变资源引用和请求值可以捕获、返回或跨子 Flow 传递；捕获不执行操作，实际
使用仍须 perform，并遵守原引用的所有权、权限和寿命。Flow 可以把读出的 Data 快照
传入 fn；fn 内不访问活跃 store。
`g = fn() -> list { facts }` 若 facts 是 store 名，静态报 `E-PURITY`，不在闭包创建时
执行完整读取。捕获快照须先 `captured = facts`，再由闭包引用 captured；该 binding
受完整读取的依赖和位置约束。

### 3.2. 不反馈计算的诊断输出

本版的 Pure 指计算结果与业务效果的纯性：固定实参及不可变捕获后，纯计算的值、错误和
控制选择不依赖诊断接收端。fn 可以产生本节规定的诊断输出；这不赋予任意外部执行权限。
诊断流不是程序可读取的值、Claim 或共享状态。将诊断流也作为观测对象时，不承诺完整的
引用透明或跨调度相同的输出次序。诊断规则本身不允许修改输入、读取时钟/随机/外部状态或调用 Flow。fn 内原生调用的
显式 @{...} 纯性承诺另按第 10.3 节，不由本节的诊断规则证明。

| 核心函数 | arity | 参数与结果 |
|---|---|---|
| print(value) | 1 | 任意 V 的诊断显示；返回固定 null |
| log(level,value) | 2 | string 级别标签与任意 V；返回固定 null |
| telemetry(name,fields) | 2 | string 事件名与 object 字段；返回固定 null |

以上名字遵守核心函数的解析、不可覆写及位置参数规则，不是新关键字。它们可用于 fn、
闭包、递归、高阶回调、管道、guard、proposal 参数和 carry 出口等 Pure 位置；运行时类型
检查与普通核心调用相同。log 不校验级别枚举。项目可用纯 fn 封装格式与字段。
它们自身的计算效果为 Pure，不降低 RetOps 精度，不引入 barrier、provider 准入、CallID、
Claim 或 journal 记录。实参的效果仍须完整合并：Flow 内 print 的实参若含 perform，
该执行仍计入节点效果；普通 fn 内 perform 仍为 E-PURITY，@{...} 内按第 10.3 节的显式承诺计为 Pure。

每次实际求值到合法诊断调用时进行一次发送尝试；正常路径返回 null。未选分支不发送，
声明、补全、静态检查与构建不执行诊断。即使关闭输出，实参仍按正常调用顺序求值并检查；
print(1/0) 仍先求值参数并显示 inf。编译器不得仅依据返回值纯性删除、合并、缓存或提前
执行带诊断的调用，从而改变实际执行路径的发送尝试。纯值优化仍须保留这些尝试与参数错误。

接收端只接受已经固定的事件，不向 LIP 返回发送状态，不同步回调 LIP，不提供程序可读的
发送计数或缓存。关闭、满队列或发送故障不转为程序错误，不触发重试、取消或控制选择。
运行时使用有界、可丢弃的缓冲；业务计算不等待外部接收端。诊断显示同样有界，过长/过深
内容可以截断；函数与原生引用仅作不透明显示，不执行它们，不隐式调用 native_data
或外部属性；effect 使用第 3.1.1 节摘要，但仍不执行 provider。直接把 effect 作为诊断值时，
人读显示必须先输出 `operation: value`，其中 value 是便于定位的
`language.path.member`（path 为空时省略该段）；随后按固定的顶层顺序输出 operation 的
`operation.language: ...`、`operation.path: ...`、`operation.member: ...`，再输出
`kind`、`contract` 和 `arguments` 的递归路径。每个叶子字段独占一行，合法标识符键使用点号，
其他键使用 `["key"]` 形式，list 使用 `[index]`（例如
`contract.parameters[0].schema.type: "string"`）；空 object/list 仍输出
该路径及 `{}`/`[]`。这里的点号形式只是诊断定位文本，不是 operation 身份或传输编码；值
使用第 3.1.1 节的规范显示。完整无损摘要仍由 `str(effect)` 提供。队列保存独立的文本与位置，
完整无损摘要仍由 `str(effect)` 提供。队列保存独立的文本与位置，
不保留业务值、闭包、AST 或资源。

同一局部游标中的发送尝试遵守求值次序，独立图节点之间不承诺诊断全序。恢复/重放只输出
本次实际重新求值的诊断，不提供恰好一次投递。源码中加入诊断仍改变 SourceHash、revision
和相关源码身份，不允许借此续用旧源码的 journal。取消、指令预算及参数本身的错误仍按
现有规则处理；诊断缓冲容量与截断不改变业务预算。

CLI 与生成程序默认将诊断显示到 stderr，stdout 继续承载入口结果；REPL 与 learn 也可
显示诊断而不把 null 当作业务反馈。嵌入 API 默认可不接收诊断；接收端的配置属于运行环境。
业务审计、要求确认送达的遥测、可读取的日志、文件/网络目标和根据发送结果分支的操作，
仍使用注册合约及 effect/perform。Go/Python 中名字恰好为 print/log 的操作不自动获得豁免。

```lip
fn doubled(x:number)->number {
    log("debug", {input:x})
    telemetry("calculation", {operation:"double"})
    x * 2
}
flow Main()->number { return doubled(3) }
```

本例在诊断开启、关闭或缓冲已满时均返回 6；诊断输出不属于该数值结果。

### 3.3. NaN 与无穷数值

`nan` 是不可重定义的 number 字面量，不是 null、string 或外部属性；可用于静态 Data、
number/number?/any 注解、容器、Claim 和入口/Host 值。所有原生 quiet/signaling NaN 的
符号与 payload 在进入 LIP 前统一为 binary64 bits `0x7ff8000000000000`；不得保留可观察的
payload。`inf` 同样是不可重定义的 number 字面量，表示 binary64 正无穷（bits 0x7ff0000000000000）；
`-inf` 是其一元负号形式（bits 0xfff0000000000000）。原生 NaN 与正负无穷可进入
相同 Data 位置。有限操作数的除零、溢出和幂/对数非法定义域按第 3 节产生 NaN 或带符号无穷；它们不是控制流错误。

NaN 检测可由现有比较组合，无需新增核心函数。以下 is_nan 指普通辅助 fn，使用时自行声明：

```lip
fn is_nan(x:number)->bool { x != x }
```

- number 间比较时，任一操作数为 NaN 则 `==` 为 false，`!=` 为 true，`< <= > >=` 均为 false；
  包括 `nan == nan` 为 false。容器结构比较递归遵守该规则，不能用对象身份或 C 字节
  相同替代语言比较；遇到不可比较的 function/effect 仍按原规则报错。
- 一元 `+nan`、`-nan` 均为 nan。二元运算先检查操作数类型；任一为 NaN 时，
  `+ - * / // % */` 返回 nan，再无有限数域检查，所以 `nan / 0` 为 nan。
  `**` 先处理指数为有限零或底数为有限 1 的情形并返回 1；否则任一 NaN 返回 nan。
  因此 `nan ** 0`、`1 ** nan` 为 1，`0 ** nan`、`(-2) ** nan` 为 nan。
  实参仍严格从左到右求值，`nan + fail("stop")` 仍失败。
- NaN 不满足安全整数要求：索引/切片用 E-INDEX，range 用 E-VALUE；不能转换为宿主
  整数。match 的 number 类型 pattern 匹配 NaN，nan 字面量 pattern 按相等规则不匹配
  任何值；检测应使用 `is_nan(x)`，或 `number if is_nan(x)` guard。
- `str(nan)` 恰为 `"nan"`；容器显示在相应数值位置递归使用裸 `nan`，如 `[nan]`。
  除原样返回的顶层 string 外，仅含有限数的显示仍为普通 JSON。print/REPL 可使用此显示；协议不得将其
  误当 JSON，不得静默替换为 null 或 string。无损传输按第 16.6 节。

语言相等与身份一致性不同：C(nan) 固定（第 16.1 节）。同 JoinID 的同一 NaN 贡献重投
幂等，同 CallID 的规范 NaN 实参可重放；与 null、string "nan" 或其他值均不合并。

`inf` 与 `-inf` 的规则如下。运算先检查类型，再处理上述幂恒等式与 NaN，
再处理本表，最后才进入有限数算法。`// %` 的零除数在 NaN 传播后总是返回 NaN；`/` 的零除数按 IEEE-754 返回带符号无穷或 NaN，
包括 `inf / 0`；实参仍按通常顺序严格求值。

| 情形 | 结果 |
|---|---|
| 比较与 pattern | 对每个有限 x，负无穷小于 x，x 小于正无穷；同号无穷相等，异号不等；NaN 仍无序；inf/-inf literal pattern 匹配同号无穷 |
| 一元 `+ -` | 正号不变，负号交换无穷的符号 |
| `+ - * /` 至少一个无穷操作数 | IEEE binary64 规则；同号无穷相减、异号无穷相加、零乘无穷、无穷除无穷为 nan；有限数除无穷为规范零 |
| `a // b` 或 `a % b`，a 为无穷、b 非零 | nan，包括 b 也为无穷 |
| a 有限、b 为无穷，`a // b` | a 非零且与 b 异号时为 -1，其他情形为 0 |
| a 有限、b 为无穷，`a % b` | a 非零且与 b 异号时为 b，其他情形为 a，零仍规范化 |
| `a ** b`，b 为无穷 | abs(a)==1 时为 1；abs(a)>1 时正无穷指数得 inf、负无穷指数得 0；abs(a)<1 时反之，含零底数 |
| `a ** b`，a 为无穷、b 为有限非零数 | b<0 时为 0；b>0 时为 inf，仅 a 为负无穷且 b 为奇整数时为 -inf；非整数 b 得正 inf |
| `a */ b`，含无穷 | 先要求 a>0、b>0、b!=1；a=b=inf 得 nan；仅 a=inf 时 b>1 得 inf、b<1 得 -inf；仅 b=inf 得 0 |

abs 是描述规则的数学记号，不增加核心函数。已有 `a ** 0 = 1`、`1 ** b = 1`
优先于 NaN/无穷规则；例如 `inf ** 0 = 1`、`(-1) ** inf = 1`、`0 ** -inf = inf`。
有限操作数的负底数非整数幂返回 NaN；有限数算法的溢出结果转为带符号 inf。
整除与余数使用有限除数趋向指定符号无穷的单侧极限规则，与 Python 相同；
不对无穷使用精确有理数算法，也不要求无穷情形满足有限余数恒等式。

`str(inf)`、`str(-inf)` 分别为 "inf"、"-inf"，容器递归显示这些裸文本。
parse_number 接受精确小写 "inf"、"+inf"、"-inf"，拒绝 "Inf"、"Infinity"。
无穷不满足安全整数要求：索引/切片为 E-INDEX，range 为 E-VALUE，宿主整数参数转换拒绝；
Go float32/float64 与 Python float 可传递其符号。普通 JSON 拒绝非有限数；无损协议见第 16.6 节。
可用普通 `fn is_inf(x:number)->bool { x == inf || x == -inf }` 检测，无需新增核心函数。
C 对正负无穷分别保留其 bits，Claim/journal 身份不会混淆符号、NaN、null 或 string。

## 4. 函数的计算能力与执行机

语言不规定 fn 的最大迭代数或最大递归次数。递归终止性不属于静态类型判定；不终止的
纯函数表示发散，不得伪装成成功、返回 null 或被静默截断。

实现必须支持 proper tail calls：包括直接、互递归和 function 值的间接尾调用。尾调用
复用 continuation，不得因尾调用次数累计栈帧或触发递归深度限制。
`if`/`match` 选中分支的最终调用也是尾位置。
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

预算耗尽是执行失败，不是函数返回值。实现不得按返回类型自动补成 null、0、false、
空字符串、空 list 或空 object；即使返回注解允许 null 或 any 也一样。Host 可以按
明确约定的应用策略将诊断映射为错误或降级响应，但不能把失败的执行当作成功的
EntryOutput。

## 5. 类型和纯求值

静态判断写作 `Γ ; S ⊢ e : τ ! ε`，表示在普通名称环境 Γ 和 Claim store 环境 S 中，
表达式 e 的类型为 τ、求值效果为 ε。Γ 保存普通值/函数名的类型和可用分析信息，
S 保存当前 Flow 实例中已声明 store 的身份；`Γ(x)=τ` 表示普通名称 x 的类型为 τ。编码函数 C
只用于第 16.1 节的数据编码，不用作环境记号。
另用 Φ 表示 source set 中以模块限定名标识的 Flow 签名与结构摘要表；Φ 不是普通值环境，Flow 调用先按
词法解析确认声明，再使用该表检查。Flow 不是 function 类型，不参与动态 callable 判断。

| 效果 ε | 含义 |
|---|---|
| Pure | 纯核心计算、第 3.2 节单向诊断及第 10.3 节作者承诺纯性的 fn 计算；其业务纯性依赖承诺成立 |
| ReadOnly | 外部读取；注册合约保证没有外部写入 |
| Write | 可能改变外部状态的操作 |
| Unknown | 无法证明实际操作集合或效果；按写入限制准入 |

Recorded 是 perform 观察的 journal 元数据，不是独立类型维度或传播到普通引用的标签。
Unknown 的启动/并发限制等同 Write；幂等性是 Host 合约，不是单独的语言效果等级。
Concurrent 是注册合约的 bool 标记；true 表示该 operation 允许多个调用重叠执行，
false 表示不得重叠调用：fn 外按第 7 节 barrier 规则调度，@{...} 内按第 10.3 节由 Host 串行准入。
ReadOnly 和 @{...} 的作者承诺均不隐含 Concurrent=true。
判定隐含一个本 run 固定的 operation registry Σ；registry 的 name/version/schema/
效果合约在 run 内不可变，执行时 capability 的撤销可导致明确拒绝，不改变既有 proposal。
`any` 为 V 的顶类型；`τ?` 表示 τ 或 null，`any?` 等于 any。list/object 注解约束外形，
不隐含所有内容均是 Data。源类型只有第 2 节列出的裸类型和可选类型；`list<object>` 等
表示数学描述，不是源语法。闭包值携带实际参数名/类型/arity/返回注解，裸 function
参数不静态声明这些信息；所有语言 fn 在类型/效果判断中均为 Pure，原生部分必须使用第 10.3 节 @{...} 显式承诺。无法静态检查的 arity/参数/结果必须在
调用时检查，不要求编译器从任意高阶函数推导完整签名。
普通函数参数按声明名称和源码参数顺序绑定，先位置后命名；重复、缺少、未知参数及 arity
不符报错。没有默认参数、隐式展开或可变参数。核心 range 的 arity 为 1/2/3。
静态可知的类型错误拒绝编译，any 带来的动态不符报 `E-TYPE`；Flow/adapter schema 不符
报 `E-SCHEMA`。没有隐式 string/number/bool 互转。

可静态确定的 callable、arity、参数名和声明类型错误必须在运行前拒绝；有限候选不同
时按第 5.2 节判定。动态函数值/any 遮蔽的信息留到运行时。动态普通调用统一按以下顺序执行：

1. 求值 callee，验证为语言闭包或第 3 节核心函数；原生 callable 报 E-TYPE。
2. 用实际签名检查实参结构、参数名、重复绑定、缺参和 arity；不符报 E-ARITY，尚不求值
   实参表达式。核心函数拒绝命名参数。
3. 按源码顺序从左到右求值全部实参，求值失败则停止；全部成功后才按参数声明顺序
   检查类型，失败时函数体不执行。较早参数的类型不符不省略后续实参求值。
4. 执行函数体；正常结果通过返回注解检查后才发布。返回类型失败时函数体已经执行，
   结果不作为成功值发布。第 3 步已经发生的 perform 不因后续类型失败回滚。

步骤 1 的 callee 求值可以本身包含已允许的外部动作（例如调用带 `@` 的函数并返回
闭包）；这些动作先于 callable/arity 检查发生并保留其 journal 事实。步骤 2 失败时，
只有尚未开始的参数和 body 被跳过；“arity 先于实参”只约束实参，不撤销 callee 的
求值。步骤 3 的参数类型错误同样不撤销已经成功的 callee/实参动作。

map/fold 逐次回调采用相同规则。外层调用先验证 map/fold 自身的固定 arity，再按源码
顺序求值 source、seed（fold 才有）和 callback 表达式；全部实参求值成功后，按核心函数
声明顺序检查 list/seed 类型，再检查 callback 是语言闭包或核心函数并验证其 arity。
因此 source 已知不是 list 时，错误早于 callback 的 arity 错误；callback 非 callable
时，错误早于 callback 的 arity 错误。空 source 也必须完成这些外层与 callback 检查，
但不激活回调、不求值回调 body、不产生回调诊断或由回调 body 触发的 provider 调用；
source/seed/callback 表达式自身在外层实参求值中产生的动作仍照常保留。已知签名的返回注解
和纯性仍在静态阶段检查。每个非空元素的回调调用再按本节的实参求值、参数类型、body、
返回注解顺序执行。proposal 按 operation schema 绑定参数，不把 Host operation 当作
普通 callable；构造请求和真正执行的阶段按第 10 节区分。

子 Flow 先静态验证目标、arity、参数名和可确定的注解约束，再按源码顺序求值实参，
验证全部参数注解，最后实例化子图。动态注解不符为 E-SCHEMA，子图 body 不执行；
此前实参中的已完成外部动作保留事实。正常输出验证返回注解，失败时已完成的 perform/join
保留事实。fn、effect 及其容器可以跨同一 run 内的 Flow 边界；Flow 定义和活动 store
始终不是值。已完成的 Claim 读取是 Data 快照，可作为普通值传递或被闭包捕获。
可静态确定的注解不符为 E-TYPE，信息不足时的动态不符为 E-SCHEMA。Host EntryInput/
EntryOutput 按第 2.3 节独立验证 Data；不能把运行前拒绝伪装成节点错误。

`Summary(F)` 表示 F 的保守执行效果与 Concurrent 上界：fn body 外的 perform 使用本节
Ops 规则，语言 fn 调用的函数体按第 10.3 节为 Pure，调用的子 Flow 使用其 Summary，
再合并全部分支、循环和节点；调用 fn 的实参仍按调用方上下文计入。
调用图无环，因此可按被调用者先于调用者的顺序计算；不按常量 guard 删去候选路径。
没有 perform 时为 Pure；有 Unknown 则保留 Unknown。Flow 的实际执行效果纳入调用方
表达式/节点的 ε，读取其已完成普通值结果本身为 Pure。完整 Claim 读取的 producer
依赖不因 Summary 为 Pure 而省略。任意要求 Pure 的位置（fn、guard、proposal/join
RHS）都不得包含结构化子 Flow 调用；纯 Flow 的 Summary 不授予
它在这些位置中执行的资格。

parallel 的接受条件必须检查子 Flow 内全部传递的、位于 fn body 外的 perform 的规范 Ops，而不是只看返回
类型或 Flow 名。仅 Pure/join/已证明 Concurrent ReadOnly 的子图可以进入 parallel；
Write、Unknown 或不可并发 ReadOnly 均静态 E-PARALLEL。结构摘要使用第 5.1–5.3 节的规范描述；无依据的动态来源与未被第 5.2 节有限规则覆盖的 carry 结果仍为 Unknown。

下列规则中 op 是已解析 operation，args 是实参表达式集合，p 是 proposal 表达式。
`args : Data ! Pure` 表示每个实参均纯求值且结果属于 Data。横线以上为前提，以下为
结论。`ε ∨ ε′` 表示两种效果的上确界。`Ops(p)` 是 p 可能指向的非空有限 operation 集合或 Unknown，其推导规则见本节下表。
`EffectOf(Ops(p))` 在已知集合上取注册效果的最大值，Unknown 时为 Unknown；
`ResultType(Ops(p))` 合并这些合约的结果类型，Unknown 时为 any。效果次序和类型
合并规则在本节后文定义。这些辅助函数不是源函数，也不是 Claim join。`CallEffect(Ops(p))` 只在词法上的最近 fn body 为 @{...} 时取 Pure（第 10.3 节显式承诺），
其他位置等于 `EffectOf(Ops(p))`。普通 fn body 中 perform 仍静态 E-PURITY。

```text
Γ(x) = τ
──────────────────────────────────
Γ ; S ⊢ x : τ ! Pure

Γ ; S ⊢ args : Data ! Pure
─────────────────────────────────────────────────
Γ ; S ⊢ effect op(args) : effect ! Pure

Γ ; S ⊢ p : effect ! ε
──────────────────────────────────────────────────────────
Γ ; S ⊢ perform p : ResultType(Ops(p)) ! (ε ∨ CallEffect(Ops(p)))
```

perform 在调用方游标中先完整求值请求表达式 p，再执行得到的请求；p 可以含子 Flow 或
其他允许的求值，其效果与实际操作效果取上确界（记为 ∨）。求值 p 失败时不执行请求。
这不放宽 effect 构造参数必须 Pure Data 的要求。@{...} 函数体中的 perform 无须证明 Ops
或原生实现的纯性；作者通过该标记对实际操作作第 10.3 节承诺。普通 fn body 仍不允许
perform，但可调用满足该承诺的 fn。fn 外的 guard、proposal/join
RHS 和 carry 出口等 Pure 位置仍不允许直接 perform，但可以调用这样的 fn。
调用 fn 的 callee 和实参在调用方上下文求值，不能因目标是 fn 就隐去其中的非 Pure 执行。

ε 在复合表达式中取上确界：Pure<ReadOnly<Write<Unknown。引用一个已完成 binding 是
读固定值，不重新观察其来源。因此一次 perform 的结果可以进入下一次 proposal。
proposal 参数可调用 fn（含第 10.3 节原生调用）；fn body 外的外部读取、写入、即时状态和
随机仍必须先单独绑定。perform 返回的 Data 不建立新的返回 Ops 信息。
普通引用读取的是已固定值；S 中 store 名不是此标识符规则的对象。完整读取在所有 producer
成功终结后成为 Pure Data 节点，带第 9 节的隐式依赖；live snapshot 为
Recorded perform。这不把活动 store 误当成随时固定的变量。

### 5.1. 有限值描述与操作信息

`Info(e)` 是表达式正常返回值的静态描述，不是值本身、源类型或执行效果。
描述包括可用的裸类型、请求候选、可调用来源及容器位置；原生来源依第 10 节独立维护。
`Ops(e)` 只读取 Info 根位置的请求候选：非空有限 operation 集合，或 Unknown。
记 `RetOps(f,A)=Ops(RetInfo(f,A))`，Flow 同理；省略 A 表示参数无额外静态描述。
容器根不是其元素中的请求；读取容器不能自动执行或合并其中的请求。
每个 operation 保留规范身份，并通过本 run 固定 registry 取得 version/schema/效果/Concurrent。

描述的构造如下；这些是分析记号，不增加源语法或可跨 Host 传输的值：

```text
D ::= Unknown(reason) | Plain(type) | Param(i, type) | Request(operations)
    | Callable(origin, captures) | Object(fields, rest)
    | List(slots, tail, element) | Union(D, ...)
```

origin 标识定义时冻结源码快照中的语言 fn 或核心函数声明/表达式，不是地址或未限定
名称。不同模块/cell 中局部 site 或名字相同的声明仍是不同 origin。captures 只含捕获值
的抽象描述；递归捕获以绑定到同一词法快照的声明引用表示，不递归展开捕获函数，也不
按后来复用的名称查找。Object 的 fields 按已知 string 键索引，rest 为未知键处的描述或 Closed；
Closed 表示正常结果中不存在其他键。List 的 slots 为已知按位置描述；`tail=false`
表示长度精确，`tail=true` 表示这些 slots 是已知前缀、后面仍有未知长度的尾部，
`slots=Absent` 表示没有可证明的位置。element 为任一元素的保守联合，空列表在元素
被使用处按 Unknown 处理。
描述没有运行时对象所有权，也不得保留运行环境、业务值或整个源码树。
静态字段名和字面量下标属于源码选择信息，不构成对运行时值的采样。

Union 保留全部候选，不按常量 guard 删除分支。请求候选作集合并，任一候选未知或
不是请求则该位置的 Ops 为 Unknown；可调用候选同样必须全部已知才建立有限目标集。
不同容器先按候选各自投影，再联合结果，不能把第一候选的结构或签名套到其他候选上。
Plain/Unknown 可共享；实现不得仅为纯 Data 大列表强制建立逐元素来源记录。

`Param(i,type)` 是函数或 Flow 的第 i 个抽象实参来源，只用于静态摘要，不是新的运行时值或源类型。
`Request(operations)` 的 operation 摘要也可以暂时包含 `ParamOps(i)`；调用点拿到实际参数描述后再替换它。
声明体内部若参数尚未绑定到已知请求，`ParamOps(i)` 不提供 parallel 资格；替换后若得到完整有限候选，
调用点可以继续使用其 effect 与 Concurrent 属性。参数以外的 `any`、开放容器和动态 callable 仍为 Unknown。

Union 规范化满足结合、交换和幂等律：展开嵌套联合并去除完全相同的描述，不改变候选。
同一 origin 但抽象 captures 不同的 Callable 必须分别保留。Unknown 吸收的是请求资格，
不是第 15.1 节所需的已知证据；结果不得取决于联合遍历顺序或缓存中的首次插入。

投影没有正常返回位置时使用内部 NoResult 标记，它不同于 Unknown 和空容器。联合时
忽略 NoResult 分支；没有其他分支时对外成为 Unknown(missing_position)，不得得到空
操作集合或 perform 资格。这只是位置记账，不证明一般表达式会终止或失败。selector
及其运行时错误仍实际求值，全部源码分支仍检查语法、名称、类型和纯性。空 object/list
仍是普通容器，只是没有可选取的位置。

NoResult 必须有已知结构缺失的依据。Unknown、any 或没有已追踪外形的 Plain(object/list)
投影后仍为带来源原因的 Unknown，不能把缺少元数据当作字段/下标不存在。已知错误
receiver 类别按普通类型规则检查，不能增强返回信息。容器传递时，无外形的 list 使用
Unknown element 和 Absent slots；无外形的 object 使用 Unknown rest。固定键 put
可以建立写入字段的信息，但未追踪的 rest 仍 Unknown，不能变为 Closed。

### 5.2. 描述的传播与调用

以下表定义全部可增强请求与可调用信息的路径。未列出的核心操作以及本表有限规则之外的
carry/fold 结果只保留可证明的普通类型，其他信息为 Unknown。不得以私有优化放行本表仍为 Unknown
的 perform，也不得执行函数、加载原生模块或检查运行时闭包来构造描述。

| 表达式或边界 | 规范传播 |
|---|---|
| 字面量、普通绑定、括号、不可变捕获 | 标量为 Plain；引用保留已有描述；any 注解不抹去已有信息 |
| `effect op(args)` | Request({op})；构造参数的效果与 Data 检查另计，构造零 provider 调用 |
| 具名/匿名 fn、核心函数值 | Callable；捕获按词法定义环境固定，不因名字后来被复用而变化 |
| object/list 构造 | 为包含请求、可调用值或已追踪结构的位置保留描述；纯位置可共享 Plain 描述 |
| 固定字段或字面量整数下标 | 各候选分别选择；字段不在 fields 中时使用 rest，Closed 时除外；负下标按该候选自身已知长度归一，slots 为 Absent 时取 element；缺失位置为 NoResult |
| 动态下标或字段键 | 闭合对象取全部字段、列表取 element 的联合；开放对象还联合 rest；没有正常候选则 Unknown |
| 索引列表与切片 | 各候选 slots 已知时，按源码安全整数字面量列表或切片字面量界限依第 3 节选择；否则返回 `List(Absent,false,原 element)`；字面量空 gather 即使 slots 未知也为空，不执行 selector 来推断 |
| if/match、value-block | 联合全部尾值候选；局部绑定按顺序传递；静态分析不求值 guard，运行时仍按第 6 节求值 |
| 推导式 | source 的 element 作为迭代值描述，body 描述作为结果 element；无 guard 时复制 source 的 exact slots 或已知前缀；若 body 直接返回迭代变量，则逐位置保留对应 slot；有 guard 时长度退化为 Absent；不按 guard 过滤候选 |
| 顺序/并行普通 for | 迭代作用域绑定 source 的 element 描述，不按轮次专门化或按 guard 过滤；局部绑定不外泄；carry 状态/结果仍保守 |
| `concat`、`slice` | concat 联合 element；空 exact list 为恒等元，即使另一侧只有 Plain/Param 的 list 形状也保留该侧描述；已知左侧 slots 与未知右侧拼接时保留左前缀，双方 exact 时按序连接；Prefix 的 slice 在所有实际选择位置都落入已知前缀、stop 为安全非负字面量、start 可省略或为安全非负字面量且步长为正时保留 exact slots，否则按未知形状处理；不改变实参求值 |
| `put` | 已知键覆盖对应字段；动态键将写入值联合进每个已知字段及 rest，Closed 的 rest 改为该值，原对象不变 |
| 普通 fn 调用、静态子 Flow 返回值、管道阶段 | 使用下面规定的实参描述传递；执行效果仍单独计算 |
| `map` | 使用下面的回调规则；对每个已知 source slot 分别以该 slot 作为抽象实参应用回调，未知尾部以 source element 应用并保留为 tail；静态空 source 返回空 List，不传递回调 body |
| `fold` | 空 exact source 返回 seed 的描述；若全部 callback candidate 的正常返回直接是 accumulator 参数 0，则任何已知为 list 的 source 都返回 seed；其他有限 exact source 在本版 64 轮预算内按回调返回描述逐轮抽象执行，预算外或未知尾部为 Unknown |
| 有 source 且所有正常出口保持原状态的 `carry`，以及空 exact source 的有限 `carry` | 返回 init 的描述；carry body 仍完整检查。前者只识别每个 `next`/`break` 出口直接返回同一个状态 binding，不建立一般跨轮状态摘要；source-free carry 不适用该规则 |
| `range`、`keys`、字面量 `split` | 安全整数字面量的 `range`、闭合 object 的 `keys`、以及两个 string literal 的 `split` 在本版 4096 个静态 slot 预算内产生 exact slots；超出预算或输入形状未知时保留 element、slots=Absent |
| `perform`、入口输入、无描述的参数或外部结果 | 不从运行时结果推导请求或可调用来源；普通类型/原生返回来源仍由实际合约和第 10 节处理 |

表中的容器传播针对 list/object；string 的索引与切片保留 Plain(string)，原生 receiver
仍按第 10 节处理。固定键指字段选择或源码 string literal，不执行一般表达式取得键。
字面量界限仅指源码中的 number 安全整数、其一元正负号形式，以及切片的 null/省略
界限，不包括求值一般算术或 fn。标量字面量经普通参数/binding 转发后，后续引用不因此
成为字面量 selector。字面量选择得到空索引列表/切片时返回空 List，不是请求；闭合空
容器没有动态投影候选。任一 gather 下标无效时，该候选的整个投影为 NoResult，不返回
部分选择列表。静态空 source 仅指 `tail=false` 且 slots 长度为零，不执行 range、guard 或算术
来证明为空；推导式、map 和空 fold/carry 此时分别返回空 List、空 List、seed/init 的描述，
不传递回调或 carry body 的动态执行。索引、切片、字段访问的实际错误仍遵守第 3 节；
静态描述只覆盖正常返回路径，不能删去 selector、实参、错误、诊断或真实调用。Prefix slice 的精确规则不得根据未知总长度、负边界或省略 stop 推断未追踪位置；不满足条件时仍返回 Unknown 形状。

容器操作先逐候选应用，再联合结果。固定键 put 只替换该字段；后续固定覆盖可恢复
该字段精度，但不清除其他字段/rest 的来源。动态键还保留新增字段的可能性。核心函数
经别名、投影、回调或管道调用时仍使用实际核心 origin 的传播表及签名，不进入语言 fn
body；未支持的核心操作不会因换一种写法而增强信息。

源码 selector 外的冗余括号透明，不抹去字面量字段/下标/gather/切片界限，也不把
binding 引用、算术表达式或函数调用变成字面量 selector。

已知 fn 调用先依声明完成可静态确定的 callable/arity/命名参数/类型检查，再将实参
描述按实际参数名和顺序绑定，分析局部绑定及尾表达式，得到 `RetInfo(f,A)`；A 是
抽象实参，不是运行时值。返回位置引用参数时保留对应描述，因此
`fn identity(p:effect)->effect {p}` 在传入已知请求的调用处保留其 Ops。
裸 function 参数本身仍不声明签名；只有传入的静态描述提供目标时才能使用已知来源。
返回的闭包保留其抽象捕获，容器投影或函数返回不再单独构成信息丢失理由。

有限可调用候选逐个按各自签名和抽象 captures 分析。只有全部候选都能绑定实参结构，
且均无可证明的参数类型不符时才增强返回描述。未知实参类型仍动态检查，不据此创造
来源。只有部分候选不兼容时返回 Unknown(dynamic_target)，不得拒绝可能成功的调用，
也不得删除不兼容候选来取得并行资格。未知/非 callable 分支同样不能构成完整目标集。

可证明的参数类型不符指可用的实际类别与参数允许类别没有交集；部分重叠仍动态检查，
仅有 any 不能证明不符。这不授权利用运行时注解检查或仅凭返回声明类型，从描述中
删除请求或可调用候选。

独立取得的 callee/实参候选须保守覆盖笛卡尔组合，可以显式表示，也可以用实参联合
描述；不能按来源列表位置、排序次序或共同来源配对，也不从 guard 创造关联。闭包仍
各自保留自身 captures。不要求实际展开可能很大的笛卡尔积，分析预算失败仍须明确。

全部候选均存在可证明的调用边界错误时，即使错误不同也须静态拒绝。已知非 callable
分支构成 callable 阶段错误；未知分支则不能证明全部失败。按这些候选中最早
失败的验证阶段选择诊断：callable、实参结构、参数类型依次优先；同阶段按定义 SourceID
的 UTF-8 字节序、定义 site 数值和参数声明顺序决定，缺少来源按第 15.1 节排序；
全部候选失败均保留为 causes。
这仅选择静态诊断，不求值实参，也不改变运行时实际错误次序。一般源码检查仍遍历实参
和分支，不能因签名错误而省略静态检查。

map/fold 的外层实参和 callback 检查按本节调用阶段执行，空列表也不例外；实际每次
回调调用在 body 前检查参数类型。静态已知的签名/类型错误仍按第 3.1 节处理。空输入
不调用回调 body，不产生回调诊断、perform 或 activation，也不授权抽象执行回调；即使
回调是带 `@` 的函数，空输入也不调用其中的原生 provider。若外层 source/seed 检查与
callback callable/arity 同时失败，按外层声明参数顺序和 callable→arity 的阶段顺序选择
主错误，并保留其余原因。
map 返回增强要求每个已知 slot 上的全部候选都接受该 slot 的抽象实参；未知尾部才使用
联合 element。有限 fold 的每一步都要求全部候选接受当前 accumulator/item 描述；只要有一步无法证明，整个 fold 退化为 Unknown。Accumulator identity 只在全部候选返回
参数 0 时成立，不因返回注解或 guard 过滤候选。空 fold 不调用回调，也不需要回调返回描述。
carry identity 仍须在完整检查 body、出口纯性、名称和类型后确认；它只保留返回值描述，
不删除 body 的实际效果、错误、预算或控制流。返回注解本身不创造请求或可调用来源。

静态 Flow 调用使用同样的实参到返回位置传递，记为 `RetInfo(F,A)`；Flow 调用图无环。
这不把 Flow 变成值，也不在调用方按实参重写子图的执行效果：`Summary(F)` 仍在声明
环境中以无额外描述的参数保守计算。返回请求的精度与执行子图的资格分别判断。
例如子 Flow 的 body 对裸参数 p 执行 `perform p`，其 Summary 仍为 Unknown；
即使调用者传入已知读取，也不能把这个执行子图放入 parallel。只返回 p 的 Flow 则可
保留返回请求的信息，调用者随后 perform 时使用该信息。

### 5.3. 递归收敛、会话与可移植接受条件

为所有语言 fn 建立 `BaseOps(f)` 与参数来源关系的联合方程。直接请求是单元素集合，
绑定/括号/简单别名/不可变捕获保留信息，if/match 联合全部候选，value-block 分析尾值。
函数返回值引用参数时保留 `Param(i,type)`；返回的请求摘要可保留 `ParamOps(i)`，由调用点
按实际抽象实参替换。只通过具名声明、直接匿名 fn 及其简单别名解析的调用连接 callee 方程；
未解析的动态 callee、开放容器、一般 carry 与核心结果仍为 Unknown；仅 §5.2 的状态恒等
carry 规则可以保留 init 描述。只计算返回的请求；未返回的
构造不进入方程。
对有限 operation 集合、参数摘要和 Unknown 取最小固定点，内部空集为 bottom，Unknown 为 top。
`R={worker.read}∪R` 得到 `{worker.read}`；`R=ParamOps(0)∪R` 在调用点传入 `worker.read`
时得到 `{worker.read}`；`R=R` 的空解在使用处成为 Unknown。固定点必须与声明顺序、联合遍历
顺序和合法缓存策略无关，不按递归深度或经验迭代次数截断。

第 5.2 节的描述传递使用当前分析调用链中的 fn origin 集合 Active。初始集合为空；
进入尚不在 Active 的 fn 时加入其 origin，完成该候选后移除。再次遇到同一 origin 时
使用该 fn 的摘要固定点，不把递归调用冻结为空的 Unknown；参数来源关系通过
`Param(i)`/`ParamOps(i)` 保留到外层调用点。没有基础返回或固定点只剩空循环时，仍只保留
声明返回类型，并标记 `recursive_boundary`。该回退不借用其他调用点实参或缓存的更强描述。
Flow 返回传递沿无环调用图分析；进入 fn 时仍遵守同一 Active 规则。
有限源码中每条展开路径的 fn origin 至多出现一次；递归固定点只保留有限摘要图，不展开
运行时递归次数，也不把递归本身当作 Flow 图循环。
联合各候选时使用独立的 Active 分支，不能用先到候选影响后到候选。

递归/前向摘要边记录有限的参数位置映射。调用 `f(..., q, p)` 产生一条带映射的摘要边，
固定点求解时先把目标的 `ParamOps(i)` 替换为该边的第 i 个抽象实参，再继续遍历有限图；
映射可循环但不得按运行时递归次数展开。映射边缺少参数、目标不是已解析声明或抽象实参
本身为 Unknown 时才回退 Unknown；不得把目标函数的参数位置直接当作调用者位置。已知摘要
完成后，普通调用仍按实际参数描述重新绑定，同位置与交换位置的递归转发均可保留
`ParamOps`。

先在调用方上下文计算抽象实参，再加入 callee origin，因此 `identity(identity(p))`
不把前后两个已经结束的传递误判为递归重入。构造闭包不进入 body；传递已知回调才
进入。记忆化须区分抽象实参、captures 及相关 Active 上下文，或使用可证明等价的表示；
不得用空 Active 时的结果替代应有的递归回退。缓存/候选访问次序不得改变精度与接受条件。

原生返回来源按第 10 节的独立规则分析，不以该请求/可调用描述的递归回退抹去已经
证明的原生来源。具体实现可以共享 DAG 和摘要；不要求多项分析共用一种数据结构。
配置的静态分析资源不足可在编译期报带阶段的 E-BUDGET，不能静默改变规范接受条件。
在足够资源下，判断必须与私有名称、声明顺序、合法调度、内联策略和执行模式无关。

会话保存成功 cell 的描述与不可变抽象捕获；失败 cell 不提交新绑定或描述。后续 cell
读取保存值时沿用已有描述，不通过实际函数、proposal 或容器内容补强 Unknown，不重跑
历史。源码描述保留信息与运行时反推信息是两种行为，前者按本节允许，后者禁止。
编译后的描述和图元数据只读共享，不改变资源寿命、CallID、授权或 journal。

例如，注册 version="1"、Concurrent ReadOnly 的 `worker.read(number)->number` 后，
下列完整程序允许并行；容器封装与参数转发均保留返回请求的操作信息：

```lip
import python "example.worker" as worker
fn read_request(x:number)->effect { effect worker.read(x) }
fn identity(p:effect)->effect { p }
flow Main()->list {
    facts=claim()
    for x in [1,2] parallel {
        boxed={request:identity(read_request(x))}
        value=perform boxed.request
        facts <+ value
    }
    return [row.value for row in facts]
}
```

递归参数传递使用同一摘要关系，不增加新的源写法：

```lip
fn retry(n:number, p:effect)->effect {
    if n == 0 { p } else { retry(n - 1, p) }
}

flow Recursive()->list {
    facts=claim()
    for x in [1,2] parallel {
        value=perform retry(3, effect worker.read(x))
        facts <+ value
    }
    return [row.value for row in facts]
}
```

`retry` 的固定点是 `return=Param(1)`；调用点传入已知读取后，两个请求仍满足
`Concurrent ReadOnly`。若参数传入 Write 或 Unknown，原有 `E-PARALLEL` 限制仍然生效。

所有候选都必须满足 ReadOnly 且 Concurrent；Write、不可并发读取或 Unknown 候选仍
导致 E-PARALLEL，包括常量未选分支中的候选。构造函数和静态分析零 provider 调用。
每个真实 perform 仍验证实际请求的类型、Data、合约、权限、资源与结果；any 不豁免
这些检查。更精确的摘要可减少普通图中的 Unknown barrier，但不改变第 7 节顺序规则。
第 13 节固定外部应答等确定性前提继续适用。

普通类型按允许的裸类别集合合并：比较非 null 类别时先分离 null；只有一个非 null
类别时保留该类别及所需可空性，只有 null 时为 null，多个非 null 类别或包含 any 时为
any。因此 number 与 number? 合并仍为 number?，不受联合顺序影响。不引入可书写 union
类型。候选操作的结果注解按 operation 规范编码字节序合并，Unknown Ops 的静态结果
为 any；实际执行始终验证真实 operation 的结果 schema。来源描述不删除或合并调用，
返回注解不自行证明 operation；普通顺序节点仍可在动态检查下执行 Unknown 请求。

对封闭纯环境使用小步机 `⟨e,ρ,κ⟩ → ⟨e',ρ',κ'⟩`：e 为当前表达式，ρ 为运行时的
不可变名称/值环境，κ 为 continuation，即当前结果之后还需进行的计算和返回检查。
`→` 表示一步归约，带撇号的量表示归约后的状态。操作数顺序、匹配次序和基础操作
都已固定；含第 10.3 节调用时再固定第 13 节的 Host 应答，所以纯小步关系是确定的。

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
call/perform/管道语句、join、selector、循环 source 和 return 也各有对应节点；结构 block
不是独立计算节点，但有完成条件：其中所有选中节点均为 Completed 或正常控制 Skipped。
子 Flow 调用建立具有入口与完成条件的 region，详见第 6.3 节；不把整个子图伪装成
一个原生 callable，也不为它新建 run。
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
其中 fn body 外的 perform 仍须证明 Concurrent ReadOnly，fn 内适用第 10.3 节；不能嵌套局部 parallel。
多个迭代以自己的局部快照工作，不共享可变原生对象。

`source` 不属于 parallel body：先在所属游标中完整求值一次并验证为 list，成功后才展开
迭代区域。source 的纯计算、诊断、ReadOnly、Write 或 Unknown 效果按普通调用方规则
执行，且 source 的失败不会创建任何迭代；这些效果不因 body 允许并发而获得并发资格。
并发准入只检查已经展开的 body 及其传递的子 Flow/回调摘要。已知空 list 仍必须静态
检查整个 body、operation、Claim 和类型；运行时不激活迭代、不产生 body 诊断或 provider。

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

静态描述可以在完整检查 body 后识别有限的状态恒等模式：source 存在且每个正常的
`next`/`break` 出口都直接返回同一个状态 binding 时，正常返回值的描述为 init。该规则
不改变循环执行、出口求值、效果、错误或预算；无 source 的 carry 不适用，因为它可能不终止。

携带值可同时包含 list 与 object。逐轮追加列表使用 `concat(s.items,[item])`，
增加或覆盖对象字段使用 `put(s.index,key,item)`，再通过 next 传递组合后的新状态。
这些操作不改变先前状态；循环结果可以作为后续 carry 的 init，因而不限制为一次性构造。
重复键按 put 规则覆盖，列表重复项仍保留；concat 不保证摊销常数时间追加。

该表达式在当前图节点内由顺序执行机求值，和列表推导式一样不创建新的 global 节点。
body 可局部绑定/if/match/顺序循环/perform，但不得声明或 join store，不得包含局部
parallel。carry 可以出现在外层 parallel body 的节点内，合法性按第 6 节检查。
在 @{...} fn 内执行时适用第 10.3 节显式承诺，普通 fn 内仍禁止 perform；在 Flow 内按上下文允许。节点效果按第 5 节取全部执行的
上界，内部循环索引加入第 6.2 节定义的 EvalPath。fn 和 fold 回调保持纯性，外部操作
仍由所属图节点的局部游标执行，fn 不创建独立节点。跨迭代的证据可携带为 s 中的普通 Data，再由独立语句 join。

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
source 为 list，元素在当前节点中按顺序求值；fn/map/fold 回调必须纯，直接原生调用
须使用第 10.3 节 @{...} 函数体；不允许子 Flow 调用。
列表推导式在 Flow 中可以含 perform，但并发只由图和显式 parallel for 引入。
推导式先在外层环境求值 source 一次，再以每个元素建立作用域。可选 `if p` 对每个
原始元素恰求值一次，必须为 Pure bool；false 跳过 body，true 才求值 body 并追加结果，
guard Error 终止推导式。即使 source 在文本上靠后，也先于 guard/body 执行。
EvalPath 在 guard/body 求值期间追加原始 source 的零基索引，不使用过滤后的结果序号；
source 求值不追加自己的元素索引。嵌套推导式按实际包围的 guard/body 从外到内追加。
推导式结果构造的额外工作须为 O(n+k)，n 为 source 长度、k 为保留项数，不计 guard/body
计算及值本身成本；可以使用不泄漏的内部 builder，最终值仍不可变。本版不要求 concat
具有摊还 O(1) 成本，fold+concat 可能二次复制，不推荐用它替代大列表的过滤推导式。

先对每个来源使用规范 token 的零基 ordinal：去除注释/空白，保留换行分隔符后，从
文件首个 token 依次编号。整个 source set 的身份顺序是根来源在前，其余来源按
SourceID 的 UTF-8 bytes 排序；token_count 是各来源全部规范 token 的数量。
某来源的 StaticSite 为其本地 ordinal 加上所有更早来源的 token_count 之和。
没有私有 AST child 编号、链接次序或运行时加载序号。单源时偏移为零，保留原有身份。
显式 run_id 在 run 开始时固定。token 化遵守第 2 节最长匹配，保留字符串正文之外的
每个物理 NEWLINE（CRLF 为一个）；字符串内换行属于 string token，不另编号。显式反斜杠续行
的反斜杠与对应换行均不产生 token，EOF 不编号。NEWLINE 即使被 parser 忽略也参与编号。
statement 的 node_site 为首 token；循环锚点为 for token；推导式锚点为它的 for token。
run_id 为 Host 分配的非空 UTF-8 string，同一 Host 身份域内不同 run 不得复用。

以下身份结构中的字段含义为：enclosing_loop_site 是包围图节点的普通 for 锚点，
index 是该循环的零基迭代索引；node_site 是该节点对应语句的首 token；
node_local_loop_site 是节点内携带值循环、普通顺序循环或推导式的锚点，
iteration_or_element_index 是它的零基迭代/元素索引；perform_site 是 perform token
的 StaticSite。Position 标识图节点，EvalPath 标识该节点内的动态求值路径，
JoinID 标识贡献，CallID 标识一次外部观察。所有 site/index 均为非负整数。

InvocationPrefix 是当前 Flow 实例的调用前缀，入口实例为 `[]`。owner 是调用点所属的
调用方图节点，EvalPath_at_call 是该节点在调用时的完整局部动态路径；invocation_site
为被调用 Flow 静态名称的首个标识符 token，`alias.Name` 使用 alias token；直接调用
和管道 stage 均使用这个原始 token。`++` 表示 pair 序列连接。局部代码中的重复调用
由调用 site 和原始迭代路径区分，不使用
运行时启动次序或“第几个完成”计数。子 Flow 内的 EvalPath 从空路径开始，调用方的
动态路径已经进入子实例的 InvocationPrefix。

```text
InvocationPrefix(entry) = []
InvocationPrefix(child) = Position(owner) ++ EvalPath_at_call ++ [(invocation_site, 0)]
Position = InvocationPrefix(current_flow) ++ [(enclosing_loop_site, index), ..., (node_site, 0)]
EvalPath = [(node_local_loop_site, iteration_or_element_index), ...]
JoinID   = (run_id, Position)
CallID   = (run_id, Position, EvalPath, perform_site)
```

循环/推导式元素索引从零开始；锚点位于索引之前。Position/EvalPath 各用非负整数 pair
序列的字典序，pair 的两个分量都按任意精度非负整数比较，不按 wire string 或 binary64
比较；相同前缀时较短者在前。Position 先记录调用前缀，再从外向内记录当前 Flow 的
包围图循环，最后记录自身 statement。循环 selector 的 Position 不含自身迭代，
只含调用前缀、外层循环和自身锚点；它比循环子节点短。
例如两个兄弟循环锚点 2/5，body 节点 3/6，则 `[(2,3),(3,0)] < [(5,0),(6,0)]`，
前一个循环的所有迭代都在后一个循环之前。
EvalPath 从外到内记录包围当前求值点的节点内循环/推导式索引；没有这些结构时为空。
perform 使用其执行点的 EvalPath，节点内错误使用其发生点的 EvalPath。
同一 revision 内，Position 唯一标识动态节点，InvocationPrefix 唯一标识 Flow 实例。
其依据是：全局 StaticSite 唯一确定原始 token；固定 region/调用元数据从入口开始区分
图循环、所属节点、节点内循环及调用目标；每次可重复执行均显式加入原始迭代索引。
在同一结构上下文中，循环锚点与调用目标 token 不会互为另一种合法解释，进入 callee
后再按其定义上下文解码。因此即使 index 为 0，两个不同结构实例也不会得到同一 pair
序列。wire 无须添加类型 tag，但脱离同 revision 的元数据不能仅凭数值猜测结构。

为外部 source-order 约束定义 Ancestor(a,b)：a 是 b 的严格执行容器祖先，即 a 的求值
实例化的子 Flow region 直接或传递包含 b；循环/分支 region 的包含关系也由 region 树
确定。此关系使用结构元数据，不把任意 Position 前缀直接当作祖先。对节点或带完成
边界的 region，定义：

```text
Earlier(a,b) iff Position(a) < Position(b) and not Ancestor(a,b)
```

子 Flow region 的排序位置为其 InvocationPrefix，循环/分支 region 的排序位置为
对应 selector 的 Position。Earlier 只定义隐式外部顺序；它不是数据依赖、显式 After
或 ErrorKey 的替代排序。祖先容器的外部前置顺序必须传递给内部动作，但内部动作不以
该容器的整体完成为前置。第 7 节按此关系构造 OrderAllows、barrier 和错误前置闭包。

重放执行相同静态 perform 及相同动态元素使用
相同 CallID；同一节点的不同 perform_site 不会冲突。fn 内的 perform 使用下述调用路径扩展。
两个相同参数的调用不会因此成为同一个 CallID；值 fingerprint 从不属于身份。
同一子 Flow 定义被两个调用点、两个图循环迭代或两个 carry/推导式元素调用时，其
子节点、join 和 perform 的完整身份均不同。被调用定义写在调用者之前或之后都不改变
实例在父图中的排序：先比较调用前缀，再比较定义内部位置。不含子 Flow 的入口在 fn 外仍使用
原来的 Position/EvalPath/JoinID/CallID；fn 内路径按下文扩展，wire 结构无需版本切换。
StaticSite/Position 是 revision 内的身份；空行/注释编辑可移动后续 ordinal，新 revision
不能用旧 snapshot id 对齐业务实体。跨 run 的业务 key 须另用显式 Data 字段。
为区分 fn 的重复调用、递归与高阶回调，每个图节点的局部求值游标保存一个从 0 开始的
`next_fn_activation` 自然数。按第 5 节完成参数验证后、进入任一语言 fn body 前，取其
当前值作为 activation，再加一；直接、间接、尾调用和 map/fold 的每次语言 fn 回调都计数，
核心函数的调用（包括作为回调）不计数。计数按该节点的严格求值次序进行，不使用全 run 计数或完成次序。
fn_site 为普通调用的实参列表左括号 token；管道 fn 阶段使用 stage 目标的首 token，
隐式 map/fold 回调使用所属核心调用的实参列表左括号 token。这样 fn 调用锚点不会与
callee 表达式内部的循环锚点重合。

每次进入 fn 都把调用方此刻的完整 EvalPath 保存为该次激活的 FunctionBase；所谓“最外层”
只表示第一次跨入函数边界，嵌套调用同样保存其调用方路径。尾调用沿用当前激活的 base，
不累积 fn 调用栈路径。fn body 内的路径为：

```text
EvalPath = FunctionBase ++ [(fn_site, activation)] ++ CurrentFunctionLocalLoops
```

CurrentFunctionLocalLoops 只含本次 fn body 内活动循环/推导式的原始索引。非尾返回时恢复
调用方活动路径；尾调用只替换当前 activation 和局部循环路径，不保留尾调用链。
调用方后续的 perform 恢复原路径，因此已有 fn 外 CallID 和 wire 结构保持不变。
局部游标的挂起、checkpoint 和恢复必须保留计数与活动路径；计数不得 wrap，超预算明确
失败。此路径同样用于 fn 内错误定位；它不创建图节点或子 Flow。
同一节点在 fn_site=25 调用函数两次、函数内 perform_site=40 时，无外层局部循环的
CallID EvalPath 分别为 `[(25,0)]`、`[(25,1)]`。尾递归、互递归及回调也不得复用 activation。

程序 revision 对没有 import lip 的单源程序仍为 SourceHash(root)，保留原有完整源码
摘要。多源程序为 `lowerhex(SHA256(C(["lip-source-set", "1.0", root_source_id,
SourceTable, DependencyLock])))`；C、SHA256、lowerhex 按第 16.1 节定义。这里的 `"1.0"` 是保留的 source-set
编码域标签，不是语言规范的版本号；本版本保留这一编码。摘要覆盖全部
来源、版本、解析边与真实内容，不仅是入口或可达 Flow，注释也参与。不允许入口文件
不变而依赖变化时复用旧 revision。source set 固定后，site 到模块/token 的映射也固定。
语言版本、根 SourceID、版本锁和入口选择另行固定为 run 元数据；改变任一项不能冒充
同一 run 的恢复。不使用工具私有 JSON、绝对路径或不确定的目录遍历次序计算身份。

实现必须以相同 source set/锁定解析/run 输入产生相同 Position。取消后的恢复保留 run_id；程序改变
则不能把未完成旧 run 按新位置恢复。新 run 与旧 run 是不同命令，不能隐式代替恢复。

### 6.3. 结构化子 Flow 的执行与完成边界

Flow 声明是可复用的图模板；声明与签名收集不执行 body、创建 store 或调用 Host。
静态调用图包含源码中全部可能的子 Flow 调用，包括未选分支；直接或间接递归报
E-FLOW-CYCLE。纯 fn 的递归保持第 4 节规则；反复执行有外部操作的子流程用显式
for/carry，不通过递归展开图。本版不定义动态 Flow 值或运行时替换调用目标。

调用实例按第 2.1/5 节固定并验证输入后建立：参数是不可变普通值，子图仅能访问其参数、
本地声明、定义模块的静态数据/fn/Flow 与显式 import 合约。它不隐式捕获调用方的局部 binding 或活动
Claim store；需要上下文须显式传参。传入的函数保留定义时捕获的不可变值，不按被调用者
的同名局部变量重新绑定。传递 effect 只传递固定请求，不执行、不授权，也不改变操作
合约；权限、资源有效性和预算仍在实际 perform 时验证。局部名称按每个实例隔离，同名 binding/store
不能串到兄弟实例。循环迭代中的调用各自拥有新的局部作用域和结构身份。

输出函数必须冻结实际使用的不可变捕获；子图结束清理局部节点后，该函数仍有效。
返回 effect 及容器同样保持自身值和必要引用，不保留整个活动图或 Claim store。
原生资源引用仍受 Host 的 application/run/session 所有权约束；跨 Flow 传值不延长
run、不让已释放引用复活，也不允许跨进程序列化函数或请求。`claim.snapshot` 请求的
身份仍绑定构造时的 run 和 Flow 实例；即使作为值传出，在其他实例执行仍为 E-RESOURCE。
durable 实现须通过固定源码、输入和记录重建中间普通值，不得把它们冒充第 16.1 节的 Data 规范编码。

子 Flow 是父图内的结构化工作，共享根 run_id、registry、capability 准入、scheduler、
journal、资源许可、预算和取消信号。实例化及求值的真实成本计入父级预算，不重置
steps/nodes/time，不建立独立并发池。子图中的 perform 仍逐次执行第 10 节准入协议。
调用方在参数就绪且控制门打开后才能进入子图；未选调用的参数/body 不求值、不产生
贡献或外部调用，静态检查和图元数据的成本仍按既有规则处理。

每个实例有参数入口、body region 和结果完成边界。成功要求 body 内所有选中节点
Completed 或正常控制 Skipped，所有嵌套 region 终结，且最终返回值通过注解检查（根入口另检查 Data）。
仅 return 表达式已算出不表示调用完成；未使用的审计、写入或 join 不得丢弃。非 void
结果在完整成功后才发布，消费者依赖整个调用完成；void 调用也等待完整完成。
管道下一阶段的额外参数和 body 同样等上一阶段成功，不与上一阶段剩余工作重叠。

Flow 中的独立图节点（包括 if/for 图 block 内的节点）可按图依赖与注册合约重叠；
汇聚节点等待其全部输入实例完整成功。value_block/carry/推导式和普通函数实参中的
调用保留原来从左到右的求值
游标：当前子图未完成前，不执行该局部位置之后的表达式或语句，不能把局部 binding
偷偷改为独立并行节点。子图内部按第 7 节调度；调用方局部 parallel 的语法禁令不变，
子图中显式图 parallel 的准入和并发限额仍由父 run 统一检查与管理。
等待子图的求值上下文保持 Running 和恢复游标，并让出 WorkerAdmission 工作额度；
子图完整成功后，恢复游标须重新取得额度。Running 表示本次节点求值尚未终结，不表示
始终占用 worker。并发上限为 1 时也必须能推进；不得创建阻塞等待的私有调度器。

子节点失败保留原始 Position、EvalPath、error_site 和根 cause；调用边界及依赖消费者
传播失败，不重新包装为调用位置处更小的根错误。实参或输入验证的错误属于调用方；
其中实参求值自身的错误保留原始发生 token，调用边界的参数注解验证错误以
invocation_site 为 error_site。管道 fn/核心/Flow 的阶段参数或返回边界检查统一使用
该 stage 的静态目标首 token；fn body 内部错误仍保留 body 的发生 token。子 Flow
自身返回注解错误始终属于其返回节点，即使调用结果被丢弃也不移到 stage。
错误前缀及 K 的计算覆盖父子图与全部完成依赖，不能遇到子 Flow 失败就另起一套
fail-fast 规则。
取消传入全部未终结实例；已开始 provider 调用的记录与 Uncertain 仍按第 10/11 节处理。
调用失败不自动回滚、补偿或重试，更不会重新执行已完成写入。`{ok:false}`、401/403
等业务 Data 不使 region 失败，也不关闭后续阶段，必须显式用 if/match 作控制门。

以下合法源码由 Host 显式选择 Main 为入口；Left 与 Right 的 Pure 子图可按依赖调度，
SumFields 等待两者完整成功。把任一阶段改为外部操作仍须服从其效果与 barrier 合约。

```lip
flow Left(value:number) -> number { return value + 1 }
flow Right(value:number) -> number { return value * 2 }
flow SumFields(parts:object) -> number { return parts.left + parts.right }
flow Main(value:number) -> object {
    left = value |> Left
    right = value |> Right
    total = {left:left, right:right} |> SumFields
    return {left:left, right:right, total:total}
}
```

输入 value=3 时输出 `{left:4,right:6,total:10}`。线性形式 `value |> Left |> Right`
则输出 8；它没有隐含分叉或自动汇聚。

## 7. 图调度和错误

节点状态只有 Pending（待调度）、Running（已开始且尚未终结，含等待子图/资源）、
Completed（成功，有值节点可发布结果）、Skipped（未执行或剩余求值被关闭）、Error（本次求值失败）、
Cancelled（被外部取消）。
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

WorkerAdmission 计算当前占用工作额度的求值，而不是统计 Running 状态总数。等待子图
或 provider permit 的容器/上下文不占额度；实际执行纯计算或进行中的 provider 调用
占用相应额度。等待恢复的 Running 游标在子图成功/资源可用且其 gate、ContextActive、
After、order 条件仍满足时可竞争额度；不将节点退回 Pending，也不重新执行已完成前缀。
permit 等待与调用准入必须协调，不能占用 worker 阻止释放 permit 所需的工作。

运行时间与相同 Ready 节点的并行选择不属于语言值。无错误/预算/取消且资源最终可用
时，scheduler 与 provider permit 分配必须公平，不能永久饿死一个持续可运行节点。

顺序模式一次只推进一个占用工作额度的 leaf 求值，包括恢复的局部游标；从可启动的
Ready 节点与可恢复游标中选择最小 Position。等待子图的 Running 祖先不参加这次选择，
也不消耗额度；因此可同时存在多个 Running 容器及一个正在推进的后代。所有模式都
保留 source order，其中“更早”统一指第 6.2 节 Earlier，而非仅比较 Position：
节点的效果按第 5 节合并；fn body 内的 perform 计为 Pure，其他 perform 按 registry/Ops
取上界。只有 fn body 外的全部可能 operation 均为 ReadOnly + Concurrent，且至少含一个
这样的 perform 时，节点才是 Concurrent ReadOnly。Write、Unknown 和不可并发
ReadOnly 节点形成 barrier。barrier 等待所有更早的含 fn body 外 perform 的节点成功或正常控制
Skipped；后续含 fn body 外 perform 的节点不得越过仍未终结的更早 barrier。Concurrent ReadOnly
节点可以相互重叠；Pure/join 节点不因外部 barrier 本身被阻塞。fn 内的实际调用仍按
第 10.3 节遵守 Host Concurrent 与资源许可，不因 Pure 而获得并发许可。
纯节点本身不是外部 barrier：一个较早、彼此无数据/控制依赖的纯节点可能尚未报告
Error，而较后节点的外部读取已经启动。随后主错误仍按较早节点的 ErrorKey 选择，已经
启动的读取事实不撤销；同一节点内的表达式和参数仍严格从左到右，不适用这种重叠。

OrderAllows 必须包含尚未实例化的更早循环/分支/子 Flow 可能产生的调用，不能只扫描当前 Ready
或已经创建的节点。跨 producer 循环，barrier 等该循环完成、或被正常 gate 关闭；若循环
尚有未开始迭代，就视为更早调用尚未终结。更早 barrier 之后的 parallel body 也受该
barrier 约束。判定使用 Position 的结构前缀和静态 producer region，适用于所有模式。
内部多个 perform 左到右执行、逐次准入；节点的保守上界可能减少并发，不改变外部次序。
子 Flow 的传递 Summary 参与调用方节点/region 的外部顺序约束，直到调用完整完成；
尚未实例化的子节点不能被当作没有外部动作。子图内部再按原有 leaf 节点与 region
规则排序。Ancestor 排除规则防止子节点等待自己容器的整体完成；子节点仍须继承
容器从外部取得的全部 order 前置条件，并服从容器内游标的先后顺序。调用容器完成前，
它对更后兄弟节点的 Summary/barrier 约束持续有效，不能因向内部让出额度而释放。

Write 不越过更早的 Concurrent ReadOnly；不从 ReadOnly 名称推断跨调用独立性。
join 必须遵守自己的数据/control 依赖。循环或分支中的源顺序通过整体入口/完成约束
保守保持，不能因不同“区域”就越过更早外部动作。无限调用生产者后的 barrier 可永远
不就绪；需要这种跨越的程序应改变图结构，而不是由 scheduler 默默放宽。

Host 可增加 After 约束，目标使用第 16.1 节 AfterTarget_wire，必须能在本 run 的冻结
region/调用元数据中解析；不接受其他 run 或无对应源码对象的目标。合法但尚未展开的
实例保留符号完成条件，不当作已完成。对动态循环和子 Flow 可约束整个 region 的完成。
After 是显式前置依赖，可指向较后位置，不受 Earlier 的隐式顺序方向限制；若要求后代
等待自身祖先完整完成，则按依赖环拒绝，不能套用隐式 barrier 的祖先豁免。
局部求值已关闭的分支/短路路径、以及已完整结束的有限局部循环，不再为其中的调用
保留未来实例。同一 owner 后续调用可依赖这些已完成或正常跳过的调用集合，不必等待
owner 自身完成。活动局部循环仍须覆盖后续可能的迭代；不能把一次迭代结束当作整个
静态调用集合完成。批量处理展开/完成事件后，判环必须使用更新后的完成集合。
子 Flow 的 After 完成目标是其完整成功边界，不能把仅参数入口就绪当作完成。
展开前后均检查环。After 目标 Completed 满足；同一关闭 gate 的
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
ErrorKey 不排除祖先前缀：调用方游标只在子图成功后继续，子图失败则只传播原 cause，
因此调用方不会再为同一次失败产生一个更小的包装根错误。容器自己在子调用之前或
成功之后产生的错误仍按其原始节点/路径/token 排序，不据实际观察时间改写位置。

F 表示当前已知活动根错误的最小 ErrorKey，F.Position 表示该键的 Position 分量；没有
活动根错误时 F 为空，不做错误截断。只有所有可能 Position≤F.Position 的节点及其
必要前置依赖已经终结，F 对应的根错误才能确认为最终主错误。前缀中无穷计算可能
延迟确认，不能凭超时宣称较早节点不会失败；预算/外部取消有独立终结规则。

某节点集合的前置依赖闭包，是反复加入其成员执行所需的全部依赖，直到不再新增所得
的集合。K 为 Position≤F.Position 的结构前缀及其完整前置依赖闭包，包括数据、控制、
After、完整读取 producer 和 barrier/循环/子 Flow 入口及完成条件；未展开 region 的入口与未来
实例也必须保留。F 为空时 K 不限制调度。即使某前置依赖的位置大于 F.Position，也不得剪掉。
其中隐式 order 边只由 Earlier 生成，不把祖先容器的整体完成误加入后代的前置闭包；
显式 After 和数据/完成依赖仍按真实边计算。
调用边界已因子图失败失去成功返回可能时，其成功完成依赖不再是判定该边界失败的必要
前置；仍保留所有可能更早的根错误及其依赖。调用容器的 Position 前缀较短，不表示
它的全部后继自动属于错误前缀，不能因此绕过 error_cut 执行所有剩余子节点。
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

compiler 除普通依赖边外，须输出 region 树：分支候选、循环与子 Flow 的入口/body/completion、
source 范围、可能的 perform/barrier/writer sites、传递效果摘要与调用实例来源。
树的父子关系对应词法包围及子 Flow 实例化关系；调用来源和定义来源都保留原始 token。
整个 region 只有选定工作成功/正常 Skipped 且不再有未来迭代或待完成子 Flow 时才完成；
失败携带根 cause，不能当正常完成。不要求某种
具体 ready queue，但必须与以下算法的可观察规则等价：

```text
1. 建立静态 region summary、完整读 producer、数据/control/After/order 条件；拒绝环。
2. 开门/展开循环/实例化子 Flow 时加入实例和 Position，保留未展开 region 的完成 token。
3. 先传播关门与失败 Skipped；有根 Error 时更新 F 和前置依赖闭包 K。
4. 仅从允许的 Ready 实例和可恢复游标选择工作；顺序模式选最小 Position，并行模式公平选择。
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

`facts = claim()` 只能在所属 Flow 定义的顶层声明，包括子 Flow 的顶层。本规范中的
Claim 是信息记录，Claim store 才
是带底 join-semilattice，不承诺任意无限 join。一次成功的 `facts <+ value` 创建一个
原子记录 `(JoinID, C(value))`；C 为第 16 节规范编码。value
必须为 Pure RHS 的 Data，允许引用已经 Completed 的 Recorded binding。
声明在本 run 创建底值 ∅；所有 join/read/snapshot 均依赖声明完成，并且只能使用
源码中已声明的 store。普通变量名不能作为 join 目标，store 也不得重新声明或赋值。
每次调用实例单独创建其 store；Runtime store identity 由 run_id、该实例的
InvocationPrefix 和声明的 node_site 共同确定，不只由字符串名确定。父子/兄弟实例
中的同名 store 相互隔离。调用边界不传活动 store：父实例可先完整读取并传入 Data，
子实例可返回经过完整读取的 Data，父实例需要记录时再显式 join。完整读取仍检查
各自 store 的全部 producer，不能因子 Flow 封装而省略第 9 节约束。

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

store 的寿命属于本次 run，名称与读写权限仅属于声明它的 Flow 实例。成功 join 可以
立刻被该实例的 live snapshot 看到，writer 随后的
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
及冻结 region/调用元数据定位贡献；列表本身不携带结构类型，不能脱离这些元数据
把某一 pair 猜作调用或循环。id 是第 16 节 JoinID 编码的 SHA-256 十六进制，排序用原身份 tuple，不能
用哈希或到达时间替代源位置排序。返回值不可变，不是随 store 增长而改变的 live handle。
构造 snapshot 或按摘要索引 journal 时，若检测到不同 canonical 身份摘要相同，报
`E-ID-COLLISION`，保留原身份，不合并、不任选其一。正式存储的比较键不能只保存摘要。

`claim.snapshot` 的第一个参数必须是当前 Flow 顶层 store 名的 string literal；compiler
解析声明 site，Runtime 结合当前实例的 InvocationPrefix 得到 store identity。
同一子 Flow 重复调用时不能共用按名字缓存的 store 解析结果。
操作不允许猜测父/兄弟实例或其他 run/store，不能按任意 string
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

本节的 import 指 go/python/host 外部命名空间；LIP 源模块按第 2.2 节解析，不是 Host
operation。外部 `import` 声明依赖命名空间/alias，不执行模块代码、不联网安装、不授予
权限。省略 alias
时取路径最后一个 `/` 或 `.` 后的片段；若不是合法 id 则必须显式 as。alias 在文件中
唯一，不同源模块的 alias 分别解析。核心中的外部 alias 只用于 operation 解析；本节的
通用适配配置可将其值用法降为
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
对象本体；需要对象身份时只保留经声明的资源引用。在实参求值完成后，构造 effect 本身
不调用目标 provider、不写 intent、不消耗目标调用预算；实参求值时 fn 内的 perform 独立执行、计费和记录。

项目可在已注册 schema 中声明普通 Data 参数（例如 `policy:object`），携带 token、
金额、时限或业务标记；其字段、单位和检查规则由 Host/adapter 定义。这些参数遵守
相同的固定、身份与重放规则，不因命名为 policy 或 limits 就获得核心内建语义。
权限需求仍由合约声明并由 Host 授权；第 10 节可释放的资源许可不等于累计花费账本。

规范 operation 名由 `[language, import_path, member_path]` 唯一解析，其中 member_path
是 alias 后以 `.` 连接的 ASCII 成员名。内建 snapshot 名为 `["lip","claim","snapshot"]`。
这里 language 取 import 的 go/python/host 标记，import_path 是解码后的路径 string，
member_path 是 alias 之后全部成员名的点分路径。普通 operation 必须是已导入的
go/python/host alias 加至少一个成员名；若 worker 是已导入 alias，`effect worker(0)`
为静态 E-OPERATION。
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

取消、预算和崩溃与 provider 的竞态按外部事实划界：在 provider permit/实际调用开始
之前拒绝或取消，不调用 provider；若已经写入 intent，恢复仍按该 intent 的合约处理。
一旦 provider 开始，随后到达的取消、预算耗尽、连接断开或 Host 进程故障都不能把调用
记成“未执行”，必须记录可信的 terminal provider fact，或记录非 terminal Uncertain。
若 terminal outcome 已落盘但节点尚未来得及发布值，当前尝试可以是 Cancelled/Error，
后续恢复必须复用该 outcome，不得再次调用。资源已取得但 intent 尚未落盘就发生故障时，
Host 只能释放租约并视为尚未开始的 provider 调用；不能从资源取得本身推断业务动作已发生。

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

Go adapter 处理 context、合法 number（包括规范 NaN 与正负无穷）、struct/string-keyed map/list、声明的多返回值和 error；
Python worker 处理相同 Data、exception、call ID、deadline、取消和有限队列。
协议的 number 始终是 binary64；传输即使用 JSON，也按第 3 节解码，不能依赖 Python
默认 int/float 猜测。默认 native 参数映射为 Go float64/Python float；需要 Go int 或
Python 整数参数时，adapter 合约必须声明并验证安全整数转换规则。bytes 不属于 Data，必须通过
声明的 string 编码（如 base64/hex）或资源引用传输，不能隐式采用平台字符集。
Python tuple 可以按声明映射为 list；set、native callable、array/DataFrame 等必须由
adapter 给出显式 Data/资源引用转换，不能靠宿主默认 str 或不可检验 pickle 混入 Data。

长期对象/大数组可以用 Host 管理的资源引用：引用是 Data object 中的不可猜测 token，
只由认可该 schema 的 operation 解释，且绑定 Host/worker generation、类型、寿命和
授权 scope。Host 必须按第 10.1 节声明应用、单次 run 或显式会话寿命；会话允许引用
跨成功 cell 使用，
不回放旧 cell 以重建对象。核心纯计算只能处理这个 object；访问原生对象必须经 perform，fn 内须用第 10.3 节 @{...} 显式承诺纯性。引用不等于
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
不把 native callable 本体变成语言 fn，不绕过授权，也不推导 Concurrent。降级到 fn body
中的 perform 仅在该函数体有 @{...} 标记时按第 10.3 节接受作者纯性承诺，否则 E-PURITY。
相同 operation 在 fn 外仍保留 Unknown
或其显式登记效果，不为单个库函数另设纯性白名单。
callee 解析和可获得的
签名/arity 检查先于实参求值；无法获得原生签名时由实际调用检查，adapter 须说明这一
限制。仅构造 effect 不执行这些原生检查或加载代码。

在这一配置中，显式 `effect receiver.member(args)` 可使用任意按词法绑定的 receiver，
包括导入模块中函数/Flow 的参数和已保存的会话值。配置将其降为携带所选接收者路径的
原生请求，不要求静态已知的原生来源。构造时只保留普通 object 包装中该调用需要的字段
路径；无关的 fn/effect 字段留在本地，不要求它们为 Data。此选择不读取原生属性，不
执行代码。原生成员查找与缺失成员错误仍在 perform 时发生；实际实参仍必须为 Data。
含普通容器包装的接收者与直接绑定的原生引用遵守同一 Host 边界。公开名字被解绑或复用时，
已保存请求仍保留捕获的 receiver。
普通对象调用保持核心纯调用语义，除非因已知原生来源而被转换。
原生来源分析必须保持输入与返回位置分离。固定字段和等长分支列表的固定下标保持各自
来源，不能被旁边的资源污染。返回闭包的回调实参保留已绑定的外层捕获；分支返回的函数
按各自的参数名和位置传递来源，不能假定各支具有第一支的签名。递归参数转发不得因声明
次序或私有改名丢失已有来源。分析可以对无法有限表示的递归容器或高阶返回保留未知，不得无限展开、
执行函数或调用 provider 来猜测来源；此时使用显式 receiver 请求。它与 §5 的 Ops 分析
相互独立，不额外授予并行资格。

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

### 10.1. Host 资源所有权与运行生命周期

资源的所有者与 provider permit 是不同概念：前者决定对象何时失效/关闭，后者限制
一次外部调用使用资源的数量。Host 合约必须声明相关资源的 scope、owner、generation、
可用操作、释放方式与取消行为；这些是 Host 元数据，不是新源类型或可变核心容器。

| scope | 所有权与结束条件 |
|---|---|
| application | Host 应用拥有的共享资源，如连接池；单个 run 结束不关闭，应用结束时清理 |
| run | 当前根 run 拥有的临时对象/租约；所有子 Flow 共享该 run，结束时撤销使用并清理 |
| session | Host 明确建立的交互会话资源；成功 cell 可继续使用，会话结束时清理 |

HTTP 请求是 run scope，不是 session；不能把会话级默认 Native Host 原封不动用于
并发请求。连接池可以 application scope，借用的连接/事务/临时对象须有明确租约和
run 所有权。引用绑定所属 owner 与 generation，不能从一个请求移到另一个请求后仍
冒充有效对象；子 Flow 在同一 run 内可按合约使用 Data 资源引用，这不授予额外权限。
capability 仍在每次 perform 准入时验证。Data 只能携带合约允许的资源引用，不能直接
包含原生对象或可执行代码；闭包捕获引用或请求不延长资源寿命，也不把原生调用变为纯调用。
scope 说明所有权和清理边界，不自动授予跨边界传递权：run 引用只能在该 run 的子 Flow
中使用，session 引用只能在同一显式 session 中使用，application 引用也不能仅因共享
所有者就作为原生对象跨 run 搬运。若 Host 要求跨 run/进程使用，必须提供明确的资源
重新绑定或快照 operation，并在新边界重新检查 generation、scope 和 capability；否则
按 E-RESOURCE 处理。保存的闭包、proposal 或诊断文本不改变这些边界。

成功、失败、取消和预算终止都触发 Host 的生命周期清理，不依赖任何 Flow gate 或
用户 finally 语句。run 结束时先拒绝新的资源使用；已开始动作记录 outcome 或可信
uncertainty，并在动作结算或明确隔离后释放租约与对象。不能为尽快清理把正在写入的
动作记成未执行，也不能在它仍使用对象时无条件复用同一租约。清理故障保留诊断与
外部事实，不把已完成操作重跑；应用停止时不再接纳新 run，再结算/隔离在途动作。

资源引用的 release 与对象的业务 close/commit/rollback 不等价。生命周期清理负责
技术资源所有权，不隐式补偿业务写入；业务提交、回滚、补偿或确认必须由显式
effect/perform 表达，或属于已注册单个 operation 的完整事务合约。语言不会因为
Flow 失败就自动回滚数据库事务。MemoryJournal 也不是跨请求事务或业务去重存储。

Python worker/远程 adapter 使用有界队列、有限工作池和明确租约；一个 run 的取消
不能杀死正在服务其他 run 的 worker。失联、重置或 generation 改变使旧引用失效，
未决 Write 按 Uncertain 处理，不自动重启并重试它。隔离、重建或 durable reconcile
遵守第 11 节；不能用另一个 worker 上的新对象伪装成原对象。

共享资源的并发访问遵守已注册 Concurrent 与资源范围的锁/许可合约；不能从方法名、
只读返回值或应用级共享推定并发安全。也不要求把所有应用资源都锁成一个全局串行
通道；声明为 Concurrent ReadOnly 且许可就绪的操作按第 7 节可重叠。Unknown 保守
保持。取消与等待资源不能耗尽释放资源所需的工作额度。

### 10.2. 静态 Host 绑定与生成程序

解释器、Go 生成和嵌入式服务使用同一冻结 source set、Flow 入口、合约版本与规范
检查结果。生成目标必须是实际 Go 求值和图调度代码，不把原始 LIP 字符串交给解释器。
模块符号、调用实例来源及类型/效果元数据在构建时确定；Program 元数据初始化一次，
之后只读，可供多个独立 run 使用，不能在请求或重放过程中重写 AST/静态表。
应用自身创建/关闭连接池等行为属于 Host 生命周期，不属于 Flow 源码求值；所用库的
进程初始化由 Host 启动合约明确承担，或在已授权 operation 中执行，不能因为 source
import/模块解析而自动触发。它不授予 Flow 调用外部操作的权限。

Go adapter 在构建/应用启动时静态注册，调用路径不加载 plugin 或下载依赖。Python
包环境和 worker 配置由 Host 锁定，不能在请求中安装/升级包；包加载等实际原生操作
仍遵守本节的授权与 perform 边界。所有直接原生语法降级、资源转换和 schema 映射在
解释/生成路径中一致；不能只在 REPL 里支持，或为生成程序省略 Unknown/权限检查。

多个模块使用同一个规范 operation 名时必须绑定兼容的同一注册合约；相同身份的
version/schema/效果/资源描述冲突在运行前拒绝为 E-OPERATION。生成或嵌入的服务先
解析所有路由入口、来源和合约，再接收请求；每个请求建立自己的 run/context/journal，
共享 application 资源和只读 Program。具体 HTTP 启动命令、打包方式与部署平台由工具
提供，不增加新的语言执行路径或核心语法。

### 10.3. @{...} 函数体的显式纯性承诺

保留字 nan、inf 仍可作字段名，例如 `np.nan`、`np.inf`、`{nan:nan,inf:inf}`；这不允许绑定名为 nan 或 inf 的变量。

fn 始终要求纯性。普通 `{...}` 函数体继续禁止直接原生调用及显式 perform；它可以调用
其他符合纯性契约的 fn。若函数体需要调用原生计算，作者必须在该函数体左花括号前写 `@`，
即 `fn a(x:number)->number @{np.sqrt(x)}`。`@` 与 `{` 间可有空格/tab；标记仅允许出现在
fn_body 的这个位置，具名/匿名 fn 均适用，不是装饰器、表达式运算符或额外类型。

`@{...}` 表示作者显式担保其中原生调用的计算纯性，不表示允许该 fn 产生业务副作用。
编译器和 Host 不必证明任意 Go/Python 实现的纯性，也不要求逐函数登记、白名单、试跑
或专用 Pure 合约；np.sqrt 与其他原生函数适用同一规则。
本节的原生调用统一指经外部边界执行的调用：既包括第 10 节直接语法降低后的 perform，
也包括标记体内显式执行的任意外部 proposal，不按 operation 的 language 或桥接名称另设区别。

作者承诺：固定实参、不可变捕获及约定的原生实现/配置后，计算值和计算错误不依赖时钟、
随机、可变外部状态、调用次数或调度；不修改输入、共享对象或外部业务状态，不产生业务
写入。只读并不等于纯：读取文件、数据库、当前时间或可变属性，同样可能违反承诺。
使用资源引用时，引用固定不代表其对象固定；作者须保证所观察内容及本次使用满足上述
承诺。第 3.2 节诊断是单独定义的例外；原生函数的普通输出不会自动成为核心诊断。
违反承诺的程序仍可能通过静态检查，运行时不承诺检测这种违约；第 13 节依赖纯性的
保证只对履行承诺的程序成立。名称、类型、不可变绑定及 Claim/Flow 结构限制仍须检查；
`@` 不允许捕获或访问活动 store、join、子 Flow 调用或 fn 内的 parallel。

标记只作用于它紧邻的 fn body，包括该体中的局部循环、carry 和推导式；不隐式传给
其中定义的另一个具名/匿名 fn。嵌套 fn 若直接执行原生调用，也必须写自己的 `@{...}`。
递归、闭包和 map/fold 回调依其实际定义的 fn body 判断。标记体中 perform 的静态执行
效果为 Pure，无须 Ops 已知，也不因 registry 的 ReadOnly、Write 或 Unknown 标记而报
E-PURITY；动态 effect 参数和容器中取出的请求同样适用作者承诺。fn 调用的 callee/实参
在调用方上下文求值，只有函数体适用该函数的标记。所有合法 fn 在调用方仍为 Pure，
所以普通 fn、guard、proposal 参数、join RHS 和 carry 出口均可调用有标记的 fn；这些
位置自身不能借被调用函数的标记直接 perform 或调用子 Flow。

外层函数仅定义或调用带标记的内层函数时，无须写 @：

```lip
fn outer(x:number)->number {
    inner = fn(y:number)->number @{ np.sqrt(y) }
    inner(x)
}
```

构造、捕获或返回请求不执行它。请求后来在 fn 外 perform 时仍按原 registry/Ops 判断；
`@` 不随请求值传播，不把整个库或 operation 的登记效果改为 Pure。会话中的闭包保留
其定义体的标记，导入/导出、别名或间接调用不增加也不移除该标记。

所有实际调用仍经过统一 perform：参数与结果 Data/schema 验证、授权、预算、取消、
资源所有权/寿命、CallID 和 journal 全部保留。通用原生适配仍可登记为 Unknown。
分别编译但共享 provider 的 Host 还须共享其 ResourcePool 准入池。
计算纯性不等于线程安全；Concurrent=false 时，Host 必须通过共享互斥许可串行准入
同一 operation 的调用（包括 fn 内外及共享该 provider 的 run），共享 worker/对象的
额外冲突由资源合约约束。许可与其他资源须按第 10 节原子取得，等待不占工作额度；
Concurrent=true 的调用仍须取得其资源许可。`@` 不授予任何额外权限。

fn 调用可以在图中重叠；标记体内的 perform 不形成业务 barrier，节点的其他执行效果
照常合并。每次实际 perform 有独立身份和记录；记录就绪后才发布结果，terminal replay
不重调 provider。只有 intent 时仍按原 operation 恢复合约处理，不能凭 `@` 重试
Unknown/Write。不得仅因 Pure 删除、合并、缓存或提前执行调用，改变调用记录、错误或
同路径诊断。声明、静态检查、补全、构建与未选分支均不调用 provider。解释、生成和
交互路径使用相同的标记、准入与第 6.2 节身份规则。

例如，在第 10 节通用原生适配配置下，以下两种 fn 写法均合法，无须为 np.sqrt 注册纯性：

```lip
import python "numpy" as np
fn a(x:number)->number @{
    if x >= 0 { np.sqrt(x) } else { nan }
}
fn explicit(x:number)->number @{
    if x >= 0 { perform effect np.sqrt(x) } else { nan }
}
flow Main()->list { return [a(122), a(-1), a(nan)] }
```

第一种写法降低为与第二种相同的 perform 边界。作者对这两个标记体承担同样的纯性承诺。
去掉相应的 `@` 后，该函数的原生调用为 E-PURITY；构造 effect 本身仍可用于普通 fn。
负数和 NaN 输入不调用 sqrt provider；返回列表后两项为 NaN。返回值无损传输按第 16.6 节。

### 10.4. 只读接口契约视图

实现必须向静态工具与嵌入 Host 提供只读接口契约视图。视图描述已有声明、注册合约
与第 5 节分析结果，不是新的语言值、operation 或注册来源；不增加关键字、必填接口
文件或第二套 schema。CLI 命令名、SDK 方法和展示格式可由工具决定，但下表的信息
必须可区分为 Known、Unknown(reason) 或 NotApplicable。未知不得显示成 false、空候选
或一个未经证明的精确类型。Known 的值与当前源码/registry 不一致时不能用于检查或准入。

| 信息 | 必须提供的内容与依据 |
|---|---|
| 接口身份 | kind 为 fn、flow、core 或 operation；公开名称；可用的 SourceID、site、line、column |
| 签名 | 按声明顺序的参数名、裸类型、可选性、可用的嵌套 schema，以及返回类型/结构；语言声明与原生签名分别标明来源 |
| 值域 | 语言 fn/同 run Flow 使用普通值；Host、入口、Claim 使用 Data；void 不伪装成 null |
| 操作身份 | operation 的完整三元组、contract_version；非 operation 接口此项为 NotApplicable |
| 执行效果 | fn 为 Pure 并关联实际 `@` 承诺；Flow 为 Summary(F)；operation 使用实际 Effect/Concurrent，不把返回请求的候选当作执行效果 |
| 返回信息 | 第 5 节可证明的请求候选、可调用来源和容器位置；无实参时只展示无额外参数描述可证明的结果 |
| 权限与资源 | 已登记 capabilities、资源许可，以及可用的 scope/owner/generation 与清理责任；未静态确定的实例所有权明确为 Unknown |
| 失败通道 | 普通业务 result schema、节点错误、Rejected、KnownSucceeded 后验证失败与 Uncertain 的区别；适用范围分别标明 |
| 保证依据 | 静态推导、Host 合约或作者纯性承诺；每项依据关联声明位置或 operation/version |
| 不完整信息 | 缺少的字段、原因，以及来源/递归边界；不强行将动态接口包装成完整静态签名 |

签名描述允许使用第 10 节已有 schema 的 type/items/fields/required 信息；缺省与可空
分别保留，不从 `number?` 推导可省略参数。有限候选函数的签名逐项展示，不能拼成一条
不存在的共同签名。返回资源的生命周期说明不授予新权限或延长原引用寿命。
Go 类型信息或 Python 文档只能支持其实际包含的签名/说明，不能证明 Pure、ReadOnly、
Concurrent、幂等性或授权。通用 bridge 的 Unknown 不因视图更详细而升级。

Known/Unknown/NotApplicable 按每项事实及嵌套 schema 字段区分，不给整个接口统一
贴标签。已知签名可与 Unknown 效果或未知所有权并存；capability 要求说明需要什么
授权，不证明当前调用者已获授权。语言参数的可空类型仍必须提供实参。operation 的
contract_version 与语言版本不同。Pure fn 视图标明自身及所依赖的第 10.3 节承诺，
不将标记传给嵌套 fn 定义。

视图从同一份源码元数据与本 run 固定 registry 派生；显式注册的同一规范 operation
合约按第 10 节规则优先，不能由生成帮助文本覆盖。改变调用/恢复语义仍须更换非空
contract_version；只改变说明文字不自动改变语义。构建与执行不得使用互相冲突的视图。
实现可缓存只读视图，但不得让缓存保留业务值、活动资源或更改调用身份。

缓存键绑定定义时冻结源码/声明及 registry 合约快照，包含 schema、效果、并发与版本；
不能只按展示名匹配。跨 cell 保存的描述保留符号 operation 身份和词法捕获，不保存
可复用的授权或资源有效性结论。执行时按实际快照解析合约；与已编译合约不兼容时按
第 10.2 节在新增 provider 准入前失败。不得重新分类旧 terminal journal，也不得把
保存的请求静默改绑到替换版本。capability 撤销和资源失效仍由实际执行边界检查，
视图信息详细不豁免这些检查。

### 10.5. 适配、发现与静态查看

直接调用、显式请求、模块包装和保存的会话值沿用同一 perform 通道。可获得的 schema
和签名由注册、参数检查、契约视图、帮助与诊断共同使用；未知部分明示，不要求作者
为这些用途重复注册相同接口。动态 Python 签名缺失不妨碍已有 Unknown 通道按真实合约
检查，但不能声称已完成静态 arity 或业务结构验证。

静态契约查看、补全与第 15 节的错误解释只读取已存在的元数据，不执行用户 fn、访问
原生属性、调用 provider 或为查询签名加载 Python 模块。Go 导出发现可使用静态工具链
和 module resolver，但不执行包初始化。需要原生运行时才能取得的文档/对象信息，必须
作为明确的 Host 查询，遵守普通 effect/perform、授权、预算、身份和记录规则；不得
把它偷偷加入静态查看。工具入口不改变这些边界。

构造 receiver 请求只固定实际成员路径需要的包装字段，不探测原生属性或执行成员；
未选分支不加载或调用原生代码。普通 Data、显式 native_data 快照与原生引用在视图中
分别说明；不隐式取快照、复制资源、关闭、缓存执行结果或重试来改善表面体验。
native_release 仍只释放引用，业务 close/commit/rollback 仍依实际合约显式执行。

解释器、生成程序与 REPL 使用相同签名绑定、描述规则和真实边界。各路径只展示当前
环境可取得的信息；缺少工具链、依赖或签名时提供明确的不可用原因，不通过执行用户
程序来补齐。性能报告必须将发现/准备/编译与实际业务执行分开，并包含适配代码、
重复声明、转换和保留元数据成本，不能只以入口源码缩短证明适配改善。

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

新 run 先完成入口选择及第 2.3 节 EntryInput 验证，再由 begin 分配 run_id 并持久登记；
运行前拒绝不占用 run_id、不写入 run journal，也不产生 durable intent。
durable profile 固定并持久保存语言版本、run_id、根 SourceID、选定入口声明名、
第 2.3 节验证通过的完整 EntryInput、
精确程序 revision、SourceTable/DependencyLock 与所用 Host contract version。程序
revision 使用第 6.2 节定义；依赖内容/版本、注释编辑也算新 revision。恢复保留入口和
全部 InvocationPrefix，不为子 Flow 分配新 run_id；同源码改选入口、更换根或语言
版本仍为 E-REPLAY-DIVERGENCE，必须在任何新 provider 准入之前拒绝。
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
| 只有 intent，ReadOnly 且 contract 明确允许重执行 | 可重新计算/读取，再记录；旧调用没有已发布的结果，仍复用 CallID |
| 只有 intent，Write 有 provider 支持的稳定 DedupKey | contract 明确证明安全时可恢复调用 |
| 只有 intent，其余 Write/Unknown | fail closed，先查询外部事实 reconcile；不盲目重试 |

此处恢复调用复用原 CallID。结果必须持久记录后才发布，所以一个尚无结果记录的读取
不可能合法地被下游已持久写入消费。Provider DedupKey 不替换 CallID，也不由参数相等
自动推导。Exactly-once 的外部世界不是语言保证。
Uncertain 保持非 terminal，ReadOnly 自动恢复同样须有明确的无外部写入和可重执行合约。
fn 内的作者纯性承诺不改变恢复分类：只有 intent 的 Unknown/Write 仍按实际合约处理，
不得仅因调用发生在 fn 内就自动重试。
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
同样地，若当前尝试在 terminal result/rejected/known_fault 已落盘后才因取消、预算或
进程故障终止，旧节点可以保持 Cancelled/Error；恢复时先消费已落盘的 terminal 记录，
不能把这次未发布值当成 intent-only，也不能重新调用 provider。若只有 intent 而 provider
尚未开始，仍按 operation 的 intent 规则决定是否可恢复。
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
| begin | 仅在入口选择及 EntryInput 验证通过后分配唯一 run_id，持久固定程序、完整输入、contracts，再允许执行 |
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

核心语法在第 2 节，核心函数在第 3.1–3.2 节。静态模块按第 2.2 节共享纯 fn、Data 和
具名 Flow，业务流程按第 6.3 节复用；不提供运行时模块加载或包初始化执行。
扩展能力分工如下：

| 能力 | 在本规范中的位置 |
|---|---|
| 持久状态、Session 与状态事务 | Host storage 合约；语言传入/输出普通 Data |
| retry | Host operation 的显式重试合约；业务重试可用 for 和 `{ok:false}` 数据 |
| 多轮计算 | 纯递归/尾调用/fold；有外部操作时用普通 for 或携带值 for 表达式 |
| 有外部操作的业务复用 | 结构化子 Flow；`\|>` 使用第 2.1 节的节点内顺序绑定，分支/汇聚仍用依赖图 |
| 消息队列、长期事件与后台任务 | Host 注册合约与运行生命周期；不增加 core 收件箱、订阅或自动启动规则 |
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

### 12.1. 可选 HTTP Host 接入

HTTP Host 是核心外的接入配置，不增加源语法，也不改变 Flow、Claim、调用身份和错误规则。
Host 接收请求，将其转换为一个 Flow 输入；成功输出经过完整验证后，Host 才提交 HTTP
状态、响应头和正文。接收请求和发送最终输出是 run 的宿主生命周期，不是源程序内的
operation；Flow 中访问数据库、其他网络服务、时钟或业务日志仍须经 effect/perform。

本配置的入口固定为 `flow Name(request:object)->object`。路由在启动时绑定预编译 Program，
同一 Program 可以供多个请求复用。路由采用带显式方法的 Go ServeMux pattern，可含路径参数；
不存在的路由、方法不匹配和路由重定向由 Host 的路由器处理。所有路由先完成编译、
入口检查和冲突检查，再开始接收请求。路由绑定一个按第 2 节选定入口的 Program；
多 Flow 源码的入口在启动时按第 2 节的默认规则或显式配置选定，请求不能临时改选入口或选择任意源码、operation
或 Host registry。子 Flow 只属于本次请求的父 run，不再创建请求或 HTTP 响应。
缺少显式方法的路由配置报 E-HTTP。Host 可以复用 ServeMux 的路径与参数匹配规则，
但必须自行按显式配置完成方法分派，并生成 405 和 Allow；不得直接采用 ServeMux 的
方法匹配或内置 405/Allow 结果。方法按字面精确匹配，HEAD 只匹配显式配置的 HEAD
路由，HEAD 请求不得回退执行 GET 路由。无需路由重定向时，若忽略方法有路由匹配请求的 host/path、
但没有请求方法的路由，则返回 405；忽略方法也无匹配路由则返回 404。两者均不创建
run、不实例化 Flow、不调用 provider。405 的 Allow 必须列出忽略方法时匹配该
host/path 的全部显式方法，去重后按 UTF-8 bytes 排序，以 `, ` 连接；不得因 GET
自动追加 HEAD。显式 HEAD 路由按自己的入口/效果合约执行。

本配置接受 origin-form 和 absolute-form 请求目标；absolute-form 从目标 URL 提取
路径和 query，host 使用目标 URL 的 authority，由 HTTP server 验证。空路径按 `/`
处理。星号形式（包括 `OPTIONS *`）及 authority-form 在路由及 run 创建前返回 400。
Go server 必须关闭或替代默认的 `OPTIONS *` 处理，例如设置
`http.Server.DisableGeneralOptionsHandler=true`，避免在 Host 检查前自动返回成功。
路径匹配按原始编码路径中的字面 `/` 分段，再逐段百分号解码；`%2F` 不作为分段符。

HTTP 输入 request 的固定字段如下：

| 字段 | Data 形式 |
|---|---|
| method / path / host | 请求方法、解码后的路径、Host 的 UTF-8 string |
| raw_path | 原始请求目标中的路径部分，不含 query；保留百分号编码字面量，供区分 `/` 与 `%2F` |
| params | 路由中的参数名到 string 的 object |
| query | 解码后的查询键到 string list 的 object；保留重复值，非法编码拒绝 |
| headers | 小写头名到 string list 的 object；不把代理头解释成可信身份 |
| body | `{encoding:string,data:string}`；合法 UTF-8 使用 `utf-8`，其他 bytes 使用标准 base64 |
| json | `application/json` 或 `application/*+json` 的非空正文经第 1 节 Data 规则解码；其他情况为 null |

Go 实现的 raw_path 应取有效请求 URL 的 `r.URL.EscapedPath()`（空路径取 `/`），
不得直接使用可能为空的 `r.URL.RawPath`。例如 `/{x}` 匹配 `/a%2Fb` 时，
path 为 `/a/b`，raw_path 为 `/a%2Fb`，params.x 为 `"a/b"`；编码斜杠仍属于同一个参数段。

JSON 仅接受 UTF-8 charset；拒绝重复键、非有限数、非法 Unicode 和尾随内容。请求 JSON
及响应 json 的任意嵌套 object 中不得出现资源协议保留键 `$lip.native`，不能从 HTTP
输入原生引用或把原生引用作为响应发布。正常业务 schema 仍由应用或显式合约检查。
空正文的 json 固定为 null。原正文与解码后 json 同时保留；原始正文长度计入请求正文
限额，base64 膨胀、JSON 副本及整个 EntryInput 的实际内存另计入已公布的 Data/内存预算。

HTTP 输出是成功 EntryOutput 的 value，按固定入口签名必须为仅含以下字段的 Data
object；未知字段拒绝，提交前须完成全部检查：

| 字段 | 规则 |
|---|---|
| status | 必填，200–599 的整数 number；不支持信息响应或协议升级 |
| headers | 可省略；头名到 string 或 string list 的 object；大小写重复、非法头名及头值控制字符拒绝 |
| json | 可省略；仅含有限数的 Data，以规范 JSON 编码；与 body 互斥；缺省 Content-Type 为 application/json |
| body | 可省略；string，缺省 encoding 为 utf-8；缺省 Content-Type 为 text/plain; charset=utf-8 |
| encoding | 仅可与 body 同时出现，值为 utf-8 或 base64；base64 解码后缺省 Content-Type 为 application/octet-stream |

Host 控制 Content-Length、Transfer-Encoding、Connection、Trailer、Upgrade、Keep-Alive、
Proxy-Connection 和 TE，不允许 Flow 设置。204/205/304 不得携带非空正文；显式 HEAD 路由仍计算
并验证响应，Host 据完整正文设置 Content-Length，但不发送正文；204/304 不设置该头。
此配置一次提交完整响应，不提供流式响应、WebSocket 或
multipart upload；这些能力需要独立的 Host 资源和生命周期合约。返回 HTML 时，模板
转义由显式模板库负责，三引号字符串本身没有 HTML 安全语义。

每个通过路由、请求转换及 EntryInput 验证的请求分配新的 run_id、MemoryJournal 和
Claim store，预算及取消信号独立；不得使用固定 run_id 或跨请求 journal。
Host registry 在 run 内固定，共享显式操作必须满足其
并发和资源合约。可信鉴权 middleware 可将身份放入请求 context，Authorize/operation
从该 context 读取；HTTP 数据不自动授予 capability。Host 或连接池的寿命由嵌入应用
管理；请求不关闭共享连接池。通用 Native bridge 只有满足第 10.1 节的请求级所有权、
租约与取消规则才可用于 HTTP；不能使用 REPL Session 共享请求状态。

响应 status 是普通业务输出，不是控制门；返回 401/403 不会取消其他已选节点。应用须
用 if/match 显式保护受条件控制的动作，并由 Host Authorize 在每次外部调用准入时验证
身份。顶层最后 return 仍遵守第 2 节的规则，不能作为命令式提前返回使用。

配置零值选择有限默认预算：请求时限 30 秒、请求与编码后响应正文各 1 MiB、全部路由
合计 64 个活动请求；每个 run 的并行上限 4、steps 1,000,000、elements/nodes 各 100,000、
非尾调用 frames 10,000。调用者可设其他正上限，负值为配置错误，零不表示无限。
请求容量耗尽时立即拒绝，不排队；HTTP 服务另须限制读取时间与请求头大小，阻塞原生
操作须遵守 context 取消，不能靠 HTTP 响应超时中止未协作的原生代码。

无效查询/JSON/Data 返回 400，正文超限返回 413，非法 media type 或 JSON charset 返回
415，容量耗尽返回 503。时限到达且连接仍可写时返回 504；由服务端中止且仍可写时返回
503。客户端断连或取消时停止未开始工作并记录取消，不要求向已断开的连接发送状态，
也不为取消合成 408。E-CAPABILITY 返回 403，其他运行错误、无效响应和响应超限返回
500。Host 生成的错误仅返回固定公开
类别，不发布诊断、调用记录、源码或内部地址；完整 Report 可交给服务端诊断 hook。
接入配置与响应验证的诊断使用 E-HTTP，不增加核心求值错误。上述状态描述 HTTP 结果，
不改变已完成或 Uncertain 的外部事实。错误、断连、响应
验证失败均不得自动重试 Flow/provider 或宣称回滚；写出已提交响应失败只能报告，不能
再次写状态。是否提供 durable profile 由实现另行声明，不从 HTTP 接入本身推导。

### 12.2. Host 消息与独立运行

同一 run 内的工作复用和等待使用结构化子 Flow；独立进程、后台任务或长期事件之间
的消息传递属于 Host。core 不定义邮箱、隐式订阅、fire-and-forget 调用、接收即自动
执行的 Flow，也不以管道表示消息投递。

程序内发送、接收、确认或查询消息状态必须通过已注册的 effect/perform，声明 Data
schema、效果、权限和资源合约。接收若消费/确认消息或改变队列状态，不能仅因名为
receive 就声明 Concurrent ReadOnly；其真实语义按合约分类。发送成功的 Result 仅
表示合约规定的投递/接收确认，不表示消费者业务完成。需要等待业务完成时，由 Host
提供明确的状态/结果操作，或在同一 run 内使用子 Flow。

Host 可以把事件转换为输入并启动选定入口；每个独立 run 使用新的 run_id、独立 Claim
和预算/取消上下文，不冒充发送方的子 Flow。跨 run 的关联使用显式 Data 业务键或
关联标识。投递保证、重复消费、幂等、确认、重试与持久恢复由 Host 合约规定，不能把
CallID/MemoryJournal 当作跨 run 消息去重或恰好一次业务执行的保证。

### 12.3. 可复用业务库的值与效果边界

输入校验、业务 Result、模板和数据库访问使用第 2.2 节的模块与既有值/操作组合，不
新增业务专用语法、隐式全局状态或绕过 effect 的库通道。库的导出接口和外部合约必须
说明输入、输出、失败通道与资源所有权；下列分工适用于所有实现，不要求内置这些库。

| 能力 | 合约边界 |
|---|---|
| 输入与 schema 校验 | 静态 Data 描述 + 纯 fn 校验，或显式 Host schema；any/has 不免除动态值检查 |
| 业务 Result | 普通 Data，如 `{ok:true,value:...}` / `{ok:false,error:...}`；不增加 Result 类型或 Error 捕获语法 |
| 文本与模板 | 纯 fn/库按输出上下文转义；三引号只是 string，不自动插值、去缩进或赋予 HTML 安全语义 |
| 数据库查询、写入、事务 | 已注册 operation 的 schema/效果/资源与事务合约；连接池由 application 管理，临时资源按 run 租约清理 |
| 跨请求幂等与持久业务状态 | 显式业务键 + Host 存储/事务；不能仅按参数相同或 MemoryJournal 推断去重 |

纯 fn 可构造 proposal 来复用单步请求，静态已知函数依第 5 节返回摘要保留 Ops；多步
有外部操作的复用使用结构化 Flow。需要原子事务的多步操作可以由一个显式 Host
operation 承接，结果必须声明为合法 Data；一串子 Flow 或管道不会自动成为数据库
事务。原生异常只有经已声明 adapter 合约转换才成为业务 Result，否则沿原 Error
规则传播。库返回 401/403 或 `{ok:false}` 仍须通过显式控制门约束后续动作。

## 13. 确定性与保证边界

在基础纯操作正确、adapter 合约诚实、fn 作者履行第 10.3 节纯性承诺、值边界被执行的条件下：

**固定外部应答下的逻辑结果确定性。** 固定 run_id、选定入口、输入、完整源码、registry/schema/
授权、After 约束与初始 journal；不含 live claim.snapshot；无外部取消、时限/预算失败、
crash、失效资源或变值重复投递。每个实际选中节点/调用有限终结，scheduler 公平。
令 R 为一个固定的响应映射：键为完整 CallID 和 canonical 请求，值为该调用的 Host
应答；所有正常重复投递携带相同值。
调度扰动测试若使用伪随机 provider，必须按完整 CallID 与 canonical 请求派生固定
应答，不能按调用抵达次序消费一条全局随机序列来冒充固定 R。
在这些前提下，所有合法调度得到相同成功 Data 输出，或相同 ErrorKey/类别/语义 details
的主错误。成功执行时所求值的 CallID 集合相同；失败执行时**不保证**实际已发出调用
集合相同，也不保证 partial store、错误之外的 trace 或动作时间相同。
这里“相同 Data”指 C 字节相同，包括规范 NaN；不要求含 NaN 的值用语言 `==` 比较为 true。

固定 R 是比较执行的数学前提，不声称远程服务实际永远返回同值。若不满足有限终结
前提，较早发散节点可能使错误前缀无法确认。Host 引入的新 cancel/budget/版本变化是
新的输入条件，不归入此定理。依赖错误传播不构造新的更小根错误。
未开 gate 的节点不求值，因此不产生动态 Error，不更新 F/K 或触发 error_cut；未选
if/match 表达式分支同样不求值。编译期错误不属于本定理对合法程序的运行结论。

1. 固定上述 Host 应答后，纯表达式的小步归约至多有一个下一状态；若终止，结果唯一。无限递归可能发散。
2. 固定输入的有限无环纯图，在公平执行且无预算/取消干扰时，所有合法调度得到同一值。
3. 对某个 store，令 B 为本次 run 中成功贡献组成的有限原子集合。B 固定时，重复投递
   和不同交付次序均得到最终 store 状态 B，因此最终 snapshot 和确定性纯投影相同。
   相同 identity 的变值呈现相同冲突，不选择抵达者。
4. 无 perform（包括原生语法降级得到的 perform）的源程序不能调用业务 provider；§3.2 的单向诊断接收端不属于 provider。
   没有完成准入的 perform 不能调用 provider。诊断接收端不参与固定应答映射 R。
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
- 第 3.3 节 NaN/无穷的运算、比较、类型 pattern、静态 Data、容器、原生边界、规范编码、Claim 幂等和 journal 参数一致性；第 16.6 节两种传输配置不能混淆 null/string/NaN；
- 第 10.3 节 @{...} 显式纯性承诺与 fn/别名/闭包/递归/map/fold/guard/carry/请求参数的组合；普通 fn 内 perform 拒绝，标记不隐式传入嵌套 fn；@{...} 不按 ReadOnly/Write/Unknown 或动态 Ops 拒绝，fn 外继续保守检查；Concurrent=false 串行准入；准入前、构建和未选路径零 provider；fn 重复/尾递归调用身份唯一且重放不重调，解释与生成路径同一计数；
- 第 3.2 节诊断与闭包/模块/高阶调用/递归/carry/guard/管道/请求构造的组合；禁用、满队列、关闭及接收故障不影响结果、错误、预算、操作身份和并行；参数仍正常检查，未选路径与静态阶段零发送；诊断不延长原生资源生命周期；
- 一万/十万次直接、互递归、function 值间接尾调用；非尾递归预算和发散取消；
- for 顺序/并行、break/continue、match first-arm 和 guard Error；
- carry 空源/next/exhaustion/break/无限源预算，迭代参数源自上一轮，恢复复用 EvalPath；
- concat/slice/keys/has/put/split/parse_number、string 顺序、带 guard 推导式及线性构造；
- 普通/三引号字符串的相同转义、三引号的 LF/CRLF、缩进、引号正文、非法控制字符与后续诊断位置；
- sum_to、fold 回调尾 if、尾 match/换行/分号、carry else-if；块局部 binding 与参数验证顺序；
- 命名参数顺序、静态/动态 arity、返回描述的函数链/别名/闭包/模块/递归回退、有限容器与可调用候选、真正 Unknown 的边界、分支联合与 parallel 判定；
- 多 Flow 的签名收集、显式入口选择、Flow 非值性质、fn 纯性和调用环拒绝；
- EntryInput 的位置/命名绑定、EntryOutput 的 null/void 区别、验证先于 run_id/begin、运行前拒绝零节点/零调用及 durable 输入固定；
- 模块/导出/静态 Data、离线锁定解析、模块环拒绝、私有成员与同名隔离；
- 多源 StaticSite/source map、依赖变化后的 revision、单源 wire 兼容与解析顺序无关性；
- 文件模式的唯一选择、import 顺序、末尾 import/data_decl 无换行、文件名精确匹配、链接拒绝与物理文件重复解析拒绝；
- 管道的优先级、初始输入单次求值、首参数注入、命名参数及阶段间完整完成依赖；
- 子 Flow 参数注解检查先于 body，函数/effect/嵌套容器可以传递，入口/Host/Claim 保持 Data，返回值检查不丢弃已完成写入；业务失败仍须显式门控；
- 同定义在两个调用点、图循环、carry/推导式及嵌套调用中的身份与 Claim 实例隔离；循环 source 的子 Flow 与 body Position 不冲突；
- 子 Flow 传递效果摘要、未展开 region 的 barrier/After/K、并发上限 1 的等待推进；
- 容器祖先不构成自身 barrier、外部 order 前置传递、Running/额度分离及 AfterTarget wire；
- 局部求值游标、父级预算/取消、子节点根错误位置、同 run journal 与解释/Go 一致性；
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
- 宿主资源引用失效和 worker 重启不能被误认作有效持久值；
- application/run/session 所有权、并发请求租约隔离、取消/失败清理及 worker 有界准入；
- 多模块静态 Host 注册、只读 Program、生成程序与解释器的合约/schema/资源映射一致性。

声明 HTTP 接入的实现还须验证：显式方法/HEAD 路由、GET 不回退执行 HEAD、405 的
Allow 只含显式方法、编码斜杠的单段 params、raw_path 与解码路径、absolute-form、
`OPTIONS *` 的运行前拒绝、空正文 json、响应提交前验证、正文与 Data 预算、断连及超时状态映射。

应用场景的验收规则如下。业务失败为 schema 中的普通 Result 数据，provider Error
仍按第 7 节传播；含恢复的用例仅用于声明 durable profile 的实现。

| 目标 | 本版方式与验收条件 |
|---|---|
| 并行搜索/三次失败 | 并行完整收集后判定；精确提前熔断用顺序 carry。并行确定提前取消不属于 core |
| 多 agent 缺失证据报告 | 完整 snapshot → 带 guard 推导式/has/put，调度扰动成功输出一致 |
| 迭代改进到 score≥0.8 | carry + perform，下一轮参数来自前一轮，命中后不再调用 |
| 多接口复用业务流程 | 静态子 Flow + 管道/命名 binding；参数/结果为普通值，完整完成后才交给下游 |
| 请求内分叉与汇聚 | 独立子 Flow binding + 对象/列表输入；按效果准入重叠，汇聚等待完整成功 |
| 后台消息任务 | Host 投递/消费合约 + 独立入口 run；投递确认不冒充业务完成 |
| crash 恢复 | durable profile，重放固定结果、不重复未决写入、未完成不能 abandon |
| 事实来源定位 | snapshot 的 run_id/Position 可结合对应源码 revision 定位 join；完整因果 UI 由 Runtime 提供 |

在第 13 节确定性命题的前提下，调度扰动须保持成功输出或主错误相同；只对成功执行
要求 CallID 集合相同。有限状态模型可以辅助检查转移规则，但不代替 parser、求值机、
Host 数据边界和持久 journal 的实现验收。

- 第 5.1–5.3 节有限容器描述、参数到返回位置、可调用候选各自签名、map 元素、BaseOps 与 Active 递归回退；静态分析预算明确失败，不因声明顺序、改名或私有内联改变接受条件；
- 第 10.4–10.5 节契约视图的 Known/Unknown/NotApplicable、可空与可缺省、候选签名、保证依据、发现/静态查看零调用、动态文档独立记录，以及三条执行路径的同一边界；
- 第 15.1–15.2 节稳定 reason、信息丢失来源、候选合约与字段/索引路径；区分已知不安全、缺乏依据与等待资源；已完成后的验证失败不能引导重复写入；
- 第 16.7–16.8 节全部封装/契约/适配及边界/次序向量；保存的抽象描述不续命、不反推运行时值，解释展开不改变 Steps、journal、诊断事件或主错误。

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
| E-ENTRY | 无默认入口且未显式选择，或入口未知/不是根 Flow；运行前拒绝 |
| E-MODULE / E-MODULE-CYCLE / E-EXPORT | 缺失/非法/锁定不符的模块、模块依赖环或访问未导出成员 |
| E-FLOW-CYCLE | 具名 Flow 的直接或间接调用环，不因常量未选分支豁免 |
| E-PURITY | fn 中的活动 store/子 Flow、普通 fn 中的 perform，或 Pure 位置含未适用第 10.3 节 @{...} 承诺的外部执行 |
| E-RETURN / E-CONTROL | 无效结果出口、return 位置、break/continue 位置 |
| E-PARALLEL | parallel body 含写入、Unknown 或转移图循环的控制操作 |
| E-CLAIM-ORDER | 完整读取与全部 producer 的源位置/循环范围不符 |
| E-GRAPH-CYCLE / E-AFTER-CYCLE | 静态可确定的依赖环/After 环 |
| E-OPERATION / E-SCHEMA | 未注册 operation/版本或可确定的合约不符 |
| E-BUDGET | 明确的静态分析/构建预算耗尽；phase 为 static，不改变规范接受规则 |

| 运行时 Error | 条件 |
|---|---|
| E-TYPE / E-ARITY / E-SCHEMA | 动态值/参数/输入/结果不符 |
| E-VALUE / E-INDEX | 非法 native 数据/传输、越界/缺键；规范 NaN 与正负无穷以及算术产生的非有限结果本身合法 |
| E-FAIL / E-MATCH-NO-ARM | fail 调用或表达式 match 无 arm |
| E-BUDGET / E-CAPABILITY / E-OPERATION | 明确资源预算、授权或注册失败 |
| E-CLAIM-CONFLICT / E-DEPENDENCY | 同 JoinID 变值或不可用数据依赖 |
| E-GRAPH-CYCLE / E-AFTER-CYCLE | 动态新增约束/展开形成环 |
| E-EFFECT-REJECTED / E-UNCERTAIN | terminal provider 失败或未知外部 outcome |
| E-REPLAY-DIVERGENCE / E-RESOURCE | 恢复请求变化或失效/越域 native 引用 |
| E-ID-COLLISION | 不同 canonical 身份/参数产生可检测的相同索引摘要 |

| Host 诊断（不属于节点根错误） | 条件 |
|---|---|
| E-ENTRY | 运行前入口选择失败，包括声明模式根模块没有 Flow |
| E-SCHEMA | 第 2.3 节 EntryInput 的结构、绑定、Data 或注解验证失败 |
| E-VALUE | 成功 EntryOutput 无法按选定 Data 传输配置编码，例如 json 下含非有限数 |
| E-HTTP | HTTP 启动配置、请求转换或响应验证失败；HTTP 状态按第 12.1 节映射 |

同一错误码可在不同阶段出现；诊断必须标明阶段。HTTP 响应验证失败发生在 Flow 结果
发布后，不追加一个虚构 ErrorKey，也不改写已经完成的调用或图节点状态。

cause 取 `unselected_gate`、`break`、`continue`、`dependency_failure`、`after_failure`、
`order_failure`、`error_cut`、`cancelled` 等明确原因，并可链接源错误；它们不是静态
诊断码。遮蔽普通循环外绑定且
RHS 引用同名旧值时，推荐 `W-SHADOW-LOOP`，语义仍是新 binding，不是累加赋值。
未加括号地混用 `&&`/`||` 与 `|>` 时推荐 W-PIPE-PRECEDENCE；它不改变第 2.1 节的
分组或合法性，不把纯性/类型检查降为 warning。

子 Flow 诊断与 trace 必须能关联选定入口、调用 token、定义位置、InvocationPrefix、
内部 node/perform 身份以及完整 cause 链；保留实例入口、完成或失败/跳过的边界信息。
等待依赖、子图或资源用原因说明，不增加新的节点生命周期状态。分层展示、参数/结果
预览及耗时统计属于 Runtime UI；展示方式不改变错误排序，也不要求 trace 文本确定。
编译器融合、管道降低或 Go 生成不能使错误只指向生成源码、临时变量或调用方包装层。

实现须声明 `LIP 1.2 core` 或 `LIP 1.2 core + durable profile`，并公布支持的
Go/Python transports、注册 contracts、资源预算和取消粒度。模型检验通过、某个 adapter
可用或部分示例运行成功，均不能替代第 14 节的一致性验收。

### 15.1. 分析依据与信息丢失诊断

诊断的 phase 为 static、runtime 或 host。已有 Code、源码位置、Position/EvalPath 和
根错误语义保持不变。依赖第 5 节描述的失败必须在结构化 details 中提供 analysis：
reason、origin、required、candidates_known、candidates、basis、causes。
origin 为可用的 SourceID/site/line/column；无法定位时明确为 null。required 描述
缺少的签名或资格；candidates_known 当且仅当描述证明完整非空请求候选集合时为 true，
非请求/未知位置使用 false。即使为 false，
candidates 也保留全部静态已知 operation；只有没有已知候选时才为空，不能当作空操作
集合的证明。按规范 operation 编码排序去重，包含 operation/version/Effect/Concurrent；
这些部分证据不能增强 Unknown Ops。
basis 与第 10.4 节一致；causes 关联已有依赖或信息丢失来源，不创造新的节点根错误。

reason 使用下列稳定含义；文案和分层排版可变化：

| reason | 含义 |
|---|---|
| unavailable_input | 参数/入口没有额外静态描述 |
| dynamic_target | 无法取得完整的有限可调用目标或兼容签名 |
| open_shape | 容器的 rest/元素中仍有未知来源 |
| recursive_boundary | 第 5.3 节重复 fn origin 的回退无法证明所需信息 |
| carry_boundary | carry 的跨轮结果，或未满足有限 exact fold 规则的 fold 结果，不提供该静态信息 |
| unsupported_transfer | 未列入第 5.2 节传播表的操作不提供该信息 |
| type_mismatch | 已知类型或候选不能提供所要求的值类别 |
| arity_mismatch | 已知签名不满足实参结构 |
| missing_position | 所选字段/下标没有正常返回位置 |
| contract_unknown | 实际登记效果为 Unknown，或所需合约信息不可得 |
| write_contract | 已知候选包含 Write |
| nonconcurrent_contract | 已知 ReadOnly 候选不允许并发 |
| analysis_budget | 明确配置的静态分析预算耗尽；诊断码为 E-BUDGET |

causes 保留全部不同原因，依次按 SourceID 的 UTF-8 字节、site 数值、reason 的 UTF-8
字节排序；SourceID/site 缺失排在已知值之后，剩余平局按原因语义字段的规范编码比较，
不含本地化文案。只去除语义相同的原因。顶层 reason/origin 取排序后第一个原因；
并行资格失败则优先 write_contract、其次 nonconcurrent_contract、再次 contract_unknown，
最后才取信息丢失原因，同类仍按上述顺序。静态预算失败使用 analysis_budget/E-BUDGET，
不推测为 E-PARALLEL。不能按分支完成或 map 遍历次序选择解释。运行时等待资源或
子图是等待原因，不伪装成静态信息丢失。

不完整候选同时含已知 Write 和未知来源时，顶层为 write_contract，保留该 operation
和未知原因，candidates_known=false。没有已证明禁止的合约时，使用排序后信息丢失
原因。诊断码和实际验证阶段仍属于失败判断，包括第 5.2 节全部候选无效的调用规则；
reason 优先级不产生新运行时根错误，也不覆盖更早的调用边界失败。

修复提示必须符合本规范：可以说明可用的已知请求路径、需要显式登记的真实合约或
顺序执行方式；不能建议把 Unknown 无依据改成 ReadOnly、随意添加 `@`、跳过授权或
重试已知完成的操作。显示或隐藏解释不重新分析运行时值，不执行程序，不改变原有
Steps、journal、诊断事件投递或错误排序。

### 15.2. 数据边界与调用事实诊断

参数/结果 schema 和原生转换失败的 details 必须标明 boundary、path、expected、actual，
以及可用的 operation/version。boundary 区分 entry_input、argument、result、entry_output
和 native_conversion；工具可进一步展示实际适配阶段。path 为从该边界根值出发的字段/
索引序列，字段项为 `{kind:"field",name:string}`，索引项为
`{kind:"index",index:string}`，index 使用规范非负十进制字符串。
expected 为声明或 schema 所要求的形状，actual 至少含实际类型，不要求输出完整业务值。
缺失字段、可缺省和显式 null 必须可区分；不能用实际值的原生 repr 代替安全类型描述。

entry_input 的根是逻辑 EntryInput 对象；argument 的根是绑定前实际提供的
`{positional,named}` 参数对象，path 标识位置下标或命名键及其嵌套值。缺少必填参数时
用 named 下的声明参数名定位，即使它原本也可按位置提供。result 和 entry_output 的
根是返回值本身，不含 Response/EntryOutput 传输包装。native_conversion 以当前转换
值为根，并标明 argument/result 方向及适用时所属边界的路径。根错误使用 `[]`；
缺失字段使用应有字段路径及 actual 的 missing 状态，与实际 null 类型区分。工具可
显示文本路径，但含点字段名仍是一个字段项。

保留原有验证阶段次序，包括普通调用的全部实参先求值、再检查参数；有声明签名时按
参数声明顺序检查。单次 Data/schema 验证先检查当前位置类别/外形，再深度优先检查
内容：list 下标按数值升序，object 字段按 UTF-8 键序，必填缺失在该字段的遍历位置
检查。多个独立违规只报告一个时取该顺序首项；额外违规也按相同顺序。不得按原生 map
遍历或并行完成次序选择。验证不填默认值，不把 missing 改成 null；诊断不为构造 actual
而访问原生属性。

调用相关诊断保留第 10 节已有 provider_fact/validation 及终态区别。准入前未调用、
已知完成后验证失败、已知拒绝和结果未定必须能被 Host 区分；原始 LIP 位置和调用路径
不能被生成源码或包装层覆盖。清理错误另行报告，不覆盖主错误，不声称已经回滚。
静态契约与诊断的序列化格式可由工具选择，但上述语义字段不能通过只保留一条文本消息
而丢失。

## 16. Core 附录：编码、显示与数值向量

### 16.1. 规范数据与身份编码

C(v)：null/false/true 为单字节 0x00/0x01/0x02；number 为 0x03 加 -0 归零、NaN 统一为 `0x7ff8000000000000` 后的八字节
大端 binary64；string 为 0x04、八字节大端无符号 UTF-8 长度与内容；list 为 0x05、
八字节大端无符号元素数与各 C(element)；object 为 0x06、八字节大端无符号字段数与
按 UTF-8 bytes 键序的 C(key),C(value)；char 为 0x07 加八字节大端无符号 Unicode
code point。char 只允许 Unicode scalar，不能编码 surrogate 或超过 U+10FFFF 的值。
大小超过编码/Host 预算必须失败。普通 JSON 将 char 显示为一个 scalar 的 string，只有
c-hex/canonical 保留 char 与 string 的类型区别。
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
CallDigest = lowerhex(SHA256(C(CallID_wire)))   # optional indexing digest
ArgFingerprint = lowerhex(SHA256(C([positional, named])))
```

字段数、tag 和 string 类型必须精确符合 wire 结构；tag 是首项的 "join" 或 "call"。
pair 排序先解码为任意精度自然数，或用等价的长度/数字串比较；`"9"` 在位置排序中
先于 `"10"`，不能用 string 的字典序。ordinal/index 任意精度或明确预算检查，不能
wrap；身份不因 binary64 安全整数范围给迭代次数设上限。
子 Flow 调用前缀已包含在 PositionPairs 内，不向 JoinID_wire/CallID_wire 追加私有字段。
wire 的解码与比较使用完整 pair 序列；源码映射依据同 revision 的 region/调用元数据
区分调用、图循环与节点内迭代锚点，不能假设 Position 的所有前缀都是图循环。

Host After 的目标使用以下 wire；它置于已固定 run_id/revision 的约束上下文中，不向
JoinID/CallID 添加字段。InvocationPrefixPairs 使用与 PositionPairs 相同的 pair 编码：

```text
AfterTarget_wire = ["node", PositionPairs]
                 | ["region", "flow", InvocationPrefixPairs]
                 | ["region", "loop", SelectorPositionPairs]
                 | ["region", "branch", SelectorPositionPairs]
                 | ["site", kind, decimal(site)]
kind = "node" | "call" | "loop" | "branch"
```

node 指一个动态节点；flow 指完整子 Flow 实例（入口实例的前缀为 []）；loop/branch
指相应图循环/分支 selector 所属 region 的完整完成。SelectorPositionPairs 是该
selector 的 Position 编码。site 指该静态 site 在本 run 中全部动态实例的完成集合，
call 使用第 6.2 节 invocation_site，其他 kind 使用对应 node/selector 的 site。
静态目标必须覆盖尚未展开的实例；无限 region 可以使约束永远等待，正常关闭与失败
按第 7 节传播。动态前缀必须能按冻结元数据验证其调用/循环路径；不能只用 callee 名称
或调用参数指认实例。根本不存在的目标拒绝；尚未选中的合法路径不凭空实例化工作。
动态索引是否存在须随 source/gate 求解；路径被正常关闭时按第 7 节 Skipped 规则
处理，其他可证不存在的实例拒绝约束，不能留下永久 Pending 的虚假完成条件。

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
| nan | 037ff8000000000000 |
| inf | 037ff0000000000000 |
| -inf | 03fff0000000000000 |
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
| nan / `[nan]` / string "nan" | `nan` / `[nan]` / `nan`；最后一项为原字符串，C 各不相同 |

普通 JSON decoder 可解码仅含有限数且非原样 string 的输出；含非有限数的显示不作为 JSON 传输。规范显示使用 UTF-8 键序和无正号的指数，控制字符
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
flush subnormal；number 的四则/literal 转换结果均按第 3 节规范化零。有限操作数除数为零和非有限
商按本节规则产生 NaN 或无穷。以上向量必须在解释器、生成 Go 和边界编解码中一致。

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
| `range(5)[[-1,0,-1]]` / `range(5)[::-2]` | `[4,0,4]` / `[4,2,0]` | core 求值及生成程序 |
| `range(5)[:-1:-1]` / `slice(range(5),null,null,-1)` | `[]` / `[4,3,2,1,0]` | core 求值及生成程序 |
| `[0][[0,1]]` / `[0][::0]` | E-INDEX / E-VALUE，无部分结果 | core 求值及生成程序 |
| function/effect list 经索引列表、切片再投影 | 原值保留；按有限描述选择对应来源，全部请求候选符合合约时可放行 parallel | core 静态/动态 |
| `{ len([0]); }` / `{ len([0]); null }` | 分别为 1 / null，末尾分号是 padding | core 求值 |
| `if false { 1 / 0 } else { 7 }` | 7；无数值错误，未选分支不消耗执行预算 | core 求值/不同 Ready 次序 |
| `flow Main()->number { if false { x=1/0 }; return 7 }` | x 为 unselected_gate；无根错误，F 为空，不截断，输出 7 | core 调度 |
| `[1 / x for x in [0,2] if x != 0]` | `[0.5]`；x=0 的 body 不求值 | core 求值 |
| `[x for x in [1] if 1 / 0 == 0]` | guard 得到 NaN==0 的 false，不能把数值 NaN 当成错误或静默跳过 | core 求值 |
| `[perform effect worker.read(x) for x in [0,1,2] if x>0]` | 输出 [1,2]；两个 CallID 的该 EvalPath 索引为 1/2，不是 0/1 | core 身份 |
| `"10" < "9"` / `"a" < "aa"` / `1 < "2"` | true / true / E-TYPE（直接已知时静态拒绝） | core 比较 |
| `parse_number("+1e-3")` / `parse_number("-0")` / `parse_number("3e-324")` | 0.001 / 0 / bits=1 | core 求值 |
| parse_number 的输入 `" 1"`、`"01"`、`"1x"`、`"NaN"`、`"1e309"` | 均为 E-VALUE | core 求值 |
| `f(x=1,2)` / `f(x=1,x=2)` | 分别静态 E-PARSE / E-ARITY | core 编译 |
| 声明 `fn known_fn(x:number)->number {x}`，调用 `known_fn(y=1)` / `len(value=[])` | 静态 E-ARITY | core 编译 |
| `f(perform effect worker.read(0), perform effect worker.read(1))` | arity 先失败，两个实参的 provider 调用均为零 | core 动态调用 |
| `f(perform effect worker.text())` | 实参调用一次，参数 E-TYPE，f body 不执行 | core 动态调用 |
| `fn request(x:number)->effect {effect worker.read(x)}`；parallel 内 perform request(1) | RetOps 为 `{worker.read}`，允许；构造和检查零 provider 调用 | core Ops |
| 多层/前向/互递归 fn 的返回联合只含 worker.read；简单别名或捕获请求 | BaseOps 最小固定点及规范递归回退不依赖声明顺序；符合条件时允许 parallel | core Ops |
| fn 返回的候选含 store.commit，即使在常量未选分支 | 不删除 Write 候选，parallel 静态 E-PARALLEL | core Ops |
| identity(p:effect) 返回 p；已知 function 实参；容器取出的函数 | 按第 5.2 节保留实参/投影来源；真正未知来源跨 REPL cell 后仍不反推增强 | core Ops |
| fn 只有无基础返回的递归环 | 内部空集固定点在使用处按 Unknown，不据此允许 parallel | core Ops |
| 纯 fn 返回普通列表，未使用的实参是原生资源；互递归仅交换并返回参数 | 普通列表不被输入污染；参数返回来源不因改名或声明次序改变 | core 原生适配 |
| 递归逐层包装或投影资源，静态来源不能有限确定 | 可用显式 receiver effect；构造零调用，不展开无限来源，不增强 Ops | core 原生适配 |
| `put({plain:[1,2]},"resource",ref).plain`；动态键更新后以固定键覆盖纯字段 | 已知覆盖字段保留其实际来源，不把其他资源字段或默认来源混入纯结果 | core 原生适配 |
| 返回闭包以外层捕获作为后来传入回调的实参；分支返回不同参数名/个数的函数 | 外层绑定和各支参数对应关系保留，合法调用的原生结果仍可使用 | core 原生适配 |
| if/match 返回等长列表，纯列表元素与资源引用相邻 | 固定下标保持位置来源；纯元素在 fn 中使用不引入 perform | core 原生适配 |
| `p=[effect worker.read(0)]; q=p[0]; r=perform q` | q 保留 read 候选，r 为 0；符合 Concurrent ReadOnly 时允许 parallel，不形成 Unknown barrier | core Ops |
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

以下完整文件使用上述 worker.read 合约，Host 显式选择 Main，输出 `{a:5,b:2}`；
同定义 Read 执行两次，两个 CallID 不同。声明顺序不改变调用解析或实例在父图的排序。

```lip
import python "example.worker" as worker
flow Main() -> object {
    a = 2 |> Read |> Adjust(delta=3)
    b = Read(2)
    return {a:a, b:b}
}
flow Read(value:number) -> number {
    return perform effect worker.read(value)
}
flow Adjust(value:number, delta:number) -> number {
    return value + delta
}
```

下表的片段位于合法 Flow 中，Read/Adjust 沿用此定义，每行单独编译/执行；普通 fn 与
故障/Claim 子 Flow 按行内条件另行声明。未标为 durable 的用例不要求持久恢复。

| 输入/固定环境 | 预期判定 | 层/模式 |
|---|---|---|
| 多 Flow 根文件包含 `flow Main`，未显式选择入口 | 选择 Main，与声明顺序无关；默认与显式选择 Main 的构建身份一致 | core 入口 |
| 根文件无 Main 且有多个 Flow，或显式选择未知/非 Flow 名 | E-ENTRY，provider 调用零次；不回退到默认入口 | core 入口 |
| 根文件同时有 Main/main；显式选择另一个根 Flow；仅依赖模块有 Main | 默认仅优先根 Main；显式选择覆盖默认；依赖 Main 不参与根入口选择 | core 入口 |
| 入口 `flow Main(x:number,y:string)->object`，输入 `{positional:[2],named:{y:"a"}}` | x=2、y="a"；完整 EntryInput 固定为 run 输入 | core 入口 |
| 同入口输入缺 y、重复绑定 x、未知名或将 x 传为 string | Host 运行前 E-SCHEMA；无节点根错误/Claim/provider 调用 | core 入口 |
| durable 新 run 的 EntryInput 非法；另一次提交合法输入 | 前者在 begin 前拒绝，不分配 run_id、不建立持久 run/journal；后者验证通过后 begin，登记完整输入再执行 | durable 入口 |
| `flow Main()->null { return null }`；把 void Flow 的调用结果绑定为变量 | 前者成功输出 null；后者静态 E-TYPE，void 不充当 null | core 类型 |
| `flow Main()->null { return null }`；显式 void 入口；空文件的隐式 main | Host 成功结果依次为 `{kind:"value",value:null}` / `{kind:"void"}` / `{kind:"void"}`；均等待全图完整成功 | core 入口输出 |
| Main 的最后节点写 `return Read(2)` | 合法；等待 Read 全图成功，再验证 Main 的返回注解 | core 返回 |
| `perform effect worker.read(2) \|> Adjust(delta=3) \|> Adjust(delta=1)` | 6；初始 read 仅调用一次，阶段按顺序完整完成 | core 管道 |
| `perform effect worker.read(2) \|> Read`；`2 \|> Read \|> Adjust(delta=3)` | 分别为 2 / 5；阶段输入是已固定值，不形成嵌套 Flow 实参 | core 管道 |
| `2 \|> Adjust(value=2,delta=3)` / `2 \|> Adjust()` | 首参数重复 / 缺少 delta，静态 E-ARITY | core 编译 |
| `xs \|> len`；`xs \|> map()`，xs 为 list | 前者等于 len(xs)；后者缺回调参数，E-ARITY | core 核心阶段 |
| `ok && x \|> F`；`x \|> F == y`；`(x \|> F) == y` | 分别按 `(ok && x) \|> F` 分组 / E-PARSE / 比较阶段结果 | core 语法 |
| guard 中的 `xs \|> len` 与 `2 \|> Adjust(delta=3)` | 前者可参与纯 bool 表达式；后者即使 Summary 为 Pure 也为 E-PURITY | core 纯性 |
| 普通 fn f 接收 number，Flow 内 `f(Read(2))`；局部函数值 f 用作 `2 \|> f` | 前者按局部游标合法求值；后者目标不静态解析为声明，E-TYPE | core 调用 |
| child 接收/返回 `function`、`effect`、可选形式或包含它们的 list/object/any | 合法；仅验证注解，不执行请求；Flow 定义仍不能作为值 | core 普通值 |
| child 返回捕获其参数的闭包，调用方在 child 完整结束后调用 | 使用原冻结捕获，局部图清理不破坏闭包，也不捕获活动 store | core 生命周期 |
| `flow Request(x:number)->effect{return effect worker.read(x)}`；parallel 内 `perform Request(1)` | RetOps(F) 保留 read；允许 Concurrent ReadOnly，构造零 provider 调用 | core 摘要 |
| child 参数 p:effect，body `perform p`；parallel 调用时实参为已知 read | 执行 Summary 的参数 Ops 仍为 Unknown，静态 E-PARALLEL；返回描述传递不改写子图执行效果 | core 摘要 |
| 嵌套实参/管道命名参数含 Write，目标 child 自身为 Pure | 调用方仍为 Write，parallel 静态 E-PARALLEL | core 效果 |
| 上一实参/阶段失败，后续实参含 perform | 后续实参和 child body 均不执行，已完成动作不回滚 | core 次序 |
| child 返回函数/effect 后供本 run 使用；根入口返回同一值或向 Host/Claim 传递 | 前者合法；入口/Host/Claim 递归 Data 边界拒绝后者 | core Data |
| `Read(perform effect worker.read(2))` / `Adjust(Read(2),delta=3)` | 合法，分别得到 2 / 5；实参按源码顺序完整完成后才启动 child | core 编译 |
| fn body 调用 Read 或效果为 Pure 的 Adjust；把 Read 作为 map 回调 | 前两者 E-PURITY；Flow 非函数值，后者 E-TYPE | core 编译 |
| F 调用 G，G 在常量 false 分支中调用 F | 静态 E-FLOW-CYCLE，不递归展开；普通 fn 互递归仍合法 | core 编译 |
| 动态 Data 参数类型不符合 Read 的 number 注解 | E-SCHEMA，Read body/provider 调用零次 | core 参数边界 |
| 管道某一级的动态参数不符合注解；某 child 返回错误注解类型；根入口返回非 Data | 前者归 owner、error_site 为该 stage 目标 token；返回错误归对应返回节点 | core 诊断 |
| `if false { ignored=Read(2) }` | 未选调用无参数求值、子节点语义求值或 provider 调用 | core gate |
| child 的 return 已有值，但已选审计节点仍未完成 | child 不发布成功结果，下游阶段及其额外参数不求值 | core 完成边界 |
| child 返回合法 `{ok:false}`，后接下一阶段；另用 if 显式保护下一阶段 | 前者仍继续；后者只执行被选分支 | core 业务控制 |
| child 内执行故障 store.commit，provider 已写入但返回 schema 不符 | 保留 known_fault/receipt，主错误定位内部 perform；下游失败跳过，不重跑写入 | core Host / durable replay |
| child 传递执行静态已知 fn 返回的 proposal，放入 parallel body | 使用 RetOps；全部候选为 Concurrent ReadOnly 时允许，Write/Unknown/不可并发读取仍 E-PARALLEL | core 摘要 |
| 同一 Claim 子 Flow 在两个调用点、两轮图循环和两轮 carry 中运行 | store 实例隔离，JoinID/CallID 不冲突；返回快照仍检查全部 producer | core 身份/Claim |
| child 较早节点失败，较后无必要依赖的节点尚未启动 | 较后节点按 F/K 截断，容器前缀不强迫其执行；根错误不重新包装 | core 错误 |
| 共享工作额度为 1；局部 value_block 中调用含多个节点的 child | 正常推进，无嵌套等待死锁；下一条局部语句等待 child 完整成功 | core 调度 |
| Running owner 的 Summary 为 Write/Unknown，child 内有 Write barrier | child 不等待 owner 整体完成；等待的祖先不占额度，不形成自等待 | core 顺序/并行 |
| child 有外部动作；owner 前有未完成 barrier，owner 后有 Write 节点 | child 继承前置 barrier；后继 Write 等 owner 完整完成，容器豁免不允许跨越 | core order |
| After 用 flow InvocationPrefix 指定同一 owner 内两个不同子实例；另要求 child 等自身 owner 完成 | 前者准确区分实例并等待完整 region；后者 E-AFTER-CYCLE | core After |
| 同一局部游标内，后续调用 After 等待已跳过分支或已完成局部循环中的调用集合 | 无虚假的未来 owner 依赖；反向约束形成环时仍明确报错 | core After |
| 父级预算耗尽或外部取消，child 含已完成/未决操作 | 不重置预算；取消传播，保留记录与 Uncertain，不保证撤销 provider | core 生命周期 |
| 恢复同 run 时仅更换入口、语言版本、模块锁或子 Flow 源码内容 | E-REPLAY-DIVERGENCE；新 provider 调用零次 | durable |

身份前缀的固定向量：owner Position=`[(20,0)]`，调用点分别为 25/40，callee 内节点为
3，则子节点分别为 `[(20,0),(25,0),(3,0)]` 和 `[(20,0),(40,0),(3,0)]`，前者先于
后者。同一调用点 25 若位于节点内循环 site=22 的第 0/1 轮，则为
`[(20,0),(22,0),(25,0),(3,0)]` 与 `[(20,0),(22,1),(25,0),(3,0)]`；其 EvalPath 不再
重复调用方的 `(22,index)`。所有 pair 仍使用第 16.1 节十进制 string wire。
第二轮的子 Flow 完成目标为
`["region","flow",[["20","0"],["22","1"],["25","0"]]]`；内部节点 3 的目标为
`["node",[["20","0"],["22","1"],["25","0"],["3","0"]]]`，两者不能混用。
若 owner 本身还在图循环 site=10 的第 2 轮，则上述全部向量前加 `(10,2)`；该循环
来自 Position(owner)，不再次计入 EvalPath_at_call。更深调用继续以其父 owner 的
完整 Position 为前缀，不从根或 callee 定义 site 重新开始。

循环 source 含子 Flow 调用的固定向量使用独立的冻结元数据环境，Read 返回 list：
入口图循环的 for_site=10，source 中 Read 调用的 invocation_site=12，Read 定义内
节点 site=3，循环 body 节点 site=20。
selector Position 为 `[(10,0)]`，其 source 调用的 InvocationPrefix 为
`[(10,0),(12,0)]`；callee 节点为 `[(10,0),(12,0),(3,0)]`。第 0/1 轮 body 节点
分别为 `[(10,0),(20,0)]` / `[(10,1),(20,0)]`。source 求值不加入该循环自己的
迭代索引；selector/source 与第 0 轮 body 虽共享 `(10,0)`，仍由不同 token 及结构
元数据区分。source 调用的全部节点先于 body 节点，因为 12<20；不能把 source
子实例误记为 body 第 0 轮，也不能把 selector 的完成误当成整个循环完成。

模块与资源的一致性用例使用冻结构建输入；这些要求同时适用于解释与生成程序。
源文件顺序是构建身份顺序，不是并发完成或文件系统遍历顺序。

| 输入/固定环境 | 预期判定 | 层/模式 |
|---|---|---|
| 根源码导入两个各自有私有 fn helper 的模块 | 名称隔离；根入口数量仅计根模块 Flow，不受依赖 Flow 数量影响 | core 模块 |
| 只含 import/普通 fn 的根文件与依赖文件；根文件只含 export fn 或静态数据 | 前两者分别产生隐式空 main / 仅收集声明；后者 E-ENTRY | core 文件模式 |
| export 数据之后再写 import | E-PARSE；import 必须先于全部声明 | core 语法 |
| 完整根文件为 `flow Main()->number { return LIMIT }`、换行、`LIMIT:number=2`，最后一字节为 `2` | 合法，无需末尾换行；唯一入口成功输出 2 | core 语法 |
| 最后一行是 import 且无末尾换行；其目标/合约已提供 | 合法；仅含 import 的根文件产生隐式空 main，依赖模块仅收集 import，不产生 main | core 语法/文件模式 |
| import 后直接接声明、或 data_decl 后直接接下一声明，中间无 sep 且未到 EOF | E-PARSE；EOF 豁免仅适用于文件结束 | core 语法 |
| 已提供 pricing 模块，访问未 export 的成员；把 alias 本身传给 fn | 分别 E-EXPORT / E-TYPE，不触发 Host | core 名称 |
| 依赖模块含合法语法的顶层 perform；静态数据 RHS 写成函数调用 | 分别 E-MODULE / E-PARSE，模块加载不执行外部动作 | core 静态初始化 |
| A import B，B import A；根源码未使用这些 alias | E-MODULE-CYCLE；未使用不免除依赖检查 | core 构建 |
| 缺失依赖、路径越界或目标实际内容与锁不符 | E-MODULE，在任何 run/provider 开始前拒绝 | core 构建 |
| 大小写/Unicode 字节与目录项不符、路径经过符号链接、两个 SourceID 对应同一物理文件 | E-MODULE，不通过文件系统宽松匹配产生第二个模块身份 | core 本地解析 |
| 同一 source set 以不同文件发现/编译并行次序构建 | SourceTable、DependencyLock、StaticSite 与 revision 完全相同 | core 可重复构建 |
| 根源码不变，只修改依赖注释、源码内容或锁定版本 | 多源 revision 改变；未完成旧 run 不得按新来源恢复 | core / durable |
| 构建快照完成后修改磁盘文件；启动已编译 Program | 使用原冻结源码；不热加载、不重新求值模块；新源码须重新构建 | core 冻结 |
| 导出 fn 返回 proposal；导入者 perform 其结果并放进 parallel | 按定义模块的 RetOps 与 registry 判定，符合 Concurrent ReadOnly 时允许 | core 效果 |
| 两个 HTTP run 共享 application 池，各有 run 对象/worker 租约；取消一个请求 | 只撤销/清理该 run 的租约，不关闭共享池或取消另一个 run | core Host |
| 请求结束后在另一 run 使用旧 run 引用，或重置 worker 后使用旧 generation | E-RESOURCE，provider 实际动作零次；不隐式重建对象 | core 资源 |
| provider 写入 outcome 不明，run 取消并触发生命周期清理 | 保留 Uncertain，安全隔离/结算租约，不自动重启重试写入 | core / durable |
| 同一只读 Program 同时被两个生成/解释请求使用 | 静态元数据不改写，run/journal/Claim/取消隔离；结果符合相同合约 | core 并发 |

多源 site 的固定向量：根来源 token_count=40，另两个来源按 SourceID 排序，token_count
分别为 20/10；三者本地 ordinal=3 的 StaticSite 分别为 3/43/63。同一调用点调用不同
模块的定义时仍按 InvocationPrefix 再按这些全局 site 定位；不能仅以本地 ordinal=3
合并节点。只有根来源时 ordinal=3 仍为 StaticSite=3，身份 wire 不变。

HTTP 接入还须满足以下用例；它们不要求未声明 HTTP 配置的 core 实现提供服务器：

| 输入/固定环境 | 预期判定 |
|---|---|
| 仅配置 `GET /items`，入口包含 Write；发送 HEAD /items | 405，`Allow: GET`，零 run/零 provider 调用，不能使用 ServeMux 自动回退 |
| 仅配置 `GET /items`；发送 DELETE /items | 405，`Allow: GET`，不含 HEAD；零 run/零 provider 调用 |
| 显式配置 `GET /items` 与 `HEAD /items`；发送 DELETE /items | 405，`Allow: GET, HEAD`；零 run/零 provider 调用 |
| 仅配置 `POST /items`；发送 HEAD /items；另发送 GET /missing | 分别 405 且 `Allow: POST` / 404；均零 run/零 provider 调用 |
| 显式配置 `HEAD /items`，输出含合法正文 | 完整求值/验证与计算长度，响应不发送正文 |
| 路由省略方法；或响应含 Host 控制的头 | E-HTTP；前者启动前拒绝，后者提交前返回 500 |
| 配置 `GET /{x}`；路径 `/a%2Fb`；空正文 | path 为 `/a/b`、raw_path 为 `/a%2Fb`、params.x 为 `"a/b"`；json 为 null |
| 配置 `GET /{x}`；分别请求 `/A` 与 `/%41` | path/params.x 均为 `/A` / `"A"`；raw_path 分别为 `/A` / `/%41`，不能因 RawPath 为空丢失普通路径 |
| absolute-form 目标 `http://lip.test/a%2Fb?q=1`，配置 `GET /{x}` | host 为 `lip.test`，path 为 `/a/b`、raw_path 为 `/a%2Fb`、params.x 为 `"a/b"`、query 为 `{q:["1"]}` |
| `OPTIONS *`；authority-form 请求目标 | 400，在路由及 run 创建前拒绝；零 run/零 provider 调用 |
| 连接断开；另一个仍可写请求达到运行时限 | 前者记录取消、不合成 408；后者 504；已发出动作不自动重试 |

第 3.2 节诊断还须满足以下组合向量；诊断内容是有界显示，不参与 Data 规范编码。

| 输入 / 环境 | 必需结果 | 层 |
|---|---|---|
| `fn twice(x:number)->number{log("debug",x);x*2}`，输入 3 | 开启/关闭/满队列/关闭缓冲均返回 6；接收端故障不能改变计算 | core 诊断 |
| `print(1/0)`，输出关闭 | 仍显示 inf；不得省略实参求值 | core 求值 |
| 未选分支中的诊断；只声明/编译带诊断的函数 | 零发送尝试 | core 路径 |
| 带诊断的已知 ReadOnly 请求构造函数在 parallel 中执行 | RetOps 不丢失；真实读取可重叠；journal 只记录 perform | core 摘要 |
| `print(perform effect store.commit())` 位于 parallel body / 普通 fn / @{...} fn | E-PARALLEL / E-PURITY / 按显式承诺接受；实际写入违反承诺，Host 检查仍生效 | core 效果 |
| fn/effect/native 引用作为诊断值 | fn/native 仅不透明显示；effect 可显示第 3.1.1 节摘要，但不执行请求、不调用属性/快照、不保留资源 | core 生命周期 |
| 重放相同源码、run 与 journal | provider 不重调用；本次重新求值的诊断可再次发送 | core 重放 |

NaN 与 @{...} 显式承诺纯性的原生调用还必须满足以下组合向量。以下使用通用原生适配配置，
np.sqrt 无专用纯性合约；worker.read 为 Concurrent ReadOnly，store.commit 为 Write。
is_nan 使用第 3.3 节的辅助 fn。实际不纯的调用属于作者违约，静态接受不保证它的业务纯性。

| 输入/环境 | 必须结果 | 配置 |
|---|---|---|
| `nan`、`-nan`、原生不同 payload/sign 的 NaN | 同一规范 number，is_nan 为 true，C 为 037ff8000000000000 | core 数值 |
| `inf`、`-inf`、Go/Python 原生无穷 | number；C 分别为 037ff0000000000000、03fff0000000000000 | core 数值 |
| `inf-inf`、`0*inf`、`inf/inf`；`inf/0`；`1e308*2` | nan、nan、nan；inf；inf | core 数值 |
| `(-inf)**3`、`(-inf)**0.5`、`0**-inf`、`inf*/inf` | -inf、inf、inf、nan | core 数值 |
| `-1//inf`、`-1%inf`、`inf//2`、`inf%2` | -1、inf、nan、nan | core 数值 |
| `parse_number(str(-inf))`；`xs[inf]`；`range(inf)` | -inf；E-INDEX；E-VALUE | core 边界 |
| 正负无穷经原生参数/结果、入口与 c-hex 往返；JSON 编码 | 保留符号与 C；JSON 拒绝，完成事实保留 | core 传输 |
| `nan == nan`、`nan != nan`、`[nan] == [nan]`、`nan < 0`、`nan >= 0` | false、true、false、false、false | core 比较 |
| `nan / 0`、`nan % 0`、`nan */ 1`、`nan ** 0`、`1 ** nan` | nan、nan、nan、1、1；`0/0`、`0**-1` 为 nan、inf | core 数值 |
| `is_nan(parse_number(str(nan)))`；`parse_number("NaN")`；`is_nan(null)` | true；E-VALUE；E-TYPE | core 函数 |
| `xs[nan]`、`xs[:nan]`、`range(nan)`、`nan + "x"` | E-INDEX、E-INDEX、E-VALUE、E-TYPE | core 边界 |
| match 的 nan literal、number 类型与 is_nan guard | literal 不匹配；类型与 guard 可选中；未选 arm 零调用 | core 控制 |
| 同 JoinID 重投 NaN；同 CallID 重放规范 NaN 参数 | 幂等；复用结果零 provider；改成 null 或 "nan" 必须冲突/分歧 | core 身份 |
| `fn a(x:number)->number @{np.sqrt(x)}`；`map([1,4],fn(x:number)->number @{np.sqrt(x)})` | 标记体合法，后者得到 [1,2]；去掉相应 @ 则 E-PURITY，无需为 sqrt 特判 | core 语法/纯性 |
| fn 执行 np.sqrt、显式 perform、已知请求构造函数结果或捕获的请求 | 普通函数体 E-PURITY；@{...} 按作者承诺接受，无逐函数纯性登记 | core 纯性 |
| flow 的 body 或普通表达式前使用 @；@{...} fn 访问活动 store/调用子 Flow | 前两者 E-PARSE；后者仍 E-PURITY，标记不豁免核心结构规则 | core 边界 |
| @{...} fn 执行 effect 参数、容器投影或混合候选；同请求在 fn 外执行 | 标记体内接受且检查实际 proposal；fn 外按第 5 节取得候选或 Unknown，不凭纯性承诺增强 parallel | core Ops |
| @{...} 内定义未标记的匿名/具名 fn，其中含原生调用 | 嵌套普通 fn 仍 E-PURITY；须在其自己的函数体写 @{...}；普通 fn 调用已标记 fn 则合法 | core 作用域 |
| 含原生调用的 fn 用于 guard、proposal 参数、join RHS、carry next 或 map/fold 回调 | 合法；fn 外这些 Pure 位置直接 perform 仍 E-PURITY；严格顺序求值，未选路径零调用 | core 组合 |
| fn 同一调用点循环/尾递归执行 perform；map 多次回调 | 每次 CallID 不同；尾调用不累积调用链；重放保留 activation | core 身份 |
| parallel 混合含原生调用的 fn 与 Concurrent ReadOnly perform；与 fn 外 Write perform | 前者允许且保留读取效果；后者 E-PARALLEL；fn 承诺不改变 operation 的原登记效果 | core 效果 |
| fn 内 Concurrent=false 的原生调用由多个图节点同时到达 | Host 串行准入，等待不占工作额度；承诺纯性不授予线程安全 | core 并发 |
| 仅构造/返回 Write proposal 的 fn；fn 内 Unknown perform 留有未决 intent | 返回请求在 fn 外仍为 Write；未决调用按 Unknown 恢复，不因 fn 自动重试 | core 边界 |
| NaN 入口、Host 参数/结果、Claim snapshot，经 c-hex 传输 | 原值与 C 往返一致；native 得到 NaN 数值，不是编码字符串 | core 传输 |
| json 配置发送 NaN；json 接收裸 NaN；有限值使用原协议 | 编码拒绝/解码拒绝；已有字节不变；不自动切换配置 | core 传输 |

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
零和溢出；`math.Inf(1)` 为此有限操作数算法的正无穷溢出结果；它成为 LIP 的 inf，而不是错误。
已有无穷操作数的规则由第 3.3 节先处理，不进入此算法。
整数/浮点转换遵循代码，涉及的循环索引可精确表示。符合本节结果的等价优化可使用；
不得借由误差容限接受不同的结果位模式。

调用前先按第 3.3 节处理非有限操作数，再执行第 3 节有限数定义域检查；corePower 接受检查通过的有限操作数，coreLog 只接受
正有限操作数，coreExp 只由 corePower 调用。`**` 取 corePower(a,b)；`*/` 先处理
第 3 节的精确特例，再取 numericDiv(coreLog(a),coreLog(b))。保留算法产生的 NaN/无穷并规范化零。
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
无符号 64 位编码，十六进制写法仅用于说明结果。数值定义域与溢出产生 NaN/无穷值；类型错误仍不产生成功值。

| 表达式 | 预期结果 |
|---|---|
| `0 ** 0`、`(-1) ** 9007199254740992` | 1 |
| `(-2) ** 3` | -8 |
| `2 ** -1074` | bits=1，最小正次正规数 |
| `2 ** -1075`、`(-2) ** -1075` | 0 |
| `2 ** 1024`、`0 ** -1`、`(-2) ** 0.5` | inf、inf、nan |
| `3 / 0`、`-3 / 0`、`0 / 0` | inf、-inf、nan |
| `222 ** 8888`、`nan * inf` | inf、nan |
| `0 */ 2`、`2 */ 1`、`2 */ 0`、`2 */ -2` | -inf、nan、nan、nan |
| `0.25 */ 2`、`8 */ 0.5` | -2、-3 |
| `2 ** 0.5`、`4 ** -0.5` | bits=`0x3ff6a09e667f3bcd`、0.5 |
| `5e-324 ** 0.5` | 精确 2^-537，bits=`0x1e60000000000000` |
| `27 ** (1/3)` | 3 |
| `2 */ 3` | bits=`0x3fe430939835353d` |
| `10 */ 2` | bits=`0x400a934f0979a372` |
| `"2" ** 3`、`8 */ true` | 静态 E-TYPE；动态 any 同样报 E-TYPE |
| `if false { (-2) ** 0.5 } else { 7 }` | 7，未选分支不求值 |

以下逐位表指定本节参考算法的结果，不能用平台 pow/log 或十进制近似相等替代。
parse_number 的显示往返另按第 3/3.1 节检查，
解析器须接受规范显示中的全部数值形式，不能要求输入具有 JCS 的显示拼写。

| 表达式 | binary64 bits |
|---|---|
| `2 ** 0.5` | `0x3ff6a09e667f3bcd` |
| `4 ** -0.5` | `0x3fe0000000000000` |
| `5e-324 ** 0.5` | `0x1e60000000000000` |
| `27 ** (1/3)` | `0x4008000000000000` |
| `0.25 */ 2` | `0xc000000000000000` |
| `8 */ 0.5` | `0xc008000000000000` |
| `8 */ 2` | `0x4008000000000000` |
| `2 */ 3` | `0x3fe430939835353d` |
| `10 */ 2` | `0x400a934f0979a372` |

### 16.6. Data 传输与普通 JSON

Data 是逻辑值域，不等于普通 JSON 的值域。语言入口与 operation 协议的通用跨进程
Data 通道必须提供以下显式配置；同进程 API 可采用等价的类型化传值。外部业务协议
（如第 12.1 节 HTTP）可明确限定为普通 JSON。配置在通道建立时固定，并与对端约定，
不得从收到的 string/object 形状猜测编码，也不得随某个值自动切换。

| 配置 | Data payload 的 wire 表示 |
|---|---|
| json（缺省） | 仅含有限数的 Data 按第 3.1 节普通规范 JSON 编码；收到裸 NaN/Infinity 或待编码值含 NaN/正负无穷均拒绝 |
| c-hex | 每个 Data payload 为 JSON string，内容恰为 `lowerhex(C(v))`；解码后再执行 schema/注解检查，支持完整 Data 域 |

这里 payload 指协议中明示为 Data 的位置：EntryInput/Request 的 positional 各元素和
named 各值、EntryOutput.value、Result 的值、ErrorData.details，以及 journal 中的
Data receipt/raw_data 等。外层协议结构、字段名、operation/version、CallID_wire、
kind/code/message 等协议元数据不转成 c-hex。嵌套 list/object 作为该 payload 的一棵
完整树编码，不能只把其中非有限数替换为私有 tag。例如 c-hex EntryInput 的
`{positional:["037ff8000000000000"],named:{}}` 表示一个 NaN 实参；同样字段在 json
配置中表示 string 实参。两端必须使用同一配置，不允许形状探测或混合解释。

c-hex 只接受小写、偶数位 hex 和第 16.1 节的完整规范 C：检查长度/预算、UTF-8、
唯一且排序的 object 键、规范零与规范 NaN，允许正负无穷，拒绝其他 NaN bits、未知 tag 和尾随
字节。不能先分配未验证长度的容器。原生 number 输入则按第 3.3 节归一化 NaN 后编码。
schema 可明确要求有限 number，但裸 number 注解本身不能拒绝 NaN/正负无穷。原生 adapter
取得解码后的数值，不把 c-hex 字符串直接交给库函数。

| 逻辑 Data | c-hex payload 的 JSON |
|---|---|
| nan | `"037ff8000000000000"` |
| inf | `"037ff0000000000000"` |
| -inf | `"03fff0000000000000"` |
| null | `"00"` |
| string "nan" | `"0400000000000000036e616e"` |
| 1 | `"033ff0000000000000"` |

json 配置下非有限数请求实参须在 provider 准入、intent 或实际调用前拒绝；非有限数结果必须
通过 c-hex/类型化通道交付，否则记录转换失败，不替换为 null/string。入口输入的
解码/验证错误仍为运行前 E-SCHEMA；入口成功后的输出编码错误是 Host E-VALUE，
不伪造节点根错误；Host 调用转换错误按第 10 节保留完成事实。
第 12.1 节 HTTP 接入固定使用普通 JSON；其 json 字段含非有限数时在提交前报 E-HTTP。
应用若需经 HTTP 交付非有限数，须通过 body 和明确 Content-Type 的自定义协议，不把裸
NaN/Infinity 宣称为 application/json。journal 与 Claim 身份始终基于 C，不能基于传输字符串
或语言 `==`；切换传输不会把非有限数与任何业务 object、null 或 string 混淆。

### 16.7. 封装、契约与适配一致性向量

各行独立使用第 16.4 节的 worker/store 合约，重置 journal 与绑定。read 为 Concurrent
ReadOnly，commit 为 Write；未显式登记的通用 bridge 保留 Unknown。表中“已知”均指
第 5 节静态描述，不指检查运行时对象。表中规则属于 core，不依赖某个工具的命令名。

| 输入或场景 | 必须结果 |
|---|---|
| 请求经 object 固定字段、list 固定下标、已知索引列表/切片再投影 | 保留对应请求候选；全为 read 时允许 parallel；构造与静态检查零 provider 调用 |
| 动态下标选择闭合的有限 read 请求列表 | 联合全部元素候选，允许 parallel；越界仍按实际求值报错，不产生 provider 调用 |
| 动态选择包含 read/commit 的请求列表或函数候选 | 候选包含 Write，E-PARALLEL，analysis.reason=write_contract；不按常量 guard 删除候选 |
| identity 的参数被直接返回，或经闭合字段包装返回 | 已知请求实参的 Ops 保留；裸 effect 参数没有实参描述时仍 Unknown |
| 纯 fn 参数接收已知请求构造函数，经容器选择或返回闭包调用 | 按各候选实际签名和抽象捕获传递；正常返回的请求保留候选；实际 arity/类型检查不省略 |
| 候选函数签名不同且只有一部分匹配实参结构 | 不套用第一候选签名，不提前拒绝可能成功的调用；返回描述 Unknown，实际 callee 动态检查 |
| map 使用已知构造函数，非空 source 元素描述已知 | 返回列表的 element 保留回调结果描述；空 source 上的已知错误 arity 仍静态拒绝 |
| 递归返回联合存在直接 read 基础分支 | BaseOps 最小固定点保留 read；重入时回退不无限展开 |
| 递归逐层包裹参数或重复同一 fn origin | 回退到 BaseOps 或声明返回类型；缺少请求信息时 reason=recursive_boundary，不因预算/声明顺序得到私有资格 |
| Flow 仅返回传入请求；另一个 Flow 对传入请求执行 perform | 前者 RetInfo 保留实参；后者 Summary 仍在声明环境保守为 Unknown，不凭调用点改写执行效果 |
| 契约视图、静态帮助、补全、错误展开 | 零新增业务调用，不导入 Python、不读取原生属性；动态文档查询作为独立 Host 动作验收 |
| 原生签名已知，但操作登记为 Unknown | 视图能显示签名，执行资格仍 Unknown；不将签名发现当作效果证明 |
| nullable 参数与 optional 参数 | 视图与 schema 检查保留两者区别，不能因允许 null 而省略必填参数 |
| result.items[2].name 类型错误 | boundary=result，path 的字段/索引项准确，expected/actual 与 operation/version 可取得 |
| 已完成 Write 的结果不符 schema，再次 terminal replay | 保留 provider_fact 与验证失败；同 CallID 不新增调用，提示不建议盲目重试 |
| 请求和资源经包装、闭包、模块与跨 cell 保存 | 描述与原 owner/generation 同时保留；失败 cell 不提交描述，不续命、不反推 Unknown |
| 重命名、声明反序、额度 1/2/7、解释器/生成程序/REPL | 接受条件和语义字段一致；显示名称或文案可变化，元数据不改变动作、预算或错误排序 |

### 16.8. 边界与次序一致性向量

使用第 16.4 节合约，每项重置绑定/journal。下表 r 为已构造的 Concurrent ReadOnly
读取请求，w 为已构造的 Write 请求，u 为真实未知的请求描述；这些名字描述测试值，
不是新增语言原语。除明确要求运行时检查的行外，“拒绝”均指静态拒绝，provider 零调用。
运行时签名/类型用例须经规范允许的边界取得未知信息，不能用 any 擦除已有描述。
适用用例经模块/闭包/cell 及解释器/实际生成 Go 检查，并使用并发额度 1/2/7；区分
源码动作次序变化与声明次序置换。

| 边界或组合 | 必须结果及可观察次序 |
|---|---|
| 重排/重新结合 Union(r,w,u)，或改变描述缓存访问顺序 | Ops 保持 Unknown；保留 r/w 已知证据；parallel 报 write_contract、candidates_known=false；全部 causes 稳定排序 |
| 同一闭包 origin 分别捕获 r 和 w，动态选择后调用 | 保留两个捕获候选，返回含 Write 并拒绝 parallel；不只按 origin 去重 |
| 对 [r,w] 与 [r] 的联合选字面量 -1 | 分别按自身长度归一，候选为 w/r，拒绝 parallel；常量 guard 不删除分支 |
| 固定字段位于开放对象 rest、外形未追踪对象，或所有闭合候选均缺字段 | 开放候选使用 rest；未知外形仍 Unknown，不是 NoResult；全缺失闭合投影为 Unknown(missing_position)，不凭空取得并行资格 |
| 对 [w,r] 作字面量 gather [1,-1,1]，再取字面量下标 0 | 保留重复/顺序且仅含读取候选，符合条件时允许 parallel；运行时动态选到无效下标仍先报错、零 provider 调用 |
| 对 [r] 作字面量 gather [0,9] | 整个候选没有正常投影，不产生部分 [r] 或 provider 调用 |
| 对 [r,w] 使用非字面量 gather 或切片界限 | 保留原 element 联合，不执行 selector 来删除 w，拒绝 parallel |
| 负步长 stop 的省略/null 与显式 -1；零步长或空选择 | 按第 3 节分别选择/运行时报错；空结果仍是 list，不是请求或成功 perform |
| 动态 put 写入 w 后对 request 固定 put 写入 r；对外形未知对象固定 put | request 固定字段恢复 r，其他字段/rest 仍可能含 w 或 Unknown；原对象不变，未知 rest 不能闭合 |
| 有括号的字面量键/下标，与字面量经参数或普通 binding 转发 | 括号保留字面量选择；参数/binding 后续引用仍动态，不把实际标量当作源码字面量 |
| 普通图循环 for p in [r,r]，与 for p in [r,w] | 迭代变量保留 source element 候选；前者允许 parallel，后者拒绝，不按轮次或常量 guard 删除候选 |
| map/推导式的 source 为已知空列表，回调有静态已知错误 arity | map 仍静态拒绝 arity；回调/body/guard 无 perform 或诊断；合法空结果不提供请求元素 |
| 空 map/fold source 上回调的签名真实未知 | 外层实参求值后检查实际 callable/arity；body 调用数和 fn activation 增量均为零 |
| 空 source 的 fold/carry 返回读取 seed，或 state-identity carry 返回原 state | 实际结果为 seed/init，静态描述保留 r；fold 不调用回调，carry body 仍静态检查；state-identity 只保留返回描述，不建立一般跨轮摘要 |
| 可调用候选均匹配名称/arity，只有部分接受已知实参类型 | Unknown(dynamic_target)；不拒绝可能成功的调用，也不删除不兼容候选取得并行资格 |
| number/string 参数的函数候选，与独立取得的 number/string 实参联合 | 保守分析组合，不把分支配对成全部失败或仅有读取；实际类型检查仍动态执行 |
| 全部可调用候选无效，一部分 arity 错、一部分类型错 | 选择更早验证阶段的静态 E-ARITY，保留全部失败原因，不求值实参、不调用 provider |
| 已知非 callable 与合法 fn 联合；再把合法 fn 改为 arity 错误 fn | 前者仍动态/Unknown；后者在 callable 阶段静态 E-TYPE，不依候选访问顺序选择 |
| identity(identity(r))、兄弟重复调用、不同 cell/模块中同名 callable | 顺序传递在下一个 body 前退出 Active；不同声明不碰撞；保留 r |
| 先缓存更精确的非递归结果，再递归重入并倒序访问候选 | 使用规定的 BaseOps/类型回退，不借用另一 Active/capture/实参上下文的更强结果 |
| 子 Flow 有未使用写入但返回 r；或执行未知 effect 参数 | 返回描述不掩盖执行 Summary，拒绝 parallel；子图成功发布仍等待全部选中工作 |
| 运行时 callee 求值失败，或实际 callable/arity 验证失败 | 不求值调用实参/body，实参 provider 调用数全部为零 |
| 较早实参求值失败，后续实参含写入 | 不求值后续实参/body；此前已完成的事实保留 |
| 全部实参求值成功，但较早参数动态类型不符 | 后续实参动作也先完成，再按声明顺序检查参数；body 零调用，已完成动作保留 journal |
| body 写入后返回注解失败；管道前一阶段失败或仍有未完成工作 | 保留写入事实；下一阶段实参/body 只在前阶段完整成功后执行；void 不替换为 null |
| object 有两个独立 schema 错误，或 list 下标 2 和 10 都有错误 | 按 UTF-8 字段/深度优先或下标数值顺序选择，不依原生 map 插入或调度次序 |
| 必填缺失、显式 null、可缺省未提供、字段名含点 | 保留不同存在性/类型及结构化 path；可缺省不自动填值；点仍属于单个字段项 |
| EntryInput 拒绝与 provider 已知完成后的转换/schema 失败 | 入口拒绝不分配 run；provider 失败保留 known_fault 及准确 boundary/path；terminal 重放零新增调用 |
| 已缓存签名/ReadOnly 视图后合约漂移、权限撤销或原生引用过期 | 缓存不授权/续命；合约不符在新增准入前失败，撤销/过期按真实边界处理，不自动重试或改绑版本 |
| 展开/隐藏解释，不完整候选且有多个或缺失来源 | reason/cause/path 稳定，不执行用户代码，不增加 provider 调用，不改 Steps/journal/主 ErrorKey，不保留资源 |
| 重排私有声明/改名，与重排有外部动作的实参或管道源码 | 资源足够时声明/改名不改分析接受条件；动作源码重排按新的求值次序；均不承诺跨 revision CallID 相同 |

### 16.9. 组合闭包、竞态与生命周期向量

本节把容易被局部规则误读的交叉边界固定为可观察向量。每行独立使用固定 source set、
registry、capability 和预算；除非写明，构造、静态检查、补全和未选路径均要求 provider
调用为零。`r`、`w`、`u` 仍分别表示已知 Concurrent ReadOnly、已知 Write 和真实 Unknown
请求描述。表中“实际调用”指 provider 进入次数，不把 proposal 构造或诊断发送计入。

| 组合 | 必须结果、先后与保留事实 |
|---|---|
| 动态 callee 求值含一个合法 `@` provider，随后普通调用 arity 错误 | 先记录 callee 的 provider；参数和 body 不求值；arity 错误不回滚 callee 事实 |
| callee 求值失败，或返回已知非 callable | 不求值任何实参/body；callee 已完成的动作保留，provider 次数只包含 callee |
| map/fold 的 source/seed 类型错误与 callback arity 同时存在 | 先按外层声明参数顺序报告 source/seed 类型错误；source/seed 合法而 callback 非 callable 时先报 callable，再报 callback arity；其余原因保留 |
| 静态空 map/fold/parallel source 与合法 callback/body | 仍检查签名、纯性、operation、Claim 与类型；运行时 callback/body/parallel 迭代均为零次，返回合法空值 |
| 空 source 的 `@` callback 含 Write/Unknown 原生调用 | 仍按 `@` 承诺完成静态检查，但不进入 callback，不产生 provider 或资源准入 |
| parallel source 本身含 Write/Unknown，body 只有 r | source 先顺序完整执行；body 可按 r 准入并发；source 效果不被 body 规则隐藏，也不随 body 重叠 |
| parallel source 失败、取消或预算耗尽 | 不实例化任何迭代；已有 source 动作/事实保留，body 无动态错误、join 或 provider |
| 较早独立纯节点产生非有限值，较晚独立 r 节点 | r 可能在较早错误被观察前启动；主错误仍为较早 ErrorKey，已启动 r 不撤销 |
| 同一节点内较早实参 Write、较晚实参失败 | 较早 Write 完成事实保留；较晚实参、body 和返回检查按局部游标停止 |
| 函数 body Write 后返回注解失败，随后有独立节点 | Write journal/外部事实保留；返回值不发布；独立节点是否启动只由其依赖/barrier 决定 |
| pipeline 前阶段返回值已就绪但内部 region 尚未完成 | 后阶段命名参数、callee/body 和子 Flow 均等待前阶段完整成功；不传递提前 return |
| pipeline 注入参数同时被显式命名，或上一阶段失败 | E-ARITY/前阶段错误在该阶段其他参数前确定；不求值重复或后续参数 |
| provider permit 等待与 worker allowance=1 | 等待 permit 的 Running 上下文让出 worker；释放 permit 的工作仍能推进，不死锁、不重复前缀 |
| provider 开始后取消/预算/断连，尚未收到应答 | 记录 terminal provider fact 或 nonterminal Uncertain；不能记成未调用，不能自动重试 Write/Unknown |
| terminal outcome 已落盘，随后节点发布前取消/预算 | 当前尝试可为 Cancelled/Error；恢复直接消费 terminal，provider 次数为零 |
| 已取得资源但 intent 尚未落盘就崩溃 | 释放租约；不能从资源取得推断业务动作发生；恢复按无 provider 开始处理 |
| application/session/run 原生引用跨 owner/generation 传递 | scope 本身不授权搬运；没有显式 rebind/snapshot 合约时报 E-RESOURCE，闭包/proposal/诊断不续命 |
| After 指向未实例化但合法的空分支/零次有限循环 | 该完成集合按正常 Skipped 满足，不能伪造节点或永久 Pending |
| After 指向可能无限展开的未来 loop/site | 保留符号完成条件，直到正常关闭/失败；不因当前没有实例而提前满足 |
| After 目标 Error/Cancelled/失败 Skipped，且另一依赖正常关闭 | 依赖节点为 Skipped(after_failure)；正常关闭目标满足，不把失败改成空值 |
| 完整 Claim 读取与 writer 在同一活动 loop，或 writer 失败 | 静态 E-CLAIM-ORDER，或运行时传播 writer 根错误；不提前返回部分/空 snapshot |
| live snapshot 与并发 join 交错 | 只返回线性化点已提交的原子；不等待未来 writer；相同 CallID replay 复用该观察 |
| 同 JoinID 重投相同/不同 value，跨不同 JoinID 相同 value | 相同 value 重投幂等；变值保留冲突并令触发读/后续读 E-CLAIM-CONFLICT；不同 ID 保留两条事实 |
| 请求候选含 `r,w,u`，改变 Union、缓存或声明访问顺序 | Ops 仍 Unknown，保留已知 evidence；parallel 以 write_contract 拒绝，主 reason/causes 稳定 |
| `BaseOps` 直接 read 基础 + 递归环，改变声明/候选访问顺序 | 得到同一最小固定点 `{r}`；Active 重入只用 BaseOps/声明类型回退，不借另一上下文的精度 |
| 递归无基础环，分析预算到边界 | 使用处为 Unknown(recursive_boundary)，或明确 E-BUDGET；不能因预算/声明顺序取得 parallel 资格 |
| durable reconcile 将 Uncertain 改为 Result，另有永久 later error | 解除暂定 F/K 后重开仅被其截断的工作；保留 later ErrorKey、旧调用和 CallID，不重做 terminal 调用 |
| 展示/隐藏诊断、契约解释或动态文档查询 | 静态查看不执行用户/provider；动态查询是独立 Host action；Steps、journal、ErrorKey、资源寿命不改变 |

这些向量与前面的表共同构成 1.2 的组合闭包：若某实现只能在删除某个候选、提前求值、
把等待当作失败、把资源事实当成值、或用完成次序替代身份后才通过，则不符合本规范。
组合测试必须分别覆盖解释器、实际生成 Go、会话保存值以及并发额度 1、2、7；只在静态
模型中通过不算执行路径合规。

## 17. 非规范性附注：仓库实现状态

本文件是完整的 1.2 规范。实现已覆盖第 5 节有限描述、容器投影、参数转发、已知
callable 候选、递归请求摘要及第 3 节非有限数值，并在解释器、生成 Go 和 session
路径中共享同一套规则；规范正文仍是语言语义的唯一依据。

现有代码提供入口、不可变值、纯函数、控制结构、静态模块、结构化 Flow、Claim、
图调度、统一 Host、诊断输出、显式原生纯性承诺和非有限数；解释、生成 Go 与 REPL
共享回归。Host 合约、原生返回来源、有限描述传播和诊断阶段均沿同一条调用边界工作。

仓库仅提供进程内调用记录，未实现可选 durable profile。HTTP 使用显式 Host 合约，
没有请求级通用原生 bridge。解释执行的自动 Go 开发 bridge 仍有工具链/CGO/plugin
平台限制，且不能据此声明满足静态绑定要求；生成程序与显式注册路径的能力分别说明。
图历史、Claim 与 journal 仍可随工作量增长，不承诺常量内存或从致命 OOM 恢复。

使用与真实限制见 [README](../README.md)，当前实现证据见 [IMPLEMENTATION.md](../IMPLEMENTATION.md)。
这些资料不是本规范语义的外部依赖。第 16.5 节包含完整数值参考代码与位向量，可用
`go run ./tests/reference_numeric -spec spec/LIP-1.2-SPEC.md -check` 独立检查该附录；
附录检查通过不构成 1.2 编译器或 core 合规声明。
