package main

import (
	"fmt"
	"log/slog"
	"net"
	"net/http"
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

// reloadHandler serves POST /-/reload and nothing else. The error stays in the
// log: the caller only needs the status to retry.
func reloadHandler(reload func() error, logger *slog.Logger) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /-/reload", func(w http.ResponseWriter, _ *http.Request) {
		if err := reload(); err != nil {
			logger.Error("configuration reload failed", "err", err)
			http.Error(w, "reload failed, see the AMP log", http.StatusInternalServerError)
		}
	})
	return mux
}
