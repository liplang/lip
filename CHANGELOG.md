# Changelog

## 0.1.0 — Alpha 0.1

This is the first publishable Alpha release of LIP.

Included:

- handwritten lexer/parser and dependency-graph compiler;
- `flow`, expression `fn`, single-assignment bindings, `when`, `return`;
- strict built-in operators with optional explicit parameter annotations;
- Go 1.27 code generation and the `runtime.Host` adapter boundary;
- deterministic sequential scheduling and bounded pure-node parallelism;
- `Ready`, `Failed` and `Await` Host results;
- Quickstart, tutorial, compiler notes and the 30-prototype coverage map.

Intentionally not included:

- loops, comprehensions or dynamic map expansion;
- persistent state, events, streams or feedback;
- retry/cancellation policy syntax;
- direct Go imports or automatic Go signature reflection;
- incremental recomputation across Flow invocations.

Release verification:

```bash
GOCACHE=/tmp/lip-gocache go test ./...
GOCACHE=/tmp/lip-gocache go vet ./...
GOCACHE=/tmp/lip-gocache go build -buildvcs=false ./...
for f in examples/*.lip; do
  GOCACHE=/tmp/lip-gocache go run -buildvcs=false ./cmd/lipc check "$f" || exit 1
done
```
