# ── Stage 1: Build ────────────────────────────────────────────────────────────
FROM golang:1.22-alpine AS builder

WORKDIR /build

# Install build dependencies
RUN apk add --no-cache git ca-certificates tzdata

# Copy module files first for layer caching
COPY go.mod go.sum* ./
RUN go mod download

# Copy source
COPY . .

# Build with optimizations — strip debug symbols for smaller binary
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build \
    -ldflags="-w -s -X main.version=$(git describe --tags --always --dirty 2>/dev/null || echo dev)" \
    -o /agentflow \
    ./cmd/agentflow

# ── Stage 2: Runtime ──────────────────────────────────────────────────────────
FROM alpine:3.19

# Security: run as non-root
RUN addgroup -g 1001 agentflow && \
    adduser -u 1001 -G agentflow -s /bin/sh -D agentflow

# Install runtime tools that the gate may need (language-agnostic detection)
# Gate uses system-installed tools — add what your target project needs
RUN apk add --no-cache \
    ca-certificates \
    tzdata \
    git \
    bash

WORKDIR /app

# Copy binary from builder
COPY --from=builder /agentflow /usr/local/bin/agentflow

# Copy prompts (agents need these at runtime)
COPY --chown=agentflow:agentflow prompts/ ./prompts/
COPY --chown=agentflow:agentflow configs/config.json ./config.json

# Directories the app writes to
RUN mkdir -p /app/workspace /app/registry && \
    chown -R agentflow:agentflow /app

USER agentflow

# Persist workspace and registry across container restarts
VOLUME ["/app/workspace", "/app/registry"]

ENTRYPOINT ["agentflow"]
CMD ["--help"]

# ── Labels ────────────────────────────────────────────────────────────────────
LABEL org.opencontainers.image.title="agentflow" \
      org.opencontainers.image.description="Agentic AI Development Pipeline" \
      org.opencontainers.image.source="https://github.com/agentflow/core"
