# 从 Hello World 到并行与外部调用

这些文件全部是 LIP 源码。`#` 开始一行注释；每个示例都在代码旁解释语法。先在项目根目录运行 `make build`，然后按下面的顺序学习。所有示例都同时验证解释器和 Go 后端。

可以先运行 `./lipc learn`，边读讲解边填写和验证代码；也可以用 `./lipc repl` 随时试算表达式。REPL 的 `:run 文件路径` 用于执行完整的 Flow 示例。

## 先记住五条规则

1. `flow Name(...) -> Type { ... }` 是程序入口，结果由末尾的 `return` 给出。
2. `name = value` 创建不可变绑定。同一作用域不能给这个名字重复赋值。
3. `fn` 是纯函数；函数体和值块的末尾表达式是结果。函数尾部可以加 `return`，这些示例通常省略它；Flow 则明确写末尾的 `return`。
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

`**` 是幂，右结合：`2 ** 3 ** 2 = 512`。一元负号在幂之后：`-3 ** 2 = -9`，对负数整体求平方写 `(-3) ** 2 = 9`；负指数写 `2 ** -3 = 0.125`。`*/` 以右边为底：`8 */ 2 = 3`，它与乘除同级、左结合。

`number` 不分整数和小数类型，`3` 与 `3.0` 相同，JSON 输出为 `3`。对数的真数和底数都要大于 0，底数不能为 1；负数底数只接受整数指数。幂和对数是固定算法的近似结果；溢出或定义域错误会报 `E-NUMBER`。

`learn -lesson power` 与 `learn -lesson logarithm` 提供这两项练习。REPL 和 learn 都可用左右方向键修改输入，用上下方向键取回历史，Tab 补全、Ctrl-R 搜索历史；完整快捷键见 [README](../README.md#交互计算和学习)。

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
| [refine](refine.lip) | 无 source 的 carry，迭代改进直到阈值 | `rounds=4, score=0.8` |
| [evidence](evidence.lip) | 并行证据汇聚、完整报告 | `missing=["b","c"]` |

```sh
./lipc run examples/advanced/13_tail_calls.lip
./lipc run -parallel 4 examples/advanced/14_parallel_claim.lip
./lipc run -input '{"name":"Ada","scores":[0.6,0.9]}' examples/advanced/15_inputs.lip
./lipc run -parallel 4 examples/advanced/16_snapshots.lip
```

live snapshot 的观察差异是语义的一部分。需要最终完整结论时直接读取 Claim，不要用即时观察猜测所有任务已经完成。`fail(...)` 是不可捕获的程序错误；可恢复业务失败需要显式的 `{ok:false,...}` 数据。

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

LIP 源码只描述业务程序。Go 操作的注册、schema 和 bytes 转换集中在 [适配器与教学 Host](../script/adapters.go) 的 `DemoHost` 中，独立于 LIP 示例。import alias 只能用于 operation 名称，例如 `effect stats.mean(xs)`；不是任意 Python 对象访问。

## 同一个例子编译执行

```sh
make compile SOURCE=examples/advanced/13_tail_calls.lip OUTPUT=.build/tail
./.build/tail
make compile SOURCE=examples/ecosystem/17_go_python.lip HOST=demo OUTPUT=.build/interop
./.build/interop
```

生成程序不再接收 `.lip` 文件，它已经包含编译后的计算和调度信息，直接接受 `-input` / `-parallel` 等运行参数。

`make examples` 会输出各个示例的验收结果；`make clean` 删除编译产物与缓存。
