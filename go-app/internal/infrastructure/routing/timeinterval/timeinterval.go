// Package timeinterval implements Prometheus Alertmanager-compatible time
// interval matching, used for the `time_intervals:` config section and the
// `mute_time_intervals`/`active_time_intervals` route fields (PARITY-B1).
//
// Semantics (mirrors upstream Alertmanager's timeinterval package):
//
//   - A TimeInterval is a named group of Interval specs. The group matches
//     a time.Time if ANY Interval in the list matches (logical OR).
//   - An Interval carries independent constraint fields (times, weekdays,
//     days_of_month, months, years, location). The Interval matches a
//     time.Time only if ALL non-empty fields match (logical AND). Within a
//     single field, the Interval matches if ANY of the listed ranges match
//     (logical OR). An Interval with every field empty matches every time.
//   - Times: start inclusive, end exclusive. All other ranges (weekdays,
//     days_of_month, months, years) are inclusive on both bounds.
//   - Location defaults to UTC when unset.
//
// This package has no dependency on the routing package so it stays pure
// and independently unit-testable; the routing package wires it in by name
// lookup (RouteConfig.TimeIntervalIndex) for use by the TimeMute pipeline
// step (Task 3.2).
package timeinterval

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// TimeInterval is a named group of Interval specs, as declared under the
// top-level `time_intervals:` (or deprecated `mute_time_intervals:`) config
// section and referenced by name from route.mute_time_intervals /
// route.active_time_intervals.
type TimeInterval struct {
	Name          string     `yaml:"name" json:"name"`
	TimeIntervals []Interval `yaml:"time_intervals" json:"time_intervals"`
}

// Matches reports whether t falls inside any of the group's Interval specs.
func (ti TimeInterval) Matches(t time.Time) bool {
	for _, iv := range ti.TimeIntervals {
		if iv.ContainsTime(t) {
			return true
		}
	}
	return false
}

// Clone returns a deep copy of the named interval group.
func (ti TimeInterval) Clone() TimeInterval {
	clone := TimeInterval{Name: ti.Name}
	if ti.TimeIntervals != nil {
		clone.TimeIntervals = make([]Interval, len(ti.TimeIntervals))
		for i, iv := range ti.TimeIntervals {
			clone.TimeIntervals[i] = iv.Clone()
		}
	}
	return clone
}

// Interval is a single set of time constraints. All non-empty fields must
// match for the Interval to match (logical AND); a zero-value Interval
// (every field empty) matches every time.
type Interval struct {
	Times       []TimeRange       `yaml:"times,omitempty" json:"times,omitempty"`
	Weekdays    []WeekdayRange    `yaml:"weekdays,omitempty" json:"weekdays,omitempty"`
	DaysOfMonth []DayOfMonthRange `yaml:"days_of_month,omitempty" json:"days_of_month,omitempty"`
	Months      []MonthRange      `yaml:"months,omitempty" json:"months,omitempty"`
	Years       []YearRange       `yaml:"years,omitempty" json:"years,omitempty"`
	Location    *Location         `yaml:"location,omitempty" json:"location,omitempty"`
}

// ContainsTime reports whether t satisfies every non-empty constraint field.
func (iv Interval) ContainsTime(t time.Time) bool {
	loc := time.UTC
	if iv.Location != nil && iv.Location.Location != nil {
		loc = iv.Location.Location
	}
	lt := t.In(loc)

	if len(iv.Times) > 0 {
		minute := lt.Hour()*60 + lt.Minute()
		if !matchesAny(iv.Times, func(r TimeRange) bool { return r.ContainsMinute(minute) }) {
			return false
		}
	}

	if len(iv.Weekdays) > 0 {
		wd := lt.Weekday()
		if !matchesAny(iv.Weekdays, func(r WeekdayRange) bool { return r.Contains(wd) }) {
			return false
		}
	}

	if len(iv.DaysOfMonth) > 0 {
		day := lt.Day()
		total := daysInMonth(lt.Year(), lt.Month(), loc)
		if !matchesAny(iv.DaysOfMonth, func(r DayOfMonthRange) bool { return r.Contains(day, total) }) {
			return false
		}
	}

	if len(iv.Months) > 0 {
		month := lt.Month()
		if !matchesAny(iv.Months, func(r MonthRange) bool { return r.Contains(month) }) {
			return false
		}
	}

	if len(iv.Years) > 0 {
		year := lt.Year()
		if !matchesAny(iv.Years, func(r YearRange) bool { return r.Contains(year) }) {
			return false
		}
	}

	return true
}

// Clone returns a deep copy of the interval.
func (iv Interval) Clone() Interval {
	clone := Interval{}
	if iv.Times != nil {
		clone.Times = append([]TimeRange{}, iv.Times...)
	}
	if iv.Weekdays != nil {
		clone.Weekdays = append([]WeekdayRange{}, iv.Weekdays...)
	}
	if iv.DaysOfMonth != nil {
		clone.DaysOfMonth = append([]DayOfMonthRange{}, iv.DaysOfMonth...)
	}
	if iv.Months != nil {
		clone.Months = append([]MonthRange{}, iv.Months...)
	}
	if iv.Years != nil {
		clone.Years = append([]YearRange{}, iv.Years...)
	}
	if iv.Location != nil {
		locCopy := *iv.Location
		clone.Location = &locCopy
	}
	return clone
}

func matchesAny[T any](items []T, pred func(T) bool) bool {
	for _, item := range items {
		if pred(item) {
			return true
		}
	}
	return false
}

func daysInMonth(year int, month time.Month, loc *time.Location) int {
	// Day 0 of the following month is the last day of the target month.
	return time.Date(year, month+1, 0, 0, 0, 0, 0, loc).Day()
}

// TimeRange is a single `{start_time, end_time}` clock-time window, stored
// as minutes since midnight (00:00 = 0 .. 24:00 = 1440). Matching is start
// inclusive, end exclusive.
type TimeRange struct {
	StartMinute int `json:"start_minute"`
	EndMinute   int `json:"end_minute"`
}

type timeRangeYAML struct {
	StartTime string `yaml:"start_time"`
	EndTime   string `yaml:"end_time"`
}

// UnmarshalYAML parses `{start_time: "HH:MM", end_time: "HH:MM"}`.
func (r *TimeRange) UnmarshalYAML(unmarshal func(interface{}) error) error {
	var raw timeRangeYAML
	if err := unmarshal(&raw); err != nil {
		return err
	}

	start, err := parseClockTime(raw.StartTime)
	if err != nil {
		return fmt.Errorf("times: invalid start_time %q: %w", raw.StartTime, err)
	}
	end, err := parseClockTime(raw.EndTime)
	if err != nil {
		return fmt.Errorf("times: invalid end_time %q: %w", raw.EndTime, err)
	}
	if start >= end {
		return fmt.Errorf("times: start_time %q must be before end_time %q", raw.StartTime, raw.EndTime)
	}

	r.StartMinute = start
	r.EndMinute = end
	return nil
}

// MarshalYAML serializes the range back to `{start_time, end_time}` strings.
func (r TimeRange) MarshalYAML() (interface{}, error) {
	return timeRangeYAML{
		StartTime: formatClockTime(r.StartMinute),
		EndTime:   formatClockTime(r.EndMinute),
	}, nil
}

// ContainsMinute reports whether minuteOfDay (0..1439) falls in
// [StartMinute, EndMinute).
func (r TimeRange) ContainsMinute(minuteOfDay int) bool {
	return minuteOfDay >= r.StartMinute && minuteOfDay < r.EndMinute
}

// parseClockTime parses "HH:MM" into minutes since midnight. Hour 24 is
// only valid as "24:00", representing the end of the day (1440).
func parseClockTime(s string) (int, error) {
	parts := strings.SplitN(s, ":", 2)
	if len(parts) != 2 {
		return 0, fmt.Errorf("expected HH:MM format")
	}

	hour, err := strconv.Atoi(parts[0])
	if err != nil || hour < 0 || hour > 24 {
		return 0, fmt.Errorf("hour must be between 00 and 24")
	}
	minute, err := strconv.Atoi(parts[1])
	if err != nil || minute < 0 || minute > 59 {
		return 0, fmt.Errorf("minute must be between 00 and 59")
	}
	if hour == 24 && minute != 0 {
		return 0, fmt.Errorf("24:00 is the only valid time with hour 24")
	}

	return hour*60 + minute, nil
}

func formatClockTime(minuteOfDay int) string {
	return fmt.Sprintf("%02d:%02d", minuteOfDay/60, minuteOfDay%60)
}

// WeekdayRange is an inclusive weekday range, e.g. "monday:friday" or a
// single "monday".
type WeekdayRange struct {
	Begin time.Weekday
	End   time.Weekday
}

var weekdayNames = map[string]time.Weekday{
	"sunday":    time.Sunday,
	"monday":    time.Monday,
	"tuesday":   time.Tuesday,
	"wednesday": time.Wednesday,
	"thursday":  time.Thursday,
	"friday":    time.Friday,
	"saturday":  time.Saturday,
}

func resolveWeekday(token string) (int, error) {
	wd, ok := weekdayNames[strings.ToLower(token)]
	if !ok {
		return 0, fmt.Errorf("invalid weekday %q (expected one of monday..sunday)", token)
	}
	return int(wd), nil
}

// UnmarshalYAML parses a weekday or weekday range, e.g. "monday" or
// "monday:friday".
func (w *WeekdayRange) UnmarshalYAML(unmarshal func(interface{}) error) error {
	s, err := decodeScalarToString(unmarshal)
	if err != nil {
		return err
	}
	begin, end, err := parseRangeString(s, resolveWeekday)
	if err != nil {
		return fmt.Errorf("weekdays: %w", err)
	}
	if begin > end {
		return fmt.Errorf("weekdays: reversed range %q (begin weekday after end weekday)", s)
	}
	w.Begin, w.End = time.Weekday(begin), time.Weekday(end)
	return nil
}

// Contains reports whether wd falls within [Begin, End] (inclusive).
func (w WeekdayRange) Contains(wd time.Weekday) bool {
	return int(wd) >= int(w.Begin) && int(wd) <= int(w.End)
}

// DayOfMonthRange is an inclusive day-of-month range. Positive values count
// from the start of the month (1 = first day); negative values count from
// the end of the month (-1 = last day). A range must not mix signs.
type DayOfMonthRange struct {
	Begin int
	End   int
}

// UnmarshalYAML parses a day-of-month value or range, e.g. "1", "1:5",
// "-3:-1", or a positive-to-negative span such as "1:-1" (the whole month) or
// "20:-1" (day 20 through the last day). Mirrors upstream Alertmanager's
// day_of_month validation: a negative Begin paired with a positive End is
// always rejected (ambiguous), but a positive Begin with a negative End is
// allowed as long as it cannot be reversed in the shortest possible month
// (28 days) — begin/end are otherwise clamped to the actual month length at
// match time by Contains.
func (d *DayOfMonthRange) UnmarshalYAML(unmarshal func(interface{}) error) error {
	s, err := decodeScalarToString(unmarshal)
	if err != nil {
		return err
	}
	begin, end, err := parseRangeString(s, resolveDayOfMonth)
	if err != nil {
		return fmt.Errorf("days_of_month: %w", err)
	}
	if begin < 0 && end > 0 {
		return fmt.Errorf("days_of_month: range %q has a negative begin day with a positive end day (end day must be negative if begin day is negative)", s)
	}
	// Compare using the shortest possible month (28 days) as a common frame
	// so a positive begin / negative end pair (e.g. "1:-1") is not rejected
	// by a naive raw-integer comparison (1 > -1) even though it is valid.
	checkBegin, checkEnd := begin, end
	if begin < 0 {
		checkBegin = 28 + begin
	}
	if end < 0 {
		checkEnd = 28 + end
	}
	if checkBegin > checkEnd {
		return fmt.Errorf("days_of_month: reversed range %q (end day %d is always before begin day %d)", s, end, begin)
	}
	d.Begin, d.End = begin, end
	return nil
}

// Contains reports whether day (1..daysInMonth) falls within the range,
// after normalizing negative bounds against the actual month length.
func (d DayOfMonthRange) Contains(day, daysInMonth int) bool {
	norm := func(v int) int {
		if v > 0 {
			return v
		}
		return daysInMonth + v + 1
	}
	begin, end := norm(d.Begin), norm(d.End)
	return day >= begin && day <= end
}

func resolveDayOfMonth(token string) (int, error) {
	n, err := strconv.Atoi(token)
	if err != nil {
		return 0, fmt.Errorf("invalid day_of_month %q: expected an integer", token)
	}
	if n == 0 || n < -31 || n > 31 {
		return 0, fmt.Errorf("day_of_month %d out of range [-31,31] (0 not allowed)", n)
	}
	return n, nil
}

// MonthRange is an inclusive month range, given as names ("january:march")
// or numbers ("1:3").
type MonthRange struct {
	Begin time.Month
	End   time.Month
}

var monthNames = map[string]int{
	"january":   1,
	"february":  2,
	"march":     3,
	"april":     4,
	"may":       5,
	"june":      6,
	"july":      7,
	"august":    8,
	"september": 9,
	"october":   10,
	"november":  11,
	"december":  12,
}

func resolveMonth(token string) (int, error) {
	if n, err := strconv.Atoi(token); err == nil {
		if n < 1 || n > 12 {
			return 0, fmt.Errorf("month %d out of range [1,12]", n)
		}
		return n, nil
	}
	m, ok := monthNames[strings.ToLower(token)]
	if !ok {
		return 0, fmt.Errorf("invalid month %q (expected 1-12 or january..december)", token)
	}
	return m, nil
}

// UnmarshalYAML parses a month or month range, e.g. "march", "1:3", or
// "january:march".
func (m *MonthRange) UnmarshalYAML(unmarshal func(interface{}) error) error {
	s, err := decodeScalarToString(unmarshal)
	if err != nil {
		return err
	}
	begin, end, err := parseRangeString(s, resolveMonth)
	if err != nil {
		return fmt.Errorf("months: %w", err)
	}
	if begin > end {
		return fmt.Errorf("months: reversed range %q", s)
	}
	m.Begin, m.End = time.Month(begin), time.Month(end)
	return nil
}

// Contains reports whether month falls within [Begin, End] (inclusive).
func (m MonthRange) Contains(month time.Month) bool {
	return int(month) >= int(m.Begin) && int(month) <= int(m.End)
}

// YearRange is an inclusive absolute-year range, e.g. "2020:2022".
type YearRange struct {
	Begin int
	End   int
}

func resolveYear(token string) (int, error) {
	n, err := strconv.Atoi(token)
	if err != nil {
		return 0, fmt.Errorf("invalid year %q: expected an integer", token)
	}
	if n < 0 || n > 9999 {
		return 0, fmt.Errorf("year %d out of range [0,9999]", n)
	}
	return n, nil
}

// UnmarshalYAML parses a year or year range, e.g. "2024" or "2020:2022".
func (y *YearRange) UnmarshalYAML(unmarshal func(interface{}) error) error {
	s, err := decodeScalarToString(unmarshal)
	if err != nil {
		return err
	}
	begin, end, err := parseRangeString(s, resolveYear)
	if err != nil {
		return fmt.Errorf("years: %w", err)
	}
	if begin > end {
		return fmt.Errorf("years: reversed range %q", s)
	}
	y.Begin, y.End = begin, end
	return nil
}

// Contains reports whether year falls within [Begin, End] (inclusive).
func (y YearRange) Contains(year int) bool {
	return year >= y.Begin && year <= y.End
}

// Location wraps *time.Location so it can be parsed from a plain IANA
// timezone string, e.g. "Europe/Moscow". Unset (nil) means UTC.
type Location struct {
	*time.Location
}

// UnmarshalYAML loads the named IANA timezone via time.LoadLocation.
func (l *Location) UnmarshalYAML(unmarshal func(interface{}) error) error {
	var s string
	if err := unmarshal(&s); err != nil {
		return err
	}
	loc, err := time.LoadLocation(s)
	if err != nil {
		return fmt.Errorf("location: %w", err)
	}
	l.Location = loc
	return nil
}

// MarshalYAML serializes the location back to its IANA name.
func (l Location) MarshalYAML() (interface{}, error) {
	if l.Location == nil {
		return "UTC", nil
	}
	return l.String(), nil
}

// parseRangeString parses either a single token ("monday") or a
// "begin:end" range ("monday:friday") using resolve to turn each token
// into a comparable int.
func parseRangeString(s string, resolve func(string) (int, error)) (int, int, error) {
	parts := strings.SplitN(s, ":", 2)

	begin, err := resolve(strings.TrimSpace(parts[0]))
	if err != nil {
		return 0, 0, err
	}

	if len(parts) == 1 {
		return begin, begin, nil
	}

	end, err := resolve(strings.TrimSpace(parts[1]))
	if err != nil {
		return 0, 0, err
	}

	return begin, end, nil
}

// decodeScalarToString decodes a YAML scalar (string or number) into its
// string form, so range fields can be written either quoted ("1:5") or bare
// (5, -3) in YAML.
func decodeScalarToString(unmarshal func(interface{}) error) (string, error) {
	var raw interface{}
	if err := unmarshal(&raw); err != nil {
		return "", err
	}

	switch v := raw.(type) {
	case string:
		return v, nil
	case int:
		return strconv.Itoa(v), nil
	case int64:
		return strconv.FormatInt(v, 10), nil
	case uint64:
		return strconv.FormatUint(v, 10), nil
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64), nil
	default:
		return "", fmt.Errorf("expected a string or number, got %T", v)
	}
}
