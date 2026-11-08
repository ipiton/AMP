package main

import (
	"bytes"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
)

// The history's dedup service logs every alert at Info; warnOnly keeps its
// warnings and errors, through the attrs and groups it adds.
func TestWarnOnlyPassesWarningsUp(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(warnOnly{slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})}).
		With("component", "dedup").WithGroup("g")

	logger.Info("processed")
	logger.Debug("details")
	assert.Empty(t, buf.String())

	logger.Warn("slow", "ms", 900)
	logger.Error("failed")
	out := buf.String()
	assert.Contains(t, out, "msg=slow component=dedup g.ms=900")
	assert.Contains(t, out, "msg=failed")
}
