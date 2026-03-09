# Internal LLM Client

This package contains the internal HTTP client used for optional BYOK-based alert classification paths in AMP.

It is an internal package for the current Go module, not a standalone public SDK or a general provider compatibility guarantee.

## Verified Request Modes

Current code and tests cover these provider modes:

- `provider=proxy`
  - classify: `POST <base_url>/classify`
  - health: `GET <base_url>/health`
- `provider=openai`
- `provider=openai-compatible`
- `provider=openai_compatible`
  - classify: `POST <base_url>/chat/completions`
  - health: `GET <base_url>/models`

For OpenAI-compatible mode, set `base_url` to the API root such as `https://api.openai.com/v1`, not to `/chat/completions`.

If you use another provider, treat that as external integration work unless it is exposed behind one of the request modes above.

## Config Shape

The exported `Config` type includes:

- `Provider`
- `BaseURL`
- `APIKey`
- `Model`
- `MaxTokens`
- `Temperature`
- `Timeout`
- `MaxRetries`
- `RetryDelay`
- `RetryBackoff`
- `EnableMetrics`
- `CircuitBreaker`

### OpenAI-Compatible Example

```yaml
llm:
  enabled: true
  provider: openai
  base_url: https://api.openai.com/v1
  api_key: ${LLM_API_KEY}
  model: gpt-4o
  timeout: 30s
  max_retries: 3
```

### Proxy Example

```yaml
llm:
  enabled: true
  provider: proxy
  base_url: https://your-proxy.example.com
  api_key: ${LLM_API_KEY}
  model: your-model
  timeout: 30s
  max_retries: 3
```

## Package Capabilities

The current package provides:

- an HTTP client with request timeouts
- retry handling for retryable failures
- circuit breaker support
- Prometheus-oriented circuit breaker metrics helpers
- a mock client for tests via `NewMockLLMClient`

See:

- [client.go](./client.go)
- [client_provider_test.go](./client_provider_test.go)
- [circuit_breaker.go](./circuit_breaker.go)
- [errors.go](./errors.go)

## Usage Notes

- Supply your own API key through config or environment management.
- Do not hardcode secrets in committed files.
- This package does not, by itself, guarantee provider quality, model quality, benchmark numbers, or production readiness.

## Related Paths

- [Repository README](../../../../README.md)
- [Contributing Guide](../../../../CONTRIBUTING.md)
- [examples/README.md](../../../../examples/README.md)

## License

This package is covered by the repository's AGPL-3.0 license. See [LICENSE](../../../../LICENSE).
