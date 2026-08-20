package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSignalNotifier_DeliversSIGHUP uses this test process as the fake signal
// target, which is the closest thing to a real check without a second process.
func TestSignalNotifier_DeliversSIGHUP(t *testing.T) {
	received := make(chan os.Signal, 1)
	signal.Notify(received, syscall.SIGHUP)
	t.Cleanup(func() { signal.Stop(received) })

	notify := &signalNotifier{pid: os.Getpid()}
	require.NoError(t, notify.Notify(context.Background()))

	select {
	case <-received:
	case <-time.After(2 * time.Second):
		t.Fatal("SIGHUP was never delivered")
	}

	assert.Contains(t, notify.Describe(), "SIGHUP")
}

func TestSignalNotifier_UnknownPidIsAnError(t *testing.T) {
	// PID 0 is "the current process group" for kill(2), and negative PIDs are
	// process groups; a very high PID is reliably absent.
	notify := &signalNotifier{pid: 1 << 30}
	require.Error(t, notify.Notify(context.Background()))
}

func TestHTTPNotifier_PostsToReloadEndpoint(t *testing.T) {
	var posts atomic.Int64
	var method atomic.Value

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		posts.Add(1)
		method.Store(r.Method)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("OK"))
	}))
	defer server.Close()

	notify := &httpNotifier{url: server.URL + "/-/reload", client: server.Client()}
	require.NoError(t, notify.Notify(context.Background()))

	assert.Equal(t, int64(1), posts.Load())
	assert.Equal(t, http.MethodPost, method.Load())
	assert.Contains(t, notify.Describe(), "/-/reload")
}

func TestHTTPNotifier_NonOKStatusIsAnError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer server.Close()

	notify := &httpNotifier{url: server.URL, client: server.Client()}
	err := notify.Notify(context.Background())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "500")
}

func TestHTTPNotifier_UnreachableIsAnError(t *testing.T) {
	notify := &httpNotifier{url: "http://127.0.0.1:1/-/reload", client: &http.Client{Timeout: time.Second}}
	require.Error(t, notify.Notify(context.Background()))
}

// ================================================================================
// Flag validation
// ================================================================================

func baseOptions() *options {
	return &options{
		configPath:    "/etc/amp/config.yaml",
		interval:      10 * time.Second,
		method:        methodHTTP,
		pid:           1,
		reloadURL:     "http://127.0.0.1:9093/-/reload",
		healthURL:     "http://127.0.0.1:9093/health/reload",
		metricsAddr:   ":9091",
		verifyTimeout: 30 * time.Second,
		verifyPoll:    500 * time.Millisecond,
		maxBackoff:    5 * time.Minute,
	}
}

func TestOptions_DefaultsAreValid(t *testing.T) {
	require.NoError(t, baseOptions().validate())
}

// TestOptions_SignalToSelfIsRefused is the misconfiguration that matters: in a
// pod WITHOUT shareProcessNamespace: true the sidecar is its own PID 1, so
// --method=signal --pid=1 would SIGHUP the reloader (which ignores it) and
// report every reload as delivered.
func TestOptions_SignalToSelfIsRefused(t *testing.T) {
	opts := baseOptions()
	opts.method = methodSignal
	opts.pid = os.Getpid()

	err := opts.validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "shareProcessNamespace")

	// ...unless the operator says they mean it.
	opts.allowSelfPID = true
	require.NoError(t, opts.validate())
}

func TestOptions_Rejections(t *testing.T) {
	cases := map[string]func(*options){
		"no config file":       func(o *options) { o.configPath = "" },
		"zero interval":        func(o *options) { o.interval = 0 },
		"zero verify timeout":  func(o *options) { o.verifyTimeout = 0 },
		"zero verify poll":     func(o *options) { o.verifyPoll = 0 },
		"backoff below tick":   func(o *options) { o.maxBackoff = time.Millisecond },
		"unknown method":       func(o *options) { o.method = "carrier-pigeon" },
		"http without url":     func(o *options) { o.reloadURL = "" },
		"signal with zero pid": func(o *options) { o.method = methodSignal; o.pid = 0 },
		"no health url":        func(o *options) { o.healthURL = "" },
	}

	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			opts := baseOptions()
			mutate(opts)
			require.Error(t, opts.validate())
		})
	}
}

func TestBuildNotifier_ByMethod(t *testing.T) {
	opts := baseOptions()

	httpNotify, err := buildNotifier(opts, http.DefaultClient)
	require.NoError(t, err)
	assert.IsType(t, &httpNotifier{}, httpNotify)

	opts.method = methodSignal
	opts.pid = 4242
	signalNotify, err := buildNotifier(opts, http.DefaultClient)
	require.NoError(t, err)
	assert.IsType(t, &signalNotifier{}, signalNotify)

	opts.method = "nope"
	_, err = buildNotifier(opts, http.DefaultClient)
	require.Error(t, err)
}
