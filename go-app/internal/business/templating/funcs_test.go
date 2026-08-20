package templating

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// render executes expr with the default library loaded and no data, which is
// all the pure functions need.
func render(t *testing.T, expr string, data any) (string, error) {
	t.Helper()
	tmpl, err := FromGlobs(nil, Options{})
	require.NoError(t, err)
	return tmpl.ExecuteTextString(expr, data)
}

// TestDefaultFuncs_AllUpstreamNamesRegistered is the ledger as a test: the list
// is upstream v0.34.0's DefaultFuncs keys, and a missing name means a config
// that renders upstream fails to even LOAD here (an unregistered function is a
// parse error, so it takes the whole template file down).
func TestDefaultFuncs_AllUpstreamNamesRegistered(t *testing.T) {
	upstreamNames := []string{
		"toUpper", "toLower", "title", "trimSpace", "join", "match",
		"safeHtml", "safeUrl", "urlUnescape", "reReplaceAll", "stringSlice",
		"routeLabels", "date", "tz", "now", "since", "humanizeDuration",
		"toDate", "mustToDate", "toJson", "base64encode", "base64decode",
		"list", "append", "dict",
	}

	for _, name := range upstreamNames {
		assert.Contains(t, DefaultFuncs, name, "upstream DefaultFuncs entry %q is missing", name)
	}
	assert.Len(t, DefaultFuncs, len(upstreamNames),
		"DefaultFuncs gained or lost an entry relative to upstream v0.34.0")
}

func TestDefaultFuncs_StringHelpers(t *testing.T) {
	cases := map[string]string{
		`{{ "abc" | toUpper }}`:                    "ABC",
		`{{ "ABC" | toLower }}`:                    "abc",
		`{{ "  x  " | trimSpace }}`:                "x",
		`{{ join "-" (stringSlice "a" "b" "c") }}`: "a-b-c",
		`{{ match "^ab" "abc" }}`:                  "true",
		`{{ match "^zz" "abc" }}`:                  "false",
		`{{ reReplaceAll "b+" "X" "abbbc" }}`:      "aXc",
		`{{ urlUnescape "a%20b" }}`:                "a b",
	}

	for expr, want := range cases {
		got, err := render(t, expr, nil)
		require.NoError(t, err, expr)
		assert.Equal(t, want, got, expr)
	}
}

// TestDefaultFuncs_Title pins `title` to x/text's American-English caser,
// including the two behaviours a hand-rolled substitute got wrong (which is why
// the substitute was dropped): a lower-cased remainder, and Unicode-aware
// casing.
func TestDefaultFuncs_Title(t *testing.T) {
	cases := map[string]string{
		`{{ title "hello world" }}`:    "Hello World",
		`{{ title "HELLO WORLD" }}`:    "Hello World",
		`{{ title "high cpu usage" }}`: "High Cpu Usage",
		`{{ title "kube-system" }}`:    "Kube-System",
		`{{ title "" }}`:               "",
		`{{ title "ätna erupted" }}`:   "Ätna Erupted",
		`{{ title "критично" }}`:       "Критично",
	}

	for expr, want := range cases {
		got, err := render(t, expr, nil)
		require.NoError(t, err, expr)
		assert.Equal(t, want, got, expr)
	}
}

// TestDefaultFuncs_HumanizeDuration pins the prometheus/common implementation,
// including the sub-second unit thresholds a hand-rolled version got wrong
// ("100us", not "100µs").
func TestDefaultFuncs_HumanizeDuration(t *testing.T) {
	cases := map[string]string{
		`{{ humanizeDuration 0 }}`:        "0s",
		`{{ humanizeDuration 1 }}`:        "1s",
		`{{ humanizeDuration 60 }}`:       "1m 0s",
		`{{ humanizeDuration 3600 }}`:     "1h 0m 0s",
		`{{ humanizeDuration 86400 }}`:    "1d 0h 0m 0s",
		`{{ humanizeDuration 899.99 }}`:   "14m 59s",
		`{{ humanizeDuration .0001 }}`:    "100us",
		`{{ humanizeDuration "1.2345" }}`: "1.234s",
		`{{ humanizeDuration -1 }}`:       "-1s",
	}

	for expr, want := range cases {
		got, err := render(t, expr, nil)
		require.NoError(t, err, expr)
		assert.Equal(t, want, got, expr)
	}
}

func TestDefaultFuncs_HumanizeDuration_NonNumericIsAnError(t *testing.T) {
	_, err := render(t, `{{ humanizeDuration "one" }}`, nil)
	require.Error(t, err)
}

func TestDefaultFuncs_TimeHelpers(t *testing.T) {
	data := map[string]any{
		"T": time.Date(2026, 8, 19, 14, 30, 0, 0, time.UTC),
	}

	got, err := render(t, `{{ date "2006-01-02 15:04" .T }}`, data)
	require.NoError(t, err)
	assert.Equal(t, "2026-08-19 14:30", got)

	got, err = render(t, `{{ date "15:04" (tz "Europe/Berlin" .T) }}`, data)
	require.NoError(t, err)
	assert.Equal(t, "16:30", got, "Berlin is UTC+2 in August")

	got, err = render(t, `{{ date "2006-01-02" (toDate "2006-01-02" "2026-08-19") }}`, nil)
	require.NoError(t, err)
	assert.Equal(t, "2026-08-19", got)

	// toDate swallows a parse failure into the zero time; mustToDate errors.
	got, err = render(t, `{{ date "2006" (toDate "2006-01-02" "nonsense") }}`, nil)
	require.NoError(t, err)
	assert.Equal(t, "0001", got)

	_, err = render(t, `{{ mustToDate "2006-01-02" "nonsense" }}`, nil)
	require.Error(t, err)

	_, err = render(t, `{{ tz "Not/AZone" .T }}`, data)
	require.Error(t, err)
}

// TestDefaultFuncs_NowAndSinceAreCallable: both are non-deterministic, so the
// assertion is that they are wired and composable, not what they return.
func TestDefaultFuncs_NowAndSinceAreCallable(t *testing.T) {
	got, err := render(t, `{{ if gt (len (date "2006" now)) 0 }}ok{{ end }}`, nil)
	require.NoError(t, err)
	assert.Equal(t, "ok", got)

	got, err = render(t, `{{ if ge (since now).Nanoseconds 0 }}ok{{ end }}`, nil)
	require.NoError(t, err)
	assert.Equal(t, "ok", got)
}

func TestDefaultFuncs_Base64Roundtrip(t *testing.T) {
	got, err := render(t, `{{ base64encode "alertname=HighCPU&x=1" }}`, nil)
	require.NoError(t, err)
	assert.Equal(t, "YWxlcnRuYW1lPUhpZ2hDUFUmeD0x", got, "URL-safe alphabet, no padding surprises")

	got, err = render(t, `{{ base64decode "YWxlcnRuYW1lPUhpZ2hDUFUmeD0x" }}`, nil)
	require.NoError(t, err)
	assert.Equal(t, "alertname=HighCPU&x=1", got)

	_, err = render(t, `{{ base64decode "!!!not-base64!!!" }}`, nil)
	require.Error(t, err)
}

func TestDefaultFuncs_ToJSON(t *testing.T) {
	got, err := render(t, `{{ .CommonLabels | toJson }}`, simpleData(t))
	require.NoError(t, err)
	assert.JSONEq(t, `{"alertname":"HighCPU","severity":"critical"}`, got)
}

func TestDefaultFuncs_ListAppendDict(t *testing.T) {
	// append takes the slice FIRST, so it cannot be used as a pipeline stage
	// (a pipeline appends its input as the LAST argument) — same as upstream.
	got, err := render(t, `{{ range append (list "a" "b") "c" }}{{ . }},{{ end }}`, nil)
	require.NoError(t, err)
	assert.Equal(t, "a,b,c,", got)

	got, err = render(t, `{{ len (list) }}/{{ len (list 1 2 3) }}`, nil)
	require.NoError(t, err)
	assert.Equal(t, "0/3", got)

	got, err = render(t, `{{ (dict "k" "v" "n" 2).k }}`, nil)
	require.NoError(t, err)
	assert.Equal(t, "v", got)

	_, err = render(t, `{{ dict "odd" }}`, nil)
	require.Error(t, err)
	_, err = render(t, `{{ dict 1 "v" }}`, nil)
	require.Error(t, err)
}

// TestDefaultFuncs_RouteLabelsIsAnEmptyPlaceholder documents the one function
// whose behaviour deliberately differs from upstream: the feature is not ported,
// so it renders empty instead of resolving a route label.
func TestDefaultFuncs_RouteLabelsIsAnEmptyPlaceholder(t *testing.T) {
	got, err := render(t, `[{{ routeLabels "team" }}]`, simpleData(t))
	require.NoError(t, err)
	assert.Equal(t, "[]", got)
}

// TestDefaultFuncs_SafeURLInHTMLContext: safeUrl is what lets an operator put a
// templated URL into an href without html/template rewriting it to
// "#ZgotmplZ".
func TestDefaultFuncs_SafeURLInHTMLContext(t *testing.T) {
	tmpl, err := FromGlobs(nil, Options{})
	require.NoError(t, err)

	data := map[string]string{"U": "javascript:alert(1)"}

	blocked, err := tmpl.ExecuteHTMLString(`<a href="{{ .U }}">x</a>`, data)
	require.NoError(t, err)
	assert.Contains(t, blocked, "#ZgotmplZ", "html/template blocks a javascript: URL by default")

	allowed, err := tmpl.ExecuteHTMLString(`<a href="{{ .U | safeUrl }}">x</a>`, data)
	require.NoError(t, err)
	assert.NotContains(t, allowed, "#ZgotmplZ")
	// html/template still percent-escapes the URL's own characters; what safeUrl
	// buys is that the SCHEME survives instead of the whole href being replaced.
	assert.Contains(t, allowed, "javascript:", "safeUrl is the documented opt-out for the scheme check")
}
