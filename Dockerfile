# Multi-stage build for Alertmanager++ (Go)
# Produces minimal production image (~50MB)

# Stage 1: Builder
FROM golang:1.22-alpine AS builder

# Install build dependencies
RUN apk add --no-cache git make ca-certificates tzdata

# Set working directory
WORKDIR /build

# Copy go.mod and go.sum first (better caching)
COPY go-app/go.mod go-app/go.sum ./
RUN go mod download

# Copy source code
COPY go-app/ ./

# Build binary
# CGO_ENABLED=0 for static binary (no C dependencies)
# -ldflags="-s -w" to strip debug info and reduce size
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -ldflags="-s -w -X main.version=$(git describe --tags --always --dirty)" \
    -o /build/amp \
    ./cmd/server

# Stage 2: Runtime
FROM alpine:3.19

# Install runtime dependencies
RUN apk add --no-cache ca-certificates tzdata

# Create non-root user
RUN addgroup -g 10001 -S appuser && \
    adduser -u 10001 -S -G appuser -h /app appuser

# Set working directory
WORKDIR /app

# Copy binary from builder
COPY --from=builder /build/amp /app/amp

# Copy migrations (if needed)
COPY --from=builder /build/migrations /app/migrations

# Set ownership
RUN chown -R appuser:appuser /app

# Switch to non-root user
USER appuser

# Expose port
EXPOSE 9093

# Health check
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD ["/app/amp", "healthz"] || exit 1

# Environment variables (can be overridden)
ENV SERVER_PORT=9093 \
    LOG_LEVEL=info \
    PROFILE=lite

# Run application
CMD ["/app/amp"]

