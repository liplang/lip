# Local caches keep builds reproducible in restricted workspaces. The go.mod
# directive sets a minimum; no specific patch release/toolchain is pinned.
export GOCACHE := $(CURDIR)/.build/cache
export GOTMPDIR := $(CURDIR)/.build/tmp
export TMPDIR := $(CURDIR)/.build/tmp
export GOPATH := $(CURDIR)/.build/gopath

.PHONY: prepare test race build compile vet examples clean
prepare:
	@mkdir -p $(GOCACHE) $(GOTMPDIR) $(GOPATH)
test: prepare
	go test ./...
race: prepare
	LIP_NATIVE_RACE=1 go test -race ./...
build: prepare
	go build -o lipc ./cmd/lipc
compile: build
	@test -n "$(SOURCE)" -a -n "$(OUTPUT)" || (echo "set SOURCE and OUTPUT"; exit 1)
	./lipc build $(if $(HOST),-host $(HOST),) -o "$(OUTPUT)" "$(SOURCE)"
vet: prepare
	go vet ./...
examples: prepare
	go test ./tests -run TestExamples -v
clean:
	rm -rf .build lip lipc
	find . -type d -name __pycache__ -prune -exec rm -rf {} +
	find . -type f \( -name '*.pyc' -o -name '*.pyo' \) -delete
