package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	return path
}

func TestHashFile_StableForIdenticalContent(t *testing.T) {
	path := writeConfig(t, "route:\n  receiver: default\n")

	first, err := hashFile(path)
	require.NoError(t, err)
	second, err := hashFile(path)
	require.NoError(t, err)

	assert.Equal(t, first, second)
	assert.Len(t, first, 64, "SHA256 hex is 64 chars")
}

func TestHashFile_ChangesWithContent(t *testing.T) {
	path := writeConfig(t, "log:\n  level: info\n")
	before, err := hashFile(path)
	require.NoError(t, err)

	require.NoError(t, os.WriteFile(path, []byte("log:\n  level: debug\n"), 0o600))
	after, err := hashFile(path)
	require.NoError(t, err)

	assert.NotEqual(t, before, after)
}

// TestHashFile_IgnoresMtimeOnlyChanges is the reason this watches content and
// not mtime: kubelet re-syncs a ConfigMap volume periodically, which touches
// the file without changing what the application reads. An mtime watcher would
// reload on every sync.
func TestHashFile_IgnoresMtimeOnlyChanges(t *testing.T) {
	path := writeConfig(t, "log:\n  level: info\n")
	before, err := hashFile(path)
	require.NoError(t, err)

	// Rewrite identical content (new mtime, same bytes).
	require.NoError(t, os.WriteFile(path, []byte("log:\n  level: info\n"), 0o600))
	after, err := hashFile(path)
	require.NoError(t, err)

	assert.Equal(t, before, after, "identical content must hash identically")
}

func TestHashFile_MissingFileIsAnError(t *testing.T) {
	_, err := hashFile(filepath.Join(t.TempDir(), "absent.yaml"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "read config file")
}

func TestHashToFloat(t *testing.T) {
	// Distinct hashes must produce distinct gauge values (for the leading bits
	// we keep), and the value must be exactly representable in float64.
	a := hashToFloat("0123456789abcdef0123")
	b := hashToFloat("fedcba98765432100000")

	assert.NotEqual(t, a, b)
	assert.Equal(t, a, float64(int64(a)), "must be integral, i.e. lossless in float64")
	assert.Less(t, a, float64(1)*(1<<52), "52 bits keeps float64 exact")

	// Same input, same output; garbage input degrades to 0 rather than panicking.
	assert.Equal(t, a, hashToFloat("0123456789abcdef0123"))
	assert.Zero(t, hashToFloat("not-a-hash!!!"))
	assert.Zero(t, hashToFloat(""))
}

func TestShortHash(t *testing.T) {
	assert.Equal(t, "0123456789ab", shortHash("0123456789abcdef"))
	assert.Equal(t, "abc", shortHash("abc"))
	assert.Equal(t, "", shortHash(""))
}
