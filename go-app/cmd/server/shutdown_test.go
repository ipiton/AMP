package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// shutdownLog records the order in which the shutdown steps ran.
type shutdownLog struct {
	mu    sync.Mutex
	steps []string
}

func (l *shutdownLog) add(step string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.steps = append(l.steps, step)
}

func (l *shutdownLog) snapshot() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.steps...)
}

type fakeGate struct {
	log *shutdownLog
}

func (g *fakeGate) BeginShutdown() { g.log.add("readiness") }

type fakeShutdowner struct {
	name     string
	log      *shutdownLog
	err      error
	deadline time.Time
	hook     func()
}

func (f *fakeShutdowner) Shutdown(ctx context.Context) error {
	f.log.add(f.name)
	f.deadline, _ = ctx.Deadline()
	if f.hook != nil {
		f.hook()
	}
	return f.err
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// runShutdownAsync starts runShutdown and returns the channel its result
// lands on, mirroring how main wires it.
func runShutdownAsync(sigChan <-chan os.Signal, gate readinessGate, server, registry shutdowner, timeout time.Duration) <-chan error {
	done := make(chan error, 1)
	go func() {
		done <- runShutdown(sigChan, gate, server, registry, timeout, discardLogger())
	}()
	return done
}

func waitShutdown(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("runShutdown did not return")
		return nil
	}
}

// PROD-GRACEFUL-SHUTDOWN: services used to stop before the HTTP server, so
// in-flight requests hit stopped services. The order must be readiness off,
// HTTP drain, then services — and nothing runs before the signal arrives.
func TestRunShutdown_OrderAndBudget(t *testing.T) {
	log := &shutdownLog{}
	server := &fakeShutdowner{name: "http", log: log}
	registry := &fakeShutdowner{name: "registry", log: log}
	sigChan := make(chan os.Signal, 1)

	const timeout = 7 * time.Second
	done := runShutdownAsync(sigChan, &fakeGate{log: log}, server, registry, timeout)

	time.Sleep(20 * time.Millisecond)
	assert.Empty(t, log.snapshot(), "no shutdown step may run before a signal")

	start := time.Now()
	sigChan <- syscall.SIGTERM
	require.NoError(t, waitShutdown(t, done))

	assert.Equal(t, []string{"readiness", "http", "registry"}, log.snapshot())
	// One budget for the whole sequence: both steps share the deadline.
	assert.WithinDuration(t, start.Add(timeout), server.deadline, time.Second)
	assert.Equal(t, server.deadline, registry.deadline)
}

// A failed or timed-out drain must not skip the service shutdown: the lite
// profile's final snapshot is written there.
func TestRunShutdown_StopsServicesWhenDrainFails(t *testing.T) {
	log := &shutdownLog{}
	drainErr := context.DeadlineExceeded
	registryErr := errors.New("silence manager stop failed")
	server := &fakeShutdowner{name: "http", log: log, err: drainErr}
	registry := &fakeShutdowner{name: "registry", log: log, err: registryErr}
	sigChan := make(chan os.Signal, 1)

	done := runShutdownAsync(sigChan, &fakeGate{log: log}, server, registry, time.Second)
	sigChan <- syscall.SIGINT
	err := waitShutdown(t, done)

	assert.Equal(t, []string{"readiness", "http", "registry"}, log.snapshot())
	require.Error(t, err)
	assert.ErrorIs(t, err, drainErr)
	assert.ErrorIs(t, err, registryErr)
}

func TestEffectiveShutdownTimeout(t *testing.T) {
	tests := []struct {
		name       string
		configured time.Duration
		want       time.Duration
	}{
		// Zero is what main gets when the config file is missing
		// (CONFIG-MISSING-FILE-DROPS-ENV); it must not mean "no drain".
		{name: "zero falls back", configured: 0, want: defaultGracefulShutdownTimeout},
		{name: "negative falls back", configured: -time.Second, want: defaultGracefulShutdownTimeout},
		{name: "configured value kept", configured: 7 * time.Second, want: 7 * time.Second},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, effectiveShutdownTimeout(tt.configured))
		})
	}
	assert.Equal(t, 30*time.Second, defaultGracefulShutdownTimeout)
}

// With a real http.Server: a request already being served when the signal
// arrives completes with 200, new connections are refused once the drain has
// started, and services stop only after the in-flight response is written.
func TestRunShutdown_DrainsInFlightRequestBeforeServices(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := ln.Addr().String()

	started := make(chan struct{})
	release := make(chan struct{})
	var handlerDone atomic.Bool
	srv := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			close(started)
			<-release
			w.WriteHeader(http.StatusOK)
			handlerDone.Store(true)
		}),
		ReadHeaderTimeout: 5 * time.Second,
	}
	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve(ln) }()

	respCode := make(chan int, 1)
	go func() {
		resp, err := http.Get("http://" + addr + "/slow")
		if err != nil {
			respCode <- -1
			return
		}
		_ = resp.Body.Close()
		respCode <- resp.StatusCode
	}()
	<-started

	log := &shutdownLog{}
	var servicesSawHandlerDone atomic.Bool
	registry := &fakeShutdowner{name: "registry", log: log, hook: func() {
		servicesSawHandlerDone.Store(handlerDone.Load())
	}}
	sigChan := make(chan os.Signal, 1)
	done := runShutdownAsync(sigChan, &fakeGate{log: log}, srv, registry, 5*time.Second)
	sigChan <- syscall.SIGTERM

	// Shutdown closes the listener first; wait until new dials fail.
	require.Eventually(t, func() bool {
		conn, err := net.DialTimeout("tcp", addr, 100*time.Millisecond)
		if err != nil {
			return true
		}
		_ = conn.Close()
		return false
	}, 2*time.Second, 10*time.Millisecond, "new connections must be refused once the drain starts")

	select {
	case err := <-done:
		t.Fatalf("runShutdown returned (%v) while a request was still in flight", err)
	default:
	}
	assert.Equal(t, []string{"readiness"}, log.snapshot(), "services must not stop while the HTTP drain is running")

	close(release)
	assert.Equal(t, http.StatusOK, <-respCode, "in-flight request must complete")
	require.NoError(t, waitShutdown(t, done))
	assert.True(t, servicesSawHandlerDone.Load(), "services stopped before the in-flight response was written")
	assert.ErrorIs(t, <-serveErr, http.ErrServerClosed)
}
