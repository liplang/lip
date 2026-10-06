# LIP Alpha 0.5 设计规范

这是一份 v0.5 的目标规范，用于把下一版必须遵守的完整程序契约、Python 边界和
迁移验收写清楚，不把尚未实现的语法伪装成现成功能。

## 1. 根本模型

LIP 文件本身是一个完整的 Flow 程序。它有明确的依赖头部、函数声明、唯一的
Flow 入口、输入参数、计算绑定和输出 `return`。编译器只能把源文件表达的程序
翻译成 Graph 和后端代码，不能从示例名称、测试期望、环境变量、命令行缺省值或
包列表推断数据，也不能替源程序补一个返回值。

“完整”不表示程序不需要输入。程序必须把输入边界写在 `flow (...)` 中；运行时
只接受这些声明的输入。调用方没有提供输入时是错误，输入类型不匹配时也是错误。

Alpha 0.5 的规范语义分成四层：

```text
LIP source → AST/name/type analysis → Graph IR → Runtime/Host execution
```

每层只消费上一层已经明确表达的内容。测试、示例和生成代码必须使用同一条路径，
不能通过旁路常量、隐式 Host 或专门的测试分支让结果“看起来正确”。

## 2. 文件结构与依赖声明

规范文件的顶层顺序是：依赖声明、零个或多个 `fn`、恰好一个 `flow`，然后结束。
`require` 是唯一的依赖声明关键字，使用单数形式：

```lip
require python "numpy>=1.26"
require python "pandas"
require go "github.com/acme/adapter"
require host "load_profile"

fn twice(x: number) {
    return x * 2
}

flow Scientific(values: any) {
    total = numpy.sum(values)
    average = numpy.mean(values)
    return [total, average]
}
```

`require` 的种类是 `python`、`go` 和 `host`：

- `python` 是 Python 导入根和可选版本范围的部署要求（例如 `sklearn` 是
  `scikit-learn` 的导入根）；它不安装包，也不把固定库名单写进编译器。Worker
  使用的 Python 环境负责真正导入模块。
- `go` 是宿主 Go module 的部署要求；LIP 不直接 import Go 包。承载生成包的
  Go 程序负责 import 包并注册 Host adapter。
- `host` 是必须由宿主注册的 operation 名称，可用于审查和启动前检查。

同一 kind/spec 不得重复。依赖声明是可查询元数据：`lipc check` 必须打印，生成
库必须暴露 `RequiredDependencies()`。编译器不得联网安装、修改解释器或猜测
版本。v0.5 不接受 `requires`、`import` 或其他同义关键字作为别名；错误应指出
应使用 `require`。

关键字表保持一套固定拼写和固定语义：`require`、`fn`、`flow`、`return`、`when`、
`if`、`then`、`else`、`for`、`in`、`true`、`false`。实现不得为了示例、后端或
历史文档再添加复数、缩写或同义别名；新能力先更新规范和语法表，再更新解析器、
生成器、示例和 conformance。

## 3. 输入、输出与绑定

Flow 参数是完整的外部输入契约。v0.5 要求边界参数写出类型；动态输入必须
明确写 `any`，不能靠省略类型获得隐式输入。Flow 返回值必须有显式输出声明，
目标语法为 `flow Name(args) -> Type { ... return value }`；条件 Flow 可以有
多个 `return`，但每条可达成功路径都必须有相容的输出类型。确实允许门控后无值
完成的 Flow 必须显式声明可选输出（目标形式为 `Type?`），不能让 `nil` 由运行时
猜测出来。

普通绑定使用 `name = expression`，绑定即声明，且每个名字只能绑定一次。名称必须
在引用前已经由 Flow 参数、同级较早绑定或当前函数参数声明；前向引用、重复绑定、
未定义名称以及从 `when` 作用域逃逸都必须在 `lipc check` 阶段失败。`when` 是
执行门控，不会把块内变量提升到外部；`if` 是值表达式，不创建隐藏节点。

`fn` 是局部、无状态的表达式函数，参数和返回类型必须能在编译期检查。未知的
外部 operation 仍通过 Host 边界调用，但调用名、参数数量、输入依赖和效果分类
不能被编译器偷偷改变。

## 4. CLI、生成代码与忠实翻译

独立入口按 Flow 参数声明顺序接收位置参数：

- `string` 原样传递；
- `number` 接受有限十进制数；
- `bool` 只接受 `true` 或 `false`；
- `any` 在命令行边界使用 JSON。

参数缺失、参数过多、解析失败和类型错误都必须非零退出，并显示用法。生成程序
不得读取 `LIP_INPUT`，不得注入 `World`、`1`、`false` 或其他样例值。`lipc run`、
`lipc build` 生成的可执行文件和直接 `go run` 生成源码必须使用相同的规则。

`lipc check` 只检查和诊断；`lipc build` 只翻译并调用 Go 工具链；`lipc run` 只
编译临时程序并运行。任何阶段都不得根据文件名、测试名称或预期输出改写 AST、
Graph、输入或结果。源程序错误就报源程序错误，并提供位置、名称和原因。

## 5. Python 与 Go 边界

Python 不是 LIP 语法中的一组内置库，而是通用 Host 能力：

```text
LIP dotted call → Go runtime.Host → resident Python Worker → installed module
```

`numpy.linalg.solve`、`networkx.*`、`sympy.*`、`statsmodels.*`、`QuantLib.*`、
`torch.*`、`jax.*`、`keras.*`、`cv2.*`、`transformers.*` 以及用户自己的包都走
同一条 dotted call 路径，不逐项修改编译器。`require python` 只说明环境需要什么；
模块 allow/deny 策略仍由 Worker 配置管理。

控制面保持进程隔离和可替换协议。默认 Worker 使用 JSONL、request ID、deadline、
取消、错误和重启语义；复杂对象以显式句柄留在 Worker，调用方通过
`python.call`、`python.to_json`、`python.release` 管理生命周期。大数组使用 P2
文件支持的只读 blob/mmap 基线，传输 dtype、shape、大小和 SHA-256 元数据；Arrow、
共享内存、Worker pool、socket/gRPC 只有基准证明必要时才进入后续阶段。

声明 Python 依赖的独立生成程序可以启动默认 Worker；库模式由 Go 宿主显式创建
Worker，并决定解释器、模块策略、数据目录和生命周期。Python 异常、Worker 崩溃、
超时和取消必须转换成 LIP Runtime 的错误/取消结果，不能变成隐式成功值。
声明 `host` 或 `go` 依赖的独立入口没有 adapter 注入点，必须明确提示使用库模式，
不能把未注册 operation 当成成功结果。

## 6. v0.5 验收门槛

以下测试必须成为 conformance，而不是只写在文档里：

1. `hello.lip` 不传参数失败，传 `Alice` 只输出 `Hello, Alice`；源文件不含
   `World` 时任何路径都不得输出 `World`。
2. 缺少 `return`、未定义名、前向引用、重复绑定、错误作用域和不完整依赖声明
   均在编译阶段失败。
3. `require` 被正确解析、重复项被拒绝、`requires` 被拒绝并提示 `require`；
   `lipc check` 和 `RequiredDependencies()` 给出相同依赖集合。
4. number、bool、any 的 CLI 解析在 `lipc run`、独立构建和生成源码三条路径上
   一致；缺参和多参均非零退出。
5. 一个带 `require python` 的真实 Flow 调用 NumPy/Pandas 或其他已安装库，
   取回标量/JSON 结果；无该包时明确失败，不以示例值代替。
6. Python Worker 重启、句柄释放、blob 篡改、配额超限和取消均有失败测试；
   顺序执行与允许并行执行只改变调度，不改变 Flow 结果。

## 7. 迁移与非目标

Alpha 0.4 程序可以继续使用省略类型的参数和普通 Host 调用；v0.5 迁移工具应先
报告，再把省略的边界类型补成 `any` 或具体类型。依赖关键字统一替换为 `require`，
不建立兼容别名，避免继续扩大语法分歧。默认输入和测试专用注入必须删除。

v0.5 仍不引入普通循环、事件/流、隐式后台任务、无界反馈、完整 Effect 类型系统、
自动包安装或嵌入式 CPython。它先把“源代码就是完整程序，编译器只做忠实翻译”
固定下来，再根据真实工作负载推进这些独立能力。
