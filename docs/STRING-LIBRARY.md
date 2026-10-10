# LIP 0.6.4 字符串库

`string.*` 提供 19 个固定纯操作，可直接用于 Flow、本地 fn、if 分支、Map
元素和 list callback。编译器与 Runtime 共用签名目录，生成代码直接调用运行库。
显式导入同名外部命名空间时，调用归属于声明的后端；使用 as 可让外部库与核心 string.* 并存。

本文件是 [0.6.4 规范](ALPHA-0.6-SPEC.md) 的组成部分。循序渐进的完整程序见
[教程](TUTORIAL.md)，标签清洗见 [examples/strings.lip](../examples/strings.lip)。

命名按数据类型分组：字符串操作保留在 `string.*`，避免与 `list.contains`、`list.count`
等同名操作混淆；跨类型的通用能力才使用裸名，例如 `len`、`isEmpty` 和 `isNotEmpty`。
因此不会额外提供裸 `contains`、`split` 或 `slice`，调用处一眼就能看出输入类型。

## 字符与共同规则

- 字符位置按 Unicode scalar value（码点）计数，从 0 开始，与 `len` 和字符串
  索引一致。`"你好🌱"` 长度为 3，`string.find("你好🌱", "🌱")` 为 2。
- 组合字符不自动合并：`"e\u0301"` 长度为 2。库不执行 Unicode normalization、
  grapheme 分割、locale 处理或正则匹配。大小写采用当前 Go 1.27 的 Unicode
  简单字符映射；例如 `string.upper("ß")` 不展开成 `"SS"`。
- 每个字符串输入与字符串结果最多 **16 MiB（16,777,216 UTF-8 字节）**；
  必须是有效 UTF-8。join 逐项检查，split/lines/chars 的列表结果最多
  **1,000,000 项**。扩张操作在分配结果前或逐步构造时检查上限。
- string 边界、len、字符索引、字符串 +/* 遵循相同 UTF-8/字节限制，不因
  更换写法绕过上限。Map/fold 的源列表也限制为 1,000,000 项。
- 参数类型、数量和已知返回类型在 check 时验证。`any`、join 的动态列表元素、
  整数值约束在运行时检查；错误含操作名，join 错误还含从 0 开始的元素下标。
- 不改写输入。执行入口与遍历过程检查取消；单次标准字符串搜索不会被中途
  抢占，完成后再检查取消。
- 以下示例是表达式；放进 `flow Example() -> any { return ... }` 可运行。

## 清理与大小写

| 签名 | 语义 | 示例结果 |
| --- | --- | --- |
| `string.trim(s: string) -> string` | 去两端 Unicode 空白 | `string.trim("\t你好　")` → `"你好"` |
| `string.trim_start(s: string) -> string` | 只去开头空白 | `string.trim_start("  x  ")` → `"x  "` |
| `string.trim_end(s: string) -> string` | 只去末尾空白 | `string.trim_end("  x  ")` → `"  x"` |
| `string.lower(s: string) -> string` | Unicode 简单小写映射 | `string.lower("ÄBC")` → `"äbc"` |
| `string.upper(s: string) -> string` | Unicode 简单大写映射 | `string.upper("äbc")` → `"ÄBC"` |

这些操作对空字符串返回空字符串；不压缩字符串内部空白。需要规范化词间空白，
组合 `string.join(string.split_whitespace(s), " ")`。

## 分割与合并

| 签名 | 语义 | 示例结果 |
| --- | --- | --- |
| `string.split(s: string, separator: string) -> list` | 按非空字面分隔符切分，保留所有空项 | `string.split("a,,b,", ",")` → `["a", "", "b", ""]` |
| `string.split_whitespace(s: string) -> list` | 按连续 Unicode 空白切词，忽略空项 | `string.split_whitespace(" a　 b\n")` → `["a", "b"]` |
| `string.lines(s: string) -> list` | 识别 LF 与 CRLF，去换行符，不保留最后的终止空项 | `string.lines("a\r\nb\n")` → `["a", "b"]` |
| `string.chars(s: string) -> list` | 每个 Unicode 字符成为一个字符串 | `string.chars("你好🌱")` → `["你", "好", "🌱"]` |
| `string.join(parts: list, separator: string) -> string` | 按顺序连接字符串项，无隐式转换 | `string.join(["a", "b"], " / ")` → `"a / b"` |

空输入：`split("", ",")` 是 `[""]`；split_whitespace、lines、chars 是 `[]`；
join 的空列表是 `""`。`lines("\n\n")` 是 `["", ""]`；独立的 `\r` 保留为内容。
`split(s, "")` 报错，字符分割明确使用 chars。join 不接受数字/null/对象项，
需要转换时先 `list.map(parts, stringify)`，其中 `fn stringify(x: any) -> string
{ return str(x) }`。

split 是字面文本切分，不理解 CSV 的引号、转义和嵌套结构。CSV/JSON 等格式
应使用明确的 Host parser。

## 查询、截取与构造

| 签名 | 语义 | 示例结果 |
| --- | --- | --- |
| `string.contains(s: string, pattern: string) -> bool` | 是否包含字面子串 | `string.contains("你好", "好")` → `true` |
| `string.starts_with(s: string, prefix: string) -> bool` | 字面前缀 | `string.starts_with("lip.txt", "lip")` → `true` |
| `string.ends_with(s: string, suffix: string) -> bool` | 字面后缀 | `string.ends_with("lip.txt", ".txt")` → `true` |
| `string.find(s: string, pattern: string) -> number` | 首次出现的字符下标；未找到为 -1 | `string.find("你好🌱", "🌱")` → `2` |
| `string.count(s: string, pattern: string) -> number` | 从左到右统计非重叠命中；pattern 非空 | `string.count("aaaaa", "aa")` → `2` |
| `string.slice(s: string, start: number, end: number) -> string` | 字符半开区间；整数；负数从末尾算，越界截断 | `string.slice("你好🌱", -2, -1)` → `"好"` |
| `string.repeat(s: string, count: number) -> string` | 非负整数次数重复；受字节上限约束 | `string.repeat("🌱", 3)` → `"🌱🌱🌱"` |
| `string.replace(s: string, old: string, replacement: string[, count: number]) -> string` | old 非空；默认替换全部非重叠命中；可限制非负整数次数 | `string.replace("a-a-a", "a", "b", 2)` → `"b-b-a"` |

contains/starts_with/ends_with 的空 pattern 总为 true；find 的空 pattern 为 0。
count/replace 的空 pattern 报错，避免隐式插入和特殊计数规则。replace 的 count=0
原样返回。repeat 的 count=0 或空文本返回 `""`，负数/小数报错。slice 的
end≤start 返回 `""`，与 list.slice 一致；直接 `s[i]` 越界仍然是错误。

## 显式数值解析

`string.parse_number(s: string) -> number` 接受有限十进制数，可有符号、小数和
十进制指数。`"-1.25e2"` → -125，`"+.5"` → 0.5。

前后空白、空串、`NaN`、`Inf`、十六进制、下划线和超出浮点范围的值报错。
需要清理时明确写 `string.parse_number(string.trim(s))`。数值语义与 CLI number
输入一致；它不提供精确十进制或大整数类型，金融精度需要适配器。

`str(number)` 把数值转换为显示文本；object/list 的结构化输出由程序入口
编码为 JSON。
