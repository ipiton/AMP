package main

import (
	"context"
	"errors"
	"os"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeReloader struct {
	mu     sync.Mutex
	calls  int
	err    error
	called chan struct{}
}

func newFakeReloader(err error) *fakeReloader {
	return &fakeReloader{err: err, called: make(chan struct{}, 8)}
}

func (f *fakeReloader) ReloadConfig(context.Context) error {
	f.mu.Lock()
	f.calls++
	f.mu.Unlock()
	f.called <- struct{}{}
	return f.err
}

func (f *fakeReloader) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// TestWatchReloadSignal_TriggersReload covers final review finding 1's second
// half: the reload coordinator and the compatibility docs both advertised
// SIGHUP as a reload trigger, but no signal handler existed — only
// POST /-/reload worked. main() now wires signal.Notify(SIGHUP) into this
// loop; this test drives the loop directly.
func TestWatchReloadSignal_TriggersReload(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	reloader := newFakeReloader(nil)
	sigChan := make(chan os.Signal, 2)

	done := make(chan struct{})
	go func() {
		watchReloadSignal(ctx, sigChan, reloader, nil)
		close(done)
	}()

	sigChan <- syscall.SIGHUP
	select {
	case <-reloader.called:
	case <-time.After(2 * time.Second):
		t.Fatal("SIGHUP did not trigger a config reload")
	}

	// SIGHUP is repeatable — the loop must survive to serve the next one.
	sigChan <- syscall.SIGHUP
	select {
	case <-reloader.called:
	case <-time.After(2 * time.Second):
		t.Fatal("second SIGHUP did not trigger a config reload")
	}
	assert.Equal(t, 2, reloader.callCount())

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("watchReloadSignal did not exit on context cancellation")
	}
}

// TestWatchReloadSignal_SurvivesReloadError pins the fail-open posture: a
// broken config on disk must not kill the handler, otherwise a single bad edit
// would permanently disable SIGHUP reloads for the process lifetime.
func TestWatchReloadSignal_SurvivesReloadError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	reloader := newFakeReloader(errors.New("phase 2 (validation) failed: 1 error(s)"))
	sigChan := make(chan os.Signal, 2)
	go watchReloadSignal(ctx, sigChan, reloader, nil)

	for i := 0; i < 2; i++ {
		sigChan <- syscall.SIGHUP
		select {
		case <-reloader.called:
		case <-time.After(2 * time.Second):
			t.Fatalf("SIGHUP %d did not reach the reloader", i+1)
		}
	}
	require.Equal(t, 2, reloader.callCount())
}

// TestWatchReloadSignal_ExitsOnClosedChannel guards against a busy loop if the
// signal channel is ever closed (e.g. a future refactor owning the channel).
func TestWatchReloadSignal_ExitsOnClosedChannel(t *testing.T) {
	sigChan := make(chan os.Signal)
	done := make(chan struct{})
	go func() {
		watchReloadSignal(context.Background(), sigChan, newFakeReloader(nil), nil)
		close(done)
	}()

	close(sigChan)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("watchReloadSignal did not exit on a closed signal channel")
	}
}
