# LIP Alpha 0.6.4：从第一个 Flow 到可验证的数据程序

按“先运行、理解数据、组合操作、观察执行、接入外部能力”的顺序学习。
前 11 节足够编写独立的文本与列表程序；后面讲 Go Host、State、策略和 Python。
核心例子只需要 Go。完整程序均保存在仓库；自动测试核对教程源码，并实际编译、
运行正常、空输入和失败样例，数据见 [cases.json](../examples/tutorial/cases.json)。
规范见 [ALPHA-0.6-SPEC.md](ALPHA-0.6-SPEC.md)，操作目录见
[列表库](LIST-LIBRARY.md)和[字符串库](STRING-LIBRARY.md)。

也可以运行 `lipc learn`，按 26 节中文交互课边写边验证。课程内置讲解、示例、
代码练习、逐步提示和进度保存，详见[交互学习](INTERACTIVE-LEARNING.md)。

## 0. 准备与阅读方法

需要 Go 1.27 或更高版本。在仓库根目录安装，安装位置跟随现有 Go 配置：

```bash
go install ./cmd/lipc
```

下面用 `lipc` 表示已安装的工具，可使用 PATH 中的命令名或实际安装路径，
见[快速入门](QUICKSTART.md#安装)。示例文件路径以仓库根目录为基准：

```bash
lipc version
# 0.6.4
```

安装后的 `run/build` 也可在自己的项目目录使用，编译器内置 Runtime，无需保留
仓库；构建仍需 Go。后面可以直接运行lipc；若找不到可使用全路径，如`~/go/bin/lipc`。
工具选项写在文件前，程序输入直接跟在文件后，无需 `--`。
每节先运行，再修改一处核对结果。
JSON 对象的键顺序以实际输出为准，列表保持元素顺序。行注释使用 `#`。
完整示例可以直接运行，标注的片段用于说明局部写法。

## 1. 第一个程序：输入、绑定、输出

[01_hello.lip](../examples/tutorial/01_hello.lip)：

<!-- example: examples/tutorial/01_hello.lip -->
```lip
flow Hello(name: string) -> string {
    greeting = "你好，" + name + "！"
    return greeting
}
```

```bash
lipc check examples/tutorial/01_hello.lip
# ok: flow Hello, 2 graph nodes
lipc run examples/tutorial/01_hello.lip 小林
# 你好，小林！
```

flow 是可执行入口，name:string 是输入，->string 是输出契约。greeting 创建
不可变绑定，也是一个依赖图节点；return 提供结果。每文件只有一个 Flow，
可有多个 fn；有返回值的 Flow 用一个 return 或 match 的互斥分支收束结果。绑定直接写 `名字 = 表达式`，
按定义顺序使用，每个名字绑定一次。

继续处理 greeting 时，为新结果取名，例如 normalized=string.trim(greeting)，
再让 return 引用 normalized。文件后的参数按 Flow 声明顺序传入；试着少传
或多传一个名字，工具会给出参数提示。

只需要打印时，省略输出声明和 return 就可以自然结束：

```lip
flow main() {
    print(79 / 134)
}
```

`print` 可以接收数字、布尔、字符串、列表、对象等任意值，也可传多个值。
它只负责控制台输出，调用结果是 null。有返回值的 Flow 需要 `-> number`
等类型，并用 return 或 match 分支给出结果；没有返回值的 Flow 可省略输出声明，或显式写 `-> void`。
可在块末尾写裸 `return`，但不需要写；return 不会取消此前声明的图节点。

一组无参数、无返回值的语句还可直接写在文件顶层，连 Flow 外壳都省略。
[statements.lip](../examples/statements.lip) 是完整可运行示例：

```lip
a = 2
b = 3
print(a / (a + b))
```

运行 `lipc run examples/statements.lip` 输出 `0.4`。这等价于 `flow main() { ... }`；
需要输入参数、指定入口名字或返回值时，使用显式 Flow。`import` 和 `fn` 声明
仍放在文件开头；一个文件使用一种入口形式，不能把顶层执行语句和显式 Flow 混写。

多条执行语句在同一行时必须用分号分隔：`a = 2; b = 3; print(a / b)`。
换行可以直接分隔；连续或独立分号表示空语句；块末或文件末尾的分号可省略。这个规则适用于顶层语句、
Flow/match 内的语句和 REPL。参数、列表元素仍用逗号；分号不是逗号的替代。

不想创建文件时，用 `lipc repl` 直接试验：

```text
In [1]: value = 79 / 134
In [2]: value
Out[2]: 0.5895522388059702
In [3]: print(value)
0.5895522388059702
```

REPL 直接接收表达式、绑定和 fn，不需要 Flow 外壳或 return。变量、函数跨输入
保留；括号未闭合时显示 `...:` 并继续接收下一行。`:vars` 查看变量，`:history`
查看输入，`:reset` 清空变量与声明，`:cancel` 丢弃未完成输入，`:quit` 或 Ctrl-D
退出。绑定仍不可重复；失败的输入不会写入会话变量，已发生的外部副作用不会回滚。
每次输入经 Go 编译后运行，只保存成功的值和声明，不重复执行历史操作。
左右方向键编辑光标，上下方向键回看历史，Tab 补全，Ctrl-C 取消当前单元。
整段多行粘贴后按 Enter 提交。管道输入自动隐藏提示符，也可显式用 `--quiet`。
详见[编译器文档](COMPILER.md#交互式-repl)。

## 2. 六种边界类型与结构化数据

| 类型 | CLI 传法 | 检查范围 |
| --- | --- | --- |
| number | `12`、`-1.5`、`1e3` | 有限数值，浮点语义 |
| bool | `true`、`false` | 严格布尔值 |
| string | 原样文本，如 `'你好 LIP'` | 不剥 JSON 引号 |
| list | JSON，如 `'[1,2,3]'` | 外层列表 |
| object | JSON，如 `'{"name":"Lin"}'` | 外层对象 |
| any | 任意 JSON，如 `'null'`、`'"text"'` | 明确的动态边界 |

所有参数必须有类型，有返回值的 Flow 必须写输出类型。本地非递归 fn 可推断结果，教程
统一写出便于阅读。null 是值，可通过 any 或 number? 等可空参数输入。
参数、纯 fn、回调和 Flow 的结果都支持 T?；参数仍必须提供。可选输出写 number? 等，
允许结果为 null 或由门控跳过；输入按上表中的六种类型声明。

可空类型也能直接用于纯函数。先匹配 null，后面的 _ 分支就可以使用非空数值：

<!-- example: examples/tutorial/12_optional.lip -->
```lip
fn double(value: number?) -> number? {
    match value { null => null, _ => value * 2 }
}

flow Optional(value: number?, label: string?) -> object {
    return {value: double(value), label: label}
}
```

```bash
lipc run examples/tutorial/12_optional.lip 3 标签
# {"label":"标签","value":6}
lipc run examples/tutorial/12_optional.lip null null
# {"label":null,"value":null}
lipc run examples/tutorial/12_optional.lip null '"null"'
# {"label":"null","value":null}
```

T? 不表示可省略参数。CLI 的 null 代表空值；string? 接受原样文本或 JSON 引号
字符串，文本 null 用 '"null"' 区分。普通 string 参数的 null 仍是原样文本。
有守卫的 null 分支可能不匹配，不能让后面的 _ 分支排除 null。

[02_data.lip](../examples/tutorial/02_data.lip)：

<!-- example: examples/tutorial/02_data.lip -->
```lip
flow Data(person: object) -> object {
    scores = [8, 9, 10]
    return {
        name: person.name,
        first_score: scores[0],
        greeting: "Hi, " + person["name"],
        size: len("你好🌱"),
        character: "你好🌱"[2],
        missing: null,
        description: "score=" + str(scores[0])
    }
}
```

```bash
lipc run examples/tutorial/02_data.lip '{"name":"Lin"}'
# {"character":"🌱","description":"score=8","first_score":8,"greeting":"Hi, Lin","missing":null,"name":"Lin","size":3}
```

列表写 [...]，对象写 {key:value}，键也可加引号，例如 {"two words":1}。
可以嵌套，键保持唯一。person.name 与 person["name"] 都读取字段；
缺少字段时报告错误。索引从 0 起，使用范围内的非负整数，
负边界截取使用 slice。

len 对字符串、列表、对象分别数 Unicode 字符、元素、字段。字符串索引返回
字符字符串："你好🌱"[2] 为 "🌱"。组合字符按各自的 Unicode 码点计数，
例如 len("e\u0301") 为 2。object 检查外层形状，字段在读取时检查；
用 {} 运行本例可以观察缺少 name 的诊断。

## 3. 运算、显式转换、选择与失败

优先级从高到低：索引/调用、**、! 与正负号、* / // % */、+ -、大小比较、== !=、&&、||。
混合条件时加括号，让分组一目了然。
数字支持以下八种运算；字符串支持 + 连接、* 非负整数重复。

| 写法 | 含义 | 例子 |
| --- | --- | --- |
| x + y | 加 | 2 + 3 = 5 |
| x - y | 减 | 7 - 2 = 5 |
| x * y | 乘 | 3 * 4 = 12 |
| x / y | 除 | 7 / 2 = 3.5 |
| x // y | 整除，向下取整 | 7 // 2 = 3；-7 // 2 = -4 |
| x % y | 取余，符号跟随除数 | 7 % 2 = 1；-7 % 2 = 1 |
| x ** y | 乘方，右结合 | 2 ** 3 = 8；2 ** 3 ** 2 = 512 |
| x */ y | 以 y 为底的对数 | 8 */ 2 = 3 |

`**` 的优先级高于负号，`-2 ** 2` 是 -4；`(-2) ** 2` 是 4，`2 ** -2` 是 0.25。
`*/` 与乘除同级、左结合，`16 */ 2 ** 2` 是 2。对数要求正真数、正底数且底数不等于 1。
number 仍为浮点数，允许小数参与整除与取余；对数结果可能有浮点误差。
完整可运行例子见 [math.lip](../examples/math.lip)：`lipc run examples/math.lip`。

"count="+3 是错误，
写 "count="+str(3)。文本转数值用 string.parse_number。str 是显示转换，
字符串原样显示，列表、对象、bool、number 和 null 按 JSON 显示；print、str、
REPL 和入口保持一致。除零、非有限结果和错误动态类型返回错误。

[03_choice.lip](../examples/tutorial/03_choice.lip)：

<!-- example: examples/tutorial/03_choice.lip -->
```lip
flow Average(total: number, count: number) -> number? {
    valid = count > 0 && total >= 0
    average = if valid { total / count } else { null }
    return average
}
```

```bash
lipc run examples/tutorial/03_choice.lip 30 3
# 10
lipc run examples/tutorial/03_choice.lip 30 0
# null
```

if condition {value} else {other_value} 是产生值的表达式，每个分支写一个值。
分支结果可有不同类型，声明的 fn/Flow 结果类型负责约束实际返回值。
只执行选中的分支，所以 count=0 时直接得到 null。&&/|| 也短路；! 接收 bool。

不应正常返回 null 的无效输入，用 fail(message) 明确失败。下面是 fn **片段**：

```lip
fn nonnegative(x: number) -> number {
    return if x >= 0 { x } else { fail("value must be nonnegative") }
}
```

fail 接一个 string，不产生正常值；错误停止依赖它的计算。未选分支不执行。
宿主可接收并处理执行错误。
练习：让 Average 对负 count 失败，0 返回 null，正数求平均；保持唯一 return。

## 4. 字符串：清理、分割、查询、合并

[04_text.lip](../examples/tutorial/04_text.lip)：

<!-- example: examples/tutorial/04_text.lip -->
```lip
flow Text(input: string) -> object {
    cleaned = string.trim(input)
    words = string.split_whitespace(cleaned)
    return {
        words: words,
        normalized: string.lower(string.join(words, " ")),
        chars: string.chars("你好🌱"),
        tail: string.slice("你好🌱", -2, 3),
        position: string.find("你好🌱", "🌱"),
        contains: string.contains(cleaned, "LIP"),
        number: string.parse_number("12.5"),
        replaced: string.replace("a-a-a", "a", "b", 2),
        lines: string.lines("first\r\nsecond\n")
    }
}
```

```bash
lipc run examples/tutorial/04_text.lip '  Hello   LIP  '
# {"chars":["你","好","🌱"],"contains":true,"lines":["first","second"],"normalized":"hello lip","number":12.5,"position":2,"replaced":"b-b-a","tail":"好🌱","words":["Hello","LIP"]}
```

trim 去两端空白 → split_whitespace 切词并忽略连续空白 → join 统一分隔 →
lower 转小写。这些纯表达式可嵌套，无需 import，不依赖机器 locale。

| 需求 | 表达式片段 | 结果 |
| --- | --- | --- |
| 保留空槽位 | `string.split("a,,b,", ",")` | `["a","","b",""]` |
| 按 Unicode 空白切词 | `string.split_whitespace(" a　 b ")` | `["a","b"]` |
| 按行处理 | `string.lines("a\r\nb\n")` | `["a","b"]` |
| 按字符处理 | `string.chars("你好🌱")` | `["你","好","🌱"]` |

split 分隔符必须非空；空文本 split 为 [""]，另外三种为空列表。lines 识别
LF/CRLF，不保留末尾换行产生的最后空项，中间空行仍保留。split 是字面切分，
不理解 CSV 引号。join 只接字符串项，不暗中转换数字。

find 返回首次命中的字符下标，未命中为 -1；contains/starts_with/ends_with
返回 bool。count 数非重叠命中。slice 半开、负索引从末尾算、越界截断；
end≤start 返回空串。replace 默认替换全部，第四参数限制次数，0 不替换。
count/replace 的匹配文本非空。trim_start/trim_end 只去一端空白，upper 转
大写，repeat 指定重复次数。整数次数不能为负或小数。

parse_number 不接受前后空白、NaN/Inf、十六进制或下划线；需要 trim 时明确
组合。库的字符串输入/结果最多 16 MiB，列表结果最多 1,000,000 项。
完整 19 项规则见 [STRING-LIBRARY.md](STRING-LIBRARY.md)。

## 5. 本地纯函数：给小变换命名

真实的标签清洗任务，[examples/strings.lip](../examples/strings.lip)：

<!-- example: examples/strings.lip -->
```lip
# intent: 清理逗号分隔的标签，忽略空项，去重并保持首次出现的顺序。
# accept: " Rust, LIP, rust, ,你好 " -> ["rust", "lip", "你好"]。
fn clean(tag: string) -> string {
    return string.lower(string.trim(tag))
}

fn nonempty(tag: string) -> bool {
    return len(tag) > 0
}

flow Tags(text: string) -> object {
    parts = string.split(text, ",")
    cleaned = list.map(parts, clean)
    tags = list.unique(list.filter(cleaned, nonempty))
    return {tags: tags, count: len(tags), label: string.join(tags, " / ")}
}
```

```bash
lipc run examples/strings.lip ' Rust, LIP, rust, ,你好 '
# {"count":3,"label":"rust / lip / 你好","tags":["rust","lip","你好"]}
lipc run examples/strings.lip ''
# {"count":0,"label":"","tags":[]}
```

clean 命名“清理单项”，nonempty 命名保留条件；Flow 组合 split → map → filter
→ unique → join。callback 可传 fn 名，例如 list.map(parts,clean)，也可写
`list.map(parts, fn(text) { string.trim(text) })`。内联参数默认 any，也可明确标注类型。
具名和内联 fn 都只写一个结果表达式，return 可省略。

fn 用一个返回表达式描述纯变换，可以组合其他纯 fn、标准库和 fail。中间步骤
可拆成小函数，或放在 Flow 绑定中；例如 normalize(string.trim(tag)) 组合清理
与规范化。文件和网络操作写在 Flow 中，便于图调度，fn 内部保持纯表达式。

## 6. 遍历与聚合：range、Map、for、fold、scan

[05_range.lip](../examples/tutorial/05_range.lip)：

<!-- example: examples/tutorial/05_range.lip -->
```lip
fn add(total: number, value: number) -> number {
    return total + value
}

flow Squares(stop: number) -> object {
    numbers = range(0, stop)
    squares = [n * n for n in numbers]
    total = fold(squares, 0, add)
    running = list.scan(squares, 0, add)
    return {squares: squares, total: total, running: running}
}
```

```bash
lipc run examples/tutorial/05_range.lip 4
# {"running":[0,0,1,5,14],"squares":[0,1,4,9],"total":14}
lipc run examples/tutorial/05_range.lip 0
# {"running":[0],"squares":[],"total":0}
```

range(4) 与 range(0,4) 都是 0、1、2、3，不含终点。Map 每项求平方；fold 从 seed=0 起，
依次加 0、1、4、9，得 14。`fold` 只回答“最后累计值是多少”；`list.scan` 保存初值
和每一步结果，所以两个开头的 0 分别是 seed 与处理首项后的值。空 fold 返回 seed，
空 scan 返回 [seed]。如果喜欢把所有列表操作放在同一个命名空间，也可写
`list.fold(squares, 0, add)`，两种写法完全相同。

range(5,0,-2) 是 [5,3,1]；方向不匹配为空。0 步长、小数和超量报错；
参数使用绝对值≤2^53−1 的可精确整数，最多生成 1,000,000 项。

Map 写作 `[expr for item in source]`，source 可直接是列表表达式，例如
`[x * x for x in range(4)]`，也可使用已有绑定。fn 内同样可以用纯推导式，
或 `list.map(range(4), fn(x) { x * x })`；筛选用 list.filter。

fold 接本地二参数 fn，分别是 accumulator 与当前元素；按顺序归约，并行
模式也不改变归约顺序。它可聚合字符串或对象；单纯求和优先 list.sum。
这个方向适合有界数据：变换用 Map、筛选用 filter、汇总用 fold。深树/分治
用递归；无界事件与大数据流由 Host 处理。

打印、外部调用和多语句操作使用语句式 for。例如 [for.lip](../examples/for.lip)：

<!-- example: examples/for.lip -->
```lip
# Statement loops execute operations without building a result list.
flow Each(stop: number) {
    for i in range(stop) {
        match i {
            2 => { continue },
            5 => { break },
            _ => {}
        }
        squared = i ** 2
        print(i, squared)
    }
    print("done")
}
```

`lipc run examples/for.lip 8` 依次打印 `0 0`、`1 1`、`3 9`、`4 16`、`done`。
continue 跳过当前项的剩余操作，break 结束最近一层循环；两者都可放在 match 分支中。
嵌套循环的 break/continue 只影响内层。source 求值一次，逐项等待整个循环体完成，
宿主取消运行上下文（CLI 的 Ctrl-C/超时，或 Go 的 `context`）或体内普通错误会停止后续操作，
空列表不执行体内语句。for 不收集结果，可直接写在顶层或 REPL。
每次迭代的绑定独立，循环变量遮蔽同名外层值，体内变量不能逃出；变量仍不可重新赋值。
如果确实需要持续运行，写无变量的 `for { ... }`；它不创建 `range` 列表，也不偷偷维护
一个会溢出的索引，直到 `break`、宿主取消运行上下文或普通错误才结束。循环体不使用 return/state，
返回写在循环之后；列表转换继续用推导式，累加用 fold。需要序号时应让 Host 提供有界
批次或事件编号，而不是依靠无限浮点数递增。

## 7. 查询、筛选、排序、去重

[06_lists.lip](../examples/tutorial/06_lists.lip)：

<!-- example: examples/tutorial/06_lists.lip -->
```lip
fn positive(x: number) -> bool { return x > 0 }
fn double(x: number) -> number { return x * 2 }

flow Lists(values: list) -> object {
    selected = list.filter(values, positive)
    doubled = list.map(selected, double)
    return {
        selected: selected,
        doubled: doubled,
        sorted: list.sort(list.unique(values)),
        total: list.sum(doubled),
        any_positive: list.any(values, positive),
        all_positive: list.all(values, positive),
        contains_zero: list.contains(values, 0),
        zero_count: list.count(values, 0),
        head: list.take(values, 2),
        tail: list.drop(values, 2)
    }
}
```

```bash
lipc run examples/tutorial/06_lists.lip '[3,0,-1,3,2]'
# {"all_positive":false,"any_positive":true,"contains_zero":true,"doubled":[6,6,4],"head":[3,0],"selected":[3,3,2],"sorted":[-1,0,2,3],"tail":[-1,3,2],"total":16,"zero_count":1}
```

filter 保留原元素，map 才变换。unique 保留首次出现顺序；再 sort 得升序。
`sort`/`list.sort` 按元素本身排序，`sort_by` 按一元 key fn 排序，`sort_with` 接收二元
比较器；三者都稳定。交互式代码可省略 `list.` 写 `sort_by(values, key)` 或
`sort_with(values, fn(a,b) { a <= b })`。`sort` 和 `sort_by` 都接受末尾的
`reverse=true`；比较器场景直接在 comparator 中决定顺序。

any 在首个 true 停止，all 在首个 false 停止；predicate 返回 bool。
空列表 any=false、all=true，表示没有反例。sum/product 空输入为 0/1，
first/last/min/max 需要非空，否则错误。

take(xs,2) 取头两项，drop(xs,2) 去头两项；负数处理尾部，例如
list.take([1,2,3],-2) 为 [2,3]。超长截断。append/prepend 添加项，concat
合并，reverse 倒序，slice 半开截取，repeat 重复值，riffle 在项间插分隔值。

再用 [] 和 [1,"bad"] 运行本例：空列表正常，后者在 positive 的参数检查失败。
list 只检查外层，不意味着元素都是数字。列表库不修改输入，输出容器新建，
嵌套值共享但视为只读。36 项操作及边界见 [LIST-LIBRARY.md](LIST-LIBRARY.md)。

## 8. 改变形状：窗口、转置、zip、笛卡尔积

[07_shapes.lip](../examples/tutorial/07_shapes.lip)：

<!-- example: examples/tutorial/07_shapes.lip -->
```lip
flow Shapes() -> object {
    values = [1, 2, 3, 4, 5]
    chunks = list.partition(values, 2)
    return {
        chunks: chunks,
        windows: list.partition(values, 3, 1),
        restored: list.flatten(chunks, 1),
        columns: list.transpose([[1, 2], [3, 4], [5, 6]]),
        pairs: list.zip(["a", "b"], [10, 20, 30]),
        indexed: list.enumerate(["a", "b"]),
        combined: list.concat([1], [2, 3]),
        product: list.cartesian([1, 2], ["a", "b"])
    }
}
```

```bash
lipc run examples/tutorial/07_shapes.lip
# {"chunks":[[1,2],[3,4],[5]],"columns":[[1,3,5],[2,4,6]],"combined":[1,2,3],"indexed":[[0,"a"],[1,"b"]],"pairs":[["a",10],["b",20]],"product":[[1,"a"],[1,"b"],[2,"a"],[2,"b"]],"restored":[1,2,3,4,5],"windows":[[1,2,3],[2,3,4],[3,4,5],[4,5],[5]]}
```

partition 的 size/step 为正整数，默认 step=size，不重叠切块，末尾短窗口
保留。step=1 得滑动窗口；若只需完整窗口，再 filter 检查 len(window)。
step>size 可产生间隔。flatten(chunks,1) 展开一层，恢复输入；省略 depth
则展开所有嵌套列表，对象/null 是叶子；depth=0 保留形状。

transpose 要求严格矩形，[[1,2],[3]] 报错，不补值、不截断。zip 按位置配对，
截到最短输入，因此 30 未出现在 pairs。enumerate 加 0 起始索引，cartesian
枚举组合，最右列表变化最快。乘积可能很大，先估算；库对新列表元素槽位
实行 1,000,000 上限，flatten 也有深度限制，不替代大型科学计算库。

## 9. 分组与聚合：group_by、split_by

[08_groups.lip](../examples/tutorial/08_groups.lip)：

<!-- example: examples/tutorial/08_groups.lip -->
```lip
fn department(row: object) -> string { return row.department }
fn amount(row: object) -> number { return row.amount }
fn summarize(group: object) -> object {
    return {department: group.key, total: list.sum(list.map(group.values, amount))}
}

flow Departments(rows: list) -> object {
    groups = list.group_by(rows, department)
    summary = list.map(groups, summarize)
    return {
        summary: summary,
        adjacent: list.split_by(rows, department),
        ranked: list.sort_by(rows, amount)
    }
}
```

```bash
lipc run examples/tutorial/08_groups.lip '[{"department":"A","amount":3},{"department":"B","amount":2},{"department":"A","amount":-1}]'
```

summary 是 [{"department":"A","total":2},{"department":"B","total":2}]。
分组结果每组是 {key,values}，key 为原始键，values 为该组输入项。summarize
读取 group.key，map 提取金额后 sum。分组键不是 object 字段名，因此可用
null、bool、列表、对象等有限 JSON 值，不必转字符串。

group_by 合并所有相同键，A 两项同组；split_by 只合并相邻相同键，A/B/A
仍为三段。group 用元素本身作为键。组按首次出现排序，组内保留输入顺序。
ranked 按金额升序为 A:-1、B:2、A:3，key fn 每项只执行一次。
练习：summarize 增加 count/average。group_by 不创建空组；若函数接受任意
组，就需要自己处理 values=[]。

## 10. 递归：终止分支与树

[09_recursion.lip](../examples/tutorial/09_recursion.lip)：

<!-- example: examples/tutorial/09_recursion.lip -->
```lip
fn factorial(n: number) -> number {
    return if n <= 1 { 1 } else { n * factorial(n - 1) }
}

fn tree_sum(node: object) -> number {
    return node.value + list.sum(list.map(node.children, tree_sum))
}

flow Recursion(n: number, tree: object) -> object {
    return {factorial: factorial(n), total: tree_sum(tree)}
}
```

```bash
lipc run examples/tutorial/09_recursion.lip 5 '{"value":1,"children":[{"value":2,"children":[]},{"value":3,"children":[]}]}'
# {"factorial":120,"total":6}
```

factorial 是教学算法，约定输入为小的非负整数。n≤1 是终止分支；number
不会自动证明整数/非负等业务域，必要时增加 if/fail。tree_sum 递归 map
children，再 sum；叶子 children=[]，空 sum=0。二叉树用 null 的完整例子
另见 [examples/tree.lip](../examples/tree.lip)。

直接/间接递归环中的每个 fn 必须显式 ->type，缺少则 check 失败。可识别的尾递归和
可累积递归会由编译器改成循环；其他递归最多保留 1024 层运行时保护。运行期间，CLI
收到 Ctrl-C/超时，或 Go 宿主取消传入的 context 时，递归会停止并返回取消错误；未转换
递归超过 1024 层时返回递归深度错误。列表遍历用 map/fold，树和分治可以使用递归；
实际上仍受内存、算术范围和宿主取消约束。

## 11. 综合例子：文本 → 部门报表

每行是“部门 金额”，Unicode 空白分隔，空行忽略；有效行必须恰好两项，
部门不含空白，每行由两项组成。
[11_report.lip](../examples/tutorial/11_report.lip)：

<!-- example: examples/tutorial/11_report.lip -->
```lip
# intent: 输入若干行文本；每行是“部门 金额”，忽略空行。
# accept: 同一部门合并金额，按部门首次出现顺序输出；无有效行输出空报告。
# error: 每个非空行必须恰有两项；金额必须是有限十进制数。
fn nonempty(line: string) -> bool { return len(string.trim(line)) > 0 }
fn parse_fields(fields: list) -> object {
    return if len(fields) == 2 {
        {department: fields[0], amount: string.parse_number(fields[1])}
    } else {
        fail("invalid row: expected department and amount")
    }
}
fn parse_row(line: string) -> object { return parse_fields(string.split_whitespace(line)) }
fn department(row: object) -> string { return row.department }
fn amount(row: object) -> number { return row.amount }
fn summarize(group: object) -> object {
    return {department: group.key, count: len(group.values), total: list.sum(list.map(group.values, amount))}
}

flow Report(text: string) -> object {
    lines = list.filter(string.lines(text), nonempty)
    rows = list.map(lines, parse_row)
    groups = list.group_by(rows, department)
    return {rows: len(rows), total: list.sum(list.map(rows, amount)), departments: list.map(groups, summarize)}
}
```

```bash
lipc run examples/tutorial/11_report.lip $'A 3\nB 2\n\nA -1\n'
# {"departments":[{"count":2,"department":"A","total":2},{"count":1,"department":"B","total":2}],"rows":3,"total":4}
lipc run examples/tutorial/11_report.lip $'  \n\n'
# {"departments":[],"rows":0,"total":0}
lipc run examples/tutorial/11_report.lip 'A nope'
# 失败：string.parse_number ... expected a finite decimal number ... "nope"
lipc run examples/tutorial/11_report.lip 'A 1 extra'
# 失败：fail: invalid row: expected department and amount
```

$'...' 是 Bash/zsh 的转义参数，含真实换行。LIP 不主动解释终端参数里的
字面 \n。Flow 先滤空行，再解析/分组；parse_row 调用 parse_fields 让切词
只做一次，符合 fn 单 return 规则。fail 防止多列被静默忽略；map 错误含
元素下标，便于回到输入定位。

反复验证时先构建，省去每次 run 的临时构建成本：

```bash
lipc build --output department-report examples/tutorial/11_report.lip
./department-report $'A 3\nB 2\nA -1\n'
./department-report ''
```

练习：加正金额筛选、金额降序排序、按两项切批。先确定排序原始 rows 还是
汇总后的 departments，再选择相应 key fn。

## 12. 图、自动等待、有限并行

Flow 引用产生数据依赖，报表中 lines → rows → groups → return 自动等待。
fn 表达式不展开独立节点，list.map 和嵌入表达式的推导式顺序求值；独立的
Map 推导式是 Runtime 动态展开节点。两种推导式都可直接使用 range 等纯列表表达式，
例如 `list.sum([x * x for x in range(1, 4)])` 得到 `14`。
已有 [fanout.lip](../examples/fanout.lip) 的 left/right 只依赖
input，可以并行，total 等待两者。Run 自动，RunSequential 顺序，
RunParallel(...,2) 最多同时调度两个节点；三者共用输入与结果契约。

```bash
lipc inspect examples/tutorial/11_report.lip
lipc run --trace report-trace.json examples/tutorial/11_report.lip $'A 3\nB 2\n'
```

inspect 是 lip.graph.v1：节点、依赖、gates、调用、类型与位置，不执行 Host。
trace 是 lip.trace.v1：Tick、node、status、reason、可选 error，状态如
Completed/Skipped/Cancelled，无耗时或局部变量快照。独立文件不污染结果；
执行失败也写出，编译/CLI 输入失败尚未执行，不写新轨迹。

Pure/ReadOnly Host 才可自动并行，外部写入形成顺序屏障。已声明的独立节点
不会因 return 的 if 未选它就被取消；要阻止外部操作执行，把操作放进 match。

## 13. match：字面量分支与返回值

[10_gate.lip](../examples/tutorial/10_gate.lip)：

<!-- example: examples/tutorial/10_gate.lip -->
```lip
flow Gated(input: number) -> number? {
    match input > 0 {
        true => {
            doubled = input * 2
            return doubled
        },
        false => {}
    }
}
```

```bash
lipc run examples/tutorial/10_gate.lip 3
# 6
lipc run --trace gate-trace.json examples/tutorial/10_gate.lip 0
# null
```

match 按整个值的字面量选择分支；当前不做列表、对象解构或字符串内容匹配。这里 false 分支为空，doubled/return 跳过，库得到 nil，
CLI 输出 null；因此输出写 number?。分支内绑定只在该分支可用，其他分支的外部操作也不会执行。
如果各分支都 return，可以使用非可选输出类型。执行中的错误向宿主返回。

match 也可直接产生值，支持数字、字符串、bool、null 字面量、`number`/`string`/`bool`/
`list`/`object` 类型分支和 `_` 默认分支；字符串字面量比较整个字符串。类型分支适合
`any` 输入，并会在分支内收窄匹配变量。列表和对象应使用索引/字段读取、`isEmpty`/`len`、布尔比较、
列表/字符串库函数和推导式组合条件。树和其他递归结构继续用纯递归函数表达；尾递归、
线性累积和部分整数分支递推会转成循环，不能识别的深度递归仍受 1024 层保护。需要
处理无限输入或极深数据时，应让 Go/Python Host 提供迭代式操作。
分支按顺序匹配，匹配值只计算一次；`模式 if 条件` 添加守卫。需要覆盖所有可能值，
bool 可列出 true/false，其他情况通常使用最后一个无守卫的 `_`。

同一结果适用于多个字面量时，可以在一个分支中用逗号列出它们；守卫和分支表达式只写一次：
值分支之间使用逗号，块分支可用换行或逗号；分号只用于块内的多条语句。

```lip
fn classify(value: number) -> string {
    match value {
        0, 1 => "small",
        _ => "other",
    }
}
```

类型分支可以处理动态输入；`_` 接收剩余类型和 null：

```lip
fn describe(value: any) -> string {
    match value {
        number => str(value),
        string => "String: " + value,
        null => "Nil",
        _ => "Other",
    }
}
```

```lip
flow Describe(mode: string) -> any {
    return match mode {
        "run" => 7,
        "off" => "停止",
        _ if len(mode) == 0 => null,
        _ => mode,
    }
}
```

match 不要求各分支结果类型一致；这里可以返回数字、字符串或 null。
返回类型由函数或 Flow 的边界负责：`-> any` 接受这些值，改成 `-> number` 时字符串分支会报错。

## 14. Vibe Coding：生成后如何验证与修复

intent/accept/error 注释保留意图，但不执行自然语言。改变需求时同时更新
代码、注释、验收样例。先检查：

```bash
lipc check --json examples/tutorial/11_report.lip
# schema=lip.diagnostics.v1，ok=true，diagnostics=[]（实际输出完整 JSON）
```

失败 JSON 有 code/message/severity、源位置、source_line、hints。位置从 1
起、列按 Unicode 字符；可能指向语句/声明，无法定位则省略。当前只报告首个
错误，不作多错误恢复。成功退出 0、源码/读文件失败 1、CLI 用法错误 2。

每次只改小步：生成完整程序 → check → 验正常/空/错误输入 → 对照需求。
把诊断、实际输入、期望结果一起交给 AI。修复后重跑所有样例，不为通过
检查把全部类型改 any、删除 fail 或悄悄丢弃数据。

| 错误 | 修复方向 |
| --- | --- |
| undefined or forward reference | 名字、定义顺序、match 分支作用域 |
| unknown string/list operation | 查实际目录，不猜别名、不回退外部操作 |
| expects string/number/list/bool | 核对契约和显式转换，运行验证动态元素 |
| callback ... local pure function | 传 fn 名或内联纯 fn，匹配参数数量与结果 |
| recursive ... explicit return type | 给递归环各 fn 标注结果 |
| must be pure | 在 Flow 中调用外部能力，把结果传给纯 fn/回调 |
| no return / 多 return | 一个结果出口；match 互斥分支可各自 return，空分支要求可选输出 |
| 字段缺失、非矩形、解析失败 | 修正动态输入或明确失败契约 |

完整机器诊断 code、可复制提示模板见 [VIBE-CODING.md](VIBE-CODING.md)。
本版不自动上传数据给 AI，无原生 IDE 集成、无损热重载或成功率保证。run
仍构建 Go；Tick 是依赖复用，不替换代码。

## 15. Go Host：显式接入外部能力

文件/网络/数据库或专门 parser 由 Go adapter 实现。LIP 文件头写
import host "load_profile"，可在 Flow 中绑定或组合调用；声明不安装或提供实现。
已有 [host_adapter/flow.lip](../examples/host_adapter/flow.lip) 读取两次文件。
使用库模式，让 Go 主程序注册能力：

```bash
lipc build --no-main --package hostflow \
  --output examples/host_adapter/flow/flow_gen.go examples/host_adapter/flow.lip
go run -buildvcs=false ./examples/host_adapter
```

完整 [main.go](../examples/host_adapter/main.go) 创建临时 JSON 文件、注册
ReadOnly 读取、执行并清理。结果 value=LIP-LIP calls=2，耗时随机器变化。
文件可变，不能为缓存而注册 Pure。

callback 是 func(context.Context,[]runtime.Value)runtime.Result，负责参数、
真实 IO、资源关闭和错误。成功 Ready(value)，失败 Failed(err)，异步 Await。
传入数据视为只读，不改共享列表/object。

| 注册 | 用途 | 调度与 Tick |
| --- | --- | --- |
| RegisterPure | 相同输入确定输出的无 IO 计算 | 可并行、依赖不变可复用 |
| RegisterReadOnly | 文件/网络等只读观察 | 可并行、每 Tick 重读 |
| Register | 外部改变，如写文件 | 顺序屏障、每 Tick 再执行 |

未知效果保守按外部写入。Host 内部状态由 adapter 自己保护；多实例可共享
Host。Runtime 不证明资源/并发安全，不回滚写入，事务由 adapter 实现。
需要无数据传递的排序时，宿主图可用 NodeSpec.After；下面是 **Go 片段**，
prepare 节点和 save callback 应由宿主提供：

```go
g.Add(runtime.NodeSpec{Name: "save", Op: "save", After: []string{"prepare"}, Eval: save})
```

这些能力通过 Go 宿主接入，图 API 和库入口详见 [COMPILER.md](COMPILER.md)。

## 16. 完整宿主例子：State、Retry、Feedback、Await

先把三个名字分开记：

| 写法 | 它解决的问题 | 直观想象 |
| --- | --- | --- |
| `state(initial)` | 跨多次 Tick 保存一个 Flow 值 | 一个由宿主持有的记事本 |
| `retry(call, n)` | 同一个调用失败时再试几次 | “这次没成功，再按原请求试一次” |
| `feedback(initial, step, verify, n)` | 候选结果不合格时修订后再检查 | “先提案 → 检查 → 修改 → 再检查” |

它们不是互相替代的循环：State 不会自动递增，Retry 不会修改输入，Feedback 也不会
无限运行；三个 `n` 都是明确的上限。

[session.lip](../examples/tutorial/session.lip)：

<!-- example: examples/tutorial/session.lip -->
```lip
import host "fetch"
import host "write_report"

fn start(input: number) -> number { return input }
fn revise(candidate: number) -> number { return candidate + 1 }
fn verify(candidate: number) -> bool { return candidate >= 3 }

flow Session(input: number, enabled: bool) -> object? {
    count = state(0)
    match enabled {
        true => {
            fetched = retry(fetch(input), 3)
            candidate = feedback(start(fetched), revise, verify, 3)
            report = {count: count, result: candidate}
            write_report(report)
            return report
        },
        false => {}
    }
}
```

完整 Go 主程序是 [host_demo/main.go](../examples/tutorial/host_demo/main.go)，
生成库在 [sessionflow/flow_gen.go](../examples/tutorial/sessionflow/flow_gen.go)：

```bash
lipc build --no-main --package sessionflow \
  --output examples/tutorial/sessionflow/flow_gen.go examples/tutorial/session.lip
go run -buildvcs=false ./examples/tutorial/host_demo
# [{"fetch_calls":2,"tick":1,"value":{"count":0,"result":3},"writes":1},{"fetch_calls":3,"tick":2,"value":{"count":2,"result":3},"writes":2},{"fetch_calls":3,"tick":3,"value":null,"writes":2}]
```

fetch 注册 ReadOnly，首次故意失败，后续通过 Await 返回输入。`retry(fetch(input), 3)`
表示“最多调用 fetch 三次”，第一次也算一次；它只重做这个调用，不会重做无关的
绑定。`feedback(start(fetched), revise, verify, 3)` 先得到一个候选，再调用 verify：
候选 1 不合格就 revise 成 2，候选 2 不合格再 revise 成 3，候选 3 合格后结束。
验证一旦成功，不再调用 revise；三次都失败则整个节点报错。

首次 Tick 初始化 count=0，fetch 第二次成功，反馈验证 1→2→3，写报告一次。
第二次前 SetState 只是安排下一次 Tick 使用 2，不会立即运行图；再次 Tick 后写次数
增至 2。第三次把 enabled 更新为 false，读/反馈/写/输出都跳过，返回 null，计数保持
2。这个例子没有真实网络或 AI；换成真实能力时由 adapter 负责超时、幂等写入和业务错误。

## 17. 持久实例、Tick 与缓存

Run 每次新建状态；跨调用持久化需 NewInstance 后重复 Tick。以下是上节
主程序的 **Go 片段**，完整文件逐次处理 err：

```go
instance, err := sessionflow.NewInstance(host, map[string]runtime.Value{
    "input": 1, "enabled": true,
})
value, trace, err := instance.Tick(ctx, nil)
err = instance.SetState("count", 2)
value, trace, err = instance.Tick(ctx, nil)
value, trace, err = instance.Tick(ctx, map[string]runtime.Value{"enabled": false})
```

NewInstance 要完整输入，Tick 接部分更新，nil 不更新。未知字段、错误类型、
错误 State 节点名失败。State 初值首次成功计算后保存，之后不重置；SetState
只更新值，不立即执行。State(name) 读值，TickCount() 读逻辑时钟。
纯依赖不变可复用，trace reason 提示 reused；外部读写重执行。State 不自动
自增，Tick 不是无限循环，宿主决定何时推进。取消 Tick 也推进时钟，非法
输入在进入 Tick 前失败。同实例执行/观察串行化，callback 不重入同实例，
避免等自己持有的锁。

生成图还会在最后一个消费者完成后解除中间值的执行引用，帮助 Go GC 较早回收。
输出、State 和有效纯缓存继续保留，ReadOnly/ExternalWrite 结果不缓存；
这不会修改返回对象或自动释放 Python 句柄。示例与成本见[值生命周期](VALUE-LIFETIMES.md)。

## 18. 有界策略、异步结果、取消

`retry(fetch(input), 3)` 最多三次，包含第一次；只接受正整数字面上限，不做退避、
错误分类或幂等性推断。写入操作重试前，宿主须确认重复执行不会产生重复扣款、重复
消息等副作用。取消会直接停止，不把取消当成一次可重试失败。

`feedback(initial_call, step, verify, 3)` 最多检查三次：先执行一次 initial，再检查；
只有 verify 返回 false 才执行 step。verify 必须返回 bool，到上限仍为 false 就报错。
step/verify 可以是纯 fn，也可以是声明并注册的 Host。它适合“候选→审核→修订”，
不适合代替没有退出条件的后台循环。

Await 的 channel 交付 Result。上节用缓冲 channel 立即交付，展示协议。
真实异步工作可用 goroutine，但应协作取消；下面是 **Go callback 片段**：

```go
func delayed(ctx context.Context, args []runtime.Value) runtime.Result {
    result := make(chan runtime.Result, 1)
    go func() {
        select {
        case <-ctx.Done():
            result <- runtime.Failed(ctx.Err())
        case <-time.After(10 * time.Millisecond):
            result <- runtime.Ready("ready")
        }
        close(result)
    }()
    return runtime.Await(result)
}
```

宿主可 context.WithTimeout，defer cancel，传给 Run/Tick。取消传播至等待、
Map、fold、本地 fn、标准库遍历和策略；网络/进程也应接收 context。
Runtime 无法安全抢占不合作的 Go callback。失败停止新工作并跳过下游，
已开始外部写入可能完成，不回滚。CLI run 转发中断，程序用 signal context。

## 19. Python：保持依赖显式

先看同一个 Flow 里自然组合三种能力：Go 读取文本，LIP 解析列表，Python
求平方根，再交给列表/字符串库汇总。普通标量、列表、对象返回后直接继续
计算，无需写通信或转换协议。

[mixed.lip](../examples/tutorial/mixed.lip)：

<!-- example: examples/tutorial/mixed.lip -->
```lip
import host "load_text"
import python "math"

fn parse(text: string) -> number { return string.parse_number(text) }
fn render(value: number) -> string { return str(value) }

flow Roots(path: string) -> object {
    text = load_text(path)
    values = list.map(string.split_whitespace(text), parse)
    roots = [math.sqrt(x) for x in values]
    return {roots: roots, total: list.sum(roots), label: string.join(list.map(roots, render), ", ")}
}
```

```bash
go run -buildvcs=false ./examples/tutorial/mixed_demo
# {"label":"1, 2, 3","roots":[1,2,3],"total":6}
```

只需要 Python 标准库 math，不需要科学包。完整
[mixed_demo/main.go](../examples/tutorial/mixed_demo/main.go) 在宿主注册 load_text、
启动 Worker、读样例文件。自动、顺序和有限并行都验证同一结果，空数据、
错误文本、Python 负数开根也有测试。

纯 string/list 与 Flow 中的外部调用都可嵌套组合；fn/集合回调保持纯计算。Go/Python
遵循同一规则，不把 IO 藏进纯 callback。普通返回值自动进入 LIP 值域；不能
直接表示的长期对象才使用下面的句柄 API。

科学计算交给已安装 Python 包：import python "numpy" 等声明启用 Worker，
普通 dotted operation 由 Python Host 解析，不执行 pip 或锁定环境。
完整 [examples/python/flow.lip](../examples/python/flow.lip) 调用 numpy.sum/mean、
pandas.Series、describe 方法：

```bash
lipc check examples/python/flow.lip
lipc run examples/python/flow.lip '[1,2,3,4,5]'
```

本例需要 Python、NumPy、Pandas。结果三项是 15、3、统计对象（count=5、
mean=3、min=1、max=5 等）。Worker 随 Python 依赖启用。

python.call(handle,method,args) 调对象方法，python.to_json 获取可序列化
数据，python.release 释放句柄。本例释放统计和 Series；Worker 重启旧句柄
失效。库模式 Go 创建/关闭 Worker，runtime.NewPythonHost(worker) 接入。
[examples/python/main.go](../examples/python/main.go) 展示 PutBlob 的 raw 数组
dtype/shape 与 ReleaseBlob；可 go run -buildvcs=false ./examples/python。
协议与配置见 [PYTHON-INTEGRATION.md](PYTHON-INTEGRATION.md)。

0.6.1 收紧丢信息的转换：Python NaN/Inf 错误，不默默变 null；字典键转字符串
碰撞时错误，不覆盖数据；set/frozenset 保留对象，不冒充有序 list。需要列表
时调用 builtins.sorted（声明 builtins）；强制 to_json set 会错误。普通有限
数值、文本、list/tuple、对象仍直接使用。

## 20. 构建、验证与练习

check/inspect 只分析；run 每次构建临时程序；build 产生可复用二进制或 Go：

```bash
lipc build --output hello examples/tutorial/01_hello.lip
./hello 小林
lipc build --emit-go --output hello.go examples/tutorial/01_hello.lip
go run hello.go 小林
```

`run/build` 使用编译器内置的 Runtime 在临时模块中编译；生成的核心二进制无需
Go 或仓库。`--emit-go` 的源码仍引用 `lipalpha/runtime`，以上 go run 在仓库
模块上下文执行。库模式用 `--no-main --package name`，宿主提供 adapter。
Host/Go 依赖的 standalone 入口
会提示使用库模式。import go 也是环境元数据，不自动导入 Go 函数。

完整本地验证：

```bash
bash scripts/verify-release.sh
```

执行 tests、race、vet、build、所有源例子的生成/编译和教程实际结果/失败
契约；Python 专项按环境明确执行或跳过。

练习按难度继续：

1. Tags 排序、取前三项、加 contains；明确空字符串行为。
2. Squares 负步长，scan 字符串累计；对照初值是否保留。
3. Shapes 筛完整滑窗，两次 transpose 验证恢复矩形。
4. Departments 增平均、按总金额排序，区分分组和连续分段。
5. Report 加业务条件和 fail，同时补正常/失败样例。
6. Host Demo 更新输入/State，观察复用，用取消 context 验证停止。

当前支持捕获不可变值的内联集合回调和有限集合的顺序 for/break/continue；可变循环变量、循环内 return、通用函数值/闭包、泛型 iterator、
代码热重载和自动包管理仍未提供。小核心
已可处理有界结构化任务，后续扩展依据真实程序的缺口和可验证失败契约。
