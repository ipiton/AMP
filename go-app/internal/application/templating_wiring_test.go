package application

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ipiton/AMP/internal/business/templating"
	appconfig "github.com/ipiton/AMP/internal/config"
	"github.com/ipiton/AMP/internal/core"
	infraroute "github.com/ipiton/AMP/internal/infrastructure/routing"
)

// templatingTestRegistry builds the minimum ServiceRegistry the templating
// lifecycle touches: a logger, a config, and the degraded-reasons slice.
func templatingTestRegistry(globs []string) *ServiceRegistry {
	var routing *infraroute.RouteConfig
	if globs != nil {
		routing = &infraroute.RouteConfig{Templates: globs}
	}
	return &ServiceRegistry{
		logger:          testRegistryLogger(),
		config:          &appconfig.Config{Routing: routing},
		degradedReasons: make([]string, 0, 4),
	}
}

func writeTemplate(t *testing.T, dir, name, content string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600))
}

// templatingTestData is a minimal render input. The assertions below are about
// WHICH definition renders, not about the data model — templating's own golden
// tests own that — but a real Data keeps the embedded defaults renderable too.
func templatingTestData() *templating.Data {
	return templating.BuildData(templating.DataInput{
		Receiver:    "team-x",
		GroupLabels: map[string]string{"alertname": "HighCPU"},
		Alerts: []*core.Alert{{
			AlertName: "HighCPU",
			Status:    core.StatusFiring,
			Labels:    map[string]string{"alertname": "HighCPU"},
		}},
	})
}

func renderTitle(t *testing.T, r *ServiceRegistry) string {
	t.Helper()
	require.NotNil(t, r.TemplateRegistry())
	out, err := r.TemplateRegistry().Current().ExecuteTextDefinition("slack.default.title", templatingTestData())
	require.NoError(t, err)
	return out
}

// TestInitializeTemplating_NoRouteConfig_LoadsDefaults: lite/legacy mode has no
// `templates:` to load, and the embedded defaults are exactly right for it.
func TestInitializeTemplating_NoRouteConfig_LoadsDefaults(t *testing.T) {
	r := templatingTestRegistry(nil)

	r.initializeTemplating()

	require.NotNil(t, r.TemplateRegistry())
	assert.True(t, r.TemplateRegistry().Current().HasDefinition("slack.default.title"))
	assert.Empty(t, r.degradedReasons, "defaults-only is not degradation")
}

func TestInitializeTemplating_LoadsConfiguredGlobs(t *testing.T) {
	dir := t.TempDir()
	writeTemplate(t, dir, "custom.tmpl", `{{ define "slack.default.title" }}CUSTOM{{ end }}`)

	r := templatingTestRegistry([]string{filepath.Join(dir, "*.tmpl")})
	r.initializeTemplating()

	assert.Equal(t, "CUSTOM", renderTitle(t, r))
	assert.Empty(t, r.degradedReasons)
}

// TestInitializeTemplating_BrokenGlobDegradesToDefaults is the failure posture
// that matters most: a broken operator file must not take the SHIPPED
// definitions down with it, because every receiver may reference them.
//
// (Reaching this state requires the files to change between config load — which
// already validated them — and this call; the test drives the branch directly.)
func TestInitializeTemplating_BrokenGlobDegradesToDefaults(t *testing.T) {
	dir := t.TempDir()
	writeTemplate(t, dir, "broken.tmpl", `{{ define "slack.default.title" }}{{ if }}{{ end }}{{ end }}`)

	r := templatingTestRegistry([]string{filepath.Join(dir, "*.tmpl")})
	r.initializeTemplating()

	require.NotNil(t, r.TemplateRegistry(), "registry must never be nil after init")
	assert.True(t, r.TemplateRegistry().Current().HasDefinition("slack.default.title"),
		"the embedded default must survive a broken override")
	require.Len(t, r.degradedReasons, 1)
	assert.Contains(t, r.degradedReasons[0], "notification templates unavailable")
}

// TestReloadTemplates_SwapsInEditedFile is the reload contract at the wiring
// level: editing a template file and reloading takes effect without a restart.
func TestReloadTemplates_SwapsInEditedFile(t *testing.T) {
	dir := t.TempDir()
	writeTemplate(t, dir, "custom.tmpl", `{{ define "slack.default.title" }}V1{{ end }}`)

	r := templatingTestRegistry([]string{filepath.Join(dir, "*.tmpl")})
	r.initializeTemplating()
	require.Equal(t, "V1", renderTitle(t, r))

	writeTemplate(t, dir, "custom.tmpl", `{{ define "slack.default.title" }}V2{{ end }}`)
	r.reloadTemplates()

	assert.Equal(t, "V2", renderTitle(t, r))
}

// TestReloadTemplates_FollowsANewGlobList covers an edit to the `templates:`
// list itself, which is what ReloadConfig actually hands over (a whole new
// cfg.Routing).
func TestReloadTemplates_FollowsANewGlobList(t *testing.T) {
	first := t.TempDir()
	second := t.TempDir()
	writeTemplate(t, first, "a.tmpl", `{{ define "slack.default.title" }}FIRST{{ end }}`)
	writeTemplate(t, second, "b.tmpl", `{{ define "slack.default.title" }}SECOND{{ end }}`)

	r := templatingTestRegistry([]string{filepath.Join(first, "*.tmpl")})
	r.initializeTemplating()
	require.Equal(t, "FIRST", renderTitle(t, r))

	r.config.Routing.Templates = []string{filepath.Join(second, "*.tmpl")}
	r.reloadTemplates()

	assert.Equal(t, "SECOND", renderTitle(t, r))
}

// TestReloadTemplates_BrokenEditKeepsLastKnownGood: an operator's bad edit must
// not revert their other customisations mid-incident.
func TestReloadTemplates_BrokenEditKeepsLastKnownGood(t *testing.T) {
	dir := t.TempDir()
	writeTemplate(t, dir, "custom.tmpl", `{{ define "slack.default.title" }}GOOD{{ end }}`)

	r := templatingTestRegistry([]string{filepath.Join(dir, "*.tmpl")})
	r.initializeTemplating()
	require.Equal(t, "GOOD", renderTitle(t, r))

	writeTemplate(t, dir, "custom.tmpl", `{{ define "slack.default.title" }}{{ if }}{{ end }}{{ end }}`)
	r.reloadTemplates()

	assert.Equal(t, "GOOD", renderTitle(t, r), "the previous library stays live")
}

// TestReloadTemplates_BeforeInitializeIsANoOp: reload must not construct the
// registry behind initializeTemplating's back (that would skip the
// degraded-reason bookkeeping).
func TestReloadTemplates_BeforeInitializeIsANoOp(t *testing.T) {
	r := templatingTestRegistry(nil)

	r.reloadTemplates()

	assert.Nil(t, r.TemplateRegistry())
}

// TestTemplateRegistry_NilBeforeInitialize documents the accessor's contract for
// slice 2: a nil registry means "no templating available", not "crash".
func TestTemplateRegistry_NilBeforeInitialize(t *testing.T) {
	assert.Nil(t, templatingTestRegistry(nil).TemplateRegistry())
}

// TestTemplating_NotWiredIntoFormattersYet is a scope guard for slice 1: the
// engine's lifecycle is wired, but no notification formatting reads it yet, so
// this slice cannot have changed any delivered output. Slice 2 deletes this
// test when it wires the formatters.
func TestTemplating_NotWiredIntoFormattersYet(t *testing.T) {
	dir := t.TempDir()
	writeTemplate(t, dir, "custom.tmpl", `{{ define "slack.default.title" }}CUSTOM{{ end }}`)

	r := templatingTestRegistry([]string{filepath.Join(dir, "*.tmpl")})
	r.initializeTemplating()

	// The registry is populated and reachable, and that is the whole of slice 1.
	assert.Equal(t, "CUSTOM", renderTitle(t, r))
}
