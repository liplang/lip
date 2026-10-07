# LIP Alpha 0.5 规范

这份规范定义 v0.5 的完整程序契约、Python 边界和迁移验收。参考实现版本为
`0.5.0`。后续能力保持在路线图中，本文中的语法和规则均由实现与测试验证。

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

fn twice(x: number) -> number {
    return x * 2
}

flow Scientific(values: any) -> any {
    total = numpy.sum(values)
    average = numpy.mean(values)
    return [total, average]
}
```

`require` 的种类是 `python`、`go` 和 `host`：

- `python` 是 Python 导入根和可选版本范围的部署要求，例如 `sklearn>=1.4`；
  声明使用导入根 `sklearn`，而非发行包名 `scikit-learn`。版本范围是部署元数据，
  不在运行时自动安装或解析发行包版本。Worker 使用的 Python 环境负责真正导入
  模块，编译器不维护固定库名单。
- `go` 是宿主 Go module 的部署要求；LIP 不直接 import Go 包。承载生成包的
  Go 程序负责 import 包并注册 Host adapter。
- `host` 是必须由宿主注册的 operation 名称，可用于审查和启动前检查。
  `require host "db.*"` 可声明一组 dotted operation；库启动检查同时核对源文件
  实际使用的每个操作，不能以另一个同前缀操作代替缺失的 adapter。

同一 kind/spec 不得重复。依赖声明是可查询元数据：`lipc check` 必须打印，生成
库必须暴露 `RequiredDependencies()`。编译器不得联网安装、修改解释器或猜测
版本。v0.5 不接受 `requires`、`import` 或其他同义关键字作为别名；错误应指出
应使用 `require`。

关键字表保持一套固定拼写和固定语义：`require`、`fn`、`flow`、`return`、`when`、
`if`、`then`、`else`、`for`、`in`、`true`、`false`。实现不得为了示例、后端或
历史文档再添加复数、缩写或同义别名；新能力先更新规范和语法表，再更新解析器、
生成器、示例和 conformance。

## 3. 输入、输出与绑定

Flow 参数是完整的外部输入契约。所有 Flow 和函数参数必须显式写类型；动态值写
`any`。无输入的 Flow 也必须写 `()`。v0.5 的边界类型是 `string`、`number`、`bool`、
`any`；列表、对象和 Python 句柄使用 `any`，不承诺尚未实现的集合或结构类型系统。
`number` 是有限的双精度数，支持十进制、小数、指数和负数，无隐式字符串转换。

Flow 必须声明输出：`flow Name(args) -> Type { ... return value }`。每个 Flow 恰好
有一个 `return`；条件值使用 `if condition then value else value`。`return` 定义图的
输出，不引入普通控制流的提前退出。同一块的 `return` 后不能再写语句。旧的“多个
条件 return，取源顺序最后一个”的规则取消，因为它容易被误认为提前返回。

门控的输出可能被跳过时，必须声明 `Type?`，例如：

```lip
flow Positive(value: number) -> number? {
    when value > 0 {
        return value * 2
    }
}
```

可选输出只表示允许门控无值完成；实际执行的 `return` 仍须符合基础类型。库以
`nil` 表示无值，独立入口以 JSON `null` 输出。`any` 本身可容纳 JSON null；`any?`
还允许整个输出节点被跳过，v0.5 不区分这两种 nil 的存储表示。非可选输出不能只
出现在可能为假的 `when` 中；`when true` 是静态无条件门控。

静态已知的类型矛盾在编译期失败；`any` 经运算、局部函数参数/结果和 Flow 输出边界
在运行时校验，不能把动态值转换成伪造的静态保证。生成库的一次性运行拒绝多余输入，
`Instance.Tick` 可以只更新部分已声明参数；非法更新在修改实例或递增 Tick 前失败。
State 初值的已知基础类型也约束 `SetState`。

普通绑定使用 `name = expression`，绑定即声明，且每个名字只能绑定一次。名称必须
在引用前已经由 Flow 参数、同级较早绑定或当前函数参数声明；前向引用、重复绑定、
未定义名称以及从 `when` 作用域逃逸都必须在 `lipc check` 阶段失败。`when` 是
执行门控，不会把块内变量提升到外部；条件可用纯表达式，编译器为复杂门控生成一个
可观察的门控节点。`if` 是值表达式，不创建隐藏节点。`if` 和 `&&`/`||` 在表达式内部
只计算需要的分支；已经绑定的图节点仍按照自身依赖和门控执行。

`fn` 是局部、无状态的纯表达式函数，返回类型可推导，也可写 `-> Type`。函数可以
组合其他局部函数和内置 `str`，不允许递归、State 或外部操作。局部纯调用可以出现在
算式、条件和其他调用参数里。`str` 是固定的语言转换，不受宿主同名注册覆盖。外部 operation 必须形成 Flow 节点：嵌套外部调用先绑定
再引用；Map 元素中的单个外部调用由 Map 调度；State、Retry、Feedback 只能独立构成
Flow 节点，不嵌入 Map 元素。调用名、参数数量、输入依赖和效果
分类保持源代码含义。编译器不能推测外部 operation 的参数类型或返回类型，真实 Host
adapter 负责参数校验，未知效果按外部写入处理。局部函数注册使用独立 Host 副本，
不会覆盖调用方的 operation。

## 4. CLI、生成代码与忠实翻译

独立入口按 Flow 参数声明顺序接收位置参数：

- `string` 原样传递；
- `number` 接受有限十进制数（含符号、小数和指数，不接受十六进制或下划线）；
- `bool` 只接受 `true` 或 `false`；
- `any` 在命令行边界使用 JSON。

参数缺失、参数过多、解析失败和类型错误都必须非零退出，并显示用法。输出字符串
原样加换行，其他结果编码为 JSON。生成程序
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
模块 allow/deny 策略仍由 Worker 配置管理。Worker 握手公布协议和通用工具能力，不列固定
科学库名单；`pandas.describe` 不作为内置替身，真实对象方法通过 `python.call` 调用。

控制面保持进程隔离和可替换协议。默认 Worker 使用 JSONL、request ID、deadline、
取消、错误和重启语义；复杂对象以显式句柄留在 Worker，调用方通过
`python.call`、`python.to_json`、`python.release` 管理生命周期。Worker 重启会丢弃
对象；旧句柄含进程身份，必须失败，不能误指向新对象。大数组使用 P2 文件支持的
只读 blob/mmap 基线，传输 dtype、shape、大小和 SHA-256 元数据，复用 descriptor 时
重新检查文件内容；Arrow、
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
6. Python Worker 崩溃/重启、旧句柄和释放后使用、blob 篡改（含已打开映射）、配额
   超限和取消均有失败测试；顺序执行与允许并行执行只改变调度，不改变 Flow 结果。
7. 动态 Host 返回错误类型（含 Await 结果）必须产生 Error Trace；可选输出、纯函数
   组合、负数/指数、惰性分支和逻辑短路通过生成代码执行验证。

测试入口为 `tests/alpha05_test.go`、`tests/conformance/*.lip` 和
`runtime/python_test.go`。Python 协议测试在解释器可用时执行，NumPy/Pandas 验收在
安装对应包时执行；缺包失败路径使用不存在的模块验证，不会以样例值回退。

## 7. 迁移与非目标

Alpha 0.4 的省略类型、缺少输出声明和旧依赖拼写只由显式迁移命令读取，正常
`check/build/run` 不保留兼容别名：

```bash
lipc migrate old.lip                  # stderr 报告，stdout 输出已检查的源代码
lipc migrate old.lip -o migrated.lip  # 保存新文件；也可明确指定原路径
lipc check migrated.lip
```

迁移保留注释和原有排版，省略的参数补为 `any`，输出根据可确定的表达式类型声明，
动态结果或列表使用 `any`，门控输出加 `?`；`requires`/`import kind "spec"` 替换为
`require`。迁移不会补输入值、绑定或缺失的 return，不会安装依赖。多输出、隐式外部
调用和其他语义错误必须先人工修复，迁移报错而不猜测。Alpha 0.4 的历史规范保留，
与本规范冲突的规则以 v0.5 为准。

v0.5 仍不引入普通循环、事件/流、隐式后台任务、无界反馈、完整 Effect 类型系统、
自动包安装或嵌入式 CPython。它先把“源代码就是完整程序，编译器只做忠实翻译”
固定下来，再根据真实工作负载推进这些独立能力。
