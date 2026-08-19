package routing

import (
	"testing"

	"github.com/ipiton/AMP/internal/infrastructure/grouping"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRouteConfig_GetReceiver(t *testing.T) {
	tests := []struct {
		name         string
		config       *RouteConfig
		receiverName string
		wantFound    bool
	}{
		{
			name: "existing receiver",
			config: &RouteConfig{
				Receivers: []*Receiver{
					{Name: "webhook1"},
					{Name: "pagerduty1"},
				},
				ReceiverIndex: map[string]*Receiver{
					"webhook1":   {Name: "webhook1"},
					"pagerduty1": {Name: "pagerduty1"},
				},
			},
			receiverName: "webhook1",
			wantFound:    true,
		},
		{
			name: "non-existing receiver",
			config: &RouteConfig{
				Receivers: []*Receiver{
					{Name: "webhook1"},
				},
				ReceiverIndex: map[string]*Receiver{
					"webhook1": {Name: "webhook1"},
				},
			},
			receiverName: "slack1",
			wantFound:    false,
		},
		{
			name: "nil receiver index",
			config: &RouteConfig{
				Receivers: []*Receiver{
					{Name: "webhook1"},
				},
				ReceiverIndex: nil,
			},
			receiverName: "webhook1",
			wantFound:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			receiver, found := tt.config.GetReceiver(tt.receiverName)

			assert.Equal(t, tt.wantFound, found)
			if tt.wantFound {
				require.NotNil(t, receiver)
				assert.Equal(t, tt.receiverName, receiver.Name)
			} else {
				assert.Nil(t, receiver)
			}
		})
	}
}

func TestRouteConfig_ListReceivers(t *testing.T) {
	receivers := []*Receiver{
		{Name: "webhook1"},
		{Name: "pagerduty1"},
		{Name: "slack1"},
	}

	config := &RouteConfig{
		Receivers: receivers,
	}

	result := config.ListReceivers()

	assert.Equal(t, receivers, result)
	assert.Len(t, result, 3)
}

func TestRouteConfig_Clone(t *testing.T) {
	original := &RouteConfig{
		Global: &GlobalConfig{
			SMTPFrom: "alerts@example.com",
		},
		Route: &grouping.Route{
			Receiver: "default",
			GroupBy:  []string{"alertname"},
		},
		Receivers: []*Receiver{
			{
				Name: "webhook1",
				WebhookConfigs: []*WebhookConfig{
					{URL: "https://example.com/webhook"},
				},
			},
		},
		Version:    1,
		SourceFile: "/etc/config.yml",
	}

	// Build receiver index
	original.ReceiverIndex = make(map[string]*Receiver)
	for _, r := range original.Receivers {
		original.ReceiverIndex[r.Name] = r
	}

	// Clone
	cloned := original.Clone()

	// Verify deep copy
	require.NotNil(t, cloned)
	assert.Equal(t, original.Version, cloned.Version)
	assert.Equal(t, original.SourceFile, cloned.SourceFile)
	assert.Equal(t, original.Route.Receiver, cloned.Route.Receiver)
	assert.Len(t, cloned.Receivers, 1)

	// Verify it's a deep copy (not same pointer)
	assert.NotSame(t, original.Route, cloned.Route)
	assert.NotSame(t, original.Receivers[0], cloned.Receivers[0])

	// Modify clone shouldn't affect original
	cloned.Receivers[0].Name = "modified"
	assert.Equal(t, "webhook1", original.Receivers[0].Name)
	assert.Equal(t, "modified", cloned.Receivers[0].Name)
}

func TestRouteConfig_String(t *testing.T) {
	config := &RouteConfig{
		Route: &grouping.Route{
			Receiver: "default",
			Routes: []*grouping.Route{
				{Receiver: "child1"},
				{Receiver: "child2"},
			},
		},
		Receivers: []*Receiver{
			{Name: "default"},
			{Name: "child1"},
			{Name: "child2"},
		},
		Version:    3,
		SourceFile: "/etc/alertmanager/config.yml",
	}

	config.ReceiverIndex = make(map[string]*Receiver)
	for _, r := range config.Receivers {
		config.ReceiverIndex[r.Name] = r
	}

	result := config.String()

	assert.Contains(t, result, "version=3")
	assert.Contains(t, result, "routes=3")
	assert.Contains(t, result, "receivers=3")
	assert.Contains(t, result, "source=/etc/alertmanager/config.yml")
}

func TestReceiver_Validate(t *testing.T) {
	tests := []struct {
		name     string
		receiver *Receiver
		wantErr  bool
	}{
		{
			name: "valid receiver with webhook",
			receiver: &Receiver{
				Name: "webhook1",
				WebhookConfigs: []*WebhookConfig{
					{URL: "https://example.com"},
				},
			},
			wantErr: false,
		},
		{
			name: "valid receiver with pagerduty",
			receiver: &Receiver{
				Name: "pd1",
				PagerDutyConfigs: []*PagerDutyConfig{
					{RoutingKey: "12345678901234567890123456789012"},
				},
			},
			wantErr: false,
		},
		{
			// Slice-1 review finding I1: a receiver with zero integrations is
			// upstream Alertmanager's BLACKHOLE receiver (`- name: 'null'`),
			// which is legal there and must therefore load here.
			name: "receiver with no configs is a valid blackhole receiver",
			receiver: &Receiver{
				Name: "null",
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.receiver.Validate()

			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestReceiver_GetConfigCount(t *testing.T) {
	receiver := &Receiver{
		Name: "multi",
		WebhookConfigs: []*WebhookConfig{
			{URL: "https://example1.com"},
			{URL: "https://example2.com"},
		},
		PagerDutyConfigs: []*PagerDutyConfig{
			{RoutingKey: "12345678901234567890123456789012"},
		},
		SlackConfigs: []*SlackConfig{
			{APIURL: "https://hooks.slack.com/xxx"},
			{APIURL: "https://hooks.slack.com/yyy"},
			{APIURL: "https://hooks.slack.com/zzz"},
		},
	}

	count := receiver.GetConfigCount()
	assert.Equal(t, 6, count) // 2 + 1 + 3
}

func TestReceiver_Clone(t *testing.T) {
	original := &Receiver{
		Name: "webhook1",
		WebhookConfigs: []*WebhookConfig{
			{
				URL:        "https://example.com/webhook",
				HTTPMethod: "POST",
			},
		},
		Referenced: true,
	}

	cloned := original.Clone()

	require.NotNil(t, cloned)
	assert.Equal(t, original.Name, cloned.Name)
	assert.Equal(t, original.Referenced, cloned.Referenced)
	assert.Len(t, cloned.WebhookConfigs, 1)

	// Verify deep copy
	assert.NotSame(t, original.WebhookConfigs[0], cloned.WebhookConfigs[0])

	// Modify clone shouldn't affect original
	cloned.WebhookConfigs[0].URL = "https://modified.com"
	assert.Equal(t, "https://example.com/webhook", original.WebhookConfigs[0].URL)
}

func TestReceiver_Sanitize(t *testing.T) {
	receiver := &Receiver{
		Name: "webhook1",
		WebhookConfigs: []*WebhookConfig{
			{
				URL: "https://webhook.site/xxx?token=secret123",
				HTTPHeaders: map[string]string{
					"Authorization": "Bearer secret_token",
					"Content-Type":  "application/json",
				},
			},
		},
	}

	sanitized := receiver.Sanitize()

	require.NotNil(t, sanitized)
	require.Len(t, sanitized.WebhookConfigs, 1)

	// URL should be sanitized
	assert.NotContains(t, sanitized.WebhookConfigs[0].URL, "secret123")
	assert.Contains(t, sanitized.WebhookConfigs[0].URL, "[REDACTED]")

	// Authorization header should be redacted
	assert.Equal(t, "[REDACTED]", sanitized.WebhookConfigs[0].HTTPHeaders["Authorization"])

	// Non-sensitive header should remain
	assert.Equal(t, "application/json", sanitized.WebhookConfigs[0].HTTPHeaders["Content-Type"])

	// Original should be unchanged
	assert.Contains(t, receiver.WebhookConfigs[0].URL, "secret123")
	assert.Equal(t, "Bearer secret_token", receiver.WebhookConfigs[0].HTTPHeaders["Authorization"])
}

func TestWebhookConfig_Defaults(t *testing.T) {
	config := &WebhookConfig{
		URL: "https://example.com/webhook",
		// HTTPMethod and SendResolved not set
	}

	config.Defaults()

	assert.Equal(t, "POST", config.HTTPMethod)
	require.NotNil(t, config.SendResolved)
	assert.True(t, *config.SendResolved)
}

func TestPagerDutyConfig_Defaults(t *testing.T) {
	config := &PagerDutyConfig{
		RoutingKey: "12345678901234567890123456789012",
		// URL and Severity not set
	}

	config.Defaults()

	assert.Equal(t, "https://events.pagerduty.com/v2/enqueue", config.URL)
	assert.Equal(t, "error", config.Severity)
	require.NotNil(t, config.SendResolved)
	assert.True(t, *config.SendResolved)
}

func TestSlackConfig_Defaults(t *testing.T) {
	config := &SlackConfig{
		APIURL: "https://hooks.slack.com/xxx",
		// SendResolved not set
	}

	config.Defaults()

	require.NotNil(t, config.SendResolved)
	assert.True(t, *config.SendResolved)
}

func TestTelegramConfig_Defaults(t *testing.T) {
	config := &TelegramConfig{
		BotToken: "test-bot-token-fixture",
		ChatID:   "-1001234567890",
		// APIURL, ParseMode, SendResolved not set
	}

	config.Defaults()

	assert.Equal(t, "https://api.telegram.org", config.APIURL)
	assert.Equal(t, "HTML", config.ParseMode)
	require.NotNil(t, config.SendResolved)
	assert.True(t, *config.SendResolved)
}

func TestTelegramConfig_Defaults_PreservesExplicitValues(t *testing.T) {
	sendResolved := false
	chatID := "channelfixture"
	config := &TelegramConfig{
		BotToken:     "test-bot-token-fixture",
		ChatID:       chatID,
		APIURL:       "https://custom.telegram.example",
		ParseMode:    "MarkdownV2",
		SendResolved: &sendResolved,
	}

	config.Defaults()

	assert.Equal(t, "https://custom.telegram.example", config.APIURL)
	assert.Equal(t, "MarkdownV2", config.ParseMode)
	require.NotNil(t, config.SendResolved)
	assert.False(t, *config.SendResolved)
}

func TestTelegramConfig_Clone(t *testing.T) {
	sendResolved := true
	original := &TelegramConfig{
		BotToken:             "test-bot-token-fixture",
		ChatID:               "-1001234567890",
		MessageThreadID:      42,
		ParseMode:            "HTML",
		APIURL:               "https://api.telegram.org",
		Message:              "{{ .GroupLabels.alertname }}",
		DisableNotifications: true,
		SendResolved:         &sendResolved,
	}

	clone := original.Clone()

	require.NotNil(t, clone)
	assert.Equal(t, original.BotToken, clone.BotToken)
	assert.Equal(t, original.ChatID, clone.ChatID)
	assert.Equal(t, original.MessageThreadID, clone.MessageThreadID)
	require.NotNil(t, clone.SendResolved)
	assert.Equal(t, *original.SendResolved, *clone.SendResolved)

	// Verify deep copy - pointer independence
	assert.NotSame(t, original.SendResolved, clone.SendResolved)
	*clone.SendResolved = false
	assert.True(t, *original.SendResolved)
}

func TestTelegramConfig_Sanitize(t *testing.T) {
	config := &TelegramConfig{
		BotToken: "test-bot-token-fixture-value",
		ChatID:   "-1001234567890",
	}

	sanitized := config.Sanitize()

	require.NotNil(t, sanitized)
	assert.Equal(t, "[REDACTED]", sanitized.BotToken)
	// ChatID is not a secret, must remain intact
	assert.Equal(t, "-1001234567890", sanitized.ChatID)

	// Original must be unchanged
	assert.Equal(t, "test-bot-token-fixture-value", config.BotToken)
}

func TestReceiver_TelegramConfigs_Validate(t *testing.T) {
	receiver := &Receiver{
		Name: "telegram-oncall",
		TelegramConfigs: []*TelegramConfig{
			{BotToken: "test-bot-token-fixture", ChatID: "-1001234567890"},
		},
	}

	assert.NoError(t, receiver.Validate())
	assert.Equal(t, 1, receiver.GetConfigCount())
}

func TestReceiver_TelegramConfigs_Clone_Sanitize(t *testing.T) {
	fixtureToken := "test-bot-token-fixture-value"
	receiver := &Receiver{
		Name: "telegram-oncall",
		TelegramConfigs: []*TelegramConfig{
			{BotToken: fixtureToken, ChatID: "channelfixture"},
		},
	}

	cloned := receiver.Clone()
	require.Len(t, cloned.TelegramConfigs, 1)
	assert.Equal(t, fixtureToken, cloned.TelegramConfigs[0].BotToken)
	assert.NotSame(t, receiver.TelegramConfigs[0], cloned.TelegramConfigs[0])

	sanitized := receiver.Sanitize()
	require.Len(t, sanitized.TelegramConfigs, 1)
	assert.Equal(t, "[REDACTED]", sanitized.TelegramConfigs[0].BotToken)
	// Original untouched
	assert.Equal(t, fixtureToken, receiver.TelegramConfigs[0].BotToken)
}
