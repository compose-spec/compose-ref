build: ## Run tests
	GOPRIVATE=github.com/compose-spec/compose-go go build ./...

test: ## Run tests
	GOPRIVATE=github.com/compose-spec/compose-go go test ./... -v

fmt: ## Format go files
	gofmt -w .

imports: ## Format go files
	goimports -w ./...

setup: ## Setup the precommit hook
	@which pre-commit > /dev/null 2>&1 || (echo "pre-commit not installed see README." && false)
	@pre-commit install

.PHONY: build test fmt imports setup
