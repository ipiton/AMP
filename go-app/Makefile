# Makefile for Alert History Service (Go version)
.PHONY: build test lint run clean help deps fmt vet mod-tidy

# Go parameters
GOCMD=go
GOBUILD=$(GOCMD) build
GOCLEAN=$(GOCMD) clean
GOTEST=$(GOCMD) test
GOGET=$(GOCMD) get
GOMOD=$(GOCMD) mod
GOFMT=$(GOCMD) fmt
GOVET=$(GOCMD) vet

# Main package path
MAIN_PATH=./cmd/server
BINARY_NAME=server
BINARY_UNIX=$(BINARY_NAME)_unix

# Build the project
build:
	@echo "Building $(BINARY_NAME)..."
	$(GOBUILD) -o $(BINARY_NAME) -v $(MAIN_PATH)
	@echo "Build complete: $(BINARY_NAME)"

# Build for Linux (for Docker)
build-linux:
	@echo "Building $(BINARY_NAME) for Linux..."
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 $(GOBUILD) -o $(BINARY_UNIX) -v $(MAIN_PATH)
	@echo "Linux build complete: $(BINARY_UNIX)"

# Run tests
test:
	@echo "Running tests..."
	$(GOTEST) -v ./...

# Run tests with coverage
test-coverage:
	@echo "Running tests with coverage..."
	$(GOTEST) -race -coverprofile=coverage.out -covermode=atomic ./...
	$(GOCMD) tool cover -html=coverage.out -o coverage.html

# Run linter (requires golangci-lint to be installed)
lint:
	@echo "Running linter..."
	$(shell go env GOPATH)/bin/golangci-lint run

# Format code
fmt:
	@echo "Formatting code..."
	$(GOFMT) ./...

# Run vet (Go's static analyzer)
vet:
	@echo "Running go vet..."
	$(GOVET) ./...

# Tidy dependencies
mod-tidy:
	@echo "Tidying dependencies..."
	$(GOMOD) tidy

# Download dependencies
deps:
	@echo "Downloading dependencies..."
	$(GOMOD) download

# Run the application
run:
	@echo "Running application..."
	$(GOCMD) run $(MAIN_PATH)

# Run with hot reload (requires air to be installed)
dev:
	@echo "Running in development mode with hot reload..."
	air

# Clean build artifacts
clean:
	@echo "Cleaning..."
	$(GOCLEAN)
	rm -f $(BINARY_NAME)
	rm -f $(BINARY_UNIX)
	rm -f coverage.out coverage.html
	@echo "Clean complete"

# Install development tools
install-tools:
	@echo "Installing development tools..."
	$(GOGET) -u github.com/cosmtrek/air
	$(GOGET) -u github.com/golangci/golangci-lint/cmd/golangci-lint@latest

# Include advanced migration commands from Makefile.migrations
include Makefile.migrations

# Database URL for migrations (uses environment variables)
DATABASE_URL ?= postgres://$(DB_USER):$(DB_PASSWORD)@$(DB_HOST):$(DB_PORT)/$(DB_NAME)?sslmode=$(DB_SSL_MODE)

# Legacy migration commands (for backward compatibility)
# Use 'make -f Makefile.migrations <command>' for advanced features

# Create new migration (requires goose to be installed)
migrate-create:
	@echo "Creating new migration..."
	@if [ -z "$(name)" ]; then echo "Usage: make migrate-create name=migration_name"; exit 1; fi
	$(shell go env GOPATH)/bin/goose -dir migrations create $(name) sql

# Run migrations up (requires goose to be installed)
migrate-up:
	@echo "Running migrations up..."
	$(shell go env GOPATH)/bin/goose -dir migrations postgres "$(DATABASE_URL)" up

# Run migrations down (requires goose to be installed)
migrate-down:
	@echo "Running migrations down..."
	$(shell go env GOPATH)/bin/goose -dir migrations postgres "$(DATABASE_URL)" down

# Show migration status (requires goose to be installed)
migrate-status:
	@echo "Checking migration status..."
	$(shell go env GOPATH)/bin/goose -dir migrations postgres "$(DATABASE_URL)" status

# Docker commands
docker-build:
	@echo "Building Docker image..."
	docker build -t alert-history:latest .
	@echo "Docker image built successfully"

docker-build-multi:
	@echo "Building multi-architecture Docker image..."
	docker buildx build \
		--platform linux/amd64,linux/arm64 \
		--tag alert-history:multi-arch \
		--push .
	@echo "Multi-arch image built and pushed"

docker-build-dev:
	@echo "Building development Docker image..."
	docker build -f Dockerfile.dev -t alert-history:dev .
	@echo "Development image built"

docker-run:
	@echo "Running Docker container..."
	docker run -p 8080:8080 --rm alert-history:latest

docker-run-detached:
	@echo "Running Docker container in background..."
	docker run -d -p 8080:8080 --name alert-history alert-history:latest
	@echo "Container started. Use 'make docker-stop' to stop it"

docker-run-dev:
	@echo "Running development container with hot reload..."
	docker run -p 8080:8080 -v $(PWD):/app alert-history:dev

docker-stop:
	@echo "Stopping Docker container..."
	docker stop alert-history || true
	docker rm alert-history || true
	@echo "Container stopped"

docker-logs:
	@echo "Showing Docker container logs..."
	docker logs alert-history

docker-clean:
	@echo "Cleaning Docker images..."
	docker rmi alert-history:latest alert-history:multi-arch alert-history:dev || true
	docker system prune -f
	@echo "Docker images cleaned"

docker-health-check:
	@echo "Checking container health..."
	@timeout 10 docker exec alert-history /server --version || echo "Container health check failed"
	@echo "Health check complete"

# Docker Compose commands
compose-up:
	@echo "Starting services with Docker Compose..."
	docker-compose up -d
	@echo "Services started"

compose-down:
	@echo "Stopping services with Docker Compose..."
	docker-compose down
	@echo "Services stopped"

compose-logs:
	@echo "Showing Docker Compose logs..."
	docker-compose logs -f

compose-build:
	@echo "Building services with Docker Compose..."
	docker-compose build
	@echo "Services built"

# Include Docker Compose development commands
include ../Makefile.docker

# Bake commands (advanced multi-arch builds)
bake-build:
	@echo "Building with Docker Bake (multi-arch)..."
	docker buildx bake --push
	@echo "Multi-arch build completed"

bake-dev:
	@echo "Building development version with Docker Bake..."
	docker buildx bake dev
	@echo "Development build completed"

bake-local:
	@echo "Building local version with Docker Bake..."
	docker buildx bake local
	@echo "Local build completed"

# Show available commands
help:
	@echo "🚀 Alert History Service - Available Commands"
	@echo ""
	@echo "📦 Build & Development:"
	@echo "  build           - Build the application"
	@echo "  build-linux     - Build for Linux (Docker)"
	@echo "  test            - Run tests"
	@echo "  test-coverage   - Run tests with coverage report"
	@echo "  lint            - Run linter (requires golangci-lint)"
	@echo "  fmt             - Format code"
	@echo "  vet             - Run go vet"
	@echo "  mod-tidy        - Tidy dependencies"
	@echo "  deps            - Download dependencies"
	@echo "  run             - Run the application"
	@echo "  dev             - Run with hot reload (requires air)"
	@echo "  clean           - Clean build artifacts"
	@echo "  install-tools   - Install development tools"
	@echo ""
	@echo "🐦 Database Migrations (Legacy):"
	@echo "  migrate-create  - Create new migration (requires goose)"
	@echo "  migrate-up      - Run migrations up"
	@echo "  migrate-down    - Run migrations down"
	@echo "  migrate-status  - Show migration status"
	@echo ""
	@echo "🛠️  Advanced Migration System:"
	@echo "  Use 'make -f Makefile.migrations help' for full migration features"
	@echo "  Features: backup, health checks, multi-driver, error recovery"
	@echo ""
	@echo "🐳 Docker Commands:"
	@echo "  docker-build        - Build single-arch Docker image"
	@echo "  docker-build-multi  - Build multi-arch (AMD64+ARM64) image with push"
	@echo "  docker-build-dev    - Build development image with hot reload"
	@echo "  docker-run          - Run Docker container"
	@echo "  docker-run-detached - Run container in background"
	@echo "  docker-run-dev      - Run development container"
	@echo "  docker-stop         - Stop running container"
	@echo "  docker-logs         - Show container logs"
	@echo "  docker-clean        - Remove Docker images"
	@echo "  docker-health-check - Check container health"
	@echo ""
	@echo "🐙 Docker Compose Commands:"
	@echo "  compose-up          - Start services"
	@echo "  compose-down        - Stop services"
	@echo "  compose-logs        - Show service logs"
	@echo "  compose-build       - Build services"
	@echo ""
	@echo "🎯 Advanced Docker Commands:"
	@echo "  bake-build          - Multi-arch build with Bake"
	@echo "  bake-dev            - Development build with Bake"
	@echo "  bake-local          - Local build with Bake"
	@echo ""
	@echo "📖 Quick Start:"
	@echo "  1. make deps                    # Install dependencies"
	@echo "  2. make -f Makefile.migrations setup  # Setup migration system"
	@echo "  3. make test                    # Run tests"
	@echo "  4. make build && make run       # Build and run application"
