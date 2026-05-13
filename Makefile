.PHONY: build install uninstall release test lint clean help

BINARY      := agentflow
VERSION     := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
BUILD_FLAGS := -ldflags="-w -s -X main.version=$(VERSION)"

INSTALL_BIN   := /usr/local/bin/$(BINARY)
INSTALL_SHARE := /usr/local/share/$(BINARY)
RELEASE_DIR   := ./dist

PLATFORMS := linux_amd64 linux_arm64 darwin_amd64 darwin_arm64

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
	@echo "Installing binary       → $(INSTALL_BIN)"
	@cp ./bin/$(BINARY) $(INSTALL_BIN)
	@chmod +x $(INSTALL_BIN)
	@echo "Installing prompts      → $(INSTALL_SHARE)/prompts/"
	@mkdir -p $(INSTALL_SHARE)/prompts
	@cp prompts/*.md $(INSTALL_SHARE)/prompts/
	@echo "Installing config       → ~/.agentflow/config.json"
	@mkdir -p ~/.agentflow
	@[ -f ~/.agentflow/config.json ] || cp configs/config.json ~/.agentflow/config.json
	@echo ""
	@echo "✓ agentflow installed."
	@echo ""
	@echo "  Next: export ANTHROPIC_API_KEY=sk-ant-..."
	@echo "  Then: mkdir my-project && cd my-project && agentflow init"

uninstall:       ## Remove agentflow from system
	@rm -f $(INSTALL_BIN)
	@rm -rf $(INSTALL_SHARE)
	@echo "✓ Uninstalled (config at ~/.agentflow kept)"

# ── Release ───────────────────────────────────────────────────────────────────

release:         ## Build release binaries for all platforms
	@mkdir -p $(RELEASE_DIR)
	@echo "Building release $(VERSION) for all platforms..."
	@for platform in $(PLATFORMS); do \
		os=$$(echo $$platform | cut -d_ -f1); \
		arch=$$(echo $$platform | cut -d_ -f2); \
		outdir=$(RELEASE_DIR)/$(BINARY)_$(VERSION)_$${platform}; \
		mkdir -p $$outdir; \
		echo "  → $${os}/$${arch}"; \
		GOOS=$$os GOARCH=$$arch CGO_ENABLED=0 \
			go build $(BUILD_FLAGS) \
			-o $$outdir/$(BINARY) \
			./cmd/agentflow; \
		cp -r prompts $$outdir/prompts; \
		cp configs/config.json $$outdir/config.json; \
		tar -czf $(RELEASE_DIR)/$(BINARY)_$(VERSION)_$${platform}.tar.gz \
			-C $(RELEASE_DIR) $(BINARY)_$(VERSION)_$${platform}; \
		rm -rf $$outdir; \
	done
	@# Prompts archive (used by install.sh)
	@tar -czf $(RELEASE_DIR)/agentflow_prompts_$(VERSION).tar.gz prompts/
	@echo ""
	@echo "✓ Release artifacts in $(RELEASE_DIR)/"
	@ls -lh $(RELEASE_DIR)/

# ── Docker ────────────────────────────────────────────────────────────────────

docker-build:    ## Build Docker image
	docker build -t agentflow:$(VERSION) -t agentflow:latest .

docker-run:      ## Run pipeline in Docker (set PRD= and NAME=)
	docker-compose run --rm agentflow run \
		--prd /app/workspace/$(PRD) \
		--name "$(NAME)" $(ARGS)

# ── Utilities ─────────────────────────────────────────────────────────────────

clean:           ## Remove build artifacts
	rm -rf ./bin ./dist

help:            ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2}'
