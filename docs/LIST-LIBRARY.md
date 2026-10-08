# list 纯函数标准库（0.6）

本库提供 34 个纯函数，可直接组合于 Flow、fn、if 和 Map 元素。
callback 使用本地纯 fn 名或内联 `fn(x) { expression }`。
编译器与 Runtime 共用一个签名目录；增加函数必须同时补契约与语义测试。

设计参考 Mathematica 的 Join、Take/Drop、Flatten、Transpose、Partition、
GatherBy/GroupBy、SplitBy、FoldList、Tuples 等组合能力。命名采用小写与下划线，
索引从 0 开始。

## 构造、选择与合并

| 调用 | 规则 / 示例 |
| --- | --- |
| `list.concat(a, b, ...)` | 按参数顺序连接；零参数返回 [] |
| `list.append(xs, value)` | 添加到尾部 |
| `list.prepend(xs, value)` | 添加到头部 |
| `list.repeat(value, n)` | 非负整数次数重复；0 返回 [] |
| `list.riffle(xs, separator)` | 元素之间插入 separator；不在两端插入 |
| `list.reverse(xs)` | 倒序 |
| `list.take(xs, n)` | n≥0 取头部，n<0 取尾部；超长截到边界 |
| `list.drop(xs, n)` | n≥0 去头部，n<0 去尾部；超长截到边界 |
| `list.slice(xs, start, end)` | 半开区间，负索引从尾部计数，越界截断，end≤start 返回 [] |
| `list.first(xs)` / `list.last(xs)` | 返回头/尾元素；空列表报错 |

这些操作都返回新列表，不原地改变输入。嵌套对象和列表仍共享只读值，不深拷贝。
取单个索引继续使用 `xs[index]`；和 slice 不同，单索引越界是错误。

## 形状与组合

| 调用 | 规则 / 示例 |
| --- | --- |
| `list.flatten(xs[, depth])` | 默认展开全部嵌套列表；depth≥0，0 保留形状，1 展开一层；对象和 null 是叶子 |
| `list.partition(xs, size[, step])` | 按正整数 size 切窗口，默认 step=size；保留末尾短窗口，step 可重叠或产生间隔 |
| `list.transpose(rows)` | 严格二维矩形列表；不补值，不截断；[] 与全空行返回 []，非矩形报错 |
| `list.zip(a, b, ...)` | 按位置组合，截到最短输入；零参数返回 [] |
| `list.enumerate(xs)` | `[[0, xs[0]], [1, xs[1]], ...]` |
| `list.cartesian(a, b, ...)` | 笛卡尔积，最右输入变化最快；任一输入空则 []，零参数返回 `[[]]` |

```lip
flow Shapes() -> object {
    return {
        windows: list.partition([1, 2, 3], 2, 1),
        columns: list.transpose([[1, 2], [3, 4]]),
        pairs: list.cartesian([1, 2], ["a", "b"])
    }
}
```

## 查询、排序与聚合

| 调用 | 规则 |
| --- | --- |
| `list.contains(xs, value)` | 按 LIP 相等语义查询，命中即停止 |
| `list.count(xs, value)` | 相等元素的数量 |
| `list.unique(xs)` | 保留首次出现顺序；比较包括 list/object，不按显示字符串去重 |
| `list.sort(xs)` | 全部数字或全部字符串；升序、稳定，不接受混合键或隐式转换 |
| `list.sum(xs)` / `list.product(xs)` | 只接受有限数字；空输入分别为 0 / 1，非有限结果报错 |
| `list.min(xs)` / `list.max(xs)` | 同类数字/字符串；空输入报错 |
| `list.group(xs)` | 按元素本身分组，首次出现的组在前，组内保持输入顺序 |

所有分组操作返回 `[{key: 原键, values: [原元素, ...]}, ...]`，键可以是数字、
bool、null、string、list 或 object，不转成字符串对象键。unique/group 的键必须
是有限 JSON 数据；Go 互操作的深相等规则与 `==` 一致。

## 纯回调操作

callback 可用本文件的纯函数名，或直接写 `fn(x) { x * x }`。具名函数传名称，
不写 `square()`；Host/Python 操作和动态变量不能当回调。数据参数可嵌套纯表达式，
例如 `list.map(range(5), square)`。一般接受一个元素；scan/fold 的 reducer 接受累计值和元素。
内联参数省略类型时为 any，也可写 `fn(x: number) -> number { return x * x }`；
单表达式前的 return 可省略，具名 fn 的单表达式体也采用相同规则。
内联回调可读取外层不可变绑定，这些捕获会计入图依赖和缓存判断；参数遮蔽同名
外层值，不泄漏到外层。回调不接受外部工作、print 或 State/Retry/Feedback。
静态检查参数数目和已知返回类型；动态元素由 fn 参数检查，错误指出元素索引。

| 调用 | 规则 |
| --- | --- |
| `list.map(xs, fn)` | 顺序变换，输出保持输入顺序；可以在 fn 中组合 |
| `list.filter(xs, predicate)` | predicate 必须返回 bool，保留 true 的原元素 |
| `list.any(xs, predicate)` | 首个 true 停止；空输入 false |
| `list.all(xs, predicate)` | 首个 false 停止；空输入 true |
| `list.sort_by(xs, key_fn)` | 每个键恰好计算一次；键为同类数字/字符串，稳定排序 |
| `list.group_by(xs, key_fn)` | 按纯函数键合并所有相同组，保持首次组顺序和组内顺序 |
| `list.split_by(xs, key_fn)` | 只合并相邻的相同键；不把非相邻的组再合并 |
| `list.scan(xs, seed, reducer)` | 返回 seed 及每步累计值；类似 FoldList，空输入 `[seed]` |

```lip
fn positive(x: number) -> bool { return x > 0 }
fn add(total: number, x: number) -> number { return total + x }

flow Groups() -> object {
    values = [-1, 2, 3, -4, 5]
    return {
        groups: list.group_by(values, positive),
        runs: list.split_by(values, positive),
        accepted: list.filter(values, positive),
        totals: list.scan(values, 0, add)
    }
}
```

无需为短变换另写函数或中间列表：

```lip
factor = 3
print(list.map(range(4), fn(x) { x * factor })) // [0,3,6,9]
print(list.sum(list.map(list.filter(range(5), fn(x) { x > 1 }), fn(x) { x * x }))) // 29
print(fold(range(5), 0, fn(total, x) { total + x })) // 10
print(list.scan(range(4), 0, fn(total, x) { total + x })) // [0,0,1,3,6]
```

`group_by`、`split_by`、`sort_by`、`any`、`all` 使用相同的内联写法。
predicate 必须返回 bool；sort_by 的键必须是同类 number/string，键每项只计算一次。

独立的 Map 推导式是 Runtime 动态展开节点，允许有界并行；嵌入参数、fn 或
其他推导式的 Map 与 `list.map` 都顺序求值。它们可直接使用 range 等纯列表表达式。
fold 只返回最终累计值，scan 返回累计历史。没有隐含线程或
独立缓存：整个表达式的依赖与纯性决定所在节点是否可复用。

## 有界执行与错误

单个操作输入列表长度不超过 1,000,000，新生成的列表元素槽位合计也不超过
1,000,000（zip/partition/transpose/cartesian 的外层和新内层都计算）。复用输入
嵌套值不再次计数；这限制新分配的列表结构，不是整份输入的字节配额。flatten
额外限制嵌套深度 256，循环的 Go 宿主列表会失败，不无限展开。

操作在遍历/调用边界检查 Context 取消，callback 失败或取消不返回部分列表。
形状、数量、整数步长和 predicate 返回值错误都有明确失败；sum/product 的溢出
与普通数字运算采用同一错误规则。排序期间也观察取消。

真实记录的分组、过滤、排序示例见 [examples/lists.lip](../examples/lists.lip)。
完成[安装](QUICKSTART.md#安装)后，在仓库根目录运行：

```bash
lipc run examples/lists.lip '[{"department":"A","amount":3},{"department":"B","amount":2},{"department":"A","amount":-1}]'
```
