package timeinterval

// Task 3.3 — upstream-fixture parity tests.
//
// Ported by hand from prometheus/alertmanager's own test corpus (fetched
// live from https://raw.githubusercontent.com/prometheus/alertmanager/main/
// timeinterval/{timeinterval,timeinterval_test}.go during this task — the
// fetch succeeded, so nothing here is reconstructed from memory).
//
// --- Shape mapping (upstream -> ours) ---------------------------------
//
//   - Upstream's package-level `TimeInterval` struct (Times/Weekdays/
//     DaysOfMonth/Months/Years/Location + `ContainsTime`) is the exact
//     analogue of OUR `Interval` type (same fields, same method). Upstream's
//     *outer* named grouping (`Name` + `[]TimeInterval`) lives one layer up,
//     in prometheus/alertmanager's `config` package, not in `timeinterval`
//     itself — that outer shape is what OUR `TimeInterval{Name,
//     TimeIntervals []Interval}` models (built in Task 3.1). So: upstream
//     `timeinterval.TimeInterval` == our `timeinterval.Interval`; upstream's
//     (unfetched, out-of-scope) `config.MuteTimeInterval` == our
//     `timeinterval.TimeInterval`.
//   - Upstream's range types (`WeekdayRange`, `DayOfMonthRange`, ...) embed
//     an anonymous `InclusiveRange{Begin, End int}`. Ours declare `Begin`/
//     `End` directly, typed (`time.Weekday`, `time.Month`, or `int`). Wire
//     (YAML) shape and match semantics are identical; only the Go literal
//     spelling differs, so fixture data below uses our typed literals
//     instead of upstream's `InclusiveRange{...}`.
//   - Upstream's YAML field names (times/weekdays/days_of_month/months/
//     years/location) are identical to ours at the Interval level, so the
//     YAML text in ported cases is reused verbatim where it targets a bare
//     `[]Interval` or `Interval` (upstream's `[]TimeInterval` / single
//     `TimeInterval`) — no field-name translation was needed.
//   - Upstream's `Intervener`/`Mutes` (mute-by-name-list evaluation) is not
//     part of this package's scope: our equivalent mute/active evaluation
//     lives one layer up in the notify chain (Task 3.2,
//     internal/infrastructure/grouping/time_mute_test.go, already covers
//     the same "muted by name" semantics against our own
//     TimeInterval.Matches). Not re-ported here to avoid duplicating that
//     coverage in the wrong package.
//
// --- Divergence found + fixed ------------------------------------------
//
// Upstream's yamlUnmarshalTestCases requires `days_of_month: ['1:-1']` to
// parse successfully (a positive begin day paired with a negative end day,
// meaning "from day N through the last day of the month"). Our original
// DayOfMonthRange.UnmarshalYAML rejected ANY sign mismatch between begin and
// end as an error ("mixes positive and negative days"), which would reject
// this valid upstream config. Fixed in timeinterval.go: only a *negative*
// begin paired with a *positive* end is now rejected outright (matches
// upstream's "end day must be negative if start day is negative"); a
// positive begin with a negative end is validated instead via upstream's
// own ordering heuristic (compare both ends normalized against a 28-day
// month), which additionally reproduces upstream's `10:-25` rejection
// ("end day -25 is always before start day 10") that a naive raw-integer
// `begin > end` comparison would get wrong in the other direction (it would
// wrongly reject the valid `1:-1` case). See
// TestDayOfMonthRange_UnmarshalYAML_UpstreamMixedSignParity below.

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func mustLoadTestLocation(t *testing.T, name string) *Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	require.NoError(t, err)
	return &Location{loc}
}

func parseRFC822Z(t *testing.T, ts string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC822Z, ts)
	require.NoError(t, err, "fixture timestamp %q must parse", ts)
	return parsed
}

// --- Ported: timeIntervalTestCases (upstream timeinterval_test.go) --------

func TestUpstream_ContainsTime(t *testing.T) {
	sydney := func() *Location { return mustLoadTestLocation(t, "Australia/Sydney") }

	cases := []struct {
		name       string
		iv         Interval
		validTimes []string
		invalid    []string
	}{
		{
			name: "zero-value interval matches everything",
			iv:   Interval{},
			validTimes: []string{
				"02 Jan 06 15:04 +0000",
				"03 Jan 07 10:04 +0000",
				"04 Jan 06 09:04 +0000",
			},
		},
		{
			name: "9am to 5pm, monday to friday",
			iv: Interval{
				Times:    []TimeRange{{StartMinute: 540, EndMinute: 1020}},
				Weekdays: []WeekdayRange{{Begin: time.Monday, End: time.Friday}},
			},
			validTimes: []string{
				"04 May 20 15:04 +0000",
				"05 May 20 10:04 +0000",
				"09 Jun 20 09:04 +0000",
			},
			invalid: []string{
				"03 May 20 15:04 +0000",
				"04 May 20 08:59 +0000",
				"05 May 20 05:00 +0000",
			},
		},
		{
			name: "Easter 2020",
			iv: Interval{
				DaysOfMonth: []DayOfMonthRange{{Begin: 4, End: 6}},
				Months:      []MonthRange{{Begin: time.April, End: time.April}},
				Years:       []YearRange{{Begin: 2020, End: 2020}},
			},
			validTimes: []string{
				"04 Apr 20 15:04 +0000",
				"05 Apr 20 00:00 +0000",
				"06 Apr 20 23:05 +0000",
			},
			invalid: []string{
				"03 May 18 15:04 +0000",
				"03 Apr 20 23:59 +0000",
				"04 Jun 20 23:59 +0000",
				"06 Apr 19 23:59 +0000",
				"07 Apr 20 00:00 +0000",
			},
		},
		{
			name: "negative days of month: last 3 days of each month",
			iv: Interval{
				DaysOfMonth: []DayOfMonthRange{{Begin: -3, End: -1}},
			},
			validTimes: []string{
				"31 Jan 20 15:04 +0000",
				"30 Jan 20 15:04 +0000",
				"29 Jan 20 15:04 +0000",
				"30 Jun 20 00:00 +0000",
				"29 Feb 20 23:05 +0000", // 2020 is a leap year
			},
			invalid: []string{
				"03 May 18 15:04 +0000",
				"27 Jan 20 15:04 +0000",
				"03 Apr 20 23:59 +0000",
				"04 Jun 20 23:59 +0000",
				"06 Apr 19 23:59 +0000",
				"07 Apr 20 00:00 +0000",
				"01 Mar 20 00:00 +0000",
			},
		},
		{
			name: "out-of-bound days do not spill into neighboring months",
			iv: Interval{
				Months:      []MonthRange{{Begin: time.June, End: time.June}},
				DaysOfMonth: []DayOfMonthRange{{Begin: -31, End: 31}},
			},
			validTimes: []string{
				"30 Jun 20 00:00 +0000",
				"01 Jun 20 00:00 +0000",
			},
			invalid: []string{
				"31 May 20 00:00 +0000",
				// Upstream's own literal here is "1 Jul 20 00:00 +0000"
				// (no leading zero), which time.RFC822Z cannot parse; their
				// test ignores the parse error (`_t, _ := time.Parse(...)`)
				// so the assertion silently no-ops there. Zero-padded here
				// to keep the intended coverage (day 1 of the following
				// month must not be pulled in by the wide days_of_month
				// range) actually exercised.
				"01 Jul 20 00:00 +0000",
			},
		},
		{
			name: "alternative timezone: AEST 9am-5pm Mon-Fri",
			iv: Interval{
				Times:    []TimeRange{{StartMinute: 540, EndMinute: 1020}},
				Weekdays: []WeekdayRange{{Begin: time.Monday, End: time.Friday}},
				Location: sydney(),
			},
			validTimes: []string{
				"06 Apr 21 13:00 +1000",
			},
			invalid: []string{
				"06 Apr 21 13:00 +0000",
			},
		},
		{
			name: "alternative timezone during daylight savings transition",
			iv: Interval{
				Times:    []TimeRange{{StartMinute: 540, EndMinute: 1020}},
				Weekdays: []WeekdayRange{{Begin: time.Monday, End: time.Friday}},
				Months:   []MonthRange{{Begin: time.November, End: time.November}},
				Location: sydney(),
			},
			validTimes: []string{
				"01 Nov 21 09:00 +1100",
				"31 Oct 21 22:00 +0000",
			},
			invalid: []string{
				"31 Oct 21 21:00 +0000",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, ts := range tc.validTimes {
				assert.True(t, tc.iv.ContainsTime(parseRFC822Z(t, ts)), "expected %s to be contained", ts)
			}
			for _, ts := range tc.invalid {
				assert.False(t, tc.iv.ContainsTime(parseRFC822Z(t, ts)), "expected %s to be excluded", ts)
			}
		})
	}
}

// --- Ported: timeStringTestCases (upstream TestParseTimeString) ----------

func TestUpstream_TimeRange_ParseTimeString(t *testing.T) {
	type wrap struct {
		V TimeRange `yaml:"v"`
	}

	cases := []struct {
		name        string
		yamlFlow    string
		want        TimeRange
		expectError bool
	}{
		{
			name:     "00:00 to 24:00 (full day)",
			yamlFlow: `v: {'start_time': '00:00', 'end_time': '24:00'}`,
			want:     TimeRange{StartMinute: 0, EndMinute: 1440},
		},
		{
			name:     "01:35 to 17:39",
			yamlFlow: `v: {'start_time': '01:35', 'end_time': '17:39'}`,
			want:     TimeRange{StartMinute: 95, EndMinute: 1059},
		},
		{
			name:     "09:35 to 09:39",
			yamlFlow: `v: {'start_time': '09:35', 'end_time': '09:39'}`,
			want:     TimeRange{StartMinute: 575, EndMinute: 579},
		},
		{
			name:        "begin equals end",
			yamlFlow:    `v: {'start_time': '17:31', 'end_time': '17:31'}`,
			expectError: true,
		},
		{
			name:        "end time out of range",
			yamlFlow:    `v: {'start_time': '12:30', 'end_time': '24:01'}`,
			expectError: true,
		},
		{
			name:        "start time greater than end time",
			yamlFlow:    `v: {'start_time': '09:30', 'end_time': '07:41'}`,
			expectError: true,
		},
		{
			name:        "start time out of range and greater than end time",
			yamlFlow:    `v: {'start_time': '24:00', 'end_time': '17:41'}`,
			expectError: true,
		},
		{
			name:        "no end_time provided",
			yamlFlow:    `v: {'start_time': '14:03'}`,
			expectError: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var w wrap
			err := yaml.Unmarshal([]byte(tc.yamlFlow), &w)
			if tc.expectError {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, w.V)
		})
	}
}

// --- Ported: yamlUnmarshalTestCases (upstream TestYamlUnmarshal) ----------

func TestUpstream_YamlUnmarshal(t *testing.T) {
	cases := []struct {
		name        string
		in          string
		contains    []string
		excludes    []string
		expectError bool
	}{
		{
			name: "simple business hours",
			in: `
---
- weekdays: ['monday:friday']
  times:
    - start_time: '09:00'
      end_time: '17:00'
`,
			contains: []string{"08 Jul 20 09:00 +0000", "08 Jul 20 16:59 +0000"},
			excludes: []string{"08 Jul 20 05:00 +0000", "08 Jul 20 08:59 +0000"},
		},
		{
			name: "negative indices, multiple ranges, business hours, two year windows",
			in: `
---
  # Last week, excluding Saturday, of the first quarter of the year during business hours from 2020 to 2025 and 2030-2035
- weekdays: ['monday:friday', 'sunday']
  months: ['january:march']
  days_of_month: ['-7:-1']
  years: ['2020:2025', '2030:2035']
  times:
    - start_time: '09:00'
      end_time: '17:00'
`,
			contains: []string{
				"27 Jan 21 09:00 +0000",
				"28 Jan 21 16:59 +0000",
				"29 Jan 21 13:00 +0000",
				"31 Mar 25 13:00 +0000",
				"31 Jan 35 13:00 +0000",
			},
			excludes: []string{
				"30 Jan 21 13:00 +0000", // Saturday
				"01 Apr 21 13:00 +0000", // 4th month
				"30 Jan 26 13:00 +0000", // 2026, outside both year windows
				"31 Jan 35 17:01 +0000", // after 5pm
			},
		},
		{
			name: "trailing-newline-free variant parses the same",
			in: `
---
- weekdays: ['monday:friday']
  times:
    - start_time: '09:00'
      end_time: '17:00'`,
			contains: []string{"01 Apr 21 13:00 +0000"},
		},
		{
			name:        "invalid start time",
			in:          "---\n- times:\n    - start_time: '01:99'\n      end_time: '23:59'",
			expectError: true,
		},
		{
			name:        "invalid end time",
			in:          "---\n- times:\n    - start_time: '00:00'\n      end_time: '99:99'",
			expectError: true,
		},
		{
			name:        "weekday range start after end",
			in:          "---\n- weekdays: ['friday:monday']",
			expectError: true,
		},
		{
			name:        "unknown weekday names",
			in:          "---\n- weekdays: ['blurgsday:flurgsday']",
			expectError: true,
		},
		{
			name:        "numeric weekdays are not allowed",
			in:          "---\n- weekdays: ['1:3']",
			expectError: true,
		},
		{
			name:        "negative numeric weekdays are not allowed",
			in:          "---\n- weekdays: ['-2:-1']",
			expectError: true,
		},
		{
			name:        "day of month 0 is invalid",
			in:          "---\n- days_of_month: ['0']",
			expectError: true,
		},
		{
			name:        "day of month begin below -31 is invalid",
			in:          "---\n- days_of_month: ['-50:-20']",
			expectError: true,
		},
		{
			name:        "day of month end above 31 is invalid",
			in:          "---\n- days_of_month: ['1:50']",
			expectError: true,
		},
		{
			name: "positive begin with negative end is valid (the fixed divergence)",
			in:   "---\n- days_of_month: ['1:-1']",
			// Not asserted upstream (empty contains/excludes there); the
			// range spans the whole month by construction, so any day of
			// any month must be contained.
			contains: []string{"15 Jan 21 12:00 +0000", "28 Feb 21 12:00 +0000"},
		},
		{
			name:        "negative begin with positive end is always invalid",
			in:          "---\n- days_of_month: ['-15:5']",
			expectError: true,
		},
		{
			name:        "negative end that is always before the positive begin",
			in:          "---\n- days_of_month: ['10:-25']",
			expectError: true,
		},
		{
			name:     "month names are case-insensitive",
			in:       "---\n- months: ['January:december']",
			contains: []string{"15 Jun 21 12:00 +0000"},
		},
		{
			name:     "location may be given as an IANA name",
			in:       "---\n- years: ['2020:2022']\n  location: 'Australia/Sydney'",
			contains: []string{"15 Jun 21 12:00 +0000"},
		},
		{
			name:        "invalid start month name",
			in:          "---\n- months: ['martius:june']",
			expectError: true,
		},
		{
			name:        "invalid end month name",
			in:          "---\n- months: ['march:junius']",
			expectError: true,
		},
		{
			name:        "start month after end month",
			in:          "---\n- months: ['december:january']",
			expectError: true,
		},
		{
			name:        "start year after end year",
			in:          "---\n- years: ['2022:2020']",
			expectError: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var ivs []Interval
			err := yaml.Unmarshal([]byte(tc.in), &ivs)
			if tc.expectError {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)

			for _, ts := range tc.contains {
				tt := parseRFC822Z(t, ts)
				matched := false
				for _, iv := range ivs {
					if iv.ContainsTime(tt) {
						matched = true
						break
					}
				}
				assert.True(t, matched, "expected parsed intervals to contain %s", ts)
			}
			for _, ts := range tc.excludes {
				tt := parseRFC822Z(t, ts)
				matched := false
				for _, iv := range ivs {
					if iv.ContainsTime(tt) {
						matched = true
						break
					}
				}
				assert.False(t, matched, "expected parsed intervals to exclude %s", ts)
			}
		})
	}
}

// --- Regression test for the fixed divergence -----------------------------

// TestDayOfMonthRange_UnmarshalYAML_UpstreamMixedSignParity pins the exact
// three upstream cases that motivated the DayOfMonthRange.UnmarshalYAML fix
// (see the file-level comment above): a positive-begin/negative-end range
// must parse, a negative-begin/positive-end range must always error, and a
// positive-begin/negative-end range that is unsatisfiable in the shortest
// possible month must still error.
func TestDayOfMonthRange_UnmarshalYAML_UpstreamMixedSignParity(t *testing.T) {
	type wrap struct {
		V DayOfMonthRange `yaml:"v"`
	}

	t.Run("1:-1 (whole month) parses", func(t *testing.T) {
		var w wrap
		require.NoError(t, yaml.Unmarshal([]byte(`v: "1:-1"`), &w))
		assert.Equal(t, DayOfMonthRange{Begin: 1, End: -1}, w.V)
		// Every day of every month-length must be contained.
		assert.True(t, w.V.Contains(1, 28))
		assert.True(t, w.V.Contains(28, 28))
		assert.True(t, w.V.Contains(31, 31))
	})

	t.Run("20:-1 (day 20 through end of month) parses and matches tail days", func(t *testing.T) {
		var w wrap
		require.NoError(t, yaml.Unmarshal([]byte(`v: "20:-1"`), &w))
		assert.Equal(t, DayOfMonthRange{Begin: 20, End: -1}, w.V)
		assert.True(t, w.V.Contains(30, 31))
		assert.False(t, w.V.Contains(19, 31))
	})

	t.Run("-15:5 (negative begin, positive end) always errors", func(t *testing.T) {
		var w wrap
		require.Error(t, yaml.Unmarshal([]byte(`v: "-15:5"`), &w))
	})

	t.Run("10:-25 (unsatisfiable even in shortest month) errors", func(t *testing.T) {
		var w wrap
		require.Error(t, yaml.Unmarshal([]byte(`v: "10:-25"`), &w))
	})
}

// --- Ported: completeTestCases (upstream TestTimeIntervalComplete) --------

func TestUpstream_KitchenSinkInterval(t *testing.T) {
	t.Run("weekdays+times+days_of_month+years+months all AND together", func(t *testing.T) {
		in := `
weekdays: ['monday:wednesday', 'saturday', 'sunday']
times:
  - start_time: '13:00'
    end_time: '15:00'
days_of_month: ['1', '10', '20:-1']
years: ['2020:2023']
months: ['january:march']
`
		var iv Interval
		require.NoError(t, yaml.Unmarshal([]byte(in), &iv))

		for _, ts := range []string{"10 Jan 21 13:00 +0000", "30 Jan 21 14:24 +0000"} {
			assert.True(t, iv.ContainsTime(parseRFC822Z(t, ts)), "expected %s to be contained", ts)
		}
		for _, ts := range []string{"09 Jan 21 13:00 +0000", "20 Jan 21 12:59 +0000", "02 Feb 21 13:00 +0000"} {
			assert.False(t, iv.ContainsTime(parseRFC822Z(t, ts)), "expected %s to be excluded", ts)
		}
	})

	t.Run("broken clamping guard: begin day past month end must not spill into the next month", func(t *testing.T) {
		in := `
days_of_month: ['30:31']
years: ['2020:2023']
months: ['february']
`
		var iv Interval
		require.NoError(t, yaml.Unmarshal([]byte(in), &iv))

		assert.False(t, iv.ContainsTime(parseRFC822Z(t, "28 Feb 21 13:00 +0000")))
	})
}
