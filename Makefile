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

# The provider tests drive a real terraform binary against a fake API. A
# version-manager shim does not work in the temporary directory they run in, so
# the binary behind it is resolved here. Without one those tests are skipped.
TERRAFORM := $(shell asdf which terraform 2>/dev/null || command -v terraform)

test: ## Unit tests. Offline: no request leaves the machine
	TF_ACC_TERRAFORM_PATH=$(TERRAFORM) CHECKPOINT_DISABLE=1 go test -race -count=1 ./...

testacc: ## Acceptance tests against the real account. Needs OPALSTACK_TOKEN and the sandbox variables; RUN=TestAccName picks one
	TF_ACC=1 TF_ACC_TERRAFORM_PATH=$(TERRAFORM) CHECKPOINT_DISABLE=1 go test ./internal/provider -v -p 1 -count=1 -timeout 60m -run '$(or $(RUN),^TestAcc)'

lint: ## gofmt, go vet and golangci-lint
	@test -z "$$(gofmt -l .)" || { gofmt -l .; echo "gofmt: the files above need formatting"; exit 1; }
	go vet ./...
	$(TOOL) golangci-lint run

# gitleaks is run at a pinned version outside tools/go.mod: built in the same
# module as the linters, it does not compile, because their dependencies clash.
GITLEAKS := github.com/zricethezav/gitleaks/v8@v8.30.1

leaks: ## Scan the git history and the staged changes for secrets
	GOTOOLCHAIN=auto go run $(GITLEAKS) git --no-banner --redact .
	GOTOOLCHAIN=auto go run $(GITLEAKS) git --no-banner --redact --pre-commit --staged .

check: lint test leaks ## Everything that must pass before a commit. Stage first: leaks reads the index

.PHONY: help build install test testacc lint leaks check
