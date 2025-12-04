# Changelog

## [1.1.0] - 2024-08-27

### Added
- **LLM Integration Support**
  - GPT-4 powered alert classification
  - Three enrichment modes: transparent, transparent_with_recommendations, enriched
  - LLM API key configuration via secrets or external secrets
  - LLM proxy URL configuration (https://llm-proxy.b2broker.tech)
  - LLM timeout and retry configuration

### Enhanced
- **Deployment Configuration**
  - Updated deployment template with LLM environment variables
  - Added LLM secret template support
  - Enhanced configmap with LLM configuration
  - Updated secret template to include LLM API keys

### Configuration
- **New LLM Settings**
  - `llm.enabled` - Enable/disable LLM integration
  - `llm.apiKey` - LLM API key (set via secret in production)
  - `llm.proxyUrl` - LLM proxy URL
  - `llm.model` - LLM model (default: openai/gpt-4o)
  - `llm.timeout` - Request timeout (default: 30s)
  - `llm.maxRetries` - Retry attempts (default: 3)
  - `llm.retryDelay` - Retry delay (default: 1.0s)
  - `llm.cacheTtl` - Cache TTL (default: 3600s)
  - `llm.batchSize` - Batch size (default: 10)

### Documentation
- **Updated README.md**
  - Added LLM integration examples
  - Updated installation instructions
  - Added LLM configuration documentation
  - Enhanced troubleshooting section

### Files Added
- `values-production.yaml` - Production configuration with LLM
- `values-dev.yaml` - Development configuration with LLM
- `DEPLOYMENT.md` - Deployment guide with LLM integration
- `CHANGELOG.md` - This changelog

### Breaking Changes
- None

### Migration Guide
- For existing deployments, add LLM configuration to your values file:
  ```yaml
  llm:
    enabled: true
    apiKey: "your-api-key"
    proxyUrl: "https://llm-proxy.b2broker.tech"
    model: "openai/gpt-4o"
  ```

## [1.0.0] - 2024-08-20

### Initial Release
- Basic Alert History Service
- PostgreSQL and SQLite support
- Redis/DragonflyDB caching
- Prometheus metrics
- Horizontal scaling support
- Basic webhook processing
