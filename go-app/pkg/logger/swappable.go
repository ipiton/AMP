package logger

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
)

// ================================================================================
// SwappableHandler — hot-reloadable slog handler (INF-A slice 1)
// ================================================================================
// slog gives no way to change a handler's level or format after
// slog.New(handler): both are baked into the handler at construction. A
// process that wants `log.level: debug` to take effect on SIGHUP therefore
// needs one level of indirection — a handler that forwards to a delegate it
// can replace atomically.
//
// This is that handler. It is installed once at startup (cmd/server/main.go)
// and the delegate is rebuilt by LoggerReloadable on a log.level/log.format
// change.

// handlerState is the atomically replaceable part: the live delegate plus the
// config it was built from.
type handlerState struct {
	delegate slog.Handler
	level    slog.Level
	format   string
}

// handlerOp records one WithAttrs/WithGroup derivation. Exactly one of the two
// fields is set.
type handlerOp struct {
	group string
	attrs []slog.Attr
}

// SwappableHandler is an slog.Handler whose underlying handler can be replaced
// at runtime.
//
// Derived handlers (from Logger.With / Logger.WithGroup) keep following the
// swap: instead of eagerly delegating WithAttrs/WithGroup to the current
// delegate — which would pin that derived logger to the pre-swap handler
// forever — the derivation is recorded and replayed against whichever delegate
// is live when a record is actually handled. That costs a few interface calls
// per record on derived loggers and is the only way a `logger.With("component",
// "grouping")` logger can honour a later level change.
type SwappableHandler struct {
	// state is shared by every handler derived from the same root, so one
	// Swap reaches all of them.
	state *atomic.Pointer[handlerState]

	// writer is fixed for the lifetime of the process: changing the output
	// sink is restart-required (see LoggerReloadable / W602).
	writer io.Writer

	// swapMu serialises Swap against itself.
	swapMu *sync.Mutex

	// ops are the WithAttrs/WithGroup derivations applied to this handler, in
	// order. Immutable once built (each derivation copies).
	ops []handlerOp
}

// NewSwappableHandler builds a handler over writer using cfg's level and
// format. Returns an error for an unusable format so a typo is loud at
// startup rather than silently JSON.
func NewSwappableHandler(writer io.Writer, level slog.Level, format string) (*SwappableHandler, error) {
	if writer == nil {
		return nil, fmt.Errorf("swappable handler: nil writer")
	}

	delegate, err := buildDelegate(writer, level, format)
	if err != nil {
		return nil, err
	}

	state := &atomic.Pointer[handlerState]{}
	state.Store(&handlerState{delegate: delegate, level: level, format: format})

	return &SwappableHandler{
		state:  state,
		writer: writer,
		swapMu: &sync.Mutex{},
	}, nil
}

// buildDelegate constructs the concrete handler for a level/format pair.
func buildDelegate(writer io.Writer, level slog.Level, format string) (slog.Handler, error) {
	opts := &slog.HandlerOptions{Level: level}

	switch strings.ToLower(strings.TrimSpace(format)) {
	case "text":
		return slog.NewTextHandler(writer, opts), nil
	case "", "json":
		return slog.NewJSONHandler(writer, opts), nil
	default:
		return nil, fmt.Errorf("swappable handler: unsupported log format %q (want \"json\" or \"text\")", format)
	}
}

// Swap rebuilds the delegate for a new level/format over the SAME writer and
// publishes it atomically. A build failure leaves the previous handler live
// and returns the error, so an invalid format never costs the process its
// logging.
func (h *SwappableHandler) Swap(level slog.Level, format string) error {
	h.swapMu.Lock()
	defer h.swapMu.Unlock()

	delegate, err := buildDelegate(h.writer, level, format)
	if err != nil {
		return err
	}

	h.state.Store(&handlerState{delegate: delegate, level: level, format: format})
	return nil
}

// Level returns the live level.
func (h *SwappableHandler) Level() slog.Level {
	return h.state.Load().level
}

// Format returns the live format ("json" or "text").
func (h *SwappableHandler) Format() string {
	format := h.state.Load().format
	if format == "" {
		return "json"
	}
	return format
}

// Enabled implements slog.Handler.
func (h *SwappableHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.state.Load().delegate.Enabled(ctx, level)
}

// Handle implements slog.Handler. The recorded derivations are replayed
// against the live delegate.
func (h *SwappableHandler) Handle(ctx context.Context, record slog.Record) error {
	delegate := h.state.Load().delegate
	for _, op := range h.ops {
		if op.group != "" {
			delegate = delegate.WithGroup(op.group)
			continue
		}
		delegate = delegate.WithAttrs(op.attrs)
	}
	return delegate.Handle(ctx, record)
}

// WithAttrs implements slog.Handler.
func (h *SwappableHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	if len(attrs) == 0 {
		return h
	}
	return h.derive(handlerOp{attrs: attrs})
}

// WithGroup implements slog.Handler.
func (h *SwappableHandler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	return h.derive(handlerOp{group: name})
}

// derive returns a handler sharing this one's swappable state with one more
// derivation appended.
func (h *SwappableHandler) derive(op handlerOp) *SwappableHandler {
	ops := make([]handlerOp, len(h.ops), len(h.ops)+1)
	copy(ops, h.ops)
	ops = append(ops, op)

	return &SwappableHandler{
		state:  h.state,
		writer: h.writer,
		swapMu: h.swapMu,
		ops:    ops,
	}
}

// NewSwappableLogger is the startup convenience wrapper: it resolves cfg's
// writer, level and format, and returns both the logger and the handler that
// LoggerReloadable will later swap.
func NewSwappableLogger(cfg Config) (*slog.Logger, *SwappableHandler, error) {
	handler, err := NewSwappableHandler(SetupWriter(cfg), ParseLevel(cfg.Level), cfg.Format)
	if err != nil {
		return nil, nil, err
	}
	return slog.New(handler), handler, nil
}

// Compile-time contract check.
var _ slog.Handler = (*SwappableHandler)(nil)
