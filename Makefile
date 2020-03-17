.DEFAULT_GOAL := help

PACKAGE=github.com/compose-spec/compose-ref

GOFLAGS=-mod=vendor

.PHONY: build
build: ## Build compose-ref binary
	GOPRIVATE=$(PACKAGE) GOFLAGS=$(GOFLAGS) go build compose-ref.go

.PHONY: test
test: ## Run tests
	GOPRIVATE=$(PACKAGE) GOFLAGS=$(GOFLAGS) go test ./... -v

.PHONY: fmt
fmt: ## Format go files
	@goimports -e -w ./

.PHONY: lint
lint: ## Verify Go files
	golangci-lint run --config ./golangci.yml ./

.PHONY: setup
setup: ## Setup the precommit hook
	@which pre-commit > /dev/null 2>&1 || (echo "pre-commit not installed see README." && false)
	@pre-commit install

.PHONY: help
help:
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-30s\033[0m %s\n", $$1, $$2}'
