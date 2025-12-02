package publishing

import "github.com/ipiton/AMP/internal/core"

// TargetConfig extends core.PublishingTarget with discovery-specific fields
type TargetConfig struct {
	*core.PublishingTarget

	// Discovery metadata
	SecretName      string
	SecretNamespace string
	LastUpdated     string
}

// TargetType represents the type of publishing target
type TargetType string

const (
	TargetTypeRootly     TargetType = "rootly"
	TargetTypePagerDuty  TargetType = "pagerduty"
	TargetTypeSlack      TargetType = "slack"
	TargetTypeWebhook    TargetType = "webhook"
	TargetTypeAlertmanager TargetType = "alertmanager"
)

// ParseTargetType converts string to TargetType
func ParseTargetType(s string) TargetType {
	switch s {
	case "rootly":
		return TargetTypeRootly
	case "pagerduty", "pager_duty":
		return TargetTypePagerDuty
	case "slack":
		return TargetTypeSlack
	case "webhook":
		return TargetTypeWebhook
	case "alertmanager":
		return TargetTypeAlertmanager
	default:
		return TargetTypeWebhook // Default to generic webhook
	}
}
