package routing

import (
	"bytes"
	"log/slog"
	"testing"
	"time"

	"github.com/ipiton/AMP/internal/infrastructure/grouping"
	"github.com/ipiton/AMP/internal/infrastructure/routing/timeinterval"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRouteConfigParser_Parse_TimeIntervals_ValidFullConfig(t *testing.T) {
	yamlConfig := `
route:
  receiver: default
  group_by: [alertname]
  mute_time_intervals: [maintenance]
  routes:
    - receiver: default
      active_time_intervals: [business-hours]

receivers:
  - name: default
    webhook_configs:
      - url: https://example.com/webhook

time_intervals:
  - name: maintenance
    time_intervals:
      - weekdays: ['saturday', 'sunday']

  - name: business-hours
    time_intervals:
      - times:
          - start_time: "09:00"
            end_time: "17:00"
        weekdays: ['monday:friday']
        location: "Europe/Moscow"
`

	parser := NewRouteConfigParser()
	config, err := parser.Parse([]byte(yamlConfig))

	require.NoError(t, err)
	require.NotNil(t, config)

	assert.Equal(t, []string{"maintenance"}, config.Route.MuteTimeIntervals)
	require.Len(t, config.Route.Routes, 1)
	assert.Equal(t, []string{"business-hours"}, config.Route.Routes[0].ActiveTimeIntervals)

	// Both named groups are indexed and independently evaluable.
	maintenance, ok := config.GetTimeInterval("maintenance")
	require.True(t, ok)

	saturday, err := time.Parse(time.RFC3339, "2024-06-15T12:00:00Z") // a Saturday
	require.NoError(t, err)
	assert.True(t, maintenance.Matches(saturday))

	tuesday, err := time.Parse(time.RFC3339, "2024-06-11T12:00:00Z") // a Tuesday
	require.NoError(t, err)
	assert.False(t, maintenance.Matches(tuesday))

	businessHours, ok := config.GetTimeInterval("business-hours")
	require.True(t, ok)

	// 08:00 UTC == 11:00 Moscow, Tuesday -> inside business hours.
	inHours, err := time.Parse(time.RFC3339, "2024-06-11T08:00:00Z")
	require.NoError(t, err)
	assert.True(t, businessHours.Matches(inHours))

	_, ok = config.GetTimeInterval("nonexistent")
	assert.False(t, ok)
}

func TestRouteConfigParser_Parse_TimeIntervals_UndefinedMuteReference(t *testing.T) {
	yamlConfig := `
route:
  receiver: default
  mute_time_intervals: [nonexistent]

receivers:
  - name: default
    webhook_configs:
      - url: https://example.com/webhook
`

	parser := NewRouteConfigParser()
	config, err := parser.Parse([]byte(yamlConfig))

	assert.Error(t, err)
	assert.Nil(t, config)
	assert.Contains(t, err.Error(), "time interval 'nonexistent' not found")
}

func TestRouteConfigParser_Parse_TimeIntervals_UndefinedActiveReference(t *testing.T) {
	yamlConfig := `
route:
  receiver: default
  active_time_intervals: [nonexistent]

receivers:
  - name: default
    webhook_configs:
      - url: https://example.com/webhook
`

	parser := NewRouteConfigParser()
	config, err := parser.Parse([]byte(yamlConfig))

	assert.Error(t, err)
	assert.Nil(t, config)
	assert.Contains(t, err.Error(), "time interval 'nonexistent' not found")
}

func TestRouteConfigParser_Parse_TimeIntervals_DuplicateName(t *testing.T) {
	yamlConfig := `
route:
  receiver: default

receivers:
  - name: default
    webhook_configs:
      - url: https://example.com/webhook

time_intervals:
  - name: dup
    time_intervals:
      - weekdays: ['monday']
  - name: dup
    time_intervals:
      - weekdays: ['tuesday']
`

	parser := NewRouteConfigParser()
	config, err := parser.Parse([]byte(yamlConfig))

	assert.Error(t, err)
	assert.Nil(t, config)
	assert.Contains(t, err.Error(), `duplicate time interval name "dup"`)
}

func TestRouteConfigParser_Parse_TimeIntervals_DuplicateAcrossSections(t *testing.T) {
	yamlConfig := `
route:
  receiver: default

receivers:
  - name: default
    webhook_configs:
      - url: https://example.com/webhook

time_intervals:
  - name: dup
    time_intervals:
      - weekdays: ['monday']

mute_time_intervals:
  - name: dup
    time_intervals:
      - weekdays: ['tuesday']
`

	parser := NewRouteConfigParser()
	config, err := parser.Parse([]byte(yamlConfig))

	assert.Error(t, err)
	assert.Nil(t, config)
	assert.Contains(t, err.Error(), `duplicate time interval name "dup"`)
}

func TestRouteConfigParser_Parse_TimeIntervals_MissingName(t *testing.T) {
	yamlConfig := `
route:
  receiver: default

receivers:
  - name: default
    webhook_configs:
      - url: https://example.com/webhook

time_intervals:
  - time_intervals:
      - weekdays: ['monday']
`

	parser := NewRouteConfigParser()
	config, err := parser.Parse([]byte(yamlConfig))

	assert.Error(t, err)
	assert.Nil(t, config)
	assert.Contains(t, err.Error(), "missing required 'name' field")
}

func TestRouteConfigParser_Parse_TimeIntervals_LegacyAliasMergedAndWarns(t *testing.T) {
	yamlConfig := `
route:
  receiver: default
  mute_time_intervals: [legacy-group]

receivers:
  - name: default
    webhook_configs:
      - url: https://example.com/webhook

mute_time_intervals:
  - name: legacy-group
    time_intervals:
      - months: ['december']
`

	var logBuf bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logBuf, nil)))
	defer slog.SetDefault(previous)

	parser := NewRouteConfigParser()
	config, err := parser.Parse([]byte(yamlConfig))

	require.NoError(t, err)
	require.NotNil(t, config)

	require.Len(t, config.MuteTimeIntervalsSection, 1)
	assert.Equal(t, "legacy-group", config.MuteTimeIntervalsSection[0].Name)

	group, ok := config.GetTimeInterval("legacy-group")
	require.True(t, ok)
	assert.Equal(t, "legacy-group", group.Name)

	assert.Contains(t, logBuf.String(), "deprecated")
	assert.Contains(t, logBuf.String(), "mute_time_intervals")
}

func TestRouteConfig_Clone_TimeIntervals(t *testing.T) {
	yamlConfig := `
route:
  receiver: default
  mute_time_intervals: [maintenance]

receivers:
  - name: default
    webhook_configs:
      - url: https://example.com/webhook

time_intervals:
  - name: maintenance
    time_intervals:
      - weekdays: ['saturday', 'sunday']
`

	parser := NewRouteConfigParser()
	config, err := parser.Parse([]byte(yamlConfig))
	require.NoError(t, err)

	clone := config.Clone()

	require.Len(t, clone.TimeIntervals, 1)
	assert.Equal(t, config.TimeIntervals[0].Name, clone.TimeIntervals[0].Name)

	group, ok := clone.GetTimeInterval("maintenance")
	require.True(t, ok)
	assert.Equal(t, "maintenance", group.Name)

	// Mutating the clone must not affect the original.
	clone.TimeIntervals[0].Name = "mutated"
	assert.Equal(t, "maintenance", config.TimeIntervals[0].Name)
	assert.NotEqual(t, config.TimeIntervals[0].Name, clone.TimeIntervals[0].Name)
}

// TestRouteConfig_Clone_TimeIntervalIndex_RebuiltWithoutParse guards against
// a regression where Clone() copied TimeIntervalIndex conditionally (only
// if already non-nil) instead of always rebuilding it from
// TimeIntervals/MuteTimeIntervalsSection - same as ReceiverIndex is always
// rebuilt from Receivers a few lines above it. A RouteConfig built by hand
// (the ValidateConfig/hot-reload path, which never calls
// buildTimeIntervalIndex) has TimeIntervals populated but
// TimeIntervalIndex == nil; Clone() must still make lookups work on the
// result.
func TestRouteConfig_Clone_TimeIntervalIndex_RebuiltWithoutParse(t *testing.T) {
	config := &RouteConfig{
		Route: &grouping.Route{
			Receiver:          "default",
			MuteTimeIntervals: []string{"maintenance"},
		},
		Receivers: []*Receiver{
			{Name: "default"},
		},
		TimeIntervals: []timeinterval.TimeInterval{
			{
				Name: "maintenance",
				TimeIntervals: []timeinterval.Interval{
					{Weekdays: []timeinterval.WeekdayRange{{Begin: time.Saturday, End: time.Saturday}}},
				},
			},
		},
		// TimeIntervalIndex intentionally left nil, as it would be for a
		// config that was never run through Parse().
	}
	require.Nil(t, config.TimeIntervalIndex)

	clone := config.Clone()

	group, ok := clone.GetTimeInterval("maintenance")
	require.True(t, ok, "Clone() must rebuild TimeIntervalIndex even when the source index was nil")
	assert.Equal(t, "maintenance", group.Name)

	saturday, err := time.Parse(time.RFC3339, "2024-06-15T12:00:00Z")
	require.NoError(t, err)
	assert.True(t, group.Matches(saturday))
}
