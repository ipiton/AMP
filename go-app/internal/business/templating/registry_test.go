package templating

import (
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewRegistry_LoadsDefaultsAndGlobs(t *testing.T) {
	dir := t.TempDir()
	writeTemplateFile(t, dir, "custom.tmpl", `{{ define "custom.title" }}V1{{ end }}`)

	registry, err := NewRegistry([]string{filepath.Join(dir, "*.tmpl")}, Options{})
	require.NoError(t, err)

	tmpl := registry.Current()
	require.NotNil(t, tmpl)
	assert.True(t, tmpl.HasDefinition("slack.default.title"))
	assert.True(t, tmpl.HasDefinition("custom.title"))
}

func TestNewRegistry_BrokenGlobIsALoadError(t *testing.T) {
	dir := t.TempDir()
	writeTemplateFile(t, dir, "broken.tmpl", `{{ define "x" }}{{ if }}{{ end }}{{ end }}`)

	registry, err := NewRegistry([]string{filepath.Join(dir, "*.tmpl")}, Options{})
	require.Error(t, err)
	assert.Nil(t, registry, "a failed load must not hand back a half-built registry")
}

// TestRegistry_ReloadSwapsTemplates is the reload contract: after Reload the
// live template renders the NEW definitions.
func TestRegistry_ReloadSwapsTemplates(t *testing.T) {
	dir := t.TempDir()
	glob := filepath.Join(dir, "*.tmpl")
	writeTemplateFile(t, dir, "custom.tmpl", `{{ define "custom.title" }}V1{{ end }}`)

	registry, err := NewRegistry([]string{glob}, Options{})
	require.NoError(t, err)

	got, err := registry.Current().ExecuteTextDefinition("custom.title", simpleData(t))
	require.NoError(t, err)
	assert.Equal(t, "V1", got)

	writeTemplateFile(t, dir, "custom.tmpl", `{{ define "custom.title" }}V2{{ end }}`)
	require.NoError(t, registry.Reload([]string{glob}))

	got, err = registry.Current().ExecuteTextDefinition("custom.title", simpleData(t))
	require.NoError(t, err)
	assert.Equal(t, "V2", got)
}

// TestRegistry_ReloadWithNewGlobList covers an operator EDITING the
// `templates:` list itself, not just the files it points at — including
// dropping a glob, after which its definitions must be gone and the shipped
// default must be back.
func TestRegistry_ReloadWithNewGlobList(t *testing.T) {
	dir := t.TempDir()
	writeTemplateFile(t, dir, "override.tmpl",
		`{{ define "slack.default.title" }}OVERRIDDEN{{ end }}`)

	registry, err := NewRegistry([]string{filepath.Join(dir, "*.tmpl")}, Options{})
	require.NoError(t, err)

	got, err := registry.Current().ExecuteTextDefinition("slack.default.title", simpleData(t))
	require.NoError(t, err)
	assert.Equal(t, "OVERRIDDEN", got)

	require.NoError(t, registry.Reload(nil))

	got, err = registry.Current().ExecuteTextDefinition("slack.default.title", simpleData(t))
	require.NoError(t, err)
	assert.Equal(t, "[FIRING:1] HighCPU (critical)", got,
		"dropping the glob restores the embedded default")
}

// TestRegistry_FailedReloadKeepsPreviousTemplate is the failure posture: a
// broken edit must not degrade notifications to bare defaults (or to nothing)
// while the operator fixes it.
func TestRegistry_FailedReloadKeepsPreviousTemplate(t *testing.T) {
	good := t.TempDir()
	writeTemplateFile(t, good, "custom.tmpl", `{{ define "custom.title" }}GOOD{{ end }}`)

	registry, err := NewRegistry([]string{filepath.Join(good, "*.tmpl")}, Options{})
	require.NoError(t, err)
	before := registry.Current()

	bad := t.TempDir()
	writeTemplateFile(t, bad, "broken.tmpl", `{{ define "custom.title" }}{{ if }}{{ end }}{{ end }}`)

	err = registry.Reload([]string{filepath.Join(bad, "*.tmpl")})
	require.Error(t, err)
	assert.Same(t, before, registry.Current(), "the live template must be untouched")

	got, execErr := registry.Current().ExecuteTextDefinition("custom.title", simpleData(t))
	require.NoError(t, execErr)
	assert.Equal(t, "GOOD", got)
}

// TestRegistry_ReloadPreservesOptions: the execution guards configured at
// startup must survive a reload — otherwise a reload silently removes the
// timeout and output cap.
func TestRegistry_ReloadPreservesOptions(t *testing.T) {
	registry, err := NewRegistry(nil, Options{MaxOutputBytes: 32})
	require.NoError(t, err)
	require.NoError(t, registry.Reload(nil))

	assert.Equal(t, 32, registry.Current().opts.MaxOutputBytes)
}

// TestRegistry_ConcurrentRenderDuringReload is the reason Registry exists at
// all: renders run from the notify chain while an operator reloads. Under -race
// this fails if reload mutated a template instead of swapping a new one in.
//
// Both alternative outputs are genuinely REACHABLE (review Minor 2: the first
// version accepted {"V", "V2"} while only ever writing "V", so the looser
// assertion could not have caught a stale-swap bug). The reloader alternates
// between two directories holding DIFFERENT definitions — swapping the glob list
// rather than rewriting a file, so no reader can observe a half-written file.
func TestRegistry_ConcurrentRenderDuringReload(t *testing.T) {
	dirV1 := t.TempDir()
	dirV2 := t.TempDir()
	writeTemplateFile(t, dirV1, "custom.tmpl", `{{ define "custom.title" }}V1{{ end }}`)
	writeTemplateFile(t, dirV2, "custom.tmpl", `{{ define "custom.title" }}V2{{ end }}`)
	globV1 := filepath.Join(dirV1, "*.tmpl")
	globV2 := filepath.Join(dirV2, "*.tmpl")

	registry, err := NewRegistry([]string{globV1}, Options{})
	require.NoError(t, err)

	data := simpleData(t)
	var wg sync.WaitGroup
	seen := make(chan string, 8*50)

	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 50 {
				out, execErr := registry.Current().ExecuteTextDefinition("custom.title", data)
				assert.NoError(t, execErr)
				// Never a partial or empty render: a reader always sees ONE
				// complete generation of the library, never a half-swapped one.
				assert.Contains(t, []string{"V1", "V2"}, out)
				seen <- out
			}
		}()
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := range 20 {
			glob := globV1
			if i%2 == 1 {
				glob = globV2
			}
			assert.NoError(t, registry.Reload([]string{glob}))
		}
	}()

	wg.Wait()
	close(seen)

	counts := map[string]int{}
	for out := range seen {
		counts[out]++
	}
	assert.NotEmpty(t, counts, "renders happened")
	// Not asserting that BOTH values appear — that would be a scheduling race.
	// What is asserted above and here is that every observed value is a whole,
	// valid generation.
	for value := range counts {
		assert.Contains(t, []string{"V1", "V2"}, value)
	}
}

// TestRegistry_ReloadIsVisibleToTheNextRender is the deterministic half of the
// concurrency test above: after Reload returns, the NEXT render sees the new
// generation — no staleness window.
func TestRegistry_ReloadIsVisibleToTheNextRender(t *testing.T) {
	dirV1 := t.TempDir()
	dirV2 := t.TempDir()
	writeTemplateFile(t, dirV1, "custom.tmpl", `{{ define "custom.title" }}V1{{ end }}`)
	writeTemplateFile(t, dirV2, "custom.tmpl", `{{ define "custom.title" }}V2{{ end }}`)

	registry, err := NewRegistry([]string{filepath.Join(dirV1, "*.tmpl")}, Options{})
	require.NoError(t, err)

	for i := range 6 {
		want := "V1"
		glob := filepath.Join(dirV1, "*.tmpl")
		if i%2 == 1 {
			want, glob = "V2", filepath.Join(dirV2, "*.tmpl")
		}
		require.NoError(t, registry.Reload([]string{glob}))

		got, execErr := registry.Current().ExecuteTextDefinition("custom.title", simpleData(t))
		require.NoError(t, execErr)
		assert.Equal(t, want, got)
	}
}

// TestRegistry_ExposesGlobMatches: the reload path needs the same zero-match
// diagnostics as the load path (review I3), so they must survive the swap.
func TestRegistry_ExposesGlobMatches(t *testing.T) {
	empty := filepath.Join(t.TempDir(), "*.tmpl")

	registry, err := NewRegistry([]string{empty}, Options{})
	require.NoError(t, err)
	assert.Equal(t, []string{empty}, registry.Current().UnmatchedGlobs())

	populated := t.TempDir()
	writeTemplateFile(t, populated, "a.tmpl", `{{ define "a" }}a{{ end }}`)
	require.NoError(t, registry.Reload([]string{filepath.Join(populated, "*.tmpl")}))

	assert.Empty(t, registry.Current().UnmatchedGlobs())
}
