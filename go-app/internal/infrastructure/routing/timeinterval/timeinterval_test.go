package timeinterval

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// --- Full-featured parser test -------------------------------------------------

func TestTimeInterval_UnmarshalYAML_FullFeatured(t *testing.T) {
	yamlConfig := `
name: workhours
time_intervals:
  - times:
      - start_time: "09:00"
        end_time: "17:00"
    weekdays: ["monday:friday"]
    days_of_month: ["1:5", "-3:-1"]
    months: ["january:march"]
    years: ["2024:2025"]
    location: "Europe/Moscow"
`
	var ti TimeInterval
	require.NoError(t, yaml.Unmarshal([]byte(yamlConfig), &ti))

	require.Equal(t, "workhours", ti.Name)
	require.Len(t, ti.TimeIntervals, 1)

	iv := ti.TimeIntervals[0]
	require.Len(t, iv.Times, 1)
	assert.Equal(t, 9*60, iv.Times[0].StartMinute)
	assert.Equal(t, 17*60, iv.Times[0].EndMinute)

	require.Len(t, iv.Weekdays, 1)
	assert.Equal(t, time.Monday, iv.Weekdays[0].Begin)
	assert.Equal(t, time.Friday, iv.Weekdays[0].End)

	require.Len(t, iv.DaysOfMonth, 2)
	assert.Equal(t, DayOfMonthRange{Begin: 1, End: 5}, iv.DaysOfMonth[0])
	assert.Equal(t, DayOfMonthRange{Begin: -3, End: -1}, iv.DaysOfMonth[1])

	require.Len(t, iv.Months, 1)
	assert.Equal(t, time.January, iv.Months[0].Begin)
	assert.Equal(t, time.March, iv.Months[0].End)

	require.Len(t, iv.Years, 1)
	assert.Equal(t, YearRange{Begin: 2024, End: 2025}, iv.Years[0])

	require.NotNil(t, iv.Location)
	require.NotNil(t, iv.Location.Location)
	assert.Equal(t, "Europe/Moscow", iv.Location.String())
}

func TestTimeInterval_UnmarshalYAML_BareNumbers(t *testing.T) {
	// days_of_month/months/years may be given unquoted; single values
	// (no ':') are also accepted as a one-day/month/year range.
	yamlConfig := `
name: single-values
time_intervals:
  - days_of_month: [15]
    months: [6]
    years: [2025]
`
	var ti TimeInterval
	require.NoError(t, yaml.Unmarshal([]byte(yamlConfig), &ti))

	iv := ti.TimeIntervals[0]
	assert.Equal(t, DayOfMonthRange{Begin: 15, End: 15}, iv.DaysOfMonth[0])
	assert.Equal(t, MonthRange{Begin: time.June, End: time.June}, iv.Months[0])
	assert.Equal(t, YearRange{Begin: 2025, End: 2025}, iv.Years[0])
}

// --- Invalid input classes ------------------------------------------------------

func TestTimeRange_UnmarshalYAML_Invalid(t *testing.T) {
	type wrap struct {
		V TimeRange `yaml:"v"`
	}

	tests := []struct {
		name string
		yaml string
	}{
		{"bad start format", `v: {start_time: "9am", end_time: "17:00"}`},
		{"bad end format", `v: {start_time: "09:00", end_time: "not-a-time"}`},
		{"hour out of range", `v: {start_time: "25:00", end_time: "26:00"}`},
		{"minute out of range", `v: {start_time: "09:60", end_time: "10:00"}`},
		{"24:00 with nonzero minute", `v: {start_time: "09:00", end_time: "24:30"}`},
		{"reversed range", `v: {start_time: "17:00", end_time: "09:00"}`},
		{"equal start and end", `v: {start_time: "09:00", end_time: "09:00"}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var w wrap
			err := yaml.Unmarshal([]byte(tt.yaml), &w)
			require.Error(t, err)
		})
	}
}

func TestWeekdayRange_UnmarshalYAML_Invalid(t *testing.T) {
	type wrap struct {
		V WeekdayRange `yaml:"v"`
	}

	tests := []struct {
		name string
		yaml string
	}{
		{"unknown weekday", `v: "funday"`},
		{"reversed range", `v: "friday:monday"`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var w wrap
			err := yaml.Unmarshal([]byte(tt.yaml), &w)
			require.Error(t, err)
		})
	}
}

func TestMonthRange_UnmarshalYAML_Invalid(t *testing.T) {
	type wrap struct {
		V MonthRange `yaml:"v"`
	}

	tests := []struct {
		name string
		yaml string
	}{
		{"unknown month name", `v: "smarch"`},
		{"month number out of range", `v: "13"`},
		{"reversed range", `v: "march:january"`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var w wrap
			err := yaml.Unmarshal([]byte(tt.yaml), &w)
			require.Error(t, err)
		})
	}
}

func TestDayOfMonthRange_UnmarshalYAML_Invalid(t *testing.T) {
	type wrap struct {
		V DayOfMonthRange `yaml:"v"`
	}

	tests := []struct {
		name string
		yaml string
	}{
		{"zero day", `v: "0"`},
		{"out of range", `v: "32"`},
		{"out of range negative", `v: "-32"`},
		{"reversed range", `v: "5:1"`},
		{"mixed sign range", `v: "-3:1"`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var w wrap
			err := yaml.Unmarshal([]byte(tt.yaml), &w)
			require.Error(t, err)
		})
	}
}

func TestYearRange_UnmarshalYAML_Invalid(t *testing.T) {
	type wrap struct {
		V YearRange `yaml:"v"`
	}

	tests := []struct {
		name string
		yaml string
	}{
		{"not a number", `v: "twenty-twenty"`},
		{"reversed range", `v: "2025:2020"`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var w wrap
			err := yaml.Unmarshal([]byte(tt.yaml), &w)
			require.Error(t, err)
		})
	}
}

func TestLocation_UnmarshalYAML_Invalid(t *testing.T) {
	type wrap struct {
		V Location `yaml:"v"`
	}

	var w wrap
	err := yaml.Unmarshal([]byte(`v: "Not/A_Real_Zone"`), &w)
	require.Error(t, err)
}

func TestLocation_UnmarshalYAML_Valid(t *testing.T) {
	type wrap struct {
		V Location `yaml:"v"`
	}

	var w wrap
	require.NoError(t, yaml.Unmarshal([]byte(`v: "Europe/Moscow"`), &w))
	require.NotNil(t, w.V.Location)
	assert.Equal(t, "Europe/Moscow", w.V.String())
}

// --- Matches() table tests -------------------------------------------------

func TestTimeRange_ContainsMinute_Boundaries(t *testing.T) {
	r := TimeRange{StartMinute: 9 * 60, EndMinute: 17 * 60}

	assert.True(t, r.ContainsMinute(9*60), "start is inclusive")
	assert.True(t, r.ContainsMinute(16*60+59), "one minute before end matches")
	assert.False(t, r.ContainsMinute(17*60), "end is exclusive")
	assert.False(t, r.ContainsMinute(8*60+59), "one minute before start does not match")
}

func TestWeekdayRange_Contains(t *testing.T) {
	r := WeekdayRange{Begin: time.Monday, End: time.Friday}

	assert.True(t, r.Contains(time.Monday))
	assert.True(t, r.Contains(time.Wednesday))
	assert.True(t, r.Contains(time.Friday))
	assert.False(t, r.Contains(time.Saturday))
	assert.False(t, r.Contains(time.Sunday))
}

func TestDayOfMonthRange_Contains_Negative(t *testing.T) {
	// "-3:-1" means the last three days of the month, regardless of length.
	r := DayOfMonthRange{Begin: -3, End: -1}

	assert.True(t, r.Contains(29, 31), "31-day month: day 29 is 3rd-from-last")
	assert.True(t, r.Contains(31, 31), "31-day month: last day")
	assert.False(t, r.Contains(28, 31), "31-day month: day 28 is 4th-from-last")

	assert.True(t, r.Contains(26, 28), "February (non-leap): day 26 is 3rd-from-last")
	assert.True(t, r.Contains(28, 28), "February (non-leap): last day")
	assert.False(t, r.Contains(25, 28), "February (non-leap): day 25 is 4th-from-last")

	assert.True(t, r.Contains(27, 29), "February (leap year): day 27 is 3rd-from-last")
}

func TestMonthRange_Contains(t *testing.T) {
	r := MonthRange{Begin: time.January, End: time.March}

	assert.True(t, r.Contains(time.January))
	assert.True(t, r.Contains(time.February))
	assert.True(t, r.Contains(time.March))
	assert.False(t, r.Contains(time.April))
	assert.False(t, r.Contains(time.December))
}

func TestYearRange_Contains(t *testing.T) {
	r := YearRange{Begin: 2020, End: 2022}

	assert.True(t, r.Contains(2020))
	assert.True(t, r.Contains(2021))
	assert.True(t, r.Contains(2022))
	assert.False(t, r.Contains(2019))
	assert.False(t, r.Contains(2023))
}

func TestInterval_ContainsTime_EmptyMatchesAlways(t *testing.T) {
	var iv Interval // zero-value: every field empty

	for _, ts := range []string{
		"2024-01-01T00:00:00Z",
		"2024-06-15T12:34:56Z",
		"2099-12-31T23:59:59Z",
	} {
		parsed, err := time.Parse(time.RFC3339, ts)
		require.NoError(t, err)
		assert.True(t, iv.ContainsTime(parsed), "empty interval must match %s", ts)
	}
}

func TestInterval_ContainsTime_AndAcrossFields(t *testing.T) {
	// Business hours, weekdays only, Jan-Mar only: all fields must hold.
	iv := Interval{
		Times:    []TimeRange{{StartMinute: 9 * 60, EndMinute: 17 * 60}},
		Weekdays: []WeekdayRange{{Begin: time.Monday, End: time.Friday}},
		Months:   []MonthRange{{Begin: time.January, End: time.March}},
	}

	// Wednesday 10:00 in February 2024 -> matches all three fields.
	match, err := time.Parse(time.RFC3339, "2024-02-14T10:00:00Z")
	require.NoError(t, err)
	assert.True(t, iv.ContainsTime(match))

	// Same weekday/time but in April -> month field fails.
	wrongMonth, err := time.Parse(time.RFC3339, "2024-04-10T10:00:00Z")
	require.NoError(t, err)
	assert.False(t, iv.ContainsTime(wrongMonth))

	// Same day/month but outside business hours -> times field fails.
	wrongTime, err := time.Parse(time.RFC3339, "2024-02-14T20:00:00Z")
	require.NoError(t, err)
	assert.False(t, iv.ContainsTime(wrongTime))

	// Saturday within business hours and month -> weekdays field fails.
	wrongDay, err := time.Parse(time.RFC3339, "2024-02-17T10:00:00Z")
	require.NoError(t, err)
	assert.False(t, iv.ContainsTime(wrongDay))
}

func TestInterval_ContainsTime_LocationConversion(t *testing.T) {
	loc, err := time.LoadLocation("Europe/Moscow")
	require.NoError(t, err)

	// 09:00-17:00 Moscow time (UTC+3, no DST since 2014).
	iv := Interval{
		Times:    []TimeRange{{StartMinute: 9 * 60, EndMinute: 17 * 60}},
		Location: &Location{loc},
	}

	// 08:00 UTC == 11:00 Moscow -> matches.
	inRange, err := time.Parse(time.RFC3339, "2024-06-10T08:00:00Z")
	require.NoError(t, err)
	assert.True(t, iv.ContainsTime(inRange))

	// 05:00 UTC == 08:00 Moscow -> before window, does not match.
	beforeRange, err := time.Parse(time.RFC3339, "2024-06-10T05:00:00Z")
	require.NoError(t, err)
	assert.False(t, iv.ContainsTime(beforeRange))
}

func TestInterval_ContainsTime_DSTEdge(t *testing.T) {
	loc, err := time.LoadLocation("Europe/Berlin")
	require.NoError(t, err)

	// Europe/Berlin springs forward at 01:00 UTC on 2024-03-31
	// (02:00 CET -> 03:00 CEST). Window is 01:00-02:00 local time.
	iv := Interval{
		Times:    []TimeRange{{StartMinute: 1 * 60, EndMinute: 2 * 60}},
		Location: &Location{loc},
	}

	// 00:30 UTC -> 01:30 CET (before the jump) -> inside the window.
	beforeJump, err := time.Parse(time.RFC3339, "2024-03-31T00:30:00Z")
	require.NoError(t, err)
	assert.True(t, iv.ContainsTime(beforeJump))

	// One hour later in UTC, 01:30 UTC -> 03:30 CEST (after the jump) ->
	// the local hour skipped straight past the window.
	afterJump, err := time.Parse(time.RFC3339, "2024-03-31T01:30:00Z")
	require.NoError(t, err)
	assert.False(t, iv.ContainsTime(afterJump))
}

func TestTimeInterval_Matches_OrAcrossIntervals(t *testing.T) {
	ti := TimeInterval{
		Name: "weekend-or-nights",
		TimeIntervals: []Interval{
			{Weekdays: []WeekdayRange{{Begin: time.Saturday, End: time.Saturday}, {Begin: time.Sunday, End: time.Sunday}}},
			{Times: []TimeRange{{StartMinute: 0, EndMinute: 6 * 60}}},
		},
	}

	// Saturday -> matches first Interval (via weekday OR: Sat range).
	saturday, err := time.Parse(time.RFC3339, "2024-06-15T12:00:00Z")
	require.NoError(t, err)
	assert.True(t, ti.Matches(saturday))

	// Tuesday 03:00 -> fails first Interval (wrong weekday), matches second
	// (early morning hours) -> group matches via OR across the list.
	tuesdayNight, err := time.Parse(time.RFC3339, "2024-06-11T03:00:00Z")
	require.NoError(t, err)
	assert.True(t, ti.Matches(tuesdayNight))

	// Tuesday 12:00 -> fails both -> no match.
	tuesdayNoon, err := time.Parse(time.RFC3339, "2024-06-11T12:00:00Z")
	require.NoError(t, err)
	assert.False(t, ti.Matches(tuesdayNoon))
}

func TestTimeInterval_Matches_EmptyGroupNeverMatches(t *testing.T) {
	ti := TimeInterval{Name: "empty"}
	now, err := time.Parse(time.RFC3339, "2024-06-15T12:00:00Z")
	require.NoError(t, err)
	assert.False(t, ti.Matches(now))
}

// --- Clone -------------------------------------------------------------------

func TestTimeInterval_Clone_IsIndependent(t *testing.T) {
	loc, err := time.LoadLocation("Europe/Moscow")
	require.NoError(t, err)

	original := TimeInterval{
		Name: "clone-me",
		TimeIntervals: []Interval{
			{
				Weekdays: []WeekdayRange{{Begin: time.Monday, End: time.Friday}},
				Location: &Location{loc},
			},
		},
	}

	clone := original.Clone()
	require.Equal(t, original, clone)

	// Mutate the clone's slice; original must stay untouched.
	clone.TimeIntervals[0].Weekdays[0] = WeekdayRange{Begin: time.Saturday, End: time.Sunday}
	assert.Equal(t, time.Monday, original.TimeIntervals[0].Weekdays[0].Begin)
}
