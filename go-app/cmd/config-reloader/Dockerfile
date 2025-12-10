# ================================================================================
# Config Reloader Sidecar - Multi-stage Dockerfile
# ================================================================================
# Lightweight sidecar container for watching config changes and triggering reload
#
# Features:
# - Multi-stage build (builder + runtime)
# - Minimal runtime image (distroless)
# - Security: non-root user, read-only filesystem
# - Size target: < 10MB
#
# Build: docker build -t amp-config-reloader:latest -f cmd/config-reloader/Dockerfile .
# Run: docker run --pid=container:amp amp-config-reloader:latest
#
# Author: AI Assistant
# Date: 2024-12-10

# ================================================================================
# Stage 1: Builder
# ================================================================================
FROM golang:1.23-alpine AS builder

WORKDIR /build

# Install build dependencies
RUN apk add --no-cache git ca-certificates tzdata

# Copy go mod files
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY cmd/config-reloader/ ./cmd/config-reloader/

# Build binary (static, no CGO)
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -ldflags="-w -s -X main.appVersion=$(git describe --tags --always --dirty 2>/dev/null || echo 'dev')" \
    -o /build/config-reloader \
    ./cmd/config-reloader

# ================================================================================
# Stage 2: Runtime
# ================================================================================
FROM gcr.io/distroless/static-debian12:nonroot

# Copy binary from builder
COPY --from=builder /build/config-reloader /usr/local/bin/config-reloader

# Copy CA certificates for HTTPS
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/

# Metadata
LABEL org.opencontainers.image.title="AMP Config Reloader"
LABEL org.opencontainers.image.description="Sidecar container for hot config reload"
LABEL org.opencontainers.image.version="1.0.0"
LABEL org.opencontainers.image.authors="AMP Team"
LABEL org.opencontainers.image.source="https://github.com/ipiton/AMP"

# Expose metrics port
EXPOSE 9091

# Health check
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD ["/usr/local/bin/config-reloader", "-h"]

# Run as non-root user (distroless nonroot user: 65532)
USER 65532:65532

# Entrypoint
ENTRYPOINT ["/usr/local/bin/config-reloader"]

# Default args
CMD ["--config-file=/etc/amp/config.yaml", "--reload-url=http://localhost:8080/health/reload", "--interval=5s"]

