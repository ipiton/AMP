//go:build futureparity && upstreamcore

package main

import (
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	code := m.Run()
	stopUpstreamCores()
	os.Exit(code)
}
