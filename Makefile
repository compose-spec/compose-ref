.DEFAULT_GOAL := help

.PHONY: build
build: ## Build compose-ref binary
	GOPRIVATE=github.com/compose-spec/compose-go go build ./...

.PHONY: test
test: ## Run tests
	GOPRIVATE=github.com/compose-spec/compose-go go test ./... -v

.PHONY: fmt
fmt: ## Format go files
	go fmt -w ./...

.PHONY: imports
imports: ## Format go files
	goimports -w ./...

.PHONY: setup
setup: ## Setup the precommit hook
	@which pre-commit > /dev/null 2>&1 || (echo "pre-commit not installed see README." && false)
	@pre-commit install

.PHONY: help
help:
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-30s\033[0m %s\n", $$1, $$2}'
