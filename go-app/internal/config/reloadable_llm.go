package config

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/ipiton/AMP/internal/infrastructure/llm"
)

// ================================================================================
// LLMReloadable (INF-A slice 1)
// ================================================================================

// llmReloadPriority: after logger/metrics, before the connection pools. The
// swap is in-process and cannot fail, so it costs nothing to do early.
const llmReloadPriority = 50

// llmRestartRequiredFields are llm.* fields that decide whether the
// investigation pipeline EXISTS, not how it talks to the provider.
// ServiceRegistry.initializeInvestigation reads them once at startup: it skips
// the whole pipeline when llm.enabled=false, and builds the agentic tool
// registry + agent loop only when llm.agent_mode=true. Neither can be created
// or torn down from here — the queue's workers, repository and tool registry
// are wired into the alert pipeline at construction.
var llmRestartRequiredFields = map[string]bool{
	"llm.enabled":    true,
	"llm.agent_mode": true,
}

// LLMReloadable hot-reloads the LLM client's provider settings.
//
// What is real: provider, base_url, api_key, model, max_tokens, temperature,
// timeout and max_retries are swapped atomically on the live
// *llm.HTTPLLMClient under its RWMutex, so the investigation pipeline's next
// request uses the new model/provider without a restart. Requests already in
// flight finish against the config snapshot they started with — a reload never
// mixes a new base URL with an old API key inside one request.
//
// What is NOT real, and says so (W604): llm.enabled and llm.agent_mode. Those
// gate whether the investigation pipeline and the agentic loop were built at
// all (see llmRestartRequiredFields).
type LLMReloadable struct {
	client   *llm.HTTPLLMClient
	logger   *slog.Logger
	warnings *RestartWarnings
}

// NewLLMReloadable wires an LLMReloadable over the live LLM client. A nil
// client is legal and expected whenever the investigation pipeline was not
// built (lite profile, investigation.enabled=false, llm.enabled=false); in
// that case an llm.* change is reported as restart-required.
func NewLLMReloadable(
	client *llm.HTTPLLMClient,
	warnings *RestartWarnings,
	logger *slog.Logger,
) *LLMReloadable {
	if logger == nil {
		logger = slog.Default()
	}
	return &LLMReloadable{client: client, logger: logger, warnings: warnings}
}

// Name implements Reloadable.
func (l *LLMReloadable) Name() string { return "llm" }

// RelevantSections implements Reloadable.
func (l *LLMReloadable) RelevantSections() []string { return []string{"llm"} }

// IsCritical implements Reloadable: AMP routes and notifies without the LLM.
func (l *LLMReloadable) IsCritical() bool { return false }

// ReloadPriority implements OrderedReloadable.
func (l *LLMReloadable) ReloadPriority() int { return llmReloadPriority }

// Reload implements Reloadable.
func (l *LLMReloadable) Reload(_ context.Context, oldCfg, newCfg *Config) error {
	if newCfg == nil {
		return fmt.Errorf("llm reload: nil config")
	}

	var fields []string
	if oldCfg != nil {
		fields = changedFields("llm", oldCfg.LLM, newCfg.LLM)
		if len(fields) == 0 {
			return nil
		}
	}

	lifecycleFields := make([]string, 0, 2)
	for _, field := range fields {
		if llmRestartRequiredFields[field] {
			lifecycleFields = append(lifecycleFields, field)
		}
	}
	if len(lifecycleFields) > 0 {
		warnRestartRequired(l.logger, l.warnings, RestartRequiredWarning{
			Code:      WarnLLMRestartRequired,
			Component: l.Name(),
			Fields:    lifecycleFields,
			Reason:    "llm.enabled and llm.agent_mode decide whether the investigation pipeline and the agentic tool loop are built at startup; they cannot be created or torn down on a running process — restart to apply (provider/model/api_key/timeout ARE applied live)",
		})
	}

	if l.client == nil {
		if len(fields) > len(lifecycleFields) {
			warnRestartRequired(l.logger, l.warnings, RestartRequiredWarning{
				Code:      WarnLLMRestartRequired,
				Component: l.Name(),
				Fields:    fields,
				Reason:    "no LLM client in this process (investigation pipeline not built: lite profile, investigation.enabled=false, or llm.enabled=false at startup); restart to apply the new LLM settings",
			})
		}
		return nil
	}

	// Only the transport-level half is applied; the lifecycle half was warned
	// about above. Feeding the whole section in is intentional — the client's
	// own config carries no enabled/agent_mode notion at all.
	l.client.UpdateConfig(llmClientConfigFrom(newCfg.LLM, l.client.GetConfig()))

	l.logger.Info("LLM client reloaded from config",
		"provider", newCfg.LLM.Provider,
		"model", newCfg.LLM.Model,
		"fields", fields,
	)
	return nil
}

// llmClientConfigFrom maps AMP's llm config section onto the llm package's own
// config, preserving the fields that section does not own.
//
// `current` is the client's live config, not llm.DefaultConfig(): the circuit
// breaker settings, RetryDelay, RetryBackoff and EnableMetrics come from the
// client's construction and are NOT exposed in AMP's llm.* YAML. Rebuilding
// them from defaults would silently reset them on every reload.
func llmClientConfigFrom(cfg LLMConfig, current llm.Config) llm.Config {
	next := current
	next.Provider = cfg.Provider
	next.BaseURL = cfg.BaseURL
	next.APIKey = cfg.APIKey
	next.Model = cfg.Model
	next.MaxTokens = cfg.MaxTokens
	next.Temperature = cfg.Temperature
	if cfg.Timeout > 0 {
		next.Timeout = cfg.Timeout
	}
	if cfg.MaxRetries > 0 {
		next.MaxRetries = cfg.MaxRetries
	}
	return next
}

// Compile-time contract checks.
var (
	_ Reloadable        = (*LLMReloadable)(nil)
	_ OrderedReloadable = (*LLMReloadable)(nil)
)
