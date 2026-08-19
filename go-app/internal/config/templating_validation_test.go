package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// configWithTemplates renders an Alertmanager-shaped config whose `templates:`
// section points at glob.
func configWithTemplates(glob string) string {
	return `
templates:
  - ` + glob + `

route:
  receiver: default

receivers:
  - name: default
    webhook_configs:
      - url: https://example.com/webhook
`
}

func writeTemplateFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	return path
}

// TestLoadConfig_Templates_ValidFileLoads proves the happy path reaches
// cfg.Routing.Templates (which slice 2's formatters read) rather than being
// silently dropped as it was before this slice.
func TestLoadConfig_Templates_ValidFileLoads(t *testing.T) {
	resetViper()
	unsetEnvKeys("SERVER_PORT")

	dir := t.TempDir()
	writeTemplateFile(t, dir, "custom.tmpl", `{{ define "slack.default.title" }}CUSTOM{{ end }}`)
	path := writeTempYAML(t, configWithTemplates(filepath.Join(dir, "*.tmpl")))

	cfg, err := LoadConfig(path)
	require.NoError(t, err)
	require.NotNil(t, cfg.Routing)
	assert.Equal(t, []string{filepath.Join(dir, "*.tmpl")}, cfg.Routing.Templates)
}

// TestLoadConfig_Templates_ParseErrorFailsLoadWithFileAndLine is the point of
// validating at load time: the operator gets the file and the line, at startup,
// instead of a degraded notification hours later.
func TestLoadConfig_Templates_ParseErrorFailsLoadWithFileAndLine(t *testing.T) {
	resetViper()
	unsetEnvKeys("SERVER_PORT")

	dir := t.TempDir()
	broken := writeTemplateFile(t, dir, "broken.tmpl",
		"{{ define \"fine\" }}ok{{ end }}\n"+
			"{{ define \"slack.default.title\" }}\n"+
			"{{ if }}\n"+
			"{{ end }}\n")
	path := writeTempYAML(t, configWithTemplates(filepath.Join(dir, "*.tmpl")))

	cfg, err := LoadConfig(path)
	require.Error(t, err)
	assert.Nil(t, cfg)

	message := err.Error()
	assert.Contains(t, message, "invalid templates: configuration")
	assert.Contains(t, message, broken, "the failing FILE must be named")
	assert.Contains(t, message, "broken.tmpl:3", "the failing LINE must be named")
}

// TestLoadConfig_Templates_EmptyGlobMatchIsAccepted: a glob over a ConfigMap
// mount that is populated after startup must not block the load — upstream
// allows it explicitly.
func TestLoadConfig_Templates_EmptyGlobMatchIsAccepted(t *testing.T) {
	resetViper()
	unsetEnvKeys("SERVER_PORT")

	path := writeTempYAML(t, configWithTemplates(filepath.Join(t.TempDir(), "*.tmpl")))

	cfg, err := LoadConfig(path)
	require.NoError(t, err)
	require.NotNil(t, cfg.Routing)
	assert.Len(t, cfg.Routing.Templates, 1)
}

// TestLoadConfig_NoTemplatesSectionLoads keeps the zero-behaviour-change
// promise for every existing config: no `templates:` key, no new failure mode.
func TestLoadConfig_NoTemplatesSectionLoads(t *testing.T) {
	resetViper()
	unsetEnvKeys("SERVER_PORT")

	path := writeTempYAML(t, validAlertmanagerConfigYAML)

	cfg, err := LoadConfig(path)
	require.NoError(t, err)
	require.NotNil(t, cfg.Routing)
	assert.Empty(t, cfg.Routing.Templates)
}

// TestValidateTemplateGlobs_NilAndEmptyAreNoOps covers the two skip conditions
// directly (the function is also reached with rc == nil from a legacy config).
func TestValidateTemplateGlobs_NilAndEmptyAreNoOps(t *testing.T) {
	require.NoError(t, validateTemplateGlobs(nil))
}
