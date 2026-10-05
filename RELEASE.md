# 0.1.0 发布清单

## 版本一致性

- [x] `VERSION` 和 `lipc version` 均为 `0.1.0`。
- [ ] 在发布仓库中创建并核对 `0.1.0` 标签（本地源码包无法代替这一步）。
- [x] 语言成熟度在文档中标为 Alpha 0.1。
- [x] 以 [ALPHA-0.1-SPEC.md](ALPHA-0.1-SPEC.md) 为唯一规范来源。

## 必须执行的验证

```bash
GOCACHE=/tmp/lip-gocache go test ./...
GOCACHE=/tmp/lip-gocache go test -race ./...
GOCACHE=/tmp/lip-gocache go vet ./...
GOCACHE=/tmp/lip-gocache go build -buildvcs=false ./...
GOCACHE=/tmp/lip-gocache go run -buildvcs=false ./cmd/lipc version
test "$(tr -d '\n' < VERSION)" = "$(GOCACHE=/tmp/lip-gocache go run -buildvcs=false ./cmd/lipc version)"

for f in examples/*.lip; do
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

GOCACHE=/tmp/lip-gocache go run -buildvcs=false ./examples/hello
GOCACHE=/tmp/lip-gocache go run -buildvcs=false ./examples/gated
GOCACHE=/tmp/lip-gocache go run -buildvcs=false ./examples/fanout
GOCACHE=/tmp/lip-gocache go run -buildvcs=false ./examples/language
GOCACHE=/tmp/lip-gocache go run -buildvcs=false ./examples/host_adapter
```

## 发布内容

保留编译器、Runtime、CLI、测试、`.lip` 示例、规范和教程。生成的
`examples/*/main.go` 文件是可选的便利输出。根目录不应包含 `language`
等本地 ELF 构建产物。

## 已知边界

Alpha 0.1 不承诺 loops、map/comprehension、动态展开、持久状态、事件、
feedback、retry 语法、增量重算或直接 Go import。它们必须通过 Host Adapter
或留待后续版本，不能在发布说明中描述成已实现能力。

## 审计结论

本版本的实现与规范边界一致：自动调度、有限纯节点并行、`Await`、类型
检查、Go Host Adapter 和 CLI 生成均有可运行验证。发布者仍需在带有 Git
元数据的仓库中完成版本标签检查；源码导出包可统一使用
`-buildvcs=false`，避免把本地 VCS 状态当成构建依赖。
