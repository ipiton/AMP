package grouping

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestIsValidLabelName tests label name validation
func TestIsValidLabelName(t *testing.T) {
	tests := []struct {
		name  string
		label string
		want  bool
	}{
		// Valid cases
		{"simple", "alertname", true},
		{"with_underscore", "alert_name", true},
		{"with_number", "alert123", true},
		{"starts_with_underscore", "_private", true},
		{"mixed_case", "AlertName", true},
		{"single_char", "a", true},
		{"all_caps", "ALERTNAME", true},

		// Invalid cases
		{"empty", "", false},
		{"starts_with_number", "123alert", false},
		{"with_dash", "alert-name", false},
		{"with_space", "alert name", false},
		{"with_dot", "alert.name", false},
		{"with_special_char", "alert@name", false},
		{"only_number", "123", false},
		{"with_slash", "alert/name", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isValidLabelName(tt.label)
			assert.Equal(t, tt.want, got)
		})
	}
}

// TestValidateGroupWaitRange tests group_wait range validation
func TestValidateGroupWaitRange(t *testing.T) {
	tests := []struct {
		name    string
		d       time.Duration
		wantErr bool
	}{
		// Valid cases
		{"zero", 0, false},
		{"30 seconds", 30 * time.Second, false},
		{"1 minute", time.Minute, false},
		{"30 minutes", 30 * time.Minute, false},
		{"1 hour", time.Hour, false},

		// Invalid cases
		{"negative", -10 * time.Second, true},
		{"over 1 hour", 2 * time.Hour, true},
		{"way over", 24 * time.Hour, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateGroupWaitRange(tt.d)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// TestValidateGroupIntervalRange tests group_interval range validation
func TestValidateGroupIntervalRange(t *testing.T) {
	tests := []struct {
		name    string
		d       time.Duration
		wantErr bool
	}{
		// Valid cases
		{"1 second", time.Second, false},
		{"5 minutes", 5 * time.Minute, false},
		{"1 hour", time.Hour, false},
		{"12 hours", 12 * time.Hour, false},
		{"24 hours", 24 * time.Hour, false},

		// Invalid cases
		{"under 1 second", 500 * time.Millisecond, true},
		{"zero", 0, true},
		{"negative", -5 * time.Minute, true},
		{"over 24 hours", 25 * time.Hour, true},
		{"way over", 48 * time.Hour, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateGroupIntervalRange(tt.d)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// TestValidateRepeatIntervalRange tests repeat_interval range validation
func TestValidateRepeatIntervalRange(t *testing.T) {
	tests := []struct {
		name    string
		d       time.Duration
		wantErr bool
	}{
		// Valid cases
		{"1 minute", time.Minute, false},
		{"1 hour", time.Hour, false},
		{"4 hours", 4 * time.Hour, false},
		{"1 day", 24 * time.Hour, false},
		{"7 days", 7 * 24 * time.Hour, false},

		// Invalid cases
		{"under 1 minute", 30 * time.Second, true},
		{"zero", 0, true},
		{"negative", -time.Hour, true},
		{"over 7 days", 8 * 24 * time.Hour, true},
		{"way over", 30 * 24 * time.Hour, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateRepeatIntervalRange(tt.d)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// TestValidateLabelNames tests multiple label name validation
func TestValidateLabelNames(t *testing.T) {
	tests := []struct {
		name        string
		labels      []string
		wantInvalid string
		wantErr     bool
	}{
		{
			name:    "all valid",
			labels:  []string{"alertname", "cluster", "namespace"},
			wantErr: false,
		},
		{
			name:        "one invalid",
			labels:      []string{"alertname", "invalid-label", "cluster"},
			wantInvalid: "invalid-label",
			wantErr:     true,
		},
		{
			name:    "empty list",
			labels:  []string{},
			wantErr: false,
		},
		{
			name:        "invalid with space",
			labels:      []string{"alert name"},
			wantInvalid: "alert name",
			wantErr:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			invalidLabel, err := ValidateLabelNames(tt.labels)
			if tt.wantErr {
				assert.Error(t, err)
				assert.Equal(t, tt.wantInvalid, invalidLabel)
			} else {
				assert.NoError(t, err)
				assert.Empty(t, invalidLabel)
			}
		})
	}
}

// TestValidateGroupByLabels tests group_by validation
func TestValidateGroupByLabels(t *testing.T) {
	tests := []struct {
		name    string
		groupBy []string
		wantErr bool
	}{
		// Valid cases
		{"normal labels", []string{"alertname", "cluster"}, false},
		{"single label", []string{"alertname"}, false},
		{"special grouping", []string{"..."}, false},
		{"empty (global)", []string{}, false},

		// Invalid cases
		{"invalid label", []string{"alert-name"}, true},
		{"mixed valid and invalid", []string{"alertname", "invalid-label"}, true},
		{"label with space", []string{"alert name"}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateGroupByLabels(tt.groupBy)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// TestValidateTimers tests timer validation
func TestValidateTimers(t *testing.T) {
	tests := []struct {
		name           string
		groupWait      *Duration
		groupInterval  *Duration
		repeatInterval *Duration
		wantErr        bool
	}{
		{
			name:           "all valid",
			groupWait:      &Duration{30 * time.Second},
			groupInterval:  &Duration{5 * time.Minute},
			repeatInterval: &Duration{4 * time.Hour},
			wantErr:        false,
		},
		{
			name:           "all nil",
			groupWait:      nil,
			groupInterval:  nil,
			repeatInterval: nil,
			wantErr:        false,
		},
		{
			name:      "invalid group_wait",
			groupWait: &Duration{-10 * time.Second},
			wantErr:   true,
		},
		{
			name:          "invalid group_interval",
			groupInterval: &Duration{500 * time.Millisecond},
			wantErr:       true,
		},
		{
			name:           "invalid repeat_interval",
			repeatInterval: &Duration{30 * time.Second},
			wantErr:        true,
		},
		{
			name:           "multiple invalid",
			groupWait:      &Duration{-10 * time.Second},
			groupInterval:  &Duration{0},
			repeatInterval: &Duration{10 * time.Second},
			wantErr:        true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateTimers(tt.groupWait, tt.groupInterval, tt.repeatInterval)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// TestValidateRoute tests route validation
func TestValidateRoute(t *testing.T) {
	tests := []struct {
		name    string
		route   *Route
		wantErr bool
	}{
		{
			name: "valid route",
			route: &Route{
				Receiver: "test",
				GroupBy:  []string{"alertname"},
			},
			wantErr: false,
		},
		{
			name:    "nil route",
			route:   nil,
			wantErr: true,
		},
		{
			name: "missing receiver",
			route: &Route{
				GroupBy: []string{"alertname"},
			},
			wantErr: true,
		},
		{
			name: "invalid label in group_by",
			route: &Route{
				Receiver: "test",
				GroupBy:  []string{"invalid-label"},
			},
			wantErr: true,
		},
		{
			name: "invalid group_wait",
			route: &Route{
				Receiver:  "test",
				GroupBy:   []string{"alertname"},
				GroupWait: &Duration{-10 * time.Second},
			},
			wantErr: true,
		},
		{
			name: "empty matchers",
			route: &Route{
				Receiver: "test",
				GroupBy:  []string{"alertname"},
				Match:    map[string]string{},
				MatchRE:  map[string]string{},
			},
			wantErr: true,
		},
		{
			name: "valid nested routes",
			route: &Route{
				Receiver: "default",
				GroupBy:  []string{"alertname"},
				Routes: []*Route{
					{
						Receiver: "nested",
						GroupBy:  []string{"cluster"},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "invalid nested route",
			route: &Route{
				Receiver: "default",
				GroupBy:  []string{"alertname"},
				Routes: []*Route{
					{
						Receiver: "nested",
						GroupBy:  []string{"invalid-label"},
					},
				},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateRoute(tt.route)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// TestValidateRoute_MaxDepth tests max depth validation
func TestValidateRoute_MaxDepth(t *testing.T) {
	// Create a deeply nested route (exceeds maxRouteDepth = 10)
	route := &Route{
		Receiver: "root",
		GroupBy:  []string{"alertname"},
	}

	current := route
	for i := 0; i < 12; i++ {
		nested := &Route{
			Receiver: "nested",
			GroupBy:  []string{"label"},
		}
		current.Routes = []*Route{nested}
		current = nested
	}

	err := ValidateRoute(route)
	assert.Error(t, err)
	// Check that error message contains depth validation keywords
	assert.Contains(t, err.Error(), "nesting depth")
	assert.Contains(t, err.Error(), "exceeds maximum")
}

// TestValidateConfig tests config validation
func TestValidateConfig(t *testing.T) {
	tests := []struct {
		name    string
		config  *GroupingConfig
		wantErr bool
	}{
		{
			name: "valid config",
			config: &GroupingConfig{
				Route: &Route{
					Receiver: "test",
					GroupBy:  []string{"alertname"},
				},
			},
			wantErr: false,
		},
		{
			name:    "nil config",
			config:  nil,
			wantErr: true,
		},
		{
			name:    "nil route",
			config:  &GroupingConfig{},
			wantErr: true,
		},
		{
			name: "invalid route",
			config: &GroupingConfig{
				Route: &Route{
					GroupBy: []string{"alertname"},
					// Missing receiver
				},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateConfig(tt.config)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// TestValidateConfigCompat tests compatibility validation (warnings)
func TestValidateConfigCompat(t *testing.T) {
	tests := []struct {
		name   string
		config *GroupingConfig
	}{
		{
			name: "very short group_wait",
			config: &GroupingConfig{
				Route: &Route{
					Receiver:  "test",
					GroupBy:   []string{"alertname"},
					GroupWait: &Duration{3 * time.Second},
				},
			},
		},
		{
			name: "very long group_wait",
			config: &GroupingConfig{
				Route: &Route{
					Receiver:  "test",
					GroupBy:   []string{"alertname"},
					GroupWait: &Duration{15 * time.Minute},
				},
			},
		},
		{
			name: "very short group_interval",
			config: &GroupingConfig{
				Route: &Route{
					Receiver:      "test",
					GroupBy:       []string{"alertname"},
					GroupInterval: &Duration{20 * time.Second},
				},
			},
		},
		{
			name: "very short repeat_interval",
			config: &GroupingConfig{
				Route: &Route{
					Receiver:       "test",
					GroupBy:        []string{"alertname"},
					RepeatInterval: &Duration{20 * time.Minute},
				},
			},
		},
		{
			name: "optimal config (no warnings)",
			config: &GroupingConfig{
				Route: &Route{
					Receiver:       "test",
					GroupBy:        []string{"alertname"},
					GroupWait:      &Duration{30 * time.Second},
					GroupInterval:  &Duration{5 * time.Minute},
					RepeatInterval: &Duration{4 * time.Hour},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// ValidateConfigCompat always returns nil currently
			// It's designed for future warning system
			err := ValidateConfigCompat(tt.config)
			assert.NoError(t, err)
		})
	}
}

// TestSanitizeConfig tests config sanitization
func TestSanitizeConfig(t *testing.T) {
	tests := []struct {
		name   string
		config *GroupingConfig
		check  func(t *testing.T, sanitized *GroupingConfig)
	}{
		{
			name:   "nil config",
			config: nil,
			check: func(t *testing.T, sanitized *GroupingConfig) {
				assert.Nil(t, sanitized)
			},
		},
		{
			name: "config with source",
			config: &GroupingConfig{
				Route: &Route{
					Receiver: "test",
					GroupBy:  []string{"alertname"},
					source:   "/path/to/config.yaml",
				},
			},
			check: func(t *testing.T, sanitized *GroupingConfig) {
				require.NotNil(t, sanitized)
				assert.Empty(t, sanitized.Route.source)
				assert.Equal(t, "test", sanitized.Route.Receiver)
			},
		},
		{
			name: "config with nested routes and sources",
			config: &GroupingConfig{
				Route: &Route{
					Receiver: "default",
					GroupBy:  []string{"alertname"},
					source:   "/path/to/config.yaml",
					Routes: []*Route{
						{
							Receiver: "nested",
							GroupBy:  []string{"cluster"},
							source:   "/path/to/nested.yaml",
						},
					},
				},
			},
			check: func(t *testing.T, sanitized *GroupingConfig) {
				require.NotNil(t, sanitized)
				assert.Empty(t, sanitized.Route.source)
				assert.Empty(t, sanitized.Route.Routes[0].source)
				assert.Equal(t, "default", sanitized.Route.Receiver)
				assert.Equal(t, "nested", sanitized.Route.Routes[0].Receiver)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sanitized := SanitizeConfig(tt.config)
			tt.check(t, sanitized)
		})
	}
}

// TestSanitizeRouteSources tests route source sanitization
func TestSanitizeRouteSources(t *testing.T) {
	route := &Route{
		Receiver: "test",
		GroupBy:  []string{"alertname"},
		source:   "/path/to/config.yaml",
		Routes: []*Route{
			{
				Receiver: "nested1",
				GroupBy:  []string{"cluster"},
				source:   "/path/to/nested1.yaml",
			},
			{
				Receiver: "nested2",
				GroupBy:  []string{"namespace"},
				source:   "/path/to/nested2.yaml",
				Routes: []*Route{
					{
						Receiver: "deeply-nested",
						GroupBy:  []string{"pod"},
						source:   "/path/to/deeply-nested.yaml",
					},
				},
			},
		},
	}

	sanitizeRouteSources(route)

	assert.Empty(t, route.source)
	assert.Empty(t, route.Routes[0].source)
	assert.Empty(t, route.Routes[1].source)
	assert.Empty(t, route.Routes[1].Routes[0].source)
}

// TestValidateRoute_ComplexScenarios tests complex validation scenarios
func TestValidateRoute_ComplexScenarios(t *testing.T) {
	tests := []struct {
		name    string
		route   *Route
		wantErr bool
	}{
		{
			name: "valid complex route with all features",
			route: &Route{
				Receiver:       "default",
				GroupBy:        []string{"alertname", "cluster"},
				GroupWait:      &Duration{30 * time.Second},
				GroupInterval:  &Duration{5 * time.Minute},
				RepeatInterval: &Duration{4 * time.Hour},
				Match:          map[string]string{"severity": "critical"},
				MatchRE:        map[string]string{"service": "^api-.*"},
				Continue:       true,
				Routes: []*Route{
					{
						Receiver: "team-a",
						GroupBy:  []string{"namespace"},
						Match:    map[string]string{"team": "frontend"},
					},
					{
						Receiver: "team-b",
						GroupBy:  []string{"pod"},
						MatchRE:  map[string]string{"namespace": "^prod-.*"},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "special grouping with nested routes",
			route: &Route{
				Receiver: "all",
				GroupBy:  []string{"..."},
				Routes: []*Route{
					{
						Receiver: "critical",
						GroupBy:  []string{"severity"},
						Match:    map[string]string{"severity": "critical"},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "global grouping",
			route: &Route{
				Receiver: "global",
				GroupBy:  []string{},
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateRoute(tt.route)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
