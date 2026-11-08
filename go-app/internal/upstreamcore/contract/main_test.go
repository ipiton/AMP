//go:build upstreamcore

package contract

import (
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	code := m.Run()
	stopUpstreamCores()
	os.Exit(code)
}
