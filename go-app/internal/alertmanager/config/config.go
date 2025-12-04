// Package config provides Alertmanager configuration types
package config

import "time"

// AlertmanagerConfig represents the top-level Alertmanager configuration
type AlertmanagerConfig struct {
	Global       *GlobalConfig   `yaml:"global,omitempty" json:"global,omitempty"`
	Route        *Route          `yaml:"route,omitempty" json:"route,omitempty"`
	InhibitRules []*InhibitRule  `yaml:"inhibit_rules,omitempty" json:"inhibit_rules,omitempty"`
	Receivers    []*Receiver     `yaml:"receivers,omitempty" json:"receivers,omitempty"`
	Templates    []string        `yaml:"templates,omitempty" json:"templates,omitempty"`
}

// GlobalConfig contains global configuration options
type GlobalConfig struct {
	ResolveTimeout   time.Duration `yaml:"resolve_timeout,omitempty" json:"resolve_timeout,omitempty"`
	HTTPConfig       *HTTPConfig   `yaml:"http_config,omitempty" json:"http_config,omitempty"`
	SMTPFrom         string        `yaml:"smtp_from,omitempty" json:"smtp_from,omitempty"`
	SMTPSmarthost    string        `yaml:"smtp_smarthost,omitempty" json:"smtp_smarthost,omitempty"`
	SMTPAuthUsername string        `yaml:"smtp_auth_username,omitempty" json:"smtp_auth_username,omitempty"`
	SMTPAuthPassword string        `yaml:"smtp_auth_password,omitempty" json:"smtp_auth_password,omitempty"`
	SMTPRequireTLS   bool          `yaml:"smtp_require_tls,omitempty" json:"smtp_require_tls,omitempty"`
	SlackAPIURL      string        `yaml:"slack_api_url,omitempty" json:"slack_api_url,omitempty"`
	PagerdutyURL     string        `yaml:"pagerduty_url,omitempty" json:"pagerduty_url,omitempty"`
	OpsGenieAPIURL   string        `yaml:"opsgenie_api_url,omitempty" json:"opsgenie_api_url,omitempty"`
	WeChatAPIURL     string        `yaml:"wechat_api_url,omitempty" json:"wechat_api_url,omitempty"`
	VictorOpsAPIURL  string        `yaml:"victorops_api_url,omitempty" json:"victorops_api_url,omitempty"`
}

// HTTPConfig contains HTTP client configuration
type HTTPConfig struct {
	BasicAuth       *BasicAuth `yaml:"basic_auth,omitempty" json:"basic_auth,omitempty"`
	BearerToken     string     `yaml:"bearer_token,omitempty" json:"bearer_token,omitempty"`
	BearerTokenFile string     `yaml:"bearer_token_file,omitempty" json:"bearer_token_file,omitempty"`
	ProxyURL        string     `yaml:"proxy_url,omitempty" json:"proxy_url,omitempty"`
	TLSConfig       *TLSConfig `yaml:"tls_config,omitempty" json:"tls_config,omitempty"`
}

// BasicAuth contains basic authentication configuration
type BasicAuth struct {
	Username     string `yaml:"username,omitempty" json:"username,omitempty"`
	Password     string `yaml:"password,omitempty" json:"password,omitempty"`
	PasswordFile string `yaml:"password_file,omitempty" json:"password_file,omitempty"`
}

// TLSConfig contains TLS configuration
type TLSConfig struct {
	CAFile             string `yaml:"ca_file,omitempty" json:"ca_file,omitempty"`
	CertFile           string `yaml:"cert_file,omitempty" json:"cert_file,omitempty"`
	KeyFile            string `yaml:"key_file,omitempty" json:"key_file,omitempty"`
	ServerName         string `yaml:"server_name,omitempty" json:"server_name,omitempty"`
	InsecureSkipVerify bool   `yaml:"insecure_skip_verify,omitempty" json:"insecure_skip_verify,omitempty"`
}

// Route defines a routing tree node
type Route struct {
	Receiver       string            `yaml:"receiver,omitempty" json:"receiver,omitempty"`
	GroupBy        []string          `yaml:"group_by,omitempty" json:"group_by,omitempty"`
	GroupWait      time.Duration     `yaml:"group_wait,omitempty" json:"group_wait,omitempty"`
	GroupInterval  time.Duration     `yaml:"group_interval,omitempty" json:"group_interval,omitempty"`
	RepeatInterval time.Duration     `yaml:"repeat_interval,omitempty" json:"repeat_interval,omitempty"`
	Match          map[string]string `yaml:"match,omitempty" json:"match,omitempty"`
	MatchRE        map[string]string `yaml:"match_re,omitempty" json:"match_re,omitempty"`
	Continue       bool              `yaml:"continue,omitempty" json:"continue,omitempty"`
	Routes         []*Route          `yaml:"routes,omitempty" json:"routes,omitempty"`
}

// InhibitRule defines an inhibition rule
type InhibitRule struct {
	SourceMatch      map[string]string `yaml:"source_match,omitempty" json:"source_match,omitempty"`
	SourceMatchRE    map[string]string `yaml:"source_match_re,omitempty" json:"source_match_re,omitempty"`
	TargetMatch      map[string]string `yaml:"target_match,omitempty" json:"target_match,omitempty"`
	TargetMatchRE    map[string]string `yaml:"target_match_re,omitempty" json:"target_match_re,omitempty"`
	Equal            []string          `yaml:"equal,omitempty" json:"equal,omitempty"`
}

// Receiver defines a notification receiver
type Receiver struct {
	Name             string                 `yaml:"name" json:"name"`
	EmailConfigs     []*EmailConfig         `yaml:"email_configs,omitempty" json:"email_configs,omitempty"`
	PagerdutyConfigs []*PagerdutyConfig     `yaml:"pagerduty_configs,omitempty" json:"pagerduty_configs,omitempty"`
	SlackConfigs     []*SlackConfig         `yaml:"slack_configs,omitempty" json:"slack_configs,omitempty"`
	WebhookConfigs   []*WebhookConfig       `yaml:"webhook_configs,omitempty" json:"webhook_configs,omitempty"`
	OpsGenieConfigs  []*OpsGenieConfig      `yaml:"opsgenie_configs,omitempty" json:"opsgenie_configs,omitempty"`
	WeChatConfigs    []*WeChatConfig        `yaml:"wechat_configs,omitempty" json:"wechat_configs,omitempty"`
	VictorOpsConfigs []*VictorOpsConfig     `yaml:"victorops_configs,omitempty" json:"victorops_configs,omitempty"`
}

// EmailConfig defines email notification configuration
type EmailConfig struct {
	To           string            `yaml:"to,omitempty" json:"to,omitempty"`
	From         string            `yaml:"from,omitempty" json:"from,omitempty"`
	Smarthost    string            `yaml:"smarthost,omitempty" json:"smarthost,omitempty"`
	Headers      map[string]string `yaml:"headers,omitempty" json:"headers,omitempty"`
	HTML         string            `yaml:"html,omitempty" json:"html,omitempty"`
	Text         string            `yaml:"text,omitempty" json:"text,omitempty"`
	RequireTLS   *bool             `yaml:"require_tls,omitempty" json:"require_tls,omitempty"`
}

// PagerdutyConfig defines PagerDuty notification configuration
type PagerdutyConfig struct {
	ServiceKey  string            `yaml:"service_key,omitempty" json:"service_key,omitempty"`
	RoutingKey  string            `yaml:"routing_key,omitempty" json:"routing_key,omitempty"`
	URL         string            `yaml:"url,omitempty" json:"url,omitempty"`
	Client      string            `yaml:"client,omitempty" json:"client,omitempty"`
	ClientURL   string            `yaml:"client_url,omitempty" json:"client_url,omitempty"`
	Description string            `yaml:"description,omitempty" json:"description,omitempty"`
	Details     map[string]string `yaml:"details,omitempty" json:"details,omitempty"`
}

// SlackConfig defines Slack notification configuration
type SlackConfig struct {
	APIURL      string            `yaml:"api_url,omitempty" json:"api_url,omitempty"`
	Channel     string            `yaml:"channel,omitempty" json:"channel,omitempty"`
	Username    string            `yaml:"username,omitempty" json:"username,omitempty"`
	Color       string            `yaml:"color,omitempty" json:"color,omitempty"`
	Title       string            `yaml:"title,omitempty" json:"title,omitempty"`
	TitleLink   string            `yaml:"title_link,omitempty" json:"title_link,omitempty"`
	Pretext     string            `yaml:"pretext,omitempty" json:"pretext,omitempty"`
	Text        string            `yaml:"text,omitempty" json:"text,omitempty"`
	Fields      []SlackField      `yaml:"fields,omitempty" json:"fields,omitempty"`
	ShortFields bool              `yaml:"short_fields,omitempty" json:"short_fields,omitempty"`
	Footer      string            `yaml:"footer,omitempty" json:"footer,omitempty"`
	Fallback    string            `yaml:"fallback,omitempty" json:"fallback,omitempty"`
	CallbackID  string            `yaml:"callback_id,omitempty" json:"callback_id,omitempty"`
	IconEmoji   string            `yaml:"icon_emoji,omitempty" json:"icon_emoji,omitempty"`
	IconURL     string            `yaml:"icon_url,omitempty" json:"icon_url,omitempty"`
	ImageURL    string            `yaml:"image_url,omitempty" json:"image_url,omitempty"`
	ThumbURL    string            `yaml:"thumb_url,omitempty" json:"thumb_url,omitempty"`
	LinkNames   bool              `yaml:"link_names,omitempty" json:"link_names,omitempty"`
	Actions     []SlackAction     `yaml:"actions,omitempty" json:"actions,omitempty"`
}

// SlackField defines a Slack message field
type SlackField struct {
	Title string `yaml:"title,omitempty" json:"title,omitempty"`
	Value string `yaml:"value,omitempty" json:"value,omitempty"`
	Short *bool  `yaml:"short,omitempty" json:"short,omitempty"`
}

// SlackAction defines a Slack message action
type SlackAction struct {
	Type  string `yaml:"type,omitempty" json:"type,omitempty"`
	Text  string `yaml:"text,omitempty" json:"text,omitempty"`
	URL   string `yaml:"url,omitempty" json:"url,omitempty"`
	Style string `yaml:"style,omitempty" json:"style,omitempty"`
}

// WebhookConfig defines webhook notification configuration
type WebhookConfig struct {
	URL        string      `yaml:"url,omitempty" json:"url,omitempty"`
	HTTPConfig *HTTPConfig `yaml:"http_config,omitempty" json:"http_config,omitempty"`
}

// OpsGenieConfig defines OpsGenie notification configuration
type OpsGenieConfig struct {
	APIKey      string            `yaml:"api_key,omitempty" json:"api_key,omitempty"`
	APIURL      string            `yaml:"api_url,omitempty" json:"api_url,omitempty"`
	Message     string            `yaml:"message,omitempty" json:"message,omitempty"`
	Description string            `yaml:"description,omitempty" json:"description,omitempty"`
	Source      string            `yaml:"source,omitempty" json:"source,omitempty"`
	Details     map[string]string `yaml:"details,omitempty" json:"details,omitempty"`
	Responders  []Responder       `yaml:"responders,omitempty" json:"responders,omitempty"`
	Tags        []string          `yaml:"tags,omitempty" json:"tags,omitempty"`
	Note        string            `yaml:"note,omitempty" json:"note,omitempty"`
	Priority    string            `yaml:"priority,omitempty" json:"priority,omitempty"`
}

// Responder defines an OpsGenie responder
type Responder struct {
	ID       string `yaml:"id,omitempty" json:"id,omitempty"`
	Name     string `yaml:"name,omitempty" json:"name,omitempty"`
	Username string `yaml:"username,omitempty" json:"username,omitempty"`
	Type     string `yaml:"type,omitempty" json:"type,omitempty"`
}

// WeChatConfig defines WeChat notification configuration
type WeChatConfig struct {
	APIURL    string `yaml:"api_url,omitempty" json:"api_url,omitempty"`
	CorpID    string `yaml:"corp_id,omitempty" json:"corp_id,omitempty"`
	AgentID   string `yaml:"agent_id,omitempty" json:"agent_id,omitempty"`
	APISecret string `yaml:"api_secret,omitempty" json:"api_secret,omitempty"`
	ToUser    string `yaml:"to_user,omitempty" json:"to_user,omitempty"`
	ToParty   string `yaml:"to_party,omitempty" json:"to_party,omitempty"`
	ToTag     string `yaml:"to_tag,omitempty" json:"to_tag,omitempty"`
	Message   string `yaml:"message,omitempty" json:"message,omitempty"`
}

// VictorOpsConfig defines VictorOps notification configuration
type VictorOpsConfig struct {
	APIURL          string            `yaml:"api_url,omitempty" json:"api_url,omitempty"`
	APIKey          string            `yaml:"api_key,omitempty" json:"api_key,omitempty"`
	RoutingKey      string            `yaml:"routing_key,omitempty" json:"routing_key,omitempty"`
	MessageType     string            `yaml:"message_type,omitempty" json:"message_type,omitempty"`
	EntityDisplayName string          `yaml:"entity_display_name,omitempty" json:"entity_display_name,omitempty"`
	StateMessage    string            `yaml:"state_message,omitempty" json:"state_message,omitempty"`
	MonitoringTool  string            `yaml:"monitoring_tool,omitempty" json:"monitoring_tool,omitempty"`
	CustomFields    map[string]string `yaml:"custom_fields,omitempty" json:"custom_fields,omitempty"`
}
