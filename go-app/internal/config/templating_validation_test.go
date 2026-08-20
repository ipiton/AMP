package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ipiton/AMP/internal/business/templating"
	infraroute "github.com/ipiton/AMP/internal/infrastructure/routing"
)

// configWithTemplates renders an Alertmanager-shaped config whose `templates:`
// section points at glob. The glob is interpolated verbatim, so callers may pass
// it quoted (`'templates/*.tmpl'`) — YAML needs the quotes for a value starting
// with `*`.
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
	require.NoError(t, validateTemplateGlobs(&infraroute.RouteConfig{}))
}

// ============================================================================
// Relative-glob resolution (review C1)
// ============================================================================
//
// EVERY test above and in the slice's other files builds an ABSOLUTE glob, which
// is exactly why the bug these tests cover went unnoticed: a relative glob —
// `templates: ['templates/*.tmpl']`, upstream's canonical idiom — used to
// resolve against the process CWD, match nothing, and load clean.
//
// writeConfigInDir puts the config file itself inside a temp dir so a relative
// glob has a meaningful base directory, which writeTempYAML (shared, writes to
// its own temp dir) cannot express.
func writeConfigInDir(t *testing.T, dir, content string) string {
	t.Helper()
	path := filepath.Join(dir, "amp.yaml")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	return path
}

// TestLoadConfig_Templates_RelativeGlobResolvesAgainstConfigDir is the C1
// regression test: the upstream idiom must load the operator's file, from a
// process whose CWD is somewhere else entirely.
func TestLoadConfig_Templates_RelativeGlobResolvesAgainstConfigDir(t *testing.T) {
	resetViper()
	unsetEnvKeys("SERVER_PORT")

	configDir := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(configDir, "templates"), 0o750))
	writeTemplateFile(t, filepath.Join(configDir, "templates"), "custom.tmpl",
		`{{ define "slack.default.title" }}CUSTOM{{ end }}`)

	path := writeConfigInDir(t, configDir, configWithTemplates("'templates/*.tmpl'"))

	// The process CWD is the package directory, NOT configDir — the point of the
	// test. No os.Chdir: that would be a global mutation and would also mask the
	// bug if the fix were wrong in the other direction.
	cfg, err := LoadConfig(path)
	require.NoError(t, err)
	require.NotNil(t, cfg.Routing)

	require.Len(t, cfg.Routing.Templates, 1)
	resolved := cfg.Routing.Templates[0]
	assert.True(t, filepath.IsAbs(resolved), "the glob must be resolved to an absolute path, got %q", resolved)
	assert.Equal(t, filepath.Join(configDir, "templates", "*.tmpl"), resolved)

	// And it must actually LOAD: the whole failure mode was an empty match that
	// looked like success.
	tmpl, err := templating.FromGlobs(cfg.Routing.Templates, templating.Options{})
	require.NoError(t, err)
	assert.Empty(t, tmpl.UnmatchedGlobs(), "the resolved glob must match the operator's file")

	got, err := tmpl.ExecuteTextDefinition("slack.default.title", templating.BuildData(templating.DataInput{Receiver: "r"}))
	require.NoError(t, err)
	assert.Equal(t, "CUSTOM", got, "the custom definition must win over the shipped default")
}

// TestLoadConfig_Templates_RelativeGlobBrokenFileFailsLoad closes the other half
// of C1: once the relative glob resolves correctly, its files are also VALIDATED
// — a broken one fails the load naming file and line, instead of being invisible
// because the glob matched nothing.
func TestLoadConfig_Templates_RelativeGlobBrokenFileFailsLoad(t *testing.T) {
	resetViper()
	unsetEnvKeys("SERVER_PORT")

	configDir := t.TempDir()
	writeTemplateFile(t, configDir, "broken.tmpl",
		"{{ define \"fine\" }}ok{{ end }}\n{{ define \"bad\" }}\n{{ if }}\n{{ end }}\n")

	path := writeConfigInDir(t, configDir, configWithTemplates("'*.tmpl'"))

	cfg, err := LoadConfig(path)
	require.Error(t, err)
	assert.Nil(t, cfg)
	assert.Contains(t, err.Error(), "broken.tmpl:3")
}

// TestLoadConfig_Templates_AbsoluteGlobIsLeftUntouched: upstream's `len(fp) > 0
// && !filepath.IsAbs(fp)` guard, in test form.
func TestLoadConfig_Templates_AbsoluteGlobIsLeftUntouched(t *testing.T) {
	resetViper()
	unsetEnvKeys("SERVER_PORT")

	templateDir := t.TempDir()
	writeTemplateFile(t, templateDir, "custom.tmpl", `{{ define "custom.title" }}X{{ end }}`)
	absGlob := filepath.Join(templateDir, "*.tmpl")

	// Config file lives in a DIFFERENT directory: if the absolute glob were
	// joined onto the config dir it would match nothing.
	path := writeConfigInDir(t, t.TempDir(), configWithTemplates("'"+absGlob+"'"))

	cfg, err := LoadConfig(path)
	require.NoError(t, err)
	require.NotNil(t, cfg.Routing)
	assert.Equal(t, []string{absGlob}, cfg.Routing.Templates)
}

// TestResolveTemplateGlobs_Unit covers the resolver's edge cases directly,
// including the empty-string guard (filepath.Join(base, "") would silently
// become the base DIRECTORY, turning "" into a glob that matches a real path).
func TestResolveTemplateGlobs_Unit(t *testing.T) {
	base := filepath.Join("/etc", "amp")
	configPath := filepath.Join(base, "amp.yaml")

	rc := &infraroute.RouteConfig{Templates: []string{
		"templates/*.tmpl",
		filepath.Join("/abs", "path", "*.tmpl"),
		"",
		filepath.Join("nested", "deeper", "*.tmpl"),
	}}

	resolveTemplateGlobs(rc, configPath)

	assert.Equal(t, []string{
		filepath.Join(base, "templates", "*.tmpl"),
		filepath.Join("/abs", "path", "*.tmpl"),
		"",
		filepath.Join(base, "nested", "deeper", "*.tmpl"),
	}, rc.Templates)
}

func TestResolveTemplateGlobs_NoOpCases(t *testing.T) {
	// nil config, empty list, and an empty configPath must all be no-ops rather
	// than panics or accidental rewrites.
	resolveTemplateGlobs(nil, "/etc/amp/amp.yaml")

	rc := &infraroute.RouteConfig{Templates: []string{"templates/*.tmpl"}}
	resolveTemplateGlobs(rc, "")
	assert.Equal(t, []string{"templates/*.tmpl"}, rc.Templates,
		"without a config path there is no base directory to resolve against")
}

// TestLoadConfig_Templates_UnmatchedGlobIsSurfaced is the I3 detection half: the
// load succeeds (legal), and the caller can see WHICH glob found nothing. The
// warning itself goes through slog; what is asserted here is the data the
// warning is built from, which is the part that can regress silently.
func TestLoadConfig_Templates_UnmatchedGlobIsSurfaced(t *testing.T) {
	resetViper()
	unsetEnvKeys("SERVER_PORT")

	configDir := t.TempDir()
	path := writeConfigInDir(t, configDir, configWithTemplates("'templates/*.tmpl'"))

	cfg, err := LoadConfig(path)
	require.NoError(t, err)

	tmpl, err := templating.FromGlobs(cfg.Routing.Templates, templating.Options{})
	require.NoError(t, err)
	assert.Equal(t, []string{filepath.Join(configDir, "templates", "*.tmpl")}, tmpl.UnmatchedGlobs())
}
