package grouping

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNewParser tests parser initialization
func TestNewParser(t *testing.T) {
	parser := NewParser()
	assert.NotNil(t, parser)
	assert.NotNil(t, parser.validator)
}

// TestParser_Parse tests basic YAML parsing
func TestParser_Parse(t *testing.T) {
	tests := []struct {
		name    string
		yaml    string
		wantErr bool
		check   func(t *testing.T, config *GroupingConfig)
	}{
		{
			name: "valid basic config",
			yaml: `
route:
  receiver: "team-X"
  group_by: ['alertname', 'cluster']
  group_wait: 30s
  group_interval: 5m
  repeat_interval: 4h
`,
			wantErr: false,
			check: func(t *testing.T, config *GroupingConfig) {
				assert.Equal(t, "team-X", config.Route.Receiver)
				assert.Equal(t, []string{"alertname", "cluster"}, config.Route.GroupBy)
				assert.Equal(t, 30*time.Second, config.Route.GroupWait.Duration)
				assert.Equal(t, 5*time.Minute, config.Route.GroupInterval.Duration)
				assert.Equal(t, 4*time.Hour, config.Route.RepeatInterval.Duration)
			},
		},
		{
			name: "valid config with defaults",
			yaml: `
route:
  receiver: "team-Y"
  group_by: ['severity']
`,
			wantErr: false,
			check: func(t *testing.T, config *GroupingConfig) {
				assert.Equal(t, "team-Y", config.Route.Receiver)
				assert.Equal(t, []string{"severity"}, config.Route.GroupBy)
				// Check defaults are applied
				assert.Equal(t, 30*time.Second, config.Route.GroupWait.Duration)
				assert.Equal(t, 5*time.Minute, config.Route.GroupInterval.Duration)
				assert.Equal(t, 4*time.Hour, config.Route.RepeatInterval.Duration)
			},
		},
		{
			name: "special grouping with '...'",
			yaml: `
route:
  receiver: "all-alerts"
  group_by: ['...']
`,
			wantErr: false,
			check: func(t *testing.T, config *GroupingConfig) {
				assert.Equal(t, "all-alerts", config.Route.Receiver)
				assert.True(t, config.Route.HasSpecialGrouping())
			},
		},
		{
			name: "global grouping (empty group_by)",
			yaml: `
route:
  receiver: "global"
  group_by: []
`,
			wantErr: false,
			check: func(t *testing.T, config *GroupingConfig) {
				assert.Equal(t, "global", config.Route.Receiver)
				assert.True(t, config.Route.IsGlobalGroup())
			},
		},
		{
			name: "config with matchers",
			yaml: `
route:
  receiver: "team-Z"
  group_by: ['alertname']
  match:
    severity: critical
    team: backend
  match_re:
    service: "^api-.*"
`,
			wantErr: false,
			check: func(t *testing.T, config *GroupingConfig) {
				assert.Equal(t, "team-Z", config.Route.Receiver)
				assert.Equal(t, map[string]string{"severity": "critical", "team": "backend"}, config.Route.Match)
				assert.Equal(t, map[string]string{"service": "^api-.*"}, config.Route.MatchRE)
			},
		},
		{
			name: "config with nested routes",
			yaml: `
route:
  receiver: "default"
  group_by: ['alertname']
  routes:
    - receiver: "team-A"
      group_by: ['cluster']
      match:
        team: frontend
    - receiver: "team-B"
      group_by: ['namespace']
      match:
        team: backend
`,
			wantErr: false,
			check: func(t *testing.T, config *GroupingConfig) {
				assert.Equal(t, "default", config.Route.Receiver)
				assert.Len(t, config.Route.Routes, 2)
				assert.Equal(t, "team-A", config.Route.Routes[0].Receiver)
				assert.Equal(t, "team-B", config.Route.Routes[1].Receiver)
			},
		},
		{
			name:    "invalid YAML syntax",
			yaml:    `route: [invalid yaml`,
			wantErr: true,
		},
		{
			name: "missing route",
			yaml: `
receiver: "test"
`,
			wantErr: true,
		},
		{
			name: "missing receiver",
			yaml: `
route:
  group_by: ['alertname']
`,
			wantErr: true,
		},
		{
			name: "empty group_by",
			yaml: `
route:
  receiver: "test"
  group_by: []
`,
			wantErr: false, // Empty group_by is valid (global grouping)
		},
		{
			name: "invalid duration format",
			yaml: `
route:
  receiver: "test"
  group_by: ['alertname']
  group_wait: "invalid"
`,
			wantErr: true,
		},
		{
			name: "invalid label name in group_by",
			yaml: `
route:
  receiver: "test"
  group_by: ['alert-name', 'invalid label']
`,
			wantErr: true,
		},
		{
			name: "group_wait out of range (negative)",
			yaml: `
route:
  receiver: "test"
  group_by: ['alertname']
  group_wait: -10s
`,
			wantErr: true,
		},
		{
			name: "group_wait out of range (too large)",
			yaml: `
route:
  receiver: "test"
  group_by: ['alertname']
  group_wait: 2h
`,
			wantErr: true,
		},
		{
			name: "group_interval out of range (too small)",
			yaml: `
route:
  receiver: "test"
  group_by: ['alertname']
  group_interval: 500ms
`,
			wantErr: true,
		},
		{
			name: "repeat_interval out of range (too small)",
			yaml: `
route:
  receiver: "test"
  group_by: ['alertname']
  repeat_interval: 30s
`,
			wantErr: true,
		},
	}

	parser := NewParser()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config, err := parser.Parse([]byte(tt.yaml))

			if tt.wantErr {
				assert.Error(t, err)
				assert.Nil(t, config)
			} else {
				require.NoError(t, err)
				require.NotNil(t, config)
				if tt.check != nil {
					tt.check(t, config)
				}
			}
		})
	}
}

// TestParser_ParseString tests string parsing
func TestParser_ParseString(t *testing.T) {
	parser := NewParser()

	yaml := `
route:
  receiver: "test"
  group_by: ['alertname']
`

	config, err := parser.ParseString(yaml)
	require.NoError(t, err)
	require.NotNil(t, config)
	assert.Equal(t, "test", config.Route.Receiver)
}

// TestParser_ParseFile tests file parsing
func TestParser_ParseFile(t *testing.T) {
	parser := NewParser()

	// Create temporary test file
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "config.yaml")

	validYAML := `
route:
  receiver: "file-test"
  group_by: ['alertname', 'cluster']
  group_wait: 45s
`

	err := os.WriteFile(testFile, []byte(validYAML), 0644)
	require.NoError(t, err)

	// Test valid file
	config, err := parser.ParseFile(testFile)
	require.NoError(t, err)
	require.NotNil(t, config)
	assert.Equal(t, "file-test", config.Route.Receiver)
	assert.Equal(t, []string{"alertname", "cluster"}, config.Route.GroupBy)
	assert.Equal(t, 45*time.Second, config.Route.GroupWait.Duration)
	assert.Equal(t, testFile, config.Route.source)

	// Test non-existent file
	_, err = parser.ParseFile("/non/existent/file.yaml")
	assert.Error(t, err)
	assert.IsType(t, &ConfigError{}, err)

	// Test invalid YAML in file
	invalidFile := filepath.Join(tmpDir, "invalid.yaml")
	err = os.WriteFile(invalidFile, []byte("route: [invalid"), 0644)
	require.NoError(t, err)

	_, err = parser.ParseFile(invalidFile)
	assert.Error(t, err)
}

// TestParser_validateSemantics tests semantic validation
func TestParser_validateSemantics(t *testing.T) {
	parser := NewParser()

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
			name: "invalid label name",
			config: &GroupingConfig{
				Route: &Route{
					Receiver: "test",
					GroupBy:  []string{"alert-name"},
				},
			},
			wantErr: true,
		},
		{
			name: "nested route with invalid label",
			config: &GroupingConfig{
				Route: &Route{
					Receiver: "default",
					GroupBy:  []string{"alertname"},
					Routes: []*Route{
						{
							Receiver: "nested",
							GroupBy:  []string{"invalid label"},
						},
					},
				},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Apply defaults first
			applyRouteDefaults(tt.config.Route)

			err := parser.validateSemantics(tt.config)

			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// TestApplyRouteDefaults tests default application
func TestApplyRouteDefaults(t *testing.T) {
	route := &Route{
		Receiver: "test",
		GroupBy:  []string{"alertname"},
		Routes: []*Route{
			{
				Receiver: "nested",
				GroupBy:  []string{"cluster"},
			},
		},
	}

	applyRouteDefaults(route)

	// Check root route defaults
	assert.NotNil(t, route.GroupWait)
	assert.Equal(t, 30*time.Second, route.GroupWait.Duration)
	assert.NotNil(t, route.GroupInterval)
	assert.Equal(t, 5*time.Minute, route.GroupInterval.Duration)
	assert.NotNil(t, route.RepeatInterval)
	assert.Equal(t, 4*time.Hour, route.RepeatInterval.Duration)

	// Check nested route defaults
	assert.NotNil(t, route.Routes[0].GroupWait)
	assert.Equal(t, 30*time.Second, route.Routes[0].GroupWait.Duration)
}

// TestCalculateRouteDepth tests depth calculation
func TestCalculateRouteDepth(t *testing.T) {
	tests := []struct {
		name  string
		route *Route
		want  int
	}{
		{
			name:  "nil route",
			route: nil,
			want:  1,
		},
		{
			name: "single route",
			route: &Route{
				Receiver: "test",
				GroupBy:  []string{"alertname"},
			},
			want: 1,
		},
		{
			name: "nested 2 levels",
			route: &Route{
				Receiver: "root",
				GroupBy:  []string{"alertname"},
				Routes: []*Route{
					{
						Receiver: "nested",
						GroupBy:  []string{"cluster"},
					},
				},
			},
			want: 2,
		},
		{
			name: "nested 3 levels",
			route: &Route{
				Receiver: "root",
				GroupBy:  []string{"alertname"},
				Routes: []*Route{
					{
						Receiver: "level2",
						GroupBy:  []string{"cluster"},
						Routes: []*Route{
							{
								Receiver: "level3",
								GroupBy:  []string{"namespace"},
							},
						},
					},
				},
			},
			want: 3,
		},
		{
			name: "multiple branches",
			route: &Route{
				Receiver: "root",
				GroupBy:  []string{"alertname"},
				Routes: []*Route{
					{
						Receiver: "branch1",
						GroupBy:  []string{"cluster"},
					},
					{
						Receiver: "branch2",
						GroupBy:  []string{"namespace"},
						Routes: []*Route{
							{
								Receiver: "branch2-nested",
								GroupBy:  []string{"pod"},
							},
						},
					},
				},
			},
			want: 3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := calculateRouteDepth(tt.route)
			assert.Equal(t, tt.want, got)
		})
	}
}

// TestParser_MaxDepthValidation tests max depth validation
func TestParser_MaxDepthValidation(t *testing.T) {
	parser := NewParser()

	// Create a deeply nested config (exceeds maxRouteDepth = 10)
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

	config := &GroupingConfig{Route: route}
	applyRouteDefaults(config.Route)

	err := parser.validateSemantics(config)
	assert.Error(t, err)
	// Check that error message contains depth validation keywords
	assert.Contains(t, err.Error(), "nesting depth")
	assert.Contains(t, err.Error(), "exceeds maximum")
}

// TestConvertValidatorErrors tests error conversion
func TestConvertValidatorErrors(t *testing.T) {
	// This is tested indirectly through Parse tests
	// Direct testing would require creating validator.ValidationErrors
	// which is complex and not necessary for coverage
}

// TestGetValidationMessage tests validation message generation
func TestGetValidationMessage(t *testing.T) {
	// This is tested indirectly through Parse tests
	// Direct testing would require creating validator.FieldError
	// which is complex and not necessary for coverage
}

// TestParser_ComplexNestedConfig tests complex nested configuration
func TestParser_ComplexNestedConfig(t *testing.T) {
	parser := NewParser()

	complexYAML := `
route:
  receiver: "default"
  group_by: ['alertname']
  group_wait: 30s
  group_interval: 5m
  repeat_interval: 4h
  routes:
    - receiver: "team-frontend"
      group_by: ['cluster', 'namespace']
      group_wait: 15s
      match:
        team: frontend
      continue: true
      routes:
        - receiver: "frontend-critical"
          group_by: ['pod']
          match:
            severity: critical
    - receiver: "team-backend"
      group_by: ['service']
      match_re:
        service: "^api-.*"
      routes:
        - receiver: "backend-database"
          group_by: ['database']
          match:
            component: database
`

	config, err := parser.Parse([]byte(complexYAML))
	require.NoError(t, err)
	require.NotNil(t, config)

	// Validate root route
	assert.Equal(t, "default", config.Route.Receiver)
	assert.Equal(t, []string{"alertname"}, config.Route.GroupBy)
	assert.Len(t, config.Route.Routes, 2)

	// Validate first nested route
	frontend := config.Route.Routes[0]
	assert.Equal(t, "team-frontend", frontend.Receiver)
	assert.Equal(t, []string{"cluster", "namespace"}, frontend.GroupBy)
	assert.True(t, frontend.Continue)
	assert.Len(t, frontend.Routes, 1)

	// Validate deeply nested route
	frontendCritical := frontend.Routes[0]
	assert.Equal(t, "frontend-critical", frontendCritical.Receiver)
	assert.Equal(t, []string{"pod"}, frontendCritical.GroupBy)

	// Validate second nested route
	backend := config.Route.Routes[1]
	assert.Equal(t, "team-backend", backend.Receiver)
	assert.Equal(t, map[string]string{"service": "^api-.*"}, backend.MatchRE)
	assert.Len(t, backend.Routes, 1)
}

// TestParser_EdgeCases tests edge cases
func TestParser_EdgeCases(t *testing.T) {
	parser := NewParser()

	tests := []struct {
		name    string
		yaml    string
		wantErr bool
	}{
		{
			name:    "empty input",
			yaml:    "",
			wantErr: true,
		},
		{
			name:    "only whitespace",
			yaml:    "   \n\t  ",
			wantErr: true,
		},
		{
			name: "minimal valid config",
			yaml: `
route:
  receiver: "min"
  group_by: ['a']
`,
			wantErr: false,
		},
		{
			name: "very long label name",
			yaml: `
route:
  receiver: "test"
  group_by: ['` + string(make([]byte, 1000)) + `']
`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parser.Parse([]byte(tt.yaml))
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
