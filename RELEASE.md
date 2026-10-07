# 0.5.0 发布清单

## 版本一致性

- [x] `VERSION` 和 `lipc version` 均为 `0.5.0`。
- [ ] 在发布仓库中创建并核对 `0.5.0` 标签（本地源码包无法代替这一步）。
- [x] 语言成熟度在文档中标为 Alpha 0.5。
- [x] 以 [ALPHA-0.5-SPEC.md](docs/ALPHA-0.5-SPEC.md) 为本次契约的规范来源。

## 必须执行的验证

```bash
GOCACHE=/tmp/lip-gocache go test ./...
GOCACHE=/tmp/lip-gocache go test -race ./...
GOCACHE=/tmp/lip-gocache go vet ./...
GOCACHE=/tmp/lip-gocache go build -buildvcs=false ./...
GOCACHE=/tmp/lip-gocache go run -buildvcs=false ./cmd/lipc version
test "$(tr -d '\n' < VERSION)" = "$(GOCACHE=/tmp/lip-gocache go run -buildvcs=false ./cmd/lipc version)"
GOCACHE=/tmp/lip-gocache go run -buildvcs=false ./cmd/lipc help >/dev/null

for f in examples/*.lip tests/conformance/*.lip; do
  GOCACHE=/tmp/lip-gocache go run -buildvcs=false ./cmd/lipc check "$f" || exit 1
done

# Verify that the CLI formats every generated source file and rejects no
# example. Write into a temporary directory so checked-in convenience outputs
# are not modified by the release check.
tmpdir=$(mktemp -d)
trap 'rm -rf "$tmpdir"' EXIT
for f in examples/*.lip; do
  GOCACHE=/tmp/lip-gocache go run -buildvcs=false ./cmd/lipc build -o "$tmpdir/$(basename "${f%.lip}").go" "$f" || exit 1
done

GOCACHE=/tmp/lip-gocache go run -buildvcs=false ./examples/hello Alice
GOCACHE=/tmp/lip-gocache go run -buildvcs=false ./examples/gated 1
GOCACHE=/tmp/lip-gocache go run -buildvcs=false ./examples/fanout 3
GOCACHE=/tmp/lip-gocache go run -buildvcs=false ./examples/language
GOCACHE=/tmp/lip-gocache go run -buildvcs=false ./examples/host_adapter
GOCACHE=/tmp/lip-gocache go run -buildvcs=false ./examples/map
GOCACHE=/tmp/lip-gocache go run -buildvcs=false ./examples/python # 需要 NumPy/Pandas

# Verify Go-like executable build and temporary run paths.
GOCACHE=/tmp/lip-gocache go run -buildvcs=false ./cmd/lipc build -o "$tmpdir/hello" examples/hello.lip
GOCACHE=/tmp/lip-gocache "$tmpdir/hello" Alice
GOCACHE=/tmp/lip-gocache go run -buildvcs=false ./cmd/lipc run examples/hello.lip -- Alice
```

## 发布内容

保留编译器、Runtime、CLI、测试、`.lip` 示例、规范和教程。生成的
`examples/*/main.go` 文件是可选的便利输出。根目录不应包含 `language`
等本地 ELF 构建产物。

## 已知边界

Alpha 0.5 支持持久 State/Tick、受限 Retry/Feedback、取消传播和 Runtime
Effect/Ordering。普通 loops、事件/streams、detach/background、完整 Effect
类型系统和直接执行 Go/Python import 仍不在本版本边界内；外部依赖
使用 `require` 元数据声明，实际 adapter 由 Go 宿主注册。

## 审计结论

本版本补齐显式输入/输出、单一输出、可选门控、纯函数组合与迁移命令。
端到端 conformance 验证三条 CLI 路径和动态类型失败；Python 失败测试覆盖重启、
句柄释放、缓存 blob 篡改与配额。已有能力继续保持：自动调度、有限并行、`Await`、动态 Map、
State/Tick、Retry、Feedback、取消传播、Effect/Ordering、类型检查、Go Host
Adapter 和 CLI 生成均有可运行验证。发布者仍需在带有 Git
元数据的仓库中完成版本标签检查；源码导出包可统一使用
`-buildvcs=false`，避免把本地 VCS 状态当成构建依赖。
