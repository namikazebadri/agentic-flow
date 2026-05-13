#!/usr/bin/env bash
# agentflow installer
# Usage: curl -fsSL https://raw.githubusercontent.com/your-org/agentflow/main/install.sh | bash

set -euo pipefail

# ── Config ────────────────────────────────────────────────────────────────────

REPO="your-org/agentflow"
BINARY="agentflow"
INSTALL_BIN="/usr/local/bin"
INSTALL_SHARE="/usr/local/share/agentflow"
CONFIG_DIR="$HOME/.agentflow"
GITHUB_API="https://api.github.com/repos/${REPO}/releases/latest"

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
BOLD='\033[1m'
RESET='\033[0m'

# ── Helpers ───────────────────────────────────────────────────────────────────

info()    { echo -e "${BLUE}  →${RESET} $*"; }
success() { echo -e "${GREEN}  ✓${RESET} $*"; }
warn()    { echo -e "${YELLOW}  ⚠${RESET} $*"; }
error()   { echo -e "${RED}  ✗${RESET} $*" >&2; exit 1; }
header()  { echo -e "\n${BOLD}$*${RESET}"; }

need_cmd() {
    if ! command -v "$1" &>/dev/null; then
        error "Required command not found: $1. Please install it and try again."
    fi
}

# ── Detect platform ───────────────────────────────────────────────────────────

detect_platform() {
    local os arch

    case "$(uname -s)" in
        Linux)   os="linux" ;;
        Darwin)  os="darwin" ;;
        *)       error "Unsupported OS: $(uname -s). Only Linux and macOS are supported." ;;
    esac

    case "$(uname -m)" in
        x86_64|amd64) arch="amd64" ;;
        arm64|aarch64) arch="arm64" ;;
        *)       error "Unsupported architecture: $(uname -m)." ;;
    esac

    echo "${os}_${arch}"
}

# ── Get latest release version ────────────────────────────────────────────────

get_latest_version() {
    need_cmd curl

    local version
    version=$(curl -fsSL "$GITHUB_API" 2>/dev/null \
        | grep '"tag_name"' \
        | sed -E 's/.*"tag_name": *"([^"]+)".*/\1/')

    if [[ -z "$version" ]]; then
        # Fallback if GitHub API fails (rate limit etc.)
        warn "Could not fetch latest version from GitHub API. Using 'latest'."
        echo "latest"
    else
        echo "$version"
    fi
}

# ── Check if sudo is needed ───────────────────────────────────────────────────

maybe_sudo() {
    if [[ -w "$INSTALL_BIN" ]]; then
        "$@"
    else
        if command -v sudo &>/dev/null; then
            sudo "$@"
        else
            error "Cannot write to $INSTALL_BIN and sudo is not available. Run as root or install to a writable directory."
        fi
    fi
}

# ── Download and install binary ───────────────────────────────────────────────

install_binary() {
    local platform="$1"
    local version="$2"
    local tmpdir
    tmpdir=$(mktemp -d)
    trap 'rm -rf "$tmpdir"' EXIT

    local archive="${BINARY}_${version}_${platform}.tar.gz"
    local url="https://github.com/${REPO}/releases/download/${version}/${archive}"

    info "Downloading ${BINARY} ${version} for ${platform}..."
    if ! curl -fsSL "$url" -o "${tmpdir}/${archive}"; then
        # Try without version prefix in filename
        archive="${BINARY}_${platform}.tar.gz"
        url="https://github.com/${REPO}/releases/download/${version}/${archive}"
        if ! curl -fsSL "$url" -o "${tmpdir}/${archive}"; then
            error "Failed to download binary from:\n  ${url}\n\nCheck that the release exists: https://github.com/${REPO}/releases"
        fi
    fi

    info "Extracting..."
    tar -xzf "${tmpdir}/${archive}" -C "$tmpdir"

    local bin_path="${tmpdir}/${BINARY}"
    if [[ ! -f "$bin_path" ]]; then
        # Some releases nest in a subdirectory
        bin_path=$(find "$tmpdir" -name "$BINARY" -type f | head -1)
        [[ -z "$bin_path" ]] && error "Binary not found in archive."
    fi

    chmod +x "$bin_path"

    info "Installing binary to ${INSTALL_BIN}/${BINARY}..."
    maybe_sudo mkdir -p "$INSTALL_BIN"
    maybe_sudo cp "$bin_path" "${INSTALL_BIN}/${BINARY}"

    success "Binary installed: ${INSTALL_BIN}/${BINARY}"
}

# ── Install prompts ───────────────────────────────────────────────────────────

install_prompts() {
    local version="$1"
    local tmpdir
    tmpdir=$(mktemp -d)
    trap 'rm -rf "$tmpdir"' EXIT

    local archive="agentflow_prompts_${version}.tar.gz"
    local url="https://github.com/${REPO}/releases/download/${version}/${archive}"

    info "Downloading prompts..."
    if curl -fsSL "$url" -o "${tmpdir}/${archive}" 2>/dev/null; then
        tar -xzf "${tmpdir}/${archive}" -C "$tmpdir"
        maybe_sudo mkdir -p "${INSTALL_SHARE}/prompts"
        maybe_sudo cp -r "${tmpdir}/prompts/"* "${INSTALL_SHARE}/prompts/"
        success "Prompts installed: ${INSTALL_SHARE}/prompts/"
    else
        warn "Could not download prompts archive. You may need to copy prompts/ manually."
    fi
}

# ── Install default config ────────────────────────────────────────────────────

install_config() {
    mkdir -p "$CONFIG_DIR"

    if [[ -f "${CONFIG_DIR}/config.json" ]]; then
        warn "Config already exists at ${CONFIG_DIR}/config.json — skipping."
        return
    fi

    cat > "${CONFIG_DIR}/config.json" << 'CONFIG'
{
  "active_provider": "anthropic",

  "providers": {
    "anthropic": {
      "name": "Anthropic",
      "base_url": "https://api.anthropic.com",
      "api_key_env": "ANTHROPIC_API_KEY",
      "model": "claude-sonnet-4-20250514",
      "max_tokens": 8192
    },
    "openai": {
      "name": "OpenAI",
      "base_url": "https://api.openai.com",
      "api_key_env": "OPENAI_API_KEY",
      "model": "gpt-4o",
      "max_tokens": 8192
    },
    "ollama": {
      "name": "Ollama (local)",
      "base_url": "http://localhost:11434",
      "api_key_env": "OLLAMA_API_KEY",
      "model": "llama3.1:8b",
      "max_tokens": 4096
    }
  },

  "models": {
    "models": {
      "claude-opus-4-6": {
        "alias": "claude-opus-4-6",
        "provider": "anthropic",
        "model_id": "claude-opus-4-6-20250514",
        "display_name": "Claude Opus 4.6",
        "max_tokens": 8192,
        "tier": "flagship",
        "cost_tier": "high",
        "strengths": ["complex reasoning", "security", "architecture"],
        "notes": "Best for security-critical or highly complex deliverables"
      },
      "claude-sonnet-4-6": {
        "alias": "claude-sonnet-4-6",
        "provider": "anthropic",
        "model_id": "claude-sonnet-4-20250514",
        "display_name": "Claude Sonnet 4.6",
        "max_tokens": 8192,
        "tier": "standard",
        "cost_tier": "medium",
        "strengths": ["code generation", "balanced quality"],
        "notes": "Default — best balance of quality and cost"
      },
      "claude-haiku-4-5": {
        "alias": "claude-haiku-4-5",
        "provider": "anthropic",
        "model_id": "claude-haiku-4-5-20251001",
        "display_name": "Claude Haiku 4.5",
        "max_tokens": 4096,
        "tier": "fast",
        "cost_tier": "low",
        "strengths": ["simple CRUD", "boilerplate", "fast iteration"],
        "notes": "Best for simple, well-defined deliverables"
      }
    },
    "suggestion_rules": [
      {
        "description": "Security or auth deliverables use flagship model",
        "conditions": {
          "has_keywords": ["security", "auth", "authentication", "jwt", "oauth", "permission", "encryption"]
        },
        "suggest_model": "claude-opus-4-6"
      },
      {
        "description": "Large complex deliverables use flagship model",
        "conditions": { "complexity": "L" },
        "suggest_model": "claude-opus-4-6"
      },
      {
        "description": "Medium deliverables use standard model",
        "conditions": { "complexity": "M" },
        "suggest_model": "claude-sonnet-4-6"
      },
      {
        "description": "Simple deliverables use fast model",
        "conditions": { "complexity": "S" },
        "suggest_model": "claude-haiku-4-5"
      }
    ]
  },

  "pipeline": {
    "max_retries": 3,
    "retry_delay_seconds": 2,
    "prompts_dir": "/usr/local/share/agentflow/prompts",
    "verbose": false
  },

  "registry": {
    "base_dir": ".agentflow/registry"
  },

  "gate": {
    "coverage_threshold": 0.8,
    "enable_lint": true,
    "enable_tests": true,
    "enable_type_check": true,
    "custom_commands": []
  }
}
CONFIG

    success "Config installed: ${CONFIG_DIR}/config.json"
}

# ── Detect shell and add to PATH if needed ────────────────────────────────────

ensure_in_path() {
    if command -v "$BINARY" &>/dev/null; then
        return
    fi

    warn "${INSTALL_BIN} is not in your PATH."

    local shell_config=""
    case "$SHELL" in
        */zsh)  shell_config="$HOME/.zshrc" ;;
        */bash) shell_config="$HOME/.bashrc" ;;
        */fish) shell_config="$HOME/.config/fish/config.fish" ;;
    esac

    if [[ -n "$shell_config" ]]; then
        echo "" >> "$shell_config"
        echo "# agentflow" >> "$shell_config"
        echo "export PATH=\"${INSTALL_BIN}:\$PATH\"" >> "$shell_config"
        warn "Added ${INSTALL_BIN} to PATH in ${shell_config}"
        warn "Run: source ${shell_config}"
    else
        warn "Add this to your shell config: export PATH=\"${INSTALL_BIN}:\$PATH\""
    fi
}

# ── Interactive API key setup ─────────────────────────────────────────────────

setup_api_key() {
    local provider
    provider=$(grep '"active_provider"' "${CONFIG_DIR}/config.json" \
        | sed -E 's/.*"active_provider": *"([^"]+)".*/\1/')

    local env_var key_hint shell_config

    case "$provider" in
        anthropic)
            env_var="ANTHROPIC_API_KEY"
            key_hint="sk-ant-..."
            ;;
        openai)
            env_var="OPENAI_API_KEY"
            key_hint="sk-..."
            ;;
        ollama)
            success "Ollama selected — no API key needed."
            info "Make sure Ollama is running: ollama serve"
            return
            ;;
        *)
            env_var="ANTHROPIC_API_KEY"
            key_hint="sk-ant-..."
            ;;
    esac

    echo ""
    echo -e "${BOLD}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${RESET}"
    echo -e "${BOLD}  API Key Setup${RESET}"
    echo -e "${BOLD}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${RESET}"
    echo ""
    echo -e "  Provider : ${BOLD}${provider}${RESET}"
    echo -e "  Env var  : ${BOLD}${env_var}${RESET}"
    echo ""
    echo -e "  ${YELLOW}You can skip this and set it manually later.${RESET}"
    echo -e "  ${YELLOW}Your key is saved to your shell config file — never sent anywhere else.${RESET}"
    echo ""
    echo -e "  Enter your API key (or press Enter to skip):"
    echo -n "  ${BOLD}→ ${RESET}"

    local api_key=""
    IFS= read -rs api_key </dev/tty || true
    echo ""

    if [[ -z "$api_key" ]]; then
        warn "Skipped. Set it manually anytime:"
        echo ""
        echo -e "    ${BOLD}export ${env_var}=${key_hint}${RESET}"
        echo ""
        echo -e "  Then add that line to your shell config to persist across sessions:"
        case "$SHELL" in
            */zsh)  echo -e "    ${BOLD}~/.zshrc${RESET}" ;;
            */bash) echo -e "    ${BOLD}~/.bashrc${RESET} or ${BOLD}~/.bash_profile${RESET}" ;;
            */fish) echo -e "    ${BOLD}~/.config/fish/config.fish${RESET}" ;;
            *)      echo -e "    ${BOLD}~/.bashrc${RESET}" ;;
        esac
        return
    fi

    # Basic format check
    case "$provider" in
        anthropic)
            if [[ ! "$api_key" =~ ^sk-ant- ]]; then
                warn "Key doesn\'t look like an Anthropic key (expected: sk-ant-...)"
                warn "Saving anyway — double-check if agentflow fails to connect."
            fi
            ;;
        openai)
            if [[ ! "$api_key" =~ ^sk- ]]; then
                warn "Key doesn\'t look like an OpenAI key (expected: sk-...)"
            fi
            ;;
    esac

    # Detect shell config file
    case "$SHELL" in
        */zsh)  shell_config="$HOME/.zshrc" ;;
        */bash)
            if [[ "$(uname -s)" == "Darwin" ]]; then
                shell_config="$HOME/.bash_profile"
            else
                shell_config="$HOME/.bashrc"
            fi
            ;;
        */fish) shell_config="$HOME/.config/fish/config.fish" ;;
        *)      shell_config="$HOME/.bashrc" ;;
    esac

    # Write to shell config
    {
        echo ""
        echo "# agentflow API key (added by installer $(date +%Y-%m-%d))"
        echo "export ${env_var}=\"${api_key}\""
    } >> "$shell_config"

    export "${env_var}=${api_key}"

    success "API key saved to ${shell_config}"
    echo ""
    echo -e "  ${YELLOW}To apply in your current terminal session:${RESET}"
    echo -e "  ${BOLD}  source ${shell_config}${RESET}"
    echo ""
    echo -e "  ${YELLOW}To change it later, edit:${RESET}"
    echo -e "  ${BOLD}  ${shell_config}${RESET}"
    echo -e "  ${YELLOW}To change the provider, edit:${RESET}"
    echo -e "  ${BOLD}  ${CONFIG_DIR}/config.json${RESET}"
}

# ── Post-install summary ──────────────────────────────────────────────────────

print_summary() {
    echo ""
    echo -e "${BOLD}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${RESET}"
    echo -e "${GREEN}${BOLD}  agentflow is ready!${RESET}"
    echo -e "${BOLD}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${RESET}"
    echo ""
    echo -e "  ${BOLD}Installed files:${RESET}"
    echo -e "  ${GREEN}✓${RESET} Binary   ${INSTALL_BIN}/${BINARY}"
    echo -e "  ${GREEN}✓${RESET} Prompts  ${INSTALL_SHARE}/prompts/"
    echo -e "  ${GREEN}✓${RESET} Config   ${CONFIG_DIR}/config.json"
    echo ""
    echo -e "  ${BOLD}Where to configure things:${RESET}"
    echo ""
    echo -e "  ${BLUE}·${RESET} ${BOLD}${CONFIG_DIR}/config.json${RESET}"
    echo    "    Global settings: active provider, model catalog,"
    echo    "    gate options, max retries, prompts directory"
    echo ""
    echo -e "  ${BLUE}·${RESET} ${BOLD}./project.json${RESET}  (created inside each project)"
    echo    "    Per-project: PRD file, tech stack, docs & src directories"
    echo ""
    echo -e "  ${BLUE}·${RESET} ${BOLD}${INSTALL_SHARE}/prompts/${RESET}"
    echo    "    Agent system prompts — edit to change agent behavior,"
    echo    "    no recompile needed"
    echo ""
    echo -e "  ${BOLD}Get started:${RESET}"
    echo ""
    echo "    mkdir my-project && cd my-project"
    echo "    agentflow init"
    echo "    agentflow prd --new --name my-feature"
    echo "    agentflow run"
    echo ""
    echo -e "  ${BOLD}Docs:${RESET} https://github.com/${REPO}#readme"
    echo ""
}

# ── Build from source fallback ────────────────────────────────────────────────

install_from_source() {
    need_cmd go

    local goversion
    goversion=$(go version | awk '{print $3}' | tr -d 'go')
    local required="1.22"

    if [[ "$(printf '%s\n' "$required" "$goversion" | sort -V | head -n1)" != "$required" ]]; then
        error "Go ${required}+ is required. Found: ${goversion}"
    fi

    local tmpdir
    tmpdir=$(mktemp -d)
    trap 'rm -rf "$tmpdir"' EXIT

    info "Cloning repository..."
    need_cmd git
    git clone --depth 1 "https://github.com/${REPO}.git" "$tmpdir" 2>/dev/null

    info "Building from source..."
    (cd "$tmpdir" && go build -ldflags="-w -s" -o "${tmpdir}/${BINARY}" ./cmd/agentflow)

    maybe_sudo mkdir -p "$INSTALL_BIN"
    maybe_sudo cp "${tmpdir}/${BINARY}" "${INSTALL_BIN}/${BINARY}"
    chmod +x "${INSTALL_BIN}/${BINARY}"

    maybe_sudo mkdir -p "${INSTALL_SHARE}/prompts"
    maybe_sudo cp "${tmpdir}/prompts/"*.md "${INSTALL_SHARE}/prompts/" 2>/dev/null || true

    success "Built and installed from source."
}

# ── Main ──────────────────────────────────────────────────────────────────────

main() {
    echo ""
    echo -e "${BOLD}  agentflow — Agentic AI Development Pipeline${RESET}"
    echo -e "  ${BLUE}https://github.com/${REPO}${RESET}"
    echo ""

    need_cmd curl
    need_cmd tar

    local platform
    platform=$(detect_platform)
    info "Detected platform: ${platform}"

    local version
    version=$(get_latest_version)
    info "Latest version: ${version}"

    header "Installing..."

    if ! install_binary "$platform" "$version" 2>/dev/null; then
        warn "Binary download failed. Attempting to build from source..."
        install_from_source
    fi

    install_prompts "$version"
    install_config
    ensure_in_path
    setup_api_key
    print_summary
}

main "$@"
