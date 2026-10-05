# LIP Alpha 0.1

语言版本：`0.1`（Alpha）  
当前实现：`0.1.0`（语言成熟度：Alpha 0.1；见 [VERSION](VERSION)）

LIP is a small dependency-oriented language compiled to Go. Alpha 0.1 keeps
the language intentionally narrow: `flow`, expression `fn`,
single-assignment bindings, dependency-derived fan-out/fan-in, `when` gates and
a deterministic sequential reference runtime with bounded automatic scheduling.

Start with [QUICKSTART.md](docs/QUICKSTART.md); the compiler details are in
[docs/COMPILER.md](docs/COMPILER.md). 想按步骤学习语言，请看
[docs/TUTORIAL.md](docs/TUTORIAL.md)。发布变更记录见
[CHANGELOG.md](CHANGELOG.md)。正式发布前的检查清单见
[RELEASE.md](RELEASE.md)。

## Quick start

```bash
GOCACHE=/tmp/lip-gocache go test ./...
GOCACHE=/tmp/lip-gocache go run -buildvcs=false ./cmd/lipc check examples/hello.lip
GOCACHE=/tmp/lip-gocache go run -buildvcs=false ./cmd/lipc examples/hello.lip
GOCACHE=/tmp/lip-gocache go build -buildvcs=false ./...
GOCACHE=/tmp/lip-gocache go run -buildvcs=false ./examples/hello
```

构建命令支持简写：`lipc file.lip`，默认输出为同目录下的`<FlowName>_generated.go`；需要自定义输出时再使用 `-o`。

The reference implementation targets Go 1.27.

## Repository layout

- `compiler/`, `runtime/`, `cmd/lipc/`: Alpha implementation;
- `examples/`, `tests/`: executable examples and conformance tests;
- `examples/PROTOTYPES.md`: coverage map for the 30 early prototype ideas;
- `QUICKSTART.md`, `docs/TUTORIAL.md`: user-facing language documents;
- `docs/COMPILER.md`: implementation notes;

The generated `examples/*/main.go` files are convenience outputs; the `.lip`
files are the source of truth. Regenerate a checked-in executable example with
an explicit output path, for example:

```bash
go run ./cmd/lipc build -o examples/hello/main.go examples/hello.lip
```
