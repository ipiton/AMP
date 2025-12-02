package routing

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/ipiton/AMP/internal/infrastructure/grouping"
)

func TestNewRouteConfigParser(t *testing.T) {
	parser := NewRouteConfigParser()

	require.NotNil(t, parser)
	require.NotNil(t, parser.validator)
}

func TestRouteConfigParser_Parse_ValidMinimal(t *testing.T) {
	yamlConfig := `
route:
  receiver: default
  group_by: [alertname]

receivers:
  - name: default
    webhook_configs:
      - url: https://example.com/webhook
`

	parser := NewRouteConfigParser()
	config, err := parser.Parse([]byte(yamlConfig))

	require.NoError(t, err)
	require.NotNil(t, config)

	assert.Equal(t, "default", config.Route.Receiver)
	assert.Len(t, config.Receivers, 1)
	assert.Equal(t, "default", config.Receivers[0].Name)

	// Verify receiver index built
	receiver, ok := config.GetReceiver("default")
	assert.True(t, ok)
	assert.Equal(t, "default", receiver.Name)

	// Verify defaults applied
	assert.Equal(t, "POST", config.Receivers[0].WebhookConfigs[0].HTTPMethod)
}

func TestRouteConfigParser_Parse_InvalidYAML(t *testing.T) {
	yamlConfig := `
route:
  receiver: default
  invalid yaml syntax here [
`

	parser := NewRouteConfigParser()
	config, err := parser.Parse([]byte(yamlConfig))

	assert.Error(t, err)
	assert.Nil(t, config)
	assert.Contains(t, err.Error(), "YAML parse error")
}

func TestRouteConfigParser_Parse_MissingRoute(t *testing.T) {
	yamlConfig := `
receivers:
  - name: default
    webhook_configs:
      - url: https://example.com/webhook
`

	parser := NewRouteConfigParser()
	config, err := parser.Parse([]byte(yamlConfig))

	assert.Error(t, err)
	assert.Nil(t, config)
	assert.Contains(t, err.Error(), "route is required")
}

func TestRouteConfigParser_Parse_MissingReceivers(t *testing.T) {
	yamlConfig := `
route:
  receiver: default
  group_by: [alertname]
`

	parser := NewRouteConfigParser()
	config, err := parser.Parse([]byte(yamlConfig))

	assert.Error(t, err)
	assert.Nil(t, config)
	assert.Contains(t, err.Error(), "at least one receiver")
}

func TestRouteConfigParser_Parse_UnknownReceiverReference(t *testing.T) {
	yamlConfig := `
route:
  receiver: nonexistent
  group_by: [alertname]

receivers:
  - name: default
    webhook_configs:
      - url: https://example.com/webhook
`

	parser := NewRouteConfigParser()
	config, err := parser.Parse([]byte(yamlConfig))

	assert.Error(t, err)
	assert.Nil(t, config)
	assert.Contains(t, err.Error(), "receiver 'nonexistent' not found")
}

func TestRouteConfigParser_Parse_InvalidReceiverNoConfigs(t *testing.T) {
	yamlConfig := `
route:
  receiver: empty
  group_by: [alertname]

receivers:
  - name: empty
    # No configs defined
`

	parser := NewRouteConfigParser()
	config, err := parser.Parse([]byte(yamlConfig))

	assert.Error(t, err)
	assert.Nil(t, config)
	assert.Contains(t, err.Error(), "at least one config")
}

func TestRouteConfigParser_Parse_WithGlobal(t *testing.T) {
	yamlConfig := `
global:
  resolve_timeout: 10m
  http_config:
    connect_timeout: 30s

route:
  receiver: default
  group_by: [alertname]

receivers:
  - name: default
    webhook_configs:
      - url: https://example.com/webhook
`

	parser := NewRouteConfigParser()
	config, err := parser.Parse([]byte(yamlConfig))

	require.NoError(t, err)
	require.NotNil(t, config)
	require.NotNil(t, config.Global)
	require.NotNil(t, config.Global.ResolveTimeout)

	// Note: Duration parsing depends on implementation
	// Just verify it's set
	assert.NotNil(t, config.Global.HTTPConfig)
}

func TestRouteConfigParser_Parse_NestedRoutes(t *testing.T) {
	yamlConfig := `
route:
  receiver: default
  group_by: [alertname]
  routes:
    - receiver: pagerduty
      match:
        severity: critical
    - receiver: slack
      match:
        severity: warning

receivers:
  - name: default
    webhook_configs:
      - url: https://example.com/webhook
  - name: pagerduty
    pagerduty_configs:
      - routing_key: "12345678901234567890123456789012"
  - name: slack
    slack_configs:
      - api_url: https://hooks.slack.com/xxx
        channel: "#alerts"
`

	parser := NewRouteConfigParser()
	config, err := parser.Parse([]byte(yamlConfig))

	require.NoError(t, err)
	require.NotNil(t, config)
	assert.Len(t, config.Route.Routes, 2)

	// Verify child routes
	assert.Equal(t, "pagerduty", config.Route.Routes[0].Receiver)
	assert.Equal(t, "slack", config.Route.Routes[1].Receiver)

	// Verify all receivers in index
	_, ok := config.GetReceiver("default")
	assert.True(t, ok)
	_, ok = config.GetReceiver("pagerduty")
	assert.True(t, ok)
	_, ok = config.GetReceiver("slack")
	assert.True(t, ok)
}

func TestRouteConfigParser_Parse_RegexCompilation(t *testing.T) {
	yamlConfig := `
route:
  receiver: default
  group_by: [alertname]
  match_re:
    service: "^(api|web).*"
    environment: "prod.*"

receivers:
  - name: default
    webhook_configs:
      - url: https://example.com/webhook
`

	parser := NewRouteConfigParser()
	config, err := parser.Parse([]byte(yamlConfig))

	require.NoError(t, err)
	require.NotNil(t, config)

	// Verify regex compiled
	regex, ok := config.GetCompiledRegex(config.Route, "service")
	assert.True(t, ok)
	assert.NotNil(t, regex)

	// Test regex
	assert.True(t, regex.MatchString("api-service"))
	assert.True(t, regex.MatchString("web-frontend"))
	assert.False(t, regex.MatchString("database"))
}

func TestRouteConfigParser_Parse_InvalidRegex(t *testing.T) {
	yamlConfig := `
route:
  receiver: default
  group_by: [alertname]
  match_re:
    service: "[invalid regex"

receivers:
  - name: default
    webhook_configs:
      - url: https://example.com/webhook
`

	parser := NewRouteConfigParser()
	config, err := parser.Parse([]byte(yamlConfig))

	assert.Error(t, err)
	assert.Nil(t, config)
	assert.Contains(t, err.Error(), "invalid regex")
}

func TestRouteConfigParser_ParseFile_Success(t *testing.T) {
	// Create temp config file
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yml")

	yamlConfig := `
route:
  receiver: default
  group_by: [alertname]

receivers:
  - name: default
    webhook_configs:
      - url: https://example.com/webhook
`

	err := os.WriteFile(configPath, []byte(yamlConfig), 0644)
	require.NoError(t, err)

	// Parse file
	parser := NewRouteConfigParser()
	config, err := parser.ParseFile(configPath)

	require.NoError(t, err)
	require.NotNil(t, config)
	assert.Equal(t, configPath, config.SourceFile)
}

func TestRouteConfigParser_ParseFile_NotFound(t *testing.T) {
	parser := NewRouteConfigParser()
	config, err := parser.ParseFile("/nonexistent/config.yml")

	assert.Error(t, err)
	assert.Nil(t, config)
	assert.Contains(t, err.Error(), "failed to stat file")
}

func TestRouteConfigParser_ParseFile_TooBig(t *testing.T) {
	// Create temp file larger than MaxConfigSize
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "huge.yml")

	// Create a file just over 10 MB
	largeContent := strings.Repeat("x", MaxConfigSize+1)
	err := os.WriteFile(configPath, []byte(largeContent), 0644)
	require.NoError(t, err)

	// Try to parse
	parser := NewRouteConfigParser()
	config, err := parser.ParseFile(configPath)

	assert.Error(t, err)
	assert.Nil(t, config)
	assert.Contains(t, err.Error(), "too large")
}

func TestRouteConfigParser_ParseString_Success(t *testing.T) {
	yamlConfig := `
route:
  receiver: default
  group_by: [alertname]

receivers:
  - name: default
    webhook_configs:
      - url: https://example.com/webhook
`

	parser := NewRouteConfigParser()
	config, err := parser.ParseString(yamlConfig)

	require.NoError(t, err)
	require.NotNil(t, config)
	assert.Equal(t, "default", config.Route.Receiver)
}

func TestRouteConfigParser_ValidateConfig(t *testing.T) {
	config := &RouteConfig{
		Route: &grouping.Route{
			Receiver: "default",
		},
		Receivers: []*Receiver{
			{
				Name: "default",
				WebhookConfigs: []*WebhookConfig{
					{URL: "https://example.com"},
				},
			},
		},
	}

	parser := NewRouteConfigParser()
	err := parser.ValidateConfig(config)

	assert.NoError(t, err)
}

func TestRouteConfigParser_ExcessiveNesting(t *testing.T) {
	// Build deeply nested route tree (> MaxRouteDepth)
	var buildDeepRoute func(depth int) *grouping.Route
	buildDeepRoute = func(depth int) *grouping.Route {
		route := &grouping.Route{
			Receiver: "default",
		}
		if depth > 0 {
			route.Routes = []*grouping.Route{
				buildDeepRoute(depth - 1),
			}
		}
		return route
	}

	config := &RouteConfig{
		Route: buildDeepRoute(MaxRouteDepth + 1),
		Receivers: []*Receiver{
			{
				Name: "default",
				WebhookConfigs: []*WebhookConfig{
					{URL: "https://example.com"},
				},
			},
		},
	}

	parser := NewRouteConfigParser()
	err := parser.ValidateConfig(config)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "nesting too deep")
}

func TestValidateAlphanumHyphen(t *testing.T) {
	parser := NewRouteConfigParser()

	type TestReceiver struct {
		Name string `validate:"alphanum_hyphen"`
	}

	tests := []struct {
		name    string
		value   string
		wantErr bool
	}{
		{"valid lowercase", "webhook", false},
		{"valid with hyphen", "webhook-prod", false},
		{"valid with underscore", "webhook_prod", false},
		{"valid alphanumeric", "webhook123", false},
		{"invalid space", "webhook prod", true},
		{"invalid dot", "webhook.prod", true},
		{"invalid slash", "webhook/prod", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := TestReceiver{Name: tt.value}
			err := parser.validator.Struct(r)

			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
