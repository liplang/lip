# LIP 0.6.3 值的计算生命周期与 Go GC

LIP 负责判断计算是否还会使用一个值，Go GC 负责判断对象是否仍可达并回收内存。
编译器生成的图默认在最后一个声明的消费者完成或跳过后，删除执行值表中的引用。
这相当于对 Runtime 持有的引用执行 `a = nil`，没有增加可变赋值语法，也没有手工释放对象。

## 一次执行中的中间值

[lifetimes.lip](../examples/lifetimes.lip)：

<!-- example: examples/lifetimes.lip -->
```lip
flow Lifetimes(count: number) -> object {
    values = range(count)
    size = len(values)
    total = fold(values, 0, fn(sum, value) { sum + value })
    print("computed", size, total)
    return {count: size, total: total}
}
```

`lipc run examples/lifetimes.lip 5` 先打印 `computed 5 10`，再输出
`{"count":5,"total":10}`。单次执行时，`values` 在 len 和 fold 都完成后可以从
值表移除；后续 print 和 return 只需要两个数值。GC 何时真正回收列表由 Go 决定。

计数覆盖 Deps 和 Gates，包括 match、集合回调捕获、Map 来源和元素表达式、for
捕获、Retry 和 Feedback。异步节点要完成整个 Await 链，才算消费完成；Map 要等
本次元素任务结束，for 要等整个循环完成或控制跳转结束。After 只消费节点状态。
未选中分支的节点被跳过时，也解除它们对依赖的需求。没有消费者的结果可以立即移出值表。

并行 worker 只复制本节点声明的依赖，避免无关的大对象被慢任务带入。
错误或取消后，已启动 worker 的迟到结果不放入无人接收的调度结果缓存。
不合作的 Host callback 仍可能持有自身引用，Runtime 不会强行终止它。

## 应当保留的引用

执行中的输出单独保存，返回后 Graph 不再额外持有该输出。返回值、Host 全局或缓存
中的引用继续有效；返回一个列表子视图也可能保留整个底层数组。Runtime 不修改对象、
不清空共享 slice 元素、不关闭外部资源。

Instance 的输入、State 和有效纯节点缓存仍有后续 Tick 用途，因此继续保留。
纯缓存比较依赖值的规则不变，快照可能合法保留大对象。重新计算前解除该节点的旧缓存，
而其他节点仍需要的有效缓存保留。ReadOnly、ExternalWrite 和未知效果不保存可复用缓存；
State 使用独立状态存储，不额外缓存 initializer 依赖。一次执行的引用释放不代替缓存淘汰策略。

Python 句柄、blob 文件、mmap、文件和连接具有各自的资源契约。
删除 Go 引用不会自动调用 `python.release`、`python.release_blob` 或 Close；这些仍按
adapter 的显式接口管理。REPL 保存的绑定也是后续单元的输入，不作为一次执行的临时值删除。

## 手写 Go 图的契约

`runtime.NewGraph()` 保留原有完整值表行为，兼容读取未声明依赖的 Go callback。
只有全部读取均已声明时，才使用：

```go
g := runtime.NewGraphWithOptions(runtime.GraphOptions{ReleaseIntermediates: true})
```

节点名称和输入名称必须唯一；Eval、Gate、Map、Retry、Feedback 读取的所有外层值
都必须列入 Deps/Gates，包括 MapSpec.Source。callback 只读值表，且不能在最终异步结果
完成后继续访问或保存整张值表；单个值可以按 Host 契约保存。编译器为全部生成图和
循环体满足这些条件，默认启用优化。

## 成本与验证

首次执行构建只含名称和整数索引的计划，后续 Run/Tick 复用；Add 节点会使计划失效。
每次执行复制整数计数，每条消费边完成时减一。并行值快照的大小与本节点依赖数相关。
实现不递归扫描对象大小，也不主动调用 Go GC。收益主要来自较大对象、长时间执行与
无关慢任务；标量本身没有可回收的底层对象。

复现命令：

```bash
go test ./runtime -run '^$' -bench 'Benchmark(ValueLifetime|ValueLifetimeFirstRun|IntermediateRetention)$' -benchmem -benchtime=500ms -count=3
```

2026-10-09，Go 1.27、linux/amd64 的样例结果：

| 场景 | 原值表 | 启用引用释放 |
| --- | --- | --- |
| 16 个已消费的 1 MiB 缓冲区，执行期间 GC 后额外存活堆 | 约 16 MiB | 约 4.3 KiB |
| 重复执行同一图的 64 节点标量链，顺序分配 | 33,128 B/op | 24,640 B/op |
| 重复执行同一图的 64 节点标量链，并行分配 | 约 214,000 B/op | 约 97,000 B/op |
| 新建并首次顺序执行 64 节点标量图，耗时 | 约 74 µs/op | 约 89 µs/op |
| 新建并首次顺序执行 64 节点标量图，分配 | 约 60,335 B/op | 约 64,910 B/op |

这些是具体工作负载的测量，不能保证所有程序更快。大缓冲基准为了观察存活堆显式
执行 GC，释放版耗时约 10.9 ms/op，原版约 5.9 ms/op；更低的存活堆也可能使 Go
更频繁地 GC。计划首次构建还有成本，生成的一次性 Flow 每次会创建新图。

Runtime 回归使用大对象 weak.Pointer 和受控 GC 验证真实可达性，覆盖最后消费者、
多消费者与嵌套 Await、无关 worker、分支跳过、乱序节点、null、返回/Host 别名、
失败与取消、失效缓存、State 和 Tick。生成程序回归另覆盖 Map、回调捕获、循环内
continue、match、Retry 与异步 Host；race 验证这些路径。测试里的 GC 调用只用于观察。
