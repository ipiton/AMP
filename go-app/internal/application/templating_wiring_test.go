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

// TestInitializeTemplating_ReportsUnmatchedGlobs is the diagnostic half of
// review I3/C1: a configured glob that matched nothing is legal, but it must be
// visible — before this, "Notification templates loaded" logged an identical
// definition count whether the operator's files loaded or not.
//
// The assertion is on the DATA the warning is built from (UnmatchedGlobs and the
// parsed file list), which is the part that can regress silently; the log call
// itself is a one-liner over that data.
func TestInitializeTemplating_ReportsUnmatchedGlobs(t *testing.T) {
	populated := t.TempDir()
	writeTemplate(t, populated, "custom.tmpl", `{{ define "custom.title" }}X{{ end }}`)
	emptyGlob := filepath.Join(t.TempDir(), "*.tmpl")

	r := templatingTestRegistry([]string{filepath.Join(populated, "*.tmpl"), emptyGlob})
	r.initializeTemplating()

	require.NotNil(t, r.TemplateRegistry())
	assert.Equal(t, []string{emptyGlob}, r.TemplateRegistry().Current().UnmatchedGlobs())
	assert.Len(t, loadedTemplateFiles(r.TemplateRegistry()), 1,
		"the log line must be able to say WHICH files loaded, not just a count")
	assert.Empty(t, r.degradedReasons, "an empty match is legal, not degradation")
}

// TestReloadTemplates_ReportsUnmatchedGlobsAfterSwap: the same diagnostic must
// survive a reload, since a reload is where a glob typically starts matching (or
// stops).
func TestReloadTemplates_ReportsUnmatchedGlobsAfterSwap(t *testing.T) {
	dir := t.TempDir()
	glob := filepath.Join(dir, "*.tmpl")

	r := templatingTestRegistry([]string{glob})
	r.initializeTemplating()
	require.Equal(t, []string{glob}, r.TemplateRegistry().Current().UnmatchedGlobs(),
		"nothing has been mounted yet")

	// The ConfigMap mounts.
	writeTemplate(t, dir, "custom.tmpl", `{{ define "custom.title" }}X{{ end }}`)
	r.reloadTemplates()

	assert.Empty(t, r.TemplateRegistry().Current().UnmatchedGlobs())
	assert.Len(t, loadedTemplateFiles(r.TemplateRegistry()), 1)
}

// TestInitializeTemplating_KillSwitchDisablesItWholesale is the config half of
// slice-2 review C1c: publishing.templates.enabled=false must leave the registry
// NIL, because that is what makes the revert wholesale — initializePublishingRuntime
// only wires a non-nil registry, so no publisher gets the template decorator and
// no `templates:` file or presentation field is rendered anywhere.
//
// Asserted with a glob that WOULD have loaded, so the test fails if the switch is
// only checked further down the path.
func TestInitializeTemplating_KillSwitchDisablesItWholesale(t *testing.T) {
	dir := t.TempDir()
	writeTemplate(t, dir, "custom.tmpl", `{{ define "slack.default.title" }}CUSTOM{{ end }}`)

	r := templatingTestRegistry([]string{filepath.Join(dir, "*.tmpl")})
	disabled := false
	r.config.Publishing.Templates.Enabled = &disabled

	r.initializeTemplating()

	assert.Nil(t, r.TemplateRegistry(),
		"a nil registry is the signal the publishing runtime reads as 'no templating'")
	assert.Empty(t, r.degradedReasons, "an operator's deliberate choice is not degradation")
}

// TestInitializeTemplating_KillSwitchDefaultsToEnabled: absent must mean ON, since
// that is the drop-in-parity default the epic ships.
func TestInitializeTemplating_KillSwitchDefaultsToEnabled(t *testing.T) {
	r := templatingTestRegistry(nil)
	require.Nil(t, r.config.Publishing.Templates.Enabled, "the fixture must not set it")

	r.initializeTemplating()

	require.NotNil(t, r.TemplateRegistry())
	assert.True(t, r.TemplateRegistry().Current().HasDefinition("slack.default.title"))
}

// TestReloadTemplates_KillSwitchSurvivesAReload: with templating off there is no
// registry to swap, so a reload must stay a no-op rather than quietly enabling it.
func TestReloadTemplates_KillSwitchSurvivesAReload(t *testing.T) {
	dir := t.TempDir()
	writeTemplate(t, dir, "custom.tmpl", `{{ define "slack.default.title" }}CUSTOM{{ end }}`)

	r := templatingTestRegistry([]string{filepath.Join(dir, "*.tmpl")})
	disabled := false
	r.config.Publishing.Templates.Enabled = &disabled
	r.initializeTemplating()

	r.reloadTemplates()

	assert.Nil(t, r.TemplateRegistry(),
		"flipping the switch back on is a restart, not a reload — and a reload must not do it by accident")
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

// TestTemplateRegistry_IsReachableForSliceTwo asserts what slice 1 actually
// delivers: the library is loaded, live, and reachable through the accessor
// slice 2 will call. (Renamed from a previous name that promised a structural
// "nothing reads it yet" guarantee it did not check — review Minor 3. That
// guarantee is now asserted for real, by the test below.)
func TestTemplateRegistry_IsReachableForSliceTwo(t *testing.T) {
	dir := t.TempDir()
	writeTemplate(t, dir, "custom.tmpl", `{{ define "slack.default.title" }}CUSTOM{{ end }}`)

	r := templatingTestRegistry([]string{filepath.Join(dir, "*.tmpl")})
	r.initializeTemplating()

	assert.Equal(t, "CUSTOM", renderTitle(t, r))
}

// The slice-1 structural scope guard TestPublishingDoesNotImportTemplatingYet
// lived here and was DELETED DELIBERATELY by slice 2, exactly as its own doc
// comment instructed: it asserted that no publishing package imports
// internal/business/templating, which was the checkable form of "slice 1 cannot
// have changed delivered output".
//
// That is no longer true, and must not be: infrastructure/publishing now imports
// templating (template_formatter.go) so a receiver's own title/text/description/
// message/subject templates render onto the wire. The replacement guarantee is
// behavioural rather than structural, and lives in
// infrastructure/publishing/template_formatter_test.go: a target with NO
// template fields gets the fixed formatter's payload byte-for-byte, and a
// template that fails to render falls back to it.
