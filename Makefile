BINARY = lens

.PHONY: build build-tool clean help

build: ## Build lens binary from lens.yaml config
	lens-build -output $(BINARY)

build-tool: ## Install the lens-build CLI tool
	go install ./cmd/lens-build

clean: ## Remove built artifacts
	rm -f $(BINARY) providers_gen.go

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*## ' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*## "}; {printf "  %-20s %s\n", $$1, $$2}'
