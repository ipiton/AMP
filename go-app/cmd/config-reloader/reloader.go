package main

import (
	"context"
	"log/slog"
	"time"
)

// reloader watches one config file and triggers + verifies an application
// reload whenever its content changes.
type reloader struct {
	configPath string
	interval   time.Duration

	notifier notifier
	verifier *verifier
	metrics  *reloaderMetrics
	logger   *slog.Logger

	// verifyTimeout bounds how long we wait for the application to report that
	// it processed our trigger; verifyPoll is how often we ask.
	verifyTimeout time.Duration
	verifyPoll    time.Duration

	// maxBackoff caps the retry delay for a config that keeps being rejected.
	maxBackoff time.Duration

	// appliedHash is the last hash the application confirmed. Updated ONLY on
	// a verified-healthy reload, so a rejected config keeps being retried
	// rather than being silently accepted as the new baseline.
	appliedHash string

	// failures counts consecutive failed attempts for the CURRENT pending
	// hash, driving the backoff. Reset whenever the file changes again —
	// an operator fixing their typo must be picked up on the next tick, not
	// after the backoff they earned with the broken version.
	pendingHash string
	failures    int
	nextAttempt time.Time

	// now is overridable in tests.
	now func() time.Time
}

// prime records the config present at startup WITHOUT triggering a reload: the
// application has just read the same file itself.
func (r *reloader) prime() {
	hash, err := hashFile(r.configPath)
	if err != nil {
		// Not fatal: the ConfigMap volume may still be mounting. The watch loop
		// will pick it up, and the error is counted and logged.
		r.metrics.watchErrors.Inc()
		r.logger.Warn("cannot read the config file yet; will keep watching",
			"path", r.configPath, "error", err)
		return
	}

	r.appliedHash = hash
	r.metrics.observeConfig(hash)
	r.logger.Info("watching config file",
		"path", r.configPath, "hash", shortHash(hash), "interval", r.interval)
}

// Run polls until the context is cancelled.
func (r *reloader) Run(ctx context.Context) {
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			r.logger.Info("config watch stopped")
			return
		case <-ticker.C:
			r.tick(ctx)
		}
	}
}

// tick performs one poll: hash the file, and reload if it differs from what the
// application has confirmed.
func (r *reloader) tick(ctx context.Context) {
	hash, err := hashFile(r.configPath)
	if err != nil {
		r.metrics.watchErrors.Inc()
		r.logger.Error("failed to read the config file", "path", r.configPath, "error", err)
		return
	}

	if hash == r.appliedHash {
		// Nothing to do, and nothing to re-publish: the gauges already carry
		// this hash from prime() or from the reload that applied it (review
		// M15 — resetting a label vector on every tick is pointless churn).
		return
	}

	r.metrics.observeConfig(hash)

	// A NEW pending hash resets the backoff: the operator has edited the file
	// again, which is the most likely thing to have fixed a rejected config.
	if hash != r.pendingHash {
		r.pendingHash = hash
		r.failures = 0
		r.nextAttempt = time.Time{}
	}

	if now := r.now(); !r.nextAttempt.IsZero() && now.Before(r.nextAttempt) {
		return
	}

	r.triggerReload(ctx, hash)
}

// triggerReload notifies the application and waits for it to confirm.
func (r *reloader) triggerReload(ctx context.Context, hash string) {
	logger := r.logger.With("hash", shortHash(hash))
	logger.Info("config change detected, triggering reload", "via", r.notifier.Describe())

	// Read the attempt counter BEFORE triggering, so the confirmation cannot be
	// satisfied by somebody else's earlier reload.
	baseline := int64(-1)
	if health, err := r.verifier.fetch(ctx); err != nil {
		// Not fatal: with baseline -1 any reported attempt count confirms, which
		// is weaker but still better than skipping verification entirely.
		logger.Warn("cannot read reload status before triggering; verification will be weaker",
			"error", err)
	} else {
		baseline = health.Attempts
	}

	at := float64(r.now().Unix())

	if err := r.notifier.Notify(ctx); err != nil {
		r.recordFailure(resultFailed, at)
		logger.Error("failed to trigger reload", "via", r.notifier.Describe(), "error", err)
		return
	}

	health, err := r.verifier.awaitAttempt(ctx, baseline, r.verifyTimeout, r.verifyPoll)
	if err != nil {
		r.recordFailure(resultUnverified, at)
		logger.Error("reload was triggered but the application never confirmed it", "error", err)
		return
	}

	// One trigger can produce a second outcome (a post-commit stage failing and
	// rolling the reload back). Look again before declaring success.
	health = r.verifier.settle(ctx, health, 2*r.verifyPoll)

	if !health.Healthy {
		r.recordFailure(resultRejected, at)
		logger.Error("the application rejected the new config",
			"status", health.Status,
			"split_state", health.SplitState,
			"version", health.Version)
		return
	}

	// Confirmed. This hash is now the baseline.
	r.appliedHash = hash
	r.pendingHash = ""
	r.failures = 0
	r.nextAttempt = time.Time{}
	r.metrics.observeReload(resultSuccess, at)

	fields := restartRequiredFields(health)
	if len(fields) > 0 {
		// The reload succeeded AND part of the operator's edit is not live.
		// Loud, because nothing else will tell them: the endpoint returned
		// healthy and the pod is running.
		logger.Warn("reload applied, but some config needs a RESTART to take effect",
			"status", health.Status, "version", health.Version, "restart_required", fields)
		return
	}

	logger.Info("reload applied", "status", health.Status, "version", health.Version)
}

// recordFailure counts a failed attempt and schedules the next one with
// exponential backoff.
//
// Without the backoff a permanently invalid config would be retried on every
// tick — one rejected reload per interval, forever, in the logs and in the
// application's own reload metrics. With it, the retries thin out while an
// edit to the file still gets picked up on the next tick.
func (r *reloader) recordFailure(result string, at float64) {
	r.metrics.observeReload(result, at)

	r.failures++
	// Builtin min (Go 1.21+); the shift is capped so it cannot overflow.
	backoff := r.interval << min(r.failures-1, 16)
	if backoff > r.maxBackoff || backoff <= 0 {
		backoff = r.maxBackoff
	}
	r.nextAttempt = r.now().Add(backoff)

	r.logger.Info("backing off before retrying this config",
		"failures", r.failures, "retry_in", backoff)
}

// restartRequiredFields flattens the health response's restart-required
// warnings into "code:field" strings for one log line.
func restartRequiredFields(health reloadHealth) []string {
	var fields []string
	for _, warning := range health.RestartRequired {
		for _, field := range warning.Fields {
			fields = append(fields, warning.Code+":"+field)
		}
		if len(warning.Fields) == 0 {
			fields = append(fields, warning.Code+":"+warning.Component)
		}
	}
	return fields
}
