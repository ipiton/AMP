package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"syscall"
)

// notifier tells the application to reload its configuration.
type notifier interface {
	// Notify triggers a reload. A nil error means the trigger was DELIVERED,
	// not that the reload succeeded — that is what the verifier is for.
	Notify(ctx context.Context) error

	// Describe returns a short human-readable form for logs.
	Describe() string
}

// ================================================================================
// SIGHUP
// ================================================================================

// signalNotifier sends SIGHUP to a PID, matching upstream Alertmanager's own
// reload trigger.
//
// IMPORTANT (and the reason the Helm chart defaults to HTTP instead): a sidecar
// can only signal the application's process if the pod sets
// `shareProcessNamespace: true`. Without it, each container has its own PID
// namespace, PID 1 is the SIDECAR ITSELF, and this would deliver SIGHUP to the
// reloader — which ignores it — while reporting success. The flag parser
// refuses `--method=signal --pid=1` unless the operator confirms with
// --allow-self-pid, so that misconfiguration cannot be silent.
type signalNotifier struct {
	pid int
}

func (n *signalNotifier) Notify(_ context.Context) error {
	process, err := os.FindProcess(n.pid)
	if err != nil {
		return fmt.Errorf("find process %d: %w", n.pid, err)
	}
	if err := process.Signal(syscall.SIGHUP); err != nil {
		return fmt.Errorf("signal SIGHUP to pid %d: %w", n.pid, err)
	}
	return nil
}

func (n *signalNotifier) Describe() string {
	return fmt.Sprintf("SIGHUP to pid %d", n.pid)
}

// ================================================================================
// HTTP POST /-/reload
// ================================================================================

// httpNotifier POSTs to the application's reload endpoint. Works across
// container boundaries, so it is the default for the Helm sidecar.
type httpNotifier struct {
	url    string
	client *http.Client
}

func (n *httpNotifier) Notify(ctx context.Context) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, n.url, http.NoBody)
	if err != nil {
		return fmt.Errorf("build reload request: %w", err)
	}

	response, err := n.client.Do(request)
	if err != nil {
		return fmt.Errorf("post %s: %w", n.url, err)
	}
	defer func() {
		// Drain before closing so the connection can be reused; the body is
		// "OK" or an error string we do not need.
		_, _ = io.Copy(io.Discard, response.Body)
		_ = response.Body.Close()
	}()

	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("post %s: unexpected status %s", n.url, response.Status)
	}
	return nil
}

func (n *httpNotifier) Describe() string {
	return "POST " + n.url
}
