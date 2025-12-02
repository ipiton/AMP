# BYK (Bring Your own Key) LLM Integration Plan

**Date:** 2025-12-02
**Status:** 🚧 **NEEDS IMPLEMENTATION**

---

## ❌ **Проблема**

В текущей OSS версии **удален весь LLM код**, но:
- ✅ BYK (Bring Your own Key) LLM должен быть **базовым OSS функционалом**
- ❌ Удалили и проприетарный, и OSS-friendly код

---

## ✅ **Решение: BYK LLM Integration**

### Что должно быть в OSS:

#### 1️⃣ **Generic LLM Client** (прямая интеграция)
```go
// Прямая интеграция с публичными LLM API
- OpenAI API (gpt-4, gpt-3.5-turbo)
- Anthropic Claude API (claude-3-opus, claude-3-sonnet)
- Google Gemini API
- Local LLMs (Ollama, LM Studio)
```

**Конфигурация через ENV:**
```bash
LLM_ENABLED=true
LLM_PROVIDER=openai         # openai, anthropic, google, ollama
LLM_API_KEY=sk-...          # User's API key (BYK!)
LLM_MODEL=gpt-4o
LLM_BASE_URL=https://api.openai.com/v1  # Optional override
```

#### 2️⃣ **Classification Service**
```go
// Classification с кешированием и fallback
- L1 cache (in-memory)
- L2 cache (Redis)
- Intelligent fallback (rule-based)
- Batch processing
- Circuit breaker
```

#### 3️⃣ **Enrichment Service**
```go
// Enrichment modes
- transparent: No AI (default)
- enriched: Add AI classification
- transparent_with_recommendations: Show AI suggestions
```

#### 4️⃣ **Extension Example**
```go
// examples/custom-llm-classifier/
- Пример кастомной интеграции
- Использование pkg/core interfaces
```

---

## ❌ **Что НЕ должно быть в OSS:**

1. ❌ **Проприетарные промпты** (если есть секретные)
2. ❌ **Платный LLM прокси** (llm-proxy.b2broker.tech)
3. ❌ **Enterprise-only провайдеры** (если есть эксклюзивные)
4. ❌ **Paid features** (advanced tuning, custom models)

---

## 📋 **Implementation Plan**

### Phase 1: Core LLM Client (2-3 hours)
```
✅ Создать pkg/llm/
├── client.go         - LLMClient interface
├── openai.go         - OpenAI implementation
├── anthropic.go      - Anthropic implementation
├── local.go          - Local LLM (Ollama)
└── errors.go         - Error types
```

**Features:**
- Direct API integration (no proxy)
- Standard OpenAI/Anthropic SDK
- Retry with exponential backoff
- Context timeout support
- Streaming support (optional)

### Phase 2: Classification Service (1-2 hours)
```
✅ Создать internal/core/services/
├── classification.go         - ClassificationService interface
├── classification_impl.go    - Implementation
├── classification_cache.go   - Two-tier caching
├── classification_fallback.go - Rule-based fallback
└── classification_test.go    - Tests
```

**Features:**
- Two-tier caching (L1 memory + L2 Redis)
- Circuit breaker (via resilience package)
- Intelligent fallback
- Batch processing
- Prometheus metrics

### Phase 3: Enrichment Service (1 hour)
```
✅ Создать internal/core/services/
├── enrichment.go       - EnrichmentService interface
├── enrichment_impl.go  - Implementation
├── enrichment_modes.go - transparent/enriched/recommendations
└── enrichment_test.go  - Tests
```

**Features:**
- Mode toggle (Redis-backed)
- Graceful degradation
- Performance tracking

### Phase 4: Integration (1 hour)
```
✅ Обновить main.go
- Optional LLM initialization (if LLM_ENABLED=true)
- Classification service registration
- Enrichment mode manager
- Alert processor integration
```

### Phase 5: Documentation (1 hour)
```
✅ Создать docs/
├── BYK_LLM_GUIDE.md           - User guide
├── LLM_PROVIDERS.md           - Provider comparison
└── MIGRATION_FROM_PROXY.md    - Migration from proprietary proxy
```

### Phase 6: Examples (1 hour)
```
✅ Создать examples/
└── custom-llm-classifier/
    ├── main.go              - Custom LLM integration example
    ├── provider.go          - Custom provider
    └── README.md            - Documentation
```

---

## 🎯 **Expected Timeline**

| Phase | Duration | Priority |
|-------|----------|----------|
| Phase 1: Core LLM Client | 2-3h | P0 (Critical) |
| Phase 2: Classification Service | 1-2h | P0 (Critical) |
| Phase 3: Enrichment Service | 1h | P1 (High) |
| Phase 4: Integration | 1h | P0 (Critical) |
| Phase 5: Documentation | 1h | P1 (High) |
| Phase 6: Examples | 1h | P2 (Medium) |

**Total:** 7-9 hours

---

## 📊 **Benefits**

### For Users:
- ✅ **Free AI classification** (using their own API keys)
- ✅ **Choice of provider** (OpenAI, Anthropic, Google, Local)
- ✅ **No vendor lock-in** (standard APIs)
- ✅ **Privacy-friendly** (no third-party proxy)
- ✅ **Cost control** (their own billing)

### For Project:
- ✅ **Competitive feature** (vs Alertmanager)
- ✅ **Community adoption** (AI is trendy)
- ✅ **Extension point** (custom classifiers)
- ✅ **100% OSS** (no proprietary code)

---

## 🔧 **Technical Requirements**

### Dependencies:
```go
// OpenAI SDK
"github.com/sashabaranov/go-openai" v1.20.0

// Anthropic SDK (community)
"github.com/liushuangls/go-anthropic" v0.5.0

// Ollama SDK
"github.com/ollama/ollama/api" latest
```

### Configuration:
```yaml
llm:
  enabled: true                          # Default: false
  provider: openai                       # openai, anthropic, google, ollama
  api_key: ${LLM_API_KEY}               # Required if enabled
  model: gpt-4o                         # Provider-specific
  base_url: https://api.openai.com/v1   # Optional override
  timeout: 30s
  max_retries: 3
  enable_cache: true
  cache_ttl: 24h
  enable_fallback: true
```

---

## ✅ **Acceptance Criteria**

### Must Have (MVP):
- [ ] OpenAI integration working
- [ ] Classification service with caching
- [ ] Enrichment modes (transparent/enriched)
- [ ] Alert processor integration
- [ ] Basic documentation
- [ ] Configuration via ENV

### Nice to Have (Post-MVP):
- [ ] Anthropic integration
- [ ] Google Gemini integration
- [ ] Local LLM support (Ollama)
- [ ] Streaming support
- [ ] Custom prompt templates
- [ ] Fine-tuning support

---

## 🚀 **Next Steps**

1. **Review this plan** with team
2. **Start Phase 1** (Core LLM Client)
3. **Update ROADMAP.md** (add BYK LLM to v1.1.0)
4. **Create GitHub issue** (track progress)
5. **Communicate to community** (feature announcement)

---

## 📚 **References**

- OpenAI API: https://platform.openai.com/docs/api-reference
- Anthropic API: https://docs.anthropic.com/claude/reference
- Ollama: https://ollama.ai/
- BYK Pattern: https://en.wikipedia.org/wiki/Bring_your_own_key

---

**Status:** READY FOR IMPLEMENTATION
**Priority:** P0 (Should be in v1.0.0 or v1.1.0)
**Estimated Effort:** 7-9 hours
