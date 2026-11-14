# Multi-stage build for AMP: one binary, amp-upstream (ADR-019)
#
# The builder runs on the BUILD platform and cross-compiles for the target
# (TARGETOS/TARGETARCH, set by buildx per --platform): a multi-arch build
# then compiles Go natively instead of under QEMU emulation. The Go patch
# version matches the `toolchain` line in go-app/go.mod -- bump them together.
FROM --platform=$BUILDPLATFORM golang:1.27.4-alpine AS builder

RUN apk add --no-cache git make ca-certificates

WORKDIR /build

COPY go-app/go.mod go-app/go.sum ./
RUN go mod download

COPY go-app/ ./

# Version metadata. prometheus/common/version is what /api/v2/status
# (versionInfo) and alertmanager_build_info report: Version is the upstream
# Alertmanager module from go.mod -- the API contract karma and amtool key
# on (ADR-009) -- and the rest describes this build. AMP's own version goes
# to internal/buildinfo (amp_build_info). Pass --build-arg VERSION=... etc
# at build time; defaults keep local `docker build` usable without flags.
ARG VERSION=dev
ARG REVISION=unknown
ARG BRANCH=unknown
ARG BUILD_DATE=unknown
ARG BUILDINFO_PKG=github.com/ipiton/AMP/internal/buildinfo
ARG VERSION_PKG=github.com/prometheus/common/version
ARG TARGETOS
ARG TARGETARCH

RUN UPSTREAM_VERSION="$(go list -m -f '{{if .Replace}}{{.Replace.Version}}{{else}}{{.Version}}{{end}}' github.com/prometheus/alertmanager)" && \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -ldflags="-s -w \
    -X ${VERSION_PKG}.Version=${UPSTREAM_VERSION#v} \
    -X ${VERSION_PKG}.Revision=${REVISION} \
    -X ${VERSION_PKG}.Branch=${BRANCH} \
    -X ${VERSION_PKG}.BuildUser=docker \
    -X ${VERSION_PKG}.BuildDate=${BUILD_DATE} \
    -X ${BUILDINFO_PKG}.Version=${VERSION} \
    -X ${BUILDINFO_PKG}.Revision=${REVISION} \
    -X ${BUILDINFO_PKG}.Branch=${BRANCH} \
    -X ${BUILDINFO_PKG}.BuildUser=docker \
    -X ${BUILDINFO_PKG}.BuildDate=${BUILD_DATE}" \
    -o amp-upstream ./cmd/amp-upstream

# Runtime
# Same Alpine branch as the golang:1.27.4-alpine builder.
FROM alpine:3.24

RUN apk add --no-cache ca-certificates tzdata && \
    adduser -D -u 10001 appuser && \
    mkdir -p /data /etc/amp && chown appuser /data

WORKDIR /app

COPY --from=builder /build/amp-upstream /app/
COPY --from=builder /build/migrations /app/migrations

USER appuser

# 9093: HTTP API, 9094: cluster gossip (TCP and UDP).
EXPOSE 9093 9094

# Docker only (Kubernetes uses the chart's probes). With --web.external-url
# carrying a path, override it: the endpoint moves to <path>/-/healthy.
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD wget --quiet --tries=1 --spider http://localhost:9093/-/healthy || exit 1

# Mount alertmanager.yml at /etc/amp and a volume at /data; add
# --amp.config.file for history, classification and investigation.
ENTRYPOINT ["/app/amp-upstream"]
CMD ["--config.file=/etc/amp/alertmanager.yml", "--storage.path=/data"]
