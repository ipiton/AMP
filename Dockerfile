# Multi-stage build for Alertmanager++ (Go)
FROM golang:1.26-alpine AS builder

RUN apk add --no-cache git make ca-certificates

WORKDIR /build

COPY go-app/go.mod go-app/go.sum ./
RUN go mod download

COPY go-app/ ./

# PARITY-4.2: version metadata surfaced via GET /api/v2/status versionInfo.
# Pass --build-arg VERSION=... etc at build time; defaults keep local
# `docker build` usable without extra flags.
ARG VERSION=dev
ARG REVISION=unknown
ARG BRANCH=unknown
ARG BUILD_DATE=unknown
ARG BUILDINFO_PKG=github.com/ipiton/AMP/internal/buildinfo

RUN CGO_ENABLED=0 go build -ldflags="-s -w \
    -X ${BUILDINFO_PKG}.Version=${VERSION} \
    -X ${BUILDINFO_PKG}.Revision=${REVISION} \
    -X ${BUILDINFO_PKG}.Branch=${BRANCH} \
    -X ${BUILDINFO_PKG}.BuildUser=docker \
    -X ${BUILDINFO_PKG}.BuildDate=${BUILD_DATE}" \
    -o amp ./cmd/server

# Runtime
FROM alpine:3.19

RUN apk add --no-cache ca-certificates tzdata && \
    adduser -D -u 10001 appuser

WORKDIR /app

COPY --from=builder /build/amp /app/
COPY --from=builder /build/migrations /app/migrations

USER appuser

EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD wget --quiet --tries=1 --spider http://localhost:8080/healthz || exit 1

CMD ["/app/amp"]
