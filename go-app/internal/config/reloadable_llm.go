package config

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

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

	// mu guards applied — the llm config actually in effect. The transport
	// half moves when UpdateConfig succeeds; enabled/agent_mode never move,
	// so comparing against this keeps W604 alive for as long as the config
	// asks for a pipeline shape this process does not have (fix-round I2).
	mu      sync.Mutex
	applied LLMConfig
}

// NewLLMReloadable wires an LLMReloadable over the live LLM client. A nil
// client is legal and expected whenever the investigation pipeline was not
// built (lite profile, investigation.enabled=false, llm.enabled=false); in
// that case an llm.* change is reported as restart-required.
func NewLLMReloadable(
	client *llm.HTTPLLMClient,
	bootCfg *Config,
	warnings *RestartWarnings,
	logger *slog.Logger,
) *LLMReloadable {
	if logger == nil {
		logger = slog.Default()
	}
	reloadable := &LLMReloadable{client: client, logger: logger, warnings: warnings}
	if bootCfg != nil {
		reloadable.applied = bootCfg.LLM
	}
	return reloadable
}

// Name implements Reloadable.
func (l *LLMReloadable) Name() string { return "llm" }

// RelevantSections implements Reloadable.
func (l *LLMReloadable) RelevantSections() []string { return []string{"llm"} }

// IsCritical implements Reloadable: AMP routes and notifies without the LLM.
func (l *LLMReloadable) IsCritical() bool { return false }

// ReloadPriority implements OrderedReloadable.
func (l *LLMReloadable) ReloadPriority() int { return llmReloadPriority }

// NeedsResync implements ResyncReloadable: true while the requested llm config
// differs from what is in effect.
func (l *LLMReloadable) NeedsResync(newCfg *Config) bool {
	if newCfg == nil {
		return false
	}
	return len(l.drift(newCfg.LLM)) > 0
}

// drift returns the field paths where the requested config differs from what
// is in effect.
func (l *LLMReloadable) drift(requested LLMConfig) []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return changedFields("llm", l.applied, requested)
}

// Reload implements Reloadable.
func (l *LLMReloadable) Reload(_ context.Context, _, newCfg *Config) error {
	if newCfg == nil {
		return fmt.Errorf("llm reload: nil config")
	}

	fields := l.drift(newCfg.LLM)
	if len(fields) == 0 {
		l.warnings.Resolve(WarnLLMRestartRequired, l.Name())
		return nil
	}

	// Apply the transport half. Only the fields AMP's llm.* section owns move;
	// llm.enabled/llm.agent_mode stay where they were, which is what leaves
	// them visible in `remaining` below.
	if l.client != nil {
		l.client.UpdateConfig(llmClientConfigFrom(newCfg.LLM, l.client.GetConfig()))

		l.mu.Lock()
		lifecycle := LLMConfig{Enabled: l.applied.Enabled, AgentMode: l.applied.AgentMode}
		l.applied = newCfg.LLM
		l.applied.Enabled = lifecycle.Enabled
		l.applied.AgentMode = lifecycle.AgentMode
		l.mu.Unlock()

		l.logger.Info("LLM client reloaded from config",
			"provider", newCfg.LLM.Provider,
			"model", newCfg.LLM.Model,
			"fields", fields,
		)
	}

	remaining := l.drift(newCfg.LLM)
	if len(remaining) == 0 {
		l.warnings.Resolve(WarnLLMRestartRequired, l.Name())
		return nil
	}

	// ONE warning per component per attempt — see LoggerReloadable.Reload.
	reason := "llm.enabled and llm.agent_mode decide whether the investigation pipeline and the agentic tool loop are built at startup; they cannot be created or torn down on a running process — restart to apply (provider/model/api_key/timeout ARE applied live)"
	switch {
	case l.client == nil:
		reason = "no LLM client in this process (investigation pipeline and classification both skipped: lite profile, or llm.enabled=false at startup); restart to apply the new LLM settings"
	case !onlyLifecycleFields(remaining):
		// Should not happen: with a live client every transport field is
		// applied above. Say so rather than blaming the pipeline shape.
		reason = "the live LLM client did not adopt these fields; restart to apply"
	}

	warnRestartRequired(l.logger, l.warnings, RestartRequiredWarning{
		Code:      WarnLLMRestartRequired,
		Component: l.Name(),
		Fields:    remaining,
		Reason:    reason,
	})
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

// onlyLifecycleFields reports whether every field is one of the pipeline-shape
// fields that genuinely need a restart.
func onlyLifecycleFields(fields []string) bool {
	for _, field := range fields {
		if !llmRestartRequiredFields[field] {
			return false
		}
	}
	return true
}

// Compile-time contract checks.
var (
	_ Reloadable        = (*LLMReloadable)(nil)
	_ OrderedReloadable = (*LLMReloadable)(nil)
	_ ResyncReloadable  = (*LLMReloadable)(nil)
)
