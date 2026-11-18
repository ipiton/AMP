package main

import (
	"crypto/sha256"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"sync"
)

// listenLoopback binds the reload listener. Only a literal loopback address is
// accepted: the listener has no authentication, so it must not be reachable
// from outside the pod (ADR-020).
func listenLoopback(address string) (net.Listener, error) {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return nil, fmt.Errorf("--web.reload-listen-address: %w", err)
	}
	if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() {
		return nil, fmt.Errorf("--web.reload-listen-address: %q is not a loopback IP address", host)
	}
	return net.Listen("tcp", address)
}

// reloadHandler serves POST /-/reload and nothing else. It reloads only when
// configFile differs from the last version loaded here: AMP itself can reach
// loopback, so a webhook URL or redirect pointing at this listener would
// otherwise restart the dispatcher on every delivery attempt. The error stays
// in the log: the caller only needs the status to retry.
func reloadHandler(configFile string, reload func() error, logger *slog.Logger) http.Handler {
	var (
		mtx    sync.Mutex
		loaded [sha256.Size]byte
		known  bool
	)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /-/reload", func(w http.ResponseWriter, _ *http.Request) {
		mtx.Lock()
		defer mtx.Unlock()
		content, err := os.ReadFile(configFile)
		if err != nil {
			logger.Error("configuration reload failed", "err", err)
			http.Error(w, "reload failed, see the AMP log", http.StatusInternalServerError)
			return
		}
		sum := sha256.Sum256(content)
		if known && sum == loaded {
			return
		}
		if err := reload(); err != nil {
			logger.Error("configuration reload failed", "err", err)
			http.Error(w, "reload failed, see the AMP log", http.StatusInternalServerError)
			return
		}
		loaded, known = sum, true
	})
	return mux
}
