# Multi-stage build for Alertmanager++ (Go)
FROM golang:1.22-alpine AS builder

RUN apk add --no-cache git make ca-certificates

WORKDIR /build

COPY go-app/go.mod go-app/go.sum ./
RUN go mod download

COPY go-app/ ./

RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o amp ./cmd/server

# Runtime
FROM alpine:3.19

RUN apk add --no-cache ca-certificates tzdata && \
    adduser -D -u 10001 appuser

WORKDIR /app

COPY --from=builder /build/amp /app/
COPY --from=builder /build/migrations /app/migrations

USER appuser

EXPOSE 9093

CMD ["/app/amp"]
