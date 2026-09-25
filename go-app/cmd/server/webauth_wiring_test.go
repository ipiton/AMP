package main

import (
	"testing"

	"github.com/ipiton/AMP/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// PROD-AUTH: the web config path must survive the config-file fallback in
// main, which does not read env; losing it would silently open the API.
func TestResolveWebConfigFile_Precedence(t *testing.T) {
	t.Setenv(webConfigFileEnv, " /env.yml ")

	assert.Equal(t, "/flag.yml", resolveWebConfigFile("/flag.yml", "/config.yml"))
	assert.Equal(t, "/config.yml", resolveWebConfigFile("  ", "/config.yml"))
	assert.Equal(t, "/env.yml", resolveWebConfigFile("", ""))

	t.Setenv(webConfigFileEnv, "")
	assert.Empty(t, resolveWebConfigFile("", ""))
}

func TestNewWebAuth_DisabledWithoutPath(t *testing.T) {
	webAuth, err := newWebAuth(&config.Config{}, nil, nil)
	require.NoError(t, err)
	assert.Nil(t, webAuth)
}
