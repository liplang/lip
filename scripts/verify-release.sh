#!/usr/bin/env bash
set -euo pipefail

repo_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
cd "$repo_dir"
export GOCACHE="${GOCACHE:-/tmp/lip-gocache}"
verify_dir=$(mktemp -d)
trap 'rm -rf "$verify_dir"' EXIT

printf 'Checking tests, races, vet and builds...\n'
go test ./...
go test -race ./...
go vet ./...
go build -buildvcs=false ./...
go build -buildvcs=false -o "$verify_dir/lipc" ./cmd/lipc

expected_version=$(tr -d '\n' < VERSION)
actual_version=$("$verify_dir/lipc" version)
test "$actual_version" = "$expected_version"
"$verify_dir/lipc" help >/dev/null 2>&1

# Simulate user installation without modifying the developer's tools or PATH.
verify_gopath="$verify_dir/custom workspace"
verify_other_gopath="$verify_dir/other workspace"
verify_custom_bin="$verify_dir/user bin"
GOENV=off GOBIN= GOPATH="$verify_gopath:$verify_other_gopath" go install ./cmd/lipc
GOENV=off GOBIN="$verify_custom_bin" GOPATH="$verify_gopath:$verify_other_gopath" go install ./cmd/lipc
test ! -e "$verify_other_gopath/bin/lipc"
verify_project="$verify_dir/independent project"
mkdir -p "$verify_project"
cp examples/core.lip "$verify_project/"
verify_core_expected='{"average":4,"count":3,"total":12,"values":[2,4,6]}'
for verify_shell in bash zsh; do
  if ! command -v "$verify_shell" >/dev/null 2>&1; then
    printf 'Skipping %s installation check: shell unavailable\n' "$verify_shell"
    continue
  fi
  for verify_install_bin in "$verify_gopath/bin" "$verify_custom_bin"; do
    "$verify_shell" -f -c '
      cd "$4" || exit 1
      test "$("$1/lipc" version)" = "$2" || exit 1
      "$1/lipc" help >/dev/null 2>&1 || exit 1
      "$1/lipc" check core.lip >/dev/null || exit 1
      test "$(GOPROXY=off "$1/lipc" run core.lip "[1,2,3]")" = "$3" || exit 1
    ' -- "$verify_install_bin" "$expected_version" "$verify_core_expected" "$verify_project"
  done
done

count=0
# All nested LIP examples and conformance programs take the same compiler path.
while IFS= read -r source; do
  "$verify_dir/lipc" check "$source" >/dev/null
  "$verify_dir/lipc" build --emit-go --output "$verify_dir/source-$count.go" "$source" >/dev/null
  go build -buildvcs=false -o "$verify_dir/program-$count" "$verify_dir/source-$count.go"
  go vet "$verify_dir/source-$count.go"
  count=$((count+1))
done < <(find examples tests/conformance -name '*.lip' -type f | sort)

printf 'Checking independent core programs and observation...\n'
"$verify_dir/lipc" inspect examples/core.lip >"$verify_dir/graph.json"
"$verify_dir/lipc" check --json examples/strings.lip >"$verify_dir/check.json"
test "$("$verify_dir/lipc" run examples/strings.lip ' Rust, LIP, rust, ,你好 ')" = '{"count":3,"label":"rust / lip / 你好","tags":["rust","lip","你好"]}'
test "$("$verify_dir/lipc" run examples/strings.lip '')" = '{"count":0,"label":"","tags":[]}'
test "$("$verify_dir/lipc" run --trace "$verify_dir/trace.json" examples/core.lip '[1,2,3]')" = '{"average":4,"count":3,"total":12,"values":[2,4,6]}'
test "$("$verify_dir/lipc" run examples/core.lip '[]')" = '{"average":null,"count":0,"total":0,"values":[]}'
test "$("$verify_dir/lipc" run examples/range.lip 5)" = '{"total":30,"values":[0,1,4,9,16]}'
test "$("$verify_dir/lipc" run examples/tree.lip '{"value":1,"left":{"value":2,"left":null,"right":null},"right":null}')" = '3'
"$verify_dir/lipc" run examples/lists.lip '[{"department":"A","amount":3},{"department":"B","amount":2},{"department":"A","amount":-1}]' >"$verify_dir/lists.json"
"$verify_dir/lipc" build --output "$verify_dir/report" examples/core.lip >/dev/null
test "$("$verify_dir/report" '[1,2,3]')" = '{"average":4,"count":3,"total":12,"values":[2,4,6]}'

printf 'Verified LIP %s: %d source programs, core/list/string/tutorial/adapter and release checks.\n' "$actual_version" "$count"
