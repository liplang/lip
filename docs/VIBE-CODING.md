# 用 LIP 0.6.3 完成需求 → 生成 → 检查 → 验证 → 修改

LIP 的编写流程是需求 → 生成 → 检查 → 验证 → 修改。用标准库组合数据处理，
用注释表达意图，再通过可复现输入输出与结构化诊断核对结果。

以下命令在仓库根目录运行，安装位置跟随现有 Go 配置。文中的 `lipc` 可使用
PATH 中的命令名或实际安装路径，见[快速入门](QUICKSTART.md#安装)：

```bash
go install ./cmd/lipc
lipc version
```

## 可交给 AI 的编写约定

将下面这段和需求一起交给编程工具，并让它阅读
[教程](TUTORIAL.md)、[列表库](LIST-LIBRARY.md)、[字符串库](STRING-LIBRARY.md)：

```text
为 LIP Alpha 0.6.3 写一个完整 .lip 文件。
采用 Rust 风格块式 if；建议 snake_case 命名与显式结果类型以便审阅。
这些是写作建议；语言允许其他标识符风格、非递归 fn 的结果类型推导和内联回调的参数类型省略。
Flow/具名 fn 的参数、有返回值 Flow 的输出，以及递归 fn 的结果类型必须声明。
每个文件一个入口：显式 Flow 或一组顶层语句。有返回值时声明输出类型，用一个 return 或 match 的互斥分支给出结果；
只做打印等操作时可以省略整个 Flow 外壳。fn 用单个返回表达式描述纯变换。
执行语句用换行分隔，同一行的多条语句必须用分号分隔。
绑定不可变，先定义后使用。操作名称和签名参照当前标准库文档。
纯数据处理使用 string.*、list.*、range、fold 或本地纯 fn。
Map 推导式、fold/list.* 可组合列表表达式，无需先绑定 source。Flow 中的 source、
元素和调用参数可含外部操作；纯 fn/集合回调仍保持纯计算。
推导式可嵌套在参数、fn 和条件表达式中；独立 Map 有界并行，嵌套推导式顺序求值。
callback 传本文件的纯 fn 名或内联 fn(x) { expression }；内联参数默认 any，可显式标注。
单表达式 fn 的 return 可省略。内联捕获是图依赖，回调不得隐藏外部调用。
条件选值用 if 或 match 表达式，多语句分支用 match 语句。
外部能力用 import 声明，可直接组合于 Flow 表达式；Host 由 Go 注册。
Python、Host、Go 都支持 as 别名；遇到导入重名或后端歧义用不同别名消除。
顺序打印或外部工作用 for 遍历有限列表，break/continue 控制最近循环。
列表转换用推导式，聚合用 fold；循环绑定仍不可变，return/state 留在循环外。
数字支持 + - * / // % ** */，行注释写 #；// 向下取整，x */ base 是对数。
用 # intent: 写意图，# accept: 写可复现输入/期望，# error: 写错误契约。
先执行 lipc check --json，再验证正常、空输入与错误输入。
根据诊断修正表达式和类型，并再次运行原有验收样例。
```

这里的“明确标注结果”是项目写作约定；非递归 fn 的结果也可以由编译器推断。
`any` 用于动态边界；list/object 静态检查外层形状，动态元素在执行时检查。

## 一份可验收的需求

比“清理一下标签”更容易检查的描述是：

```text
输入是逗号分隔的字符串；去每项两端 Unicode 空白；转成小写。
丢弃空项，去重，保留首次出现顺序。
输出 {tags, count, label}，label 用 " / " 连接。
例子：" Rust, LIP, rust, ,你好 "
期望：{"tags":["rust","lip","你好"],"count":3,"label":"rust / lip / 你好"}
空字符串应输出空 tags、0、空 label。
```

实现已保存在 [examples/strings.lip](../examples/strings.lip)。验证：

```bash
lipc check --json examples/strings.lip
lipc run examples/strings.lip ' Rust, LIP, rust, ,你好 '
# {"count":3,"label":"rust / lip / 你好","tags":["rust","lip","你好"]}
lipc run examples/strings.lip ''
# {"count":0,"label":"","tags":[]}
```

意图注释使用 `#`，紧贴 fn/Flow 放置，
需求变化时同时更新代码、注释和验收样例；不会产生隐藏的语言状态。

## 结构化检查与修复

`lipc check --json file.lip` 向 stdout 输出一个 `lip.diagnostics.v1` 对象。成功
退出 0，`diagnostics` 为 `[]`；读文件或编译失败退出 1，保留第一个诊断。
CLI 参数用法错误退出 2，向 stderr 打印用法错误，不冒充源码诊断。

例如文件内容是：

<!-- lip-check: LIP_TYPE_ERROR -->
```lip
flow Bad() -> string {
    return string.trim(1)
}
```

失败结果含这些字段（message 的具体英语文字不作为兼容性 API）：

<!-- lip-diagnostic: previous -->
```json
{
  "schema": "lip.diagnostics.v1",
  "ok": false,
  "file": "bad.lip",
  "diagnostics": [{
    "code": "LIP_TYPE_ERROR",
    "severity": "error",
    "message": "2:5: string.trim argument 1 expects string, got number",
    "line": 2,
    "column": 5,
    "source_line": "    return string.trim(1)",
    "hints": [
      "Make the indicated argument match its parameter type; changing a different argument or the output type does not fix this call."
    ]
  }]
}
```

AI 应先确定意图：这里需要修正输入类型，还是确实需要 `str(1)`？hints 是修复
指导，不是自动应用的代码编辑。不会为了凑齐三条建议而生成无依据的修改。

| code | 下一步 |
| --- | --- |
| `LIP_IO_ERROR` | 核对路径和读取权限 |
| `LIP_LEX_ERROR` / `LIP_SYNTAX_ERROR` | 核对字符串转义、括号、参数与输出声明 |
| `LIP_NAME_ERROR` | 核对拼写、定义顺序和 match 作用域 |
| `LIP_UNKNOWN_OPERATION` | 查标准库目录，使用实际支持的名称 |
| `LIP_DEPENDENCY_ERROR` | 明确真实外部操作及其 import/adapter |
| `LIP_TYPE_ERROR` | 核对实际类型与显式转换 |
| `LIP_ARGUMENT_ERROR` | 核对调用签名与实际参数数量，或控制操作的参数形式 |
| `LIP_CALLBACK_ERROR` | 传本地 fn 名或内联纯 fn，匹配参数数量、返回类型与 accumulator 类型 |
| `LIP_EFFECT_ERROR` | 在 Flow 中调用外部能力，再把结果传给纯 fn/回调 |
| `LIP_RECURSION_ERROR` | 给递归环的 fn 标注结果类型并提供终止分支 |
| `LIP_LOOP_ERROR` | 核对有限 list 来源、循环作用域及 break/continue，return/state 放在循环外 |
| `LIP_RETURN_ERROR` | 核对 Flow 输出声明、可选输出及 return；纯打印入口省略结果 |
| `LIP_CHECK_ERROR` | 按 message 修正其他契约错误，再检查 |

位置从 1 开始，column 按 Unicode 字符计数；部分语义错误指向声明或语句，
不保证指向最内层表达式。无法定位时省略 line/column/source_line。JSON 是对
现有第一错误诊断的稳定外层封装，尚未提供多错误恢复或自动补丁。

## 一次小迭代的工作顺序

1. 把需求拆成输入、输出、顺序和错误条件，写 2–4 个验收样例。
2. 生成最小完整程序，优先组合纯操作。先 check，再 run。
3. 运行空输入和动态错误值；check 通过不等于所有数据都有效。
4. 出错时把诊断、实际输入、期望结果一起交回 AI。运行失败可附 trace。
5. 修复后重跑既有样例，核对原需求的输入、输出和错误契约。

```bash
lipc inspect examples/tutorial/11_report.lip
lipc run --trace report-trace.json examples/tutorial/11_report.lip $'A 3\nB 2\nA -1\n'
lipc run --trace report-error.json examples/tutorial/11_report.lip 'A nope'
```

inspect 输出依赖图而不执行外部操作；trace 给出节点状态、Tick 和失败原因。
它不采集完整局部变量、值快照或自动上传上下文，是否把资料交给 AI 由使用者
及其现有编程工具决定。编译/CLI 输入失败尚未执行图，不产生新执行 trace。

## 本版做了什么，后续如何取舍

当前语法提供 15 个关键字（含语句式 for/break/continue）、
19 个字符串操作、34 个列表操作、显式 fail、JSON 检查结果、图/执行观察、
有输入输出验收的渐进教程。标准库函数名不等同于新增关键字。

重复验证不同输入时，可以先 build，再反复运行同一可执行文件。
Tick 复用未变化的纯依赖，由宿主推进输入与 State 更新。

后续改进以真实程序中的诊断修复步数、check/run 延迟和组合需求为依据。
