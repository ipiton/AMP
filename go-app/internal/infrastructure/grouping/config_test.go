package grouping

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestDuration_UnmarshalYAML(t *testing.T) {
	tests := []struct {
		name    string
		yaml    string
		want    time.Duration
		wantErr bool
	}{
		{
			name: "valid seconds",
			yaml: `duration: 30s`,
			want: 30 * time.Second,
		},
		{
			name: "valid minutes",
			yaml: `duration: 5m`,
			want: 5 * time.Minute,
		},
		{
			name: "valid hours",
			yaml: `duration: 2h`,
			want: 2 * time.Hour,
		},
		{
			name: "valid complex",
			yaml: `duration: 1h30m`,
			want: 90 * time.Minute,
		},
		{
			name:    "invalid format",
			yaml:    `duration: invalid`,
			wantErr: true,
		},
		{
			name:    "empty value",
			yaml:    `duration: ""`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			type testStruct struct {
				Duration *Duration `yaml:"duration"`
			}

			var s testStruct
			err := yaml.Unmarshal([]byte(tt.yaml), &s)

			if tt.wantErr {
				assert.Error(t, err)
				return
			}

			require.NoError(t, err)
			require.NotNil(t, s.Duration)
			assert.Equal(t, tt.want, s.Duration.Duration)
		})
	}
}

func TestDuration_MarshalYAML(t *testing.T) {
	tests := []struct {
		name     string
		duration Duration
		want     string
	}{
		{
			name:     "seconds",
			duration: Duration{30 * time.Second},
			want:     "30s",
		},
		{
			name:     "minutes",
			duration: Duration{5 * time.Minute},
			want:     "5m0s",
		},
		{
			name:     "hours",
			duration: Duration{2 * time.Hour},
			want:     "2h0m0s",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			type testStruct struct {
				Duration Duration `yaml:"duration"`
			}

			s := testStruct{Duration: tt.duration}
			data, err := yaml.Marshal(&s)

			require.NoError(t, err)
			assert.Contains(t, string(data), tt.want)
		})
	}
}

func TestRoute_Defaults(t *testing.T) {
	route := &Route{
		Receiver: "test",
		GroupBy:  []string{"alertname"},
	}

	// Before defaults
	assert.Nil(t, route.GroupWait)
	assert.Nil(t, route.GroupInterval)
	assert.Nil(t, route.RepeatInterval)

	// Apply defaults
	route.Defaults()

	// After defaults
	assert.NotNil(t, route.GroupWait)
	assert.Equal(t, 30*time.Second, route.GroupWait.Duration)

	assert.NotNil(t, route.GroupInterval)
	assert.Equal(t, 5*time.Minute, route.GroupInterval.Duration)

	assert.NotNil(t, route.RepeatInterval)
	assert.Equal(t, 4*time.Hour, route.RepeatInterval.Duration)
}

func TestRoute_HasSpecialGrouping(t *testing.T) {
	tests := []struct {
		name    string
		groupBy []string
		want    bool
	}{
		{
			name:    "special value",
			groupBy: []string{"..."},
			want:    true,
		},
		{
			name:    "normal grouping",
			groupBy: []string{"alertname"},
			want:    false,
		},
		{
			name:    "multiple labels",
			groupBy: []string{"alertname", "cluster"},
			want:    false,
		},
		{
			name:    "empty",
			groupBy: []string{},
			want:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			route := &Route{GroupBy: tt.groupBy}
			assert.Equal(t, tt.want, route.HasSpecialGrouping())
		})
	}
}

func TestRoute_IsGlobalGroup(t *testing.T) {
	tests := []struct {
		name    string
		groupBy []string
		want    bool
	}{
		{
			name:    "empty group_by",
			groupBy: []string{},
			want:    true,
		},
		{
			name:    "nil group_by",
			groupBy: nil,
			want:    true,
		},
		{
			name:    "single label",
			groupBy: []string{"alertname"},
			want:    false,
		},
		{
			name:    "special value",
			groupBy: []string{"..."},
			want:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			route := &Route{GroupBy: tt.groupBy}
			assert.Equal(t, tt.want, route.IsGlobalGroup())
		})
	}
}

func TestRoute_GetGroupingLabels(t *testing.T) {
	tests := []struct {
		name    string
		groupBy []string
		want    []string
	}{
		{
			name:    "normal labels",
			groupBy: []string{"alertname", "cluster"},
			want:    []string{"alertname", "cluster"},
		},
		{
			name:    "special value",
			groupBy: []string{"..."},
			want:    nil,
		},
		{
			name:    "empty",
			groupBy: []string{},
			want:    []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			route := &Route{GroupBy: tt.groupBy}
			assert.Equal(t, tt.want, route.GetGroupingLabels())
		})
	}
}

func TestRoute_GetEffective(t *testing.T) {
	t.Run("with set values", func(t *testing.T) {
		route := &Route{
			GroupWait:      &Duration{10 * time.Second},
			GroupInterval:  &Duration{2 * time.Minute},
			RepeatInterval: &Duration{1 * time.Hour},
		}

		assert.Equal(t, 10*time.Second, route.GetEffectiveGroupWait())
		assert.Equal(t, 2*time.Minute, route.GetEffectiveGroupInterval())
		assert.Equal(t, 1*time.Hour, route.GetEffectiveRepeatInterval())
	})

	t.Run("with defaults", func(t *testing.T) {
		route := &Route{}

		assert.Equal(t, 30*time.Second, route.GetEffectiveGroupWait())
		assert.Equal(t, 5*time.Minute, route.GetEffectiveGroupInterval())
		assert.Equal(t, 4*time.Hour, route.GetEffectiveRepeatInterval())
	})
}

func TestRoute_Validate(t *testing.T) {
	tests := []struct {
		name    string
		route   *Route
		wantErr bool
	}{
		{
			name: "valid route",
			route: &Route{
				Receiver: "default",
				GroupBy:  []string{"alertname"},
			},
			wantErr: false,
		},
		{
			name: "missing receiver",
			route: &Route{
				GroupBy: []string{"alertname"},
			},
			wantErr: true,
		},
		{
			name: "global group",
			route: &Route{
				Receiver: "default",
				GroupBy:  []string{},
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.route.Validate()
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestRoute_Clone(t *testing.T) {
	original := &Route{
		Receiver:       "test",
		GroupBy:        []string{"alertname", "cluster"},
		GroupWait:      &Duration{30 * time.Second},
		GroupInterval:  &Duration{5 * time.Minute},
		RepeatInterval: &Duration{4 * time.Hour},
		Match:          map[string]string{"severity": "critical"},
		MatchRE:        map[string]string{"service": "^api.*"},
		Continue:       true,
		Routes: []*Route{
			{
				Receiver: "nested",
				GroupBy:  []string{"namespace"},
			},
		},
	}

	clone := original.Clone()

	// Verify deep copy
	assert.Equal(t, original.Receiver, clone.Receiver)
	assert.Equal(t, original.GroupBy, clone.GroupBy)
	assert.Equal(t, original.Continue, clone.Continue)

	// Verify pointers are different (not same memory address)
	assert.NotEqual(t, fmt.Sprintf("%p", original.GroupWait), fmt.Sprintf("%p", clone.GroupWait))
	assert.NotEqual(t, fmt.Sprintf("%p", original.Match), fmt.Sprintf("%p", clone.Match))
	assert.NotEqual(t, fmt.Sprintf("%p", original.Routes), fmt.Sprintf("%p", clone.Routes))
	assert.NotEqual(t, fmt.Sprintf("%p", original.Routes[0]), fmt.Sprintf("%p", clone.Routes[0]))

	// Verify values are equal
	assert.Equal(t, original.GroupWait.Duration, clone.GroupWait.Duration)
	assert.Equal(t, original.Match, clone.Match)
	assert.Equal(t, original.Routes[0].Receiver, clone.Routes[0].Receiver)

	// Verify modification doesn't affect original
	clone.GroupBy[0] = "modified"
	assert.NotEqual(t, original.GroupBy[0], clone.GroupBy[0])

	clone.Match["new"] = "value"
	assert.NotContains(t, original.Match, "new")
}

func TestRoute_String(t *testing.T) {
	route := &Route{
		Receiver:       "test",
		GroupBy:        []string{"alertname"},
		GroupWait:      &Duration{30 * time.Second},
		GroupInterval:  &Duration{5 * time.Minute},
		RepeatInterval: &Duration{4 * time.Hour},
		Routes: []*Route{
			{Receiver: "nested1"},
			{Receiver: "nested2"},
		},
	}

	str := route.String()

	assert.Contains(t, str, "receiver=test")
	assert.Contains(t, str, "group_by=[alertname]")
	assert.Contains(t, str, "30s")
	assert.Contains(t, str, "5m")
	assert.Contains(t, str, "4h")
	assert.Contains(t, str, "nested_routes=2")
}
