package templating

import (
	"errors"
	tmplhtml "html/template"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	tmpltext "text/template"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ipiton/AMP/internal/core"
)

// writeTemplateFile creates dir/name with content and returns its path.
func writeTemplateFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	return path
}

func simpleData(t *testing.T) *Data {
	t.Helper()
	return BuildData(DataInput{
		Receiver:    "team-x",
		GroupLabels: map[string]string{"alertname": "HighCPU"},
		Alerts: []*core.Alert{{
			AlertName: "HighCPU",
			Status:    core.StatusFiring,
			Labels:    map[string]string{"alertname": "HighCPU", "severity": "critical"},
			StartsAt:  time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
		}},
		ExternalURL: "http://amp.example.com",
	})
}

// === templates: glob loading and override precedence ===

// TestFromGlobs_UserTemplateOverridesDefault is the core `templates:` contract:
// a user definition of a default name wins, because later definitions win.
func TestFromGlobs_UserTemplateOverridesDefault(t *testing.T) {
	dir := t.TempDir()
	writeTemplateFile(t, dir, "custom.tmpl",
		`{{ define "slack.default.title" }}CUSTOM {{ .GroupLabels.alertname }}{{ end }}`)

	tmpl, err := FromGlobs([]string{filepath.Join(dir, "*.tmpl")}, Options{})
	require.NoError(t, err)

	got, err := tmpl.ExecuteTextDefinition("slack.default.title", simpleData(t))
	require.NoError(t, err)
	assert.Equal(t, "CUSTOM HighCPU", got)

	// A default the user did NOT override still resolves.
	got, err = tmpl.ExecuteTextDefinition("pagerduty.default.description", simpleData(t))
	require.NoError(t, err)
	assert.Equal(t, "[FIRING:1] HighCPU (critical)", got)
}

// TestFromGlobs_LastGlobWins covers the multi-file precedence rule operators
// actually hit: two globs defining the same name resolve to the later glob.
func TestFromGlobs_LastGlobWins(t *testing.T) {
	first := t.TempDir()
	second := t.TempDir()
	writeTemplateFile(t, first, "a.tmpl", `{{ define "custom.title" }}FIRST{{ end }}`)
	writeTemplateFile(t, second, "a.tmpl", `{{ define "custom.title" }}SECOND{{ end }}`)

	tmpl, err := FromGlobs([]string{
		filepath.Join(first, "*.tmpl"),
		filepath.Join(second, "*.tmpl"),
	}, Options{})
	require.NoError(t, err)

	got, err := tmpl.ExecuteTextDefinition("custom.title", simpleData(t))
	require.NoError(t, err)
	assert.Equal(t, "SECOND", got)
}

// TestFromGlobs_WithinOneGlobFilesAreOrdered pins the intra-glob order to
// lexical, so precedence between two files matched by one glob is deterministic
// rather than filesystem-dependent.
func TestFromGlobs_WithinOneGlobFilesAreOrdered(t *testing.T) {
	dir := t.TempDir()
	writeTemplateFile(t, dir, "01-base.tmpl", `{{ define "custom.title" }}BASE{{ end }}`)
	writeTemplateFile(t, dir, "99-override.tmpl", `{{ define "custom.title" }}OVERRIDE{{ end }}`)

	tmpl, err := FromGlobs([]string{filepath.Join(dir, "*.tmpl")}, Options{})
	require.NoError(t, err)

	got, err := tmpl.ExecuteTextDefinition("custom.title", simpleData(t))
	require.NoError(t, err)
	assert.Equal(t, "OVERRIDE", got)
}

// TestFromGlobs_EmptyMatchIsNotAnError is upstream's explicit allowance: a glob
// pointing at a ConfigMap mount that is not populated yet must not fail startup.
func TestFromGlobs_EmptyMatchIsNotAnError(t *testing.T) {
	tmpl, err := FromGlobs([]string{filepath.Join(t.TempDir(), "*.tmpl")}, Options{})
	require.NoError(t, err)
	assert.True(t, tmpl.HasDefinition("slack.default.title"), "defaults still load")
}

func TestFromGlobs_NilAndEmptyGlobs_LoadDefaultsOnly(t *testing.T) {
	for _, globs := range [][]string{nil, {}} {
		tmpl, err := FromGlobs(globs, Options{})
		require.NoError(t, err)
		assert.True(t, tmpl.HasDefinition("email.default.subject"))
	}
}

// TestFromGlobs_ParseErrorNamesFileAndLine is a hard requirement of this slice:
// an operator must be able to find the broken line without bisecting their
// template directory.
func TestFromGlobs_ParseErrorNamesFileAndLine(t *testing.T) {
	dir := t.TempDir()
	path := writeTemplateFile(t, dir, "broken.tmpl",
		"{{ define \"ok\" }}fine{{ end }}\n"+
			"{{ define \"bad\" }}\n"+
			"{{ if }}\n"+
			"{{ end }}\n")

	_, err := FromGlobs([]string{filepath.Join(dir, "*.tmpl")}, Options{})
	require.Error(t, err)

	message := err.Error()
	assert.Contains(t, message, path, "error must name the offending FILE")
	assert.Contains(t, message, "broken.tmpl:3", "error must name the offending LINE")
}

// TestFromGlob_ParseErrorNamesTheRightFileAmongMany guards the reason this
// package parses files one by one instead of using ParseGlob: with several
// files matched, the message must point at the broken one.
func TestFromGlob_ParseErrorNamesTheRightFileAmongMany(t *testing.T) {
	dir := t.TempDir()
	writeTemplateFile(t, dir, "a-good.tmpl", `{{ define "a" }}a{{ end }}`)
	broken := writeTemplateFile(t, dir, "b-broken.tmpl", `{{ define "b" }}{{ if }}{{ end }}{{ end }}`)
	writeTemplateFile(t, dir, "c-good.tmpl", `{{ define "c" }}c{{ end }}`)

	_, err := FromGlobs([]string{filepath.Join(dir, "*.tmpl")}, Options{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), broken)
	assert.NotContains(t, err.Error(), "a-good.tmpl")
}

func TestFromGlob_MalformedGlobPatternIsAnError(t *testing.T) {
	_, err := FromGlobs([]string{"[invalid"}, Options{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid template glob")
}

// TestFromGlob_UnreadableFileIsAnError: a glob matching something that cannot
// be read must fail loudly at load, not silently drop the operator's templates.
func TestFromGlob_UnreadableFileIsAnError(t *testing.T) {
	dir := t.TempDir()
	path := writeTemplateFile(t, dir, "secret.tmpl", `{{ define "x" }}x{{ end }}`)
	require.NoError(t, os.Chmod(path, 0o000))
	t.Cleanup(func() { _ = os.Chmod(path, 0o600) })

	if _, err := os.ReadFile(path); err == nil {
		t.Skip("running as a user that ignores file permissions (root); nothing to assert")
	}

	_, err := FromGlobs([]string{filepath.Join(dir, "*.tmpl")}, Options{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), path)
}

// TestFromGlob_DirectoryMatchIsSkipped: `templates/*` legitimately matches
// subdirectories; skipping them beats failing the whole load.
func TestFromGlob_DirectoryMatchIsSkipped(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(dir, "subdir"), 0o750))
	writeTemplateFile(t, dir, "ok.tmpl", `{{ define "custom.title" }}OK{{ end }}`)

	tmpl, err := FromGlobs([]string{filepath.Join(dir, "*")}, Options{})
	require.NoError(t, err)
	assert.True(t, tmpl.HasDefinition("custom.title"))
}

// TestFromGlobs_UserTemplateMayReferenceDefaults: composing on top of the
// default library is the common real-world case (a custom title that reuses
// `__subject`), and it only works because the defaults are parsed first.
func TestFromGlobs_UserTemplateMayReferenceDefaults(t *testing.T) {
	dir := t.TempDir()
	writeTemplateFile(t, dir, "custom.tmpl",
		`{{ define "slack.default.title" }}[prod] {{ template "__subject" . }}{{ end }}`)

	tmpl, err := FromGlobs([]string{filepath.Join(dir, "*.tmpl")}, Options{})
	require.NoError(t, err)

	got, err := tmpl.ExecuteTextDefinition("slack.default.title", simpleData(t))
	require.NoError(t, err)
	assert.Equal(t, "[prod] [FIRING:1] HighCPU (critical)", got)
}

// TestFromGlobs_TemplateUsingRouteLabelsStillLoads: the `routeLabels` function
// is registered precisely so an upstream config using the unported feature
// loads instead of taking every other definition in the file down with it.
func TestFromGlobs_TemplateUsingRouteLabelsStillLoads(t *testing.T) {
	dir := t.TempDir()
	writeTemplateFile(t, dir, "routelabels.tmpl",
		`{{ define "custom.title" }}[{{ routeLabels "team" }}] ok{{ end }}`)

	tmpl, err := FromGlobs([]string{filepath.Join(dir, "*.tmpl")}, Options{})
	require.NoError(t, err)

	got, err := tmpl.ExecuteTextDefinition("custom.title", simpleData(t))
	require.NoError(t, err)
	assert.Equal(t, "[] ok", got, "unported feature renders empty rather than failing the load")
}

// === execution: inline expressions, definitions, errors ===

func TestExecuteTextString_EmptyExpressionIsEmpty(t *testing.T) {
	tmpl, err := FromGlobs(nil, Options{})
	require.NoError(t, err)

	got, err := tmpl.ExecuteTextString("", simpleData(t))
	require.NoError(t, err)
	assert.Equal(t, "", got)
}

func TestExecuteTextString_InlineExpressionsOverData(t *testing.T) {
	tmpl, err := FromGlobs(nil, Options{})
	require.NoError(t, err)

	cases := map[string]string{
		`{{ .Status }}`:                                     "firing",
		`{{ .Receiver }}`:                                   "team-x",
		`{{ .CommonLabels.severity | toUpper }}`:            "CRITICAL",
		`{{ .CommonLabels.SortedPairs.Values | join "/" }}`: "HighCPU/critical",
		`{{ len .Alerts.Firing }}`:                          "1",
		`{{ .ExternalURL }}`:                                "http://amp.example.com",
	}

	for expr, want := range cases {
		got, err := tmpl.ExecuteTextString(expr, simpleData(t))
		require.NoError(t, err, expr)
		assert.Equal(t, want, got, expr)
	}
}

// TestExecuteTextString_MissingKeyRendersEmpty pins the `missingkey=zero`
// option: referencing a label that does not exist is extremely common in
// hand-written templates and must render empty, not error.
func TestExecuteTextString_MissingKeyRendersEmpty(t *testing.T) {
	tmpl, err := FromGlobs(nil, Options{})
	require.NoError(t, err)

	got, err := tmpl.ExecuteTextString(`[{{ .CommonLabels.nope }}]`, simpleData(t))
	require.NoError(t, err)
	assert.Equal(t, "[]", got)
}

func TestExecuteTextString_MalformedExpressionIsAnError(t *testing.T) {
	tmpl, err := FromGlobs(nil, Options{})
	require.NoError(t, err)

	_, err = tmpl.ExecuteTextString(`{{ if }}`, simpleData(t))
	require.Error(t, err)
}

// TestExecuteTextString_ParsingDoesNotMutateTheTemplate: execution clones, so
// two different inline expressions cannot clobber each other — the property
// that makes a single *Template safe to share across all receivers.
func TestExecuteTextString_ParsingDoesNotMutateTheTemplate(t *testing.T) {
	tmpl, err := FromGlobs(nil, Options{})
	require.NoError(t, err)
	before := tmpl.DefinitionNames()

	_, err = tmpl.ExecuteTextString(`{{ define "sneaky" }}x{{ end }}{{ .Status }}`, simpleData(t))
	require.NoError(t, err)

	assert.Equal(t, before, tmpl.DefinitionNames())
	assert.False(t, tmpl.HasDefinition("sneaky"))
}

func TestExecuteTextDefinition_MissingDefinitionIsErrNotDefined(t *testing.T) {
	tmpl, err := FromGlobs(nil, Options{})
	require.NoError(t, err)

	_, err = tmpl.ExecuteTextDefinition("no.such.template", simpleData(t))
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrNotDefined))
	assert.Contains(t, err.Error(), "no.such.template")
}

func TestExecuteHTMLDefinition_MissingDefinitionIsErrNotDefined(t *testing.T) {
	tmpl, err := FromGlobs(nil, Options{})
	require.NoError(t, err)

	_, err = tmpl.ExecuteHTMLDefinition("no.such.template", simpleData(t))
	require.True(t, errors.Is(err, ErrNotDefined))
}

// TestExecuteHTMLString_EscapesWhereTextDoesNot is the whole reason two
// instances are kept: the same source renders raw as text and escaped as HTML.
func TestExecuteHTMLString_EscapesWhereTextDoesNot(t *testing.T) {
	tmpl, err := FromGlobs(nil, Options{})
	require.NoError(t, err)

	data := BuildData(DataInput{
		Receiver: "r",
		Alerts: []*core.Alert{{
			AlertName: "A",
			Status:    core.StatusFiring,
			Labels:    map[string]string{"alertname": "A", "payload": `<b>&"</b>`},
		}},
	})

	text, err := tmpl.ExecuteTextString(`{{ .CommonLabels.payload }}`, data)
	require.NoError(t, err)
	assert.Equal(t, `<b>&"</b>`, text)

	html, err := tmpl.ExecuteHTMLString(`{{ .CommonLabels.payload }}`, data)
	require.NoError(t, err)
	assert.Equal(t, `&lt;b&gt;&amp;&#34;&lt;/b&gt;`, html)

	safe, err := tmpl.ExecuteHTMLString(`{{ .CommonLabels.payload | safeHtml }}`, data)
	require.NoError(t, err)
	assert.Equal(t, `<b>&"</b>`, safe, "safeHtml is the documented escape hatch")
}

// === execution guards (AMP divergences from upstream) ===

// TestExecute_OutputCapAborts pins the size guard: a template that fans out
// over a large group must fail with ErrOutputTooLarge rather than allocating
// without bound.
func TestExecute_OutputCapAborts(t *testing.T) {
	tmpl, err := FromGlobs(nil, Options{MaxOutputBytes: 64})
	require.NoError(t, err)

	_, err = tmpl.ExecuteTextString(`{{ range $i := .Alerts }}{{ end }}`+strings.Repeat("x", 128), simpleData(t))
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrOutputTooLarge), "got %v", err)
}

func TestExecute_OutputJustUnderCapSucceeds(t *testing.T) {
	tmpl, err := FromGlobs(nil, Options{MaxOutputBytes: 16})
	require.NoError(t, err)

	got, err := tmpl.ExecuteTextString(strings.Repeat("x", 16), simpleData(t))
	require.NoError(t, err)
	assert.Len(t, got, 16)
}

// TestExecute_HTMLOutputCapAborts: the guard applies to the HTML instance too
// (email bodies are the largest renders AMP performs).
func TestExecute_HTMLOutputCapAborts(t *testing.T) {
	tmpl, err := FromGlobs(nil, Options{MaxOutputBytes: 256})
	require.NoError(t, err)

	_, err = tmpl.ExecuteHTMLDefinition("email.default.html", simpleData(t))
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrOutputTooLarge), "got %v", err)
}

// TestExecute_TimeoutAborts pins the time guard. An already-expired deadline is
// used rather than a slow template so the test is deterministic and instant.
func TestExecute_TimeoutAborts(t *testing.T) {
	tmpl, err := FromGlobs(nil, Options{Timeout: -1})
	require.NoError(t, err)
	// Timeout <= 0 means "use the default", so build the expired case directly.
	tmpl.opts.Timeout = time.Nanosecond
	time.Sleep(time.Millisecond)

	_, err = tmpl.ExecuteTextString(`{{ .Status }}`, simpleData(t))
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrTimeout), "got %v", err)
}

// TestExecute_PanickingFuncNeverPanicsTheCaller is the "never panics the notify
// chain" contract, stated as the property that actually matters: whatever a
// template does, the caller gets an error.
//
// reReplaceAll uses regexp.MustCompile exactly as upstream does, so a bad
// pattern panics inside the function call. text/template's own safeCall already
// converts that particular panic into an error (Go 1.21+), and guarded()'s
// recover is the second line of defence for panics text/template does NOT
// convert — hence assert.NotPanics rather than a message match: the contract is
// "no panic escapes and an error comes back", not "which layer caught it".
func TestExecute_PanickingFuncNeverPanicsTheCaller(t *testing.T) {
	tmpl, err := FromGlobs(nil, Options{})
	require.NoError(t, err)

	var (
		out     string
		execErr error
	)
	assert.NotPanics(t, func() {
		out, execErr = tmpl.ExecuteTextString(`{{ reReplaceAll "([" "x" .Status }}`, simpleData(t))
	})
	require.Error(t, execErr)
	assert.Empty(t, out)
}

// TestGuarded_RecoversPanic tests guarded()'s recover directly, because no
// template input reaches it today: text/template's safeCall converts a
// panicking template function into an error, and fmt swallows a panicking
// String() method into a "%!v(PANIC=...)" placeholder. The recover is therefore
// a genuine backstop — for a future stdlib change, an Option registering a
// function through a path safeCall does not cover, or a panic raised inside
// text/template itself — and this is the only way to prove it works rather than
// asserting it by inspection.
func TestGuarded_RecoversPanic(t *testing.T) {
	tmpl, err := New(Options{})
	require.NoError(t, err)

	var (
		out     string
		execErr error
	)
	assert.NotPanics(t, func() {
		out, execErr = tmpl.guarded(func(w io.Writer) error {
			_, _ = w.Write([]byte("partial output"))
			panic("boom from execution")
		})
	})
	require.Error(t, execErr)
	assert.Contains(t, execErr.Error(), "boom from execution")
	assert.Empty(t, out, "partial output is discarded so the caller falls back cleanly")
}

// TestExecute_ErrorReturnsNoPartialOutput: half a rendered notification is
// worse than none, because none is what triggers the fixed-formatter fallback.
func TestExecute_ErrorReturnsNoPartialOutput(t *testing.T) {
	tmpl, err := FromGlobs(nil, Options{MaxOutputBytes: 8})
	require.NoError(t, err)

	got, err := tmpl.ExecuteTextString(strings.Repeat("y", 32), simpleData(t))
	require.Error(t, err)
	assert.Equal(t, "", got)
}

func TestOptions_ZeroValueUsesDefaults(t *testing.T) {
	tmpl, err := New(Options{})
	require.NoError(t, err)
	assert.Equal(t, DefaultTimeout, tmpl.opts.Timeout)
	assert.Equal(t, DefaultMaxOutputBytes, tmpl.opts.MaxOutputBytes)

	tmpl, err = New(Options{Timeout: time.Second, MaxOutputBytes: 10})
	require.NoError(t, err)
	assert.Equal(t, time.Second, tmpl.opts.Timeout)
	assert.Equal(t, 10, tmpl.opts.MaxOutputBytes)
}

// === Options / New ===

// TestNew_DefaultFuncsWinOverOptions pins upstream's precedence: an Option may
// add functions, but it cannot shadow a DefaultFunc.
func TestNew_DefaultFuncsWinOverOptions(t *testing.T) {
	tmpl, err := New(Options{}, func(text *tmpltext.Template, _ *tmplhtml.Template) {
		text.Funcs(tmpltext.FuncMap{
			"toUpper": func(string) string { return "HIJACKED" },
			"custom":  func() string { return "extra" },
		})
	})
	require.NoError(t, err)

	got, err := tmpl.ExecuteTextString(`{{ "abc" | toUpper }}/{{ custom }}`, nil)
	require.NoError(t, err)
	assert.Equal(t, "ABC/extra", got)
}

func TestNew_HasNoDefinitions(t *testing.T) {
	tmpl, err := New(Options{})
	require.NoError(t, err)
	assert.Empty(t, tmpl.DefinitionNames())
	assert.False(t, tmpl.HasDefinition("slack.default.title"))
}

func TestDefinitionNames_ExcludesTheUnnamedRoot(t *testing.T) {
	tmpl, err := FromGlobs(nil, Options{})
	require.NoError(t, err)

	names := tmpl.DefinitionNames()
	assert.NotEmpty(t, names)
	for _, name := range names {
		assert.NotEmpty(t, name)
	}
	assert.Contains(t, names, "slack.default.title")
	assert.True(t, sortedStrings(names), "DefinitionNames must be sorted")
}

func sortedStrings(values []string) bool {
	for i := 1; i < len(values); i++ {
		if values[i-1] > values[i] {
			return false
		}
	}
	return true
}
