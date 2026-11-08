package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Keys of AMP's removed core are not read since UPSTREAM-CORE-REMOVAL;
// amp-upstream logs them instead of ignoring them silently.
func TestIgnoredKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "amp.yaml")
	require.NoError(t, os.WriteFile(path, []byte(`profile: standard
database: {host: db}
llm: {enabled: false}
server:
  port: 8080
  auth: {unauthenticated_paths: ["/-/healthy"]}
  web_config_file: /etc/amp/web/web.yml
route: {receiver: default}
redis: {addr: x}
`), 0o600))
	assert.Equal(t, []string{"redis", "route", "server.port"}, IgnoredKeys(path))
}

func TestIgnoredKeysUnreadableFile(t *testing.T) {
	assert.Nil(t, IgnoredKeys(filepath.Join(t.TempDir(), "missing.yaml")))
}
