# 从 Hello World 到 Flow 组合、并行与外部调用

[English](README.en.md) | [简体中文](README.md)

这些可执行示例逐步展开 LIP 1.0 的同一计算模型：用纯函数计算值，用 Flow 组织依赖，用 Claim 汇聚事实，用 effect / perform 执行外部动作。`#` 开始行注释，每个文件都用英文注释在代码旁解释语法。先在项目根目录运行 `make build`；示例同时通过解释器和实际生成的 Go 程序验证。

第一次接触可运行 `./lipc learn`，跟随 18 课填写和验证代码；随时用 `./lipc repl` 试算表达式。想先看组合能力，可直接阅读 [26_flows](advanced/26_flows.lip)，再回到基础语法。REPL 的 `:run 文件路径` 执行完整 Flow；多入口示例带 `-entry`，输入用 `-input` 或 `-entry-input`。

REPL 支持表达式、语句与声明，Flow 可跨 cell 调用和用作管道阶段。输入完整脚本或前向声明时，使用 `:begin` / `:end` 作为一个 cell 提交；`-project DIR` 指定静态导入根目录。

## 先记住五条规则

1. `flow Name(...) -> Type { ... }` 声明可复用的图；只有一个根 Flow 时自动作为入口，多个时用 `-entry Name`。有值的 Flow 由末尾的 `return` 给出结果，void 没有返回值。
2. `name = value` 创建不可变绑定。同一作用域不能给这个名字重复赋值。
3. `fn` 是纯函数；函数体和值块的末尾表达式是结果。函数尾部可以加 `return`。Flow 不是函数值；子 Flow 与管道用于组合图，参数和成功结果必须是 Data。
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
| [04_containers](basics/04_containers.lip) | list/object、索引、has/keys/put/concat | 原对象 role 仍为 `reader` |
| [05_strings_numbers](basics/05_strings_numbers.lip) | Unicode、slice/split、数字解析、string 排序 | `score=0.8, middle="𐀀"` |
| [22_multiline_strings](basics/22_multiline_strings.lip) | 三引号、缩进、转义与物理换行 | HTML 与 prompt 均为普通 string |

`**` 是幂，右结合：`2 ** 3 ** 2 = 512`。一元负号在幂之后：`-3 ** 2 = -9`，对负数整体求平方写 `(-3) ** 2 = 9`；负指数写 `2 ** -3 = 0.125`。`*/` 以右边为底：`8 */ 2 = 3`，它与乘除同级、左结合。

`number` 不分整数和小数类型，`3` 与 `3.0` 相同，JSON 输出为 `3`。对数的真数和底数都要大于 0，底数不能为 1；负数底数只接受整数指数。幂和对数是固定算法的近似结果；溢出或定义域错误会报 `E-NUMBER`。

`learn -lesson power` 与 `learn -lesson logarithm` 提供这两项练习。REPL 和 learn 都可用左右方向键修改输入，用上下方向键取回历史，Tab 补全、Ctrl-R 搜索历史；完整快捷键见 [README](../README.md#交互计算和学习)。

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
| [12_carry](structures/12_carry.lip) | 有结果的迭代、next/break、source 耗尽 | `early=6, complete=6` |

普通 for 内的绑定每轮独立。需要携带上一轮结果时用 carry；需要收集独立贡献时用 Claim。普通循环的 `break` 与 carry 的 `break value` 各自作用于自己的构造。

## 高级计算与并发

| 文件 | 学习内容 | 运行方式或结果 |
| --- | --- | --- |
| [13_tail_calls](advanced/13_tail_calls.lip) | 十万次直接、互递归、间接尾调用 | `sum=5000050000, even=true, indirect=0` |
| [14_parallel_claim](advanced/14_parallel_claim.lip) | parallel、逻辑位置、事实投影 | `-parallel 4`；结果按输入顺序排列 |
| [15_inputs](advanced/15_inputs.lip) | 类型化 Flow 参数与 JSON 输入 | 使用下方带 `-input` 的命令 |
| [16_snapshots](advanced/16_snapshots.lip) | 即时观察与完整读取的区别 | 完整数为 3，即时观察数允许 0–3 |
| [21_business_failures](advanced/21_business_failures.lip) | 用 Data 表达失败、第三次失败停止 | `failures=3, visited=4` |
| [27_linear_model](advanced/27_linear_model.lip) | 中心化最小二乘、训练/预测 Flow、MSE、奇异输入 | -entry Main; slope=2, intercept=1, predictions=[9,11] |
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
./lipc run -entry Main -input '{"xs":[3,1,2]}' examples/advanced/26_flows.lip
# {"count":3,"values":[5,3,4]}
./lipc run -entry Adjust -entry-input '{"positional":[3],"named":{}}' -entry-output examples/advanced/26_flows.lip
# {"kind":"value","value":5}
```

`x |> Adjust` 先固定 x，再完整完成子 Flow；具名纯函数和核心函数也能作为阶段。Flow 显式实参须为纯 Data，需要先执行子调用时用绑定或管道。原生库函数与普通函数值使用普通调用。`OFFSET:number=2` 是静态字面量，`export` 使其可被其他模块读取。入口 JSON 的重复键、非法 Unicode、格式或参数不符均在运行前以 E-SCHEMA 拒绝。

将下面的 `reuse.lip` 放在项目根目录，便可复用同一示例，无需复制实现：

```lip
import lip "./examples/advanced/26_flows.lip" as adjust
flow Main(xs:list)->list {return [x |> adjust.Adjust for x in xs]}
```

`./lipc run -input '{"xs":[3,1,2]}' reuse.lip` 得到 `[5,3,4]`。模块只能访问 export；私有 `add` 不可访问。编译先冻结完整依赖和版本，运行与生成程序不会重新读取模块。`-project` 可显式指定项目根；所有声明都要合法，不能靠选择另一个入口绕过检查。

`learn -lesson entries`、`flows`、`modules` 分别练习入口、组合和模块；课程依赖已内置。REPL 中可输入 `:run -entry Main -input '{"xs":[3,1,2]}' examples/advanced/26_flows.lip`，文件运行独立于会话绑定。

## Go / Python 生态

下面的例子使用 `-host demo`，显式启用项目的教学合约。Python 例子需要 Python 3，未涉及额外 package 安装。

| 文件 | 学习内容 | 结果 |
| --- | --- | --- |
| [17_go_python](ecosystem/17_go_python.lip) | 在同一 Flow 中调用两个生态 | Python 均值为 2，Go 返回 SHA-256 字符串 |
| [18_proposals](ecosystem/18_proposals.lip) | proposal 绑定、执行与请求链 | `first="HELLO", second="HELLO, LIP"` |
| [19_parallel_read_write](ecosystem/19_parallel_read_write.lip) | 并行 ReadOnly、完整收集、顺序 Write | `["ADA","LIN","SAM"]`；日志写 stderr |
| [20_recorded_iteration](ecosystem/20_recorded_iteration.lip) | 记录结果进入下一轮外部请求 | `rounds=4, score=0.8` |

```sh
./lipc run -host demo examples/ecosystem/17_go_python.lip
./lipc run -host demo -parallel 4 examples/ecosystem/19_parallel_read_write.lip
./lipc run -host demo examples/ecosystem/20_recorded_iteration.lip
```

这些教学示例的显式操作合约、schema 和 bytes 编码集中在 [适配器与教学 Host](../script/adapters.go) 的 `DemoHost` 中。CLI 默认还提供通用 Go/Python 桥接，可以直接调用导入库的函数、读取模块属性和操作原生对象，例如 `import go "strings"; strings.ToUpper("hello")` 或 `import python "math"; math.sqrt(3)`。直接调用和属性访问会降为 `perform effect`；数组等原生对象以资源引用保留，使用 `native_data(value)` 显式取得普通数据快照。通用配置、转换规则和运行条件见 [README](../README.md#接入-go--python)。

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
