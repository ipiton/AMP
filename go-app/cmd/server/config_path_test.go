package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveRuntimeConfigPath(t *testing.T) {
	t.Setenv(runtimeConfigFileEnv, "")
	path, explicit := resolveRuntimeConfigPath()
	assert.Equal(t, "config.yaml", path)
	assert.False(t, explicit)

	t.Setenv(runtimeConfigFileEnv, "   ")
	path, explicit = resolveRuntimeConfigPath()
	assert.Equal(t, "config.yaml", path)
	assert.False(t, explicit, "whitespace-only AMP_CONFIG_FILE counts as unset")

	t.Setenv(runtimeConfigFileEnv, " /etc/amp/config.yaml ")
	path, explicit = resolveRuntimeConfigPath()
	assert.Equal(t, "/etc/amp/config.yaml", path)
	assert.True(t, explicit)
}

// PROD-CONFIG-FILE-FALLBACK: an AMP_CONFIG_FILE that does not exist is a
// broken mount, not "configure from env" — main exits on this error.
func TestCheckConfigPath(t *testing.T) {
	dir := t.TempDir()
	existing := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(existing, []byte("server: {}\n"), 0o600))
	missing := filepath.Join(dir, "absent.yaml")
	dangling := filepath.Join(dir, "dangling.yaml")
	require.NoError(t, os.Symlink(filepath.Join(dir, "gone.yaml"), dangling))

	t.Run("explicit missing path fails with the path", func(t *testing.T) {
		err := checkConfigPath(missing, true)
		require.Error(t, err)
		assert.Contains(t, err.Error(), missing)
		assert.Contains(t, err.Error(), runtimeConfigFileEnv)
	})
	t.Run("explicit dangling symlink fails", func(t *testing.T) {
		require.Error(t, checkConfigPath(dangling, true))
	})
	t.Run("explicit existing path passes", func(t *testing.T) {
		assert.NoError(t, checkConfigPath(existing, true))
	})
	t.Run("default missing path passes", func(t *testing.T) {
		assert.NoError(t, checkConfigPath(missing, false))
	})
	t.Run("other stat errors are left to LoadConfig", func(t *testing.T) {
		// Path under a regular file: ENOTDIR, not ErrNotExist.
		assert.NoError(t, checkConfigPath(filepath.Join(existing, "x.yaml"), true))
	})
}
