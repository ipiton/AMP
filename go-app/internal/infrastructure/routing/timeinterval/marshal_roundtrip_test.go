package timeinterval

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// Final review finding 15: /api/v2/status re-emits the parsed Alertmanager
// config as YAML for `amtool config routes show` to re-parse. WeekdayRange,
// MonthRange, DayOfMonthRange and YearRange had UnmarshalYAML but no
// MarshalYAML, so yaml.Marshal fell back to their exported fields and produced
// `{begin: 1, end: 5}` — which their own UnmarshalYAML rejects. The emitted
// config was therefore unparsable as soon as any time_intervals were
// configured.
func TestTimeInterval_YAMLRoundTrip(t *testing.T) {
	sources := []string{
		// Every range type, in both single-value and span form.
		`
name: everything
time_intervals:
  - times:
      - start_time: "09:00"
        end_time: "17:30"
    weekdays: ['monday:friday', 'sunday']
    days_of_month: ['1:5', '20:-1', '15']
    months: ['january:march', 'december']
    years: ['2024', '2030:2032']
    location: Europe/Berlin
`,
		`
name: minimal
time_intervals:
  - weekdays: ['saturday']
`,
	}

	for _, src := range sources {
		var original TimeInterval
		require.NoError(t, yaml.Unmarshal([]byte(src), &original))

		emitted, err := yaml.Marshal(original)
		require.NoError(t, err)

		var reparsed TimeInterval
		require.NoError(t, yaml.Unmarshal(emitted, &reparsed),
			"the emitted YAML must be re-parsable by the same types that wrote it:\n%s", emitted)

		assert.Equal(t, original.Name, reparsed.Name)
		require.Len(t, reparsed.TimeIntervals, len(original.TimeIntervals))
		for i := range original.TimeIntervals {
			assert.Equal(t, original.TimeIntervals[i].Times, reparsed.TimeIntervals[i].Times)
			assert.Equal(t, original.TimeIntervals[i].Weekdays, reparsed.TimeIntervals[i].Weekdays)
			assert.Equal(t, original.TimeIntervals[i].DaysOfMonth, reparsed.TimeIntervals[i].DaysOfMonth)
			assert.Equal(t, original.TimeIntervals[i].Months, reparsed.TimeIntervals[i].Months)
			assert.Equal(t, original.TimeIntervals[i].Years, reparsed.TimeIntervals[i].Years)
		}
	}
}

// TestRangeMarshalYAML_UpstreamScalarForms pins the exact wire text, since
// upstream configs (and operators reading /api/v2/status) expect these forms.
func TestRangeMarshalYAML_UpstreamScalarForms(t *testing.T) {
	marshalScalar := func(t *testing.T, v any) string {
		t.Helper()
		out, err := yaml.Marshal(v)
		require.NoError(t, err)
		var s string
		require.NoError(t, yaml.Unmarshal(out, &s))
		return s
	}

	var span, single WeekdayRange
	require.NoError(t, yaml.Unmarshal([]byte(`'monday:friday'`), &span))
	require.NoError(t, yaml.Unmarshal([]byte(`'wednesday'`), &single))
	assert.Equal(t, "monday:friday", marshalScalar(t, span))
	assert.Equal(t, "wednesday", marshalScalar(t, single), "a single day must not be emitted as a degenerate range")

	var months MonthRange
	require.NoError(t, yaml.Unmarshal([]byte(`'1:3'`), &months))
	assert.Equal(t, "january:march", marshalScalar(t, months), "numeric input normalizes to names, both of which upstream accepts")

	var dom DayOfMonthRange
	require.NoError(t, yaml.Unmarshal([]byte(`'20:-1'`), &dom))
	assert.Equal(t, "20:-1", marshalScalar(t, dom), "negative bounds must survive the round trip")

	var years YearRange
	require.NoError(t, yaml.Unmarshal([]byte(`'2020:2022'`), &years))
	assert.Equal(t, "2020:2022", marshalScalar(t, years))
}
