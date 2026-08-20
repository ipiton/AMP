package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// reloadHealth mirrors the fields this sidecar needs from the application's
// GET /health/reload response (handlers.ReloadHealthResponse). Deliberately a
// separate, minimal struct: the sidecar is a distinct binary that may be a
// version ahead of or behind the app, so it must tolerate extra fields and
// must not import the app's HTTP types.
type reloadHealth struct {
	Healthy    bool   `json:"healthy"`
	Status     string `json:"status"`
	Version    int64  `json:"version"`
	Attempts   int64  `json:"attempts"`
	SplitState bool   `json:"split_state"`

	RestartRequired []struct {
		Code      string   `json:"code"`
		Component string   `json:"component"`
		Fields    []string `json:"fields"`
	} `json:"restart_required"`
}

// verifier reads the application's reload status.
type verifier struct {
	url    string
	client *http.Client
}

// fetch reads the current reload status.
//
// A 503 body is still parsed: that is exactly the "reload was rejected" case,
// and the body says which. Only a transport error or unparseable body is an
// error here.
func (v *verifier) fetch(ctx context.Context) (reloadHealth, error) {
	var health reloadHealth

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, v.url, http.NoBody)
	if err != nil {
		return health, fmt.Errorf("build health request: %w", err)
	}

	response, err := v.client.Do(request)
	if err != nil {
		return health, fmt.Errorf("get %s: %w", v.url, err)
	}
	defer func() { _ = response.Body.Close() }()

	if response.StatusCode != http.StatusOK && response.StatusCode != http.StatusServiceUnavailable {
		return health, fmt.Errorf("get %s: unexpected status %s", v.url, response.Status)
	}

	if err := json.NewDecoder(response.Body).Decode(&health); err != nil {
		return health, fmt.Errorf("decode %s: %w", v.url, err)
	}
	return health, nil
}

// awaitAttempt polls until the application reports an attempt counter strictly
// greater than baseline, then returns that status.
//
// The counter, not the timestamp: it is monotonic and needs no clock agreement
// between containers, and it distinguishes "the reload was processed and
// rejected" (attempts advanced, healthy=false) from "the trigger never arrived"
// (attempts unchanged) — a distinction a status label alone cannot make,
// because a comment-only edit legitimately produces no state change beyond the
// counter.
func (v *verifier) awaitAttempt(
	ctx context.Context,
	baseline int64,
	timeout time.Duration,
	poll time.Duration,
) (reloadHealth, error) {
	deadline := time.Now().Add(timeout)

	var lastErr error
	for {
		health, err := v.fetch(ctx)
		switch {
		case err != nil:
			// The app may be briefly unreachable (restarting, or still
			// applying); keep trying until the deadline.
			lastErr = err
		case health.Attempts > baseline:
			return health, nil
		default:
			lastErr = fmt.Errorf("attempts still %d (baseline %d)", health.Attempts, baseline)
		}

		if time.Now().After(deadline) {
			return reloadHealth{}, fmt.Errorf("reload not confirmed within %s: %w", timeout, lastErr)
		}

		select {
		case <-ctx.Done():
			return reloadHealth{}, ctx.Err()
		case <-time.After(poll):
		}
	}
}

// settle re-reads the status a moment after a confirmation and returns the
// newer snapshot if the application recorded ANOTHER outcome in the meantime.
//
// One file change can produce two recorded outcomes: ServiceRegistry applies
// routing/templates/receivers AFTER the coordinator commits, and if one of those
// fails it rolls the whole reload back — a second outcome for the same trigger
// (the application's own FU-RELOAD-UNIFY-APPLIERS covers merging those stages).
// With the SIGHUP trigger the sidecar can observe the first outcome before the
// second exists, so it must look again before declaring success.
func (v *verifier) settle(ctx context.Context, confirmed reloadHealth, delay time.Duration) reloadHealth {
	select {
	case <-ctx.Done():
		return confirmed
	case <-time.After(delay):
	}

	later, err := v.fetch(ctx)
	if err != nil || later.Attempts <= confirmed.Attempts {
		return confirmed
	}
	return later
}
