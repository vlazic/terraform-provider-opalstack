.DEFAULT_GOAL := help

# The dev tools are pinned in tools/go.mod, so the provider's own go.mod stays small.
# Some of them need a newer Go than the provider does, and with -modfile the go
# command picks its toolchain from the root go.mod, so the version is passed on.
TOOLS_GO := go$(shell awk '/^go /{print $$2}' tools/go.mod)
TOOL := GOTOOLCHAIN=$(TOOLS_GO)+auto go tool -modfile=tools/go.mod

help: ## Show all targets
	@grep -hE '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | \
	  awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-10s\033[0m %s\n", $$1, $$2}'

build: ## Compile everything
	go build ./...

install: ## Install the provider binary into GOBIN, for dev_overrides
	go install .

test: ## Unit tests. Offline: no request leaves the machine
	go test -race -count=1 ./...

lint: ## gofmt, go vet and golangci-lint
	@test -z "$$(gofmt -l .)" || { gofmt -l .; echo "gofmt: the files above need formatting"; exit 1; }
	go vet ./...
	$(TOOL) golangci-lint run

# gitleaks is run at a pinned version outside tools/go.mod: built in the same
# module as the linters, it does not compile, because their dependencies clash.
GITLEAKS := github.com/zricethezav/gitleaks/v8@v8.30.1

leaks: ## Scan the git history for secrets
	GOTOOLCHAIN=auto go run $(GITLEAKS) git --no-banner --redact .

check: lint test leaks ## Everything that must pass before a commit

.PHONY: help build install test lint leaks check
