.PHONY: build install uninstall run test lint clean help

BINARY      := agentflow
VERSION     := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
BUILD_FLAGS := -ldflags="-w -s -X main.version=$(VERSION)"

INSTALL_BIN    := /usr/local/bin/$(BINARY)
INSTALL_SHARE  := /usr/local/share/$(BINARY)

# ── Development ───────────────────────────────────────────────────────────────

build:           ## Build binary to ./bin/agentflow
	@mkdir -p bin
	go build $(BUILD_FLAGS) -o ./bin/$(BINARY) ./cmd/agentflow

test:            ## Run all Go tests
	go test -v -race ./...

lint:            ## Run go vet
	go vet ./...

tidy:            ## Tidy go modules
	go mod tidy

# ── Install / Uninstall ───────────────────────────────────────────────────────

install: build   ## Install agentflow system-wide
	@echo "Installing binary → $(INSTALL_BIN)"
	@cp ./bin/$(BINARY) $(INSTALL_BIN)
	@chmod +x $(INSTALL_BIN)
	@echo "Installing prompts → $(INSTALL_SHARE)/prompts/"
	@mkdir -p $(INSTALL_SHARE)/prompts
	@cp prompts/*.md $(INSTALL_SHARE)/prompts/
	@echo "Installing config  → ~/.agentflow/config.json"
	@mkdir -p ~/.agentflow
	@cp configs/config.json ~/.agentflow/config.json
	@echo ""
	@echo "✓ agentflow installed. Next:"
	@echo "  1. Edit ~/.agentflow/config.json — set active_provider"
	@echo "  2. Export your API key, e.g.: export ANTHROPIC_API_KEY=sk-ant-..."
	@echo "  3. cd your-project && agentflow init && agentflow run"

uninstall:       ## Remove agentflow from system
	@rm -f $(INSTALL_BIN)
	@rm -rf $(INSTALL_SHARE)
	@echo "✓ agentflow uninstalled (config at ~/.agentflow kept)"

# ── Utilities ─────────────────────────────────────────────────────────────────

clean:           ## Remove build artifacts
	rm -rf ./bin

help:            ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2}'
