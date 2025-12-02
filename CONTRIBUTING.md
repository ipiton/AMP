# Contributing to Alertmanager++

Thank you for considering contributing to Alertmanager++! 🎉

## 📋 Code of Conduct

This project adheres to the [Contributor Covenant Code of Conduct](CODE_OF_CONDUCT.md).
By participating, you are expected to uphold this code.

## 🚀 How to Contribute

### Reporting Bugs

1. Check if the issue already exists in [Issues](https://github.com/ipiton/AMP/issues)
2. Create a new issue with:
   - Clear description
   - Steps to reproduce
   - Expected vs actual behavior
   - Environment details (OS, Go version, deployment method)

### Suggesting Features

1. Check [GitHub Discussions](https://github.com/ipiton/AMP/discussions) first
2. Create a new discussion with:
   - Use case description
   - Expected behavior
   - Alternative solutions considered

### Submitting Pull Requests

1. **Fork** the repository
2. **Create a feature branch**: `git checkout -b feature/amazing-feature`
3. **Make your changes** with clear, focused commits
4. **Add tests** for new functionality
5. **Run tests**: `cd go-app && make test`
6. **Run linter**: `make lint`
7. **Commit**: `git commit -m 'feat: add amazing feature'`
8. **Push**: `git push origin feature/amazing-feature`
9. **Create Pull Request** with detailed description

## 💻 Development Setup

### Prerequisites

- Go 1.22+
- Docker & Docker Compose
- Make
- golangci-lint

### Local Setup

```bash
# Clone repository
git clone https://github.com/ipiton/AMP.git
cd AMP

# Install dependencies
cd go-app
go mod download

# Build
make build

# Run tests
make test

# Run locally
./bin/server
```

## 📝 Commit Message Convention

We follow [Conventional Commits](https://www.conventionalcommits.org/):

- `feat:` New feature
- `fix:` Bug fix
- `docs:` Documentation changes
- `test:` Test additions/modifications
- `refactor:` Code refactoring
- `chore:` Maintenance tasks

Examples:
```
feat: add custom classifier interface
fix: resolve race condition in cache
docs: update migration guide
test: add integration tests for silence API
```

## 🧪 Testing Guidelines

- Write tests for all new functionality
- Maintain or improve test coverage (target: 80%+)
- Include unit tests and integration tests
- Use table-driven tests where appropriate

```go
func TestFeature(t *testing.T) {
    tests := []struct {
        name     string
        input    string
        expected string
    }{
        {"case1", "input1", "output1"},
        {"case2", "input2", "output2"},
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            // Test implementation
        })
    }
}
```

## 📦 Project Structure

```
AMP/
├── go-app/
│   ├── cmd/server/         # Application entry point
│   ├── internal/
│   │   ├── application/    # Application framework
│   │   ├── business/       # Business logic
│   │   ├── infrastructure/ # Infrastructure layer
│   │   └── config/         # Configuration
│   └── migrations/         # Database migrations
├── pkg/core/               # Core interfaces and domain models
├── examples/               # Extension examples
└── docs/                   # Documentation
```

## 🔍 Code Review Process

1. All PRs require at least one approval
2. CI checks must pass
3. Test coverage must not decrease
4. Code must follow project conventions
5. Documentation must be updated if needed

## 📊 Performance Considerations

- Profile before optimizing
- Add benchmarks for performance-critical code
- Target: <5ms p95 latency for API endpoints
- Memory efficiency: avoid unnecessary allocations

## 🔒 Security

- Never commit secrets or credentials
- Follow [SECURITY.md](SECURITY.md) guidelines
- Report vulnerabilities privately

## 📄 License

By contributing, you agree that your contributions will be licensed under the Apache 2.0 License.

## 💬 Questions?

- [GitHub Discussions](https://github.com/ipiton/AMP/discussions)
- [GitHub Issues](https://github.com/ipiton/AMP/issues)

Thank you for contributing! 🙌
