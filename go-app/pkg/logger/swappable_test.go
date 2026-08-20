package logger

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// syncBuffer is a bytes.Buffer safe for the concurrency test below.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func TestSwappableHandler_LevelSwapTakesEffect(t *testing.T) {
	var buf syncBuffer
	handler, err := NewSwappableHandler(&buf, slog.LevelInfo, "json")
	require.NoError(t, err)
	logger := slog.New(handler)

	logger.Debug("before-swap")
	assert.NotContains(t, buf.String(), "before-swap", "debug must be filtered at info level")

	require.NoError(t, handler.Swap(slog.LevelDebug, "json"))
	assert.Equal(t, slog.LevelDebug, handler.Level())

	logger.Debug("after-swap")
	assert.Contains(t, buf.String(), "after-swap", "debug must pass after the level swap")
}

func TestSwappableHandler_FormatSwapJSONToText(t *testing.T) {
	var buf syncBuffer
	handler, err := NewSwappableHandler(&buf, slog.LevelInfo, "json")
	require.NoError(t, err)
	logger := slog.New(handler)

	logger.Info("json-line", "k", "v")
	first := strings.Split(strings.TrimSpace(buf.String()), "\n")[0]
	var decoded map[string]any
	require.NoError(t, json.Unmarshal([]byte(first), &decoded), "first line must be JSON")

	require.NoError(t, handler.Swap(slog.LevelInfo, "text"))
	assert.Equal(t, "text", handler.Format())

	logger.Info("text-line", "k", "v")
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	last := lines[len(lines)-1]
	assert.False(t, json.Valid([]byte(last)), "post-swap line must not be JSON: %s", last)
	assert.Contains(t, last, "msg=text-line")
}

func TestSwappableHandler_DerivedLoggersFollowTheSwap(t *testing.T) {
	var buf syncBuffer
	handler, err := NewSwappableHandler(&buf, slog.LevelInfo, "json")
	require.NoError(t, err)

	// The whole point of replaying derivations: a logger built with With()
	// BEFORE the swap must honour the new level, and must keep its attrs.
	derived := slog.New(handler).With("component", "grouping").WithGroup("phase")

	derived.Debug("pre-swap-debug")
	assert.NotContains(t, buf.String(), "pre-swap-debug")

	require.NoError(t, handler.Swap(slog.LevelDebug, "json"))

	derived.Debug("post-swap-debug", "step", "notify")
	out := buf.String()
	require.Contains(t, out, "post-swap-debug")
	assert.Contains(t, out, `"component":"grouping"`, "derived attrs must survive the swap")
	assert.Contains(t, out, `"phase":{"step":"notify"}`, "derived group must survive the swap")
}

func TestSwappableHandler_BadFormatKeepsPreviousHandler(t *testing.T) {
	var buf syncBuffer
	handler, err := NewSwappableHandler(&buf, slog.LevelInfo, "json")
	require.NoError(t, err)
	logger := slog.New(handler)

	err = handler.Swap(slog.LevelDebug, "yaml")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported log format")

	// Neither half of the failed swap was applied.
	assert.Equal(t, slog.LevelInfo, handler.Level())
	assert.Equal(t, "json", handler.Format())

	logger.Info("still-logging")
	assert.Contains(t, buf.String(), "still-logging")
}

func TestNewSwappableHandler_RejectsNilWriter(t *testing.T) {
	_, err := NewSwappableHandler(nil, slog.LevelInfo, "json")
	require.Error(t, err)
}

func TestNewSwappableLogger_FromConfig(t *testing.T) {
	logger, handler, err := NewSwappableLogger(Config{Level: "warn", Format: "text", Output: "stdout"})
	require.NoError(t, err)
	require.NotNil(t, logger)
	assert.Equal(t, slog.LevelWarn, handler.Level())
	assert.Equal(t, "text", handler.Format())
}

// TestSwappableHandler_ConcurrentSwapAndLog is the -race guard: logging and
// swapping happen on different goroutines in production (request handlers vs
// the SIGHUP reload path).
func TestSwappableHandler_ConcurrentSwapAndLog(t *testing.T) {
	var buf syncBuffer
	handler, err := NewSwappableHandler(&buf, slog.LevelInfo, "json")
	require.NoError(t, err)
	logger := slog.New(handler).With("component", "test")

	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				logger.Info("concurrent")
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for j := 0; j < 20; j++ {
			level := slog.LevelInfo
			format := "json"
			if j%2 == 0 {
				level, format = slog.LevelDebug, "text"
			}
			_ = handler.Swap(level, format)
		}
	}()
	wg.Wait()

	assert.Contains(t, buf.String(), "concurrent")
}

// BenchmarkSwappableHandler_Handle measures the plain (undelivered) path.
func BenchmarkSwappableHandler_Handle(b *testing.B) {
	handler, err := NewSwappableHandler(io.Discard, slog.LevelInfo, "json")
	if err != nil {
		b.Fatal(err)
	}
	logger := slog.New(handler)

	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		logger.Info("bench", "i", i)
	}
}

// BenchmarkSwappableHandler_DerivedHandle measures the replay cost of the
// recorded With()/WithGroup() derivations (review M4): every record rebuilds
// them against the live delegate, which is what lets a pre-swap logger honour a
// later level change. This is the number to watch if the replay ever needs
// caching.
func BenchmarkSwappableHandler_DerivedHandle(b *testing.B) {
	handler, err := NewSwappableHandler(io.Discard, slog.LevelInfo, "json")
	if err != nil {
		b.Fatal(err)
	}
	logger := slog.New(handler).
		With("component", "grouping").
		With("replica", "amp-0").
		WithGroup("phase")

	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		logger.Info("bench", "i", i)
	}
}
