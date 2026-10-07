package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"time"
)

// defaultGracefulShutdownTimeout is used when server.graceful_shutdown_timeout
// is zero or negative (e.g. set to 0 explicitly): a zero budget would cancel
// the HTTP drain immediately.
const defaultGracefulShutdownTimeout = 30 * time.Second

// readinessGate is the slice of ServiceRegistry that flips readiness to
// "not ready" at the start of shutdown.
type readinessGate interface {
	BeginShutdown()
}

// shutdowner is both *http.Server and *ServiceRegistry. Declared as
// interfaces, like configReloader, so the sequence is unit-testable without a
// full registry.
type shutdowner interface {
	Shutdown(ctx context.Context) error
}

func effectiveShutdownTimeout(configured time.Duration) time.Duration {
	if configured <= 0 {
		return defaultGracefulShutdownTimeout
	}
	return configured
}

// runShutdown waits for the first signal on sigChan, then stops the process in
// the order a rolling update needs (PROD-GRACEFUL-SHUTDOWN):
//
//  1. readiness → not ready, so load balancers that probe /-/ready stop
//     routing here (in Kubernetes the preStop sleep covers endpoint removal);
//  2. the HTTP server stops accepting connections and drains in-flight
//     requests, which still reach live services;
//  3. the registry stops the services: final snapshot, flushes, storage.
//
// One timeout bounds the whole sequence. The registry is stopped even when
// the drain fails or runs out of time: its final snapshot does not depend on
// the context. Returns both errors joined; main exits only after it returns.
//
// Later signals are ignored: the sequence is already running and the kubelet
// sends SIGKILL when the grace period ends, as upstream Alertmanager relies on.
func runShutdown(sigChan <-chan os.Signal, gate readinessGate, server, registry shutdowner, timeout time.Duration, logger *slog.Logger) error {
	if logger == nil {
		logger = slog.Default()
	}

	sig := <-sigChan
	logger.Info("Shutdown signal received, shutting down", "signal", sig, "timeout", timeout)

	gate.BeginShutdown()
	logger.Info("Readiness set to not ready")

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	start := time.Now()
	serverErr := server.Shutdown(ctx)
	if serverErr != nil {
		logger.Error("HTTP server drain failed", "error", serverErr, "duration", time.Since(start))
	} else {
		logger.Info("HTTP server drained", "duration", time.Since(start))
	}

	start = time.Now()
	registryErr := registry.Shutdown(ctx)
	if registryErr != nil {
		logger.Error("Services shutdown failed", "error", registryErr, "duration", time.Since(start))
	} else {
		logger.Info("Services stopped", "duration", time.Since(start))
	}

	return errors.Join(serverErr, registryErr)
}
