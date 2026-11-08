package upstreamtargets

import (
	"cmp"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/prometheus/alertmanager/config"
	"github.com/prometheus/alertmanager/notify/pagerduty"
	"github.com/prometheus/alertmanager/notify/webhook"
	commoncfg "github.com/prometheus/common/config"
	"gopkg.in/yaml.v2"

	"github.com/ipiton/AMP/internal/core"
)

// Skip reasons, the values of the "reason" log field.
const (
	reasonInvalid         = "invalid"
	reasonRootlyREST      = "rootly_rest_api"
	reasonNoReceiver      = "no_receiver"
	reasonUnsupportedType = "unsupported_type"
)

// skipError says why a target is left out of the configuration.
type skipError struct {
	reason string
	err    error
}

func (e *skipError) Error() string { return e.reason + ": " + e.err.Error() }

func skip(reason string, err error) error { return &skipError{reason: reason, err: err} }

// integration is the one upstream integration built from a target; exactly
// one of webhook, slack and pagerduty is set.
type integration struct {
	webhook   *webhook.WebhookConfig
	slack     *config.SlackConfig
	pagerduty *pagerduty.PagerdutyConfig
	// dropped names the target's headers that upstream reserves
	// (commoncfg.ReservedHeaders) and the integration does not send.
	dropped []string
}

// appendTo adds the integration after the receiver's own. Clip forces a new
// backing array, so the receiver copied from the coordinator's config never
// shares one with it.
func (in integration) appendTo(rc *config.Receiver) {
	switch {
	case in.webhook != nil:
		rc.WebhookConfigs = append(slices.Clip(rc.WebhookConfigs), in.webhook)
	case in.slack != nil:
		rc.SlackConfigs = append(slices.Clip(rc.SlackConfigs), in.slack)
	case in.pagerduty != nil:
		rc.PagerdutyConfigs = append(slices.Clip(rc.PagerdutyConfigs), in.pagerduty)
	}
}

// Template keys a K8s target may carry (core.PublishingTarget.Templates); they
// are named as the upstream fields they set. Others are ignored, as the
// fixed set of the old core's publishers ignores them.
var (
	slackTemplateKeys     = []string{"title", "text", "color", "channel", "pretext", "username", "title_link", "icon_emoji", "icon_url"}
	pagerdutyTemplateKeys = []string{"description", "severity", "client", "client_url"}
)

const pagerdutyDetailsPrefix = "details."

// PagerDuty Events API v2, as the old core's client addresses it
// (infrastructure/publishing/pagerduty_client.go): a target URL is a base
// URL, with or without an API path, and events go to its /v2/enqueue.
const pagerdutyEnqueuePath = "/v2/enqueue"

var pagerdutyPathSuffixes = []string{"/v2/change/enqueue", pagerdutyEnqueuePath, "/v2/events"}

// rootlyRESTHosts serve Rootly's REST API, which the Alertmanager webhook
// payload does not fit; the Helm chart's rootly.webhookUrl used to point
// there.
var rootlyRESTHosts = []string{"api.rootly.com", "api.rootly.io"}

// convert translates a K8s target into an upstream integration. The YAML
// fragment goes through the upstream type's UnmarshalYAML, which applies its
// defaults and checks; the global fallbacks are then filled in as
// config.Config.UnmarshalYAML does for the file's integrations.
func convert(t *core.PublishingTarget, global *config.GlobalConfig) (integration, error) {
	frag := map[string]any{
		// The old core sends resolved notifications unless
		// filter_config.send_resolved is false; Rootly needs them to resolve.
		"send_resolved": sendResolved(t),
	}
	headers := make(map[string]string, len(t.Headers))
	for k, v := range t.Headers {
		headers[k] = v
	}

	switch t.Type {
	case "webhook", "rootly":
		if t.Type == "rootly" && isRootlyREST(t.URL) {
			return integration{}, skip(reasonRootlyREST, errors.New("the URL is Rootly's REST API; use the webhook URL of an Alertmanager alert source"))
		}
		frag["url"] = t.URL
	case "slack":
		frag["api_url"] = t.URL
		copyTemplates(frag, t.Templates, slackTemplateKeys)
	case "pagerduty":
		routingKey := pagerdutyRoutingKey(headers)
		if routingKey == "" {
			return integration{}, skip(reasonInvalid, errors.New("pagerduty target needs headers.routing_key"))
		}
		frag["routing_key"] = routingKey
		if u := pagerdutyURL(t.URL); u != "" {
			frag["url"] = u
		}
		copyTemplates(frag, t.Templates, pagerdutyTemplateKeys)
		details := map[string]any{}
		for k, v := range t.Templates {
			if key, ok := strings.CutPrefix(k, pagerdutyDetailsPrefix); ok && key != "" {
				details[key] = v
			}
		}
		if len(details) > 0 {
			frag["details"] = details
		}
	default:
		return integration{}, skip(reasonUnsupportedType, fmt.Errorf("type %q has no upstream integration", t.Type))
	}

	httpConfig, dropped, err := httpConfigFragment(t.HTTPConfig, headers)
	if err != nil {
		return integration{}, skip(reasonInvalid, err)
	}
	if httpConfig != nil {
		frag["http_config"] = httpConfig
	}

	raw, err := yaml.Marshal(frag)
	if err != nil {
		return integration{}, skip(reasonInvalid, err)
	}
	if global == nil {
		g := config.DefaultGlobalConfig()
		global = &g
	}

	switch t.Type {
	case "slack":
		var sc config.SlackConfig
		if err := yaml.UnmarshalStrict(raw, &sc); err != nil {
			return integration{}, skip(reasonInvalid, err)
		}
		sc.AppURL = cmp.Or(sc.AppURL, global.SlackAppURL)
		if sc.HTTPConfig == nil {
			// A copy, as upstream makes: the Slack notifier may set
			// Authorization on its own config.
			sc.HTTPConfig = copyHTTPConfig(global.HTTPConfig)
		}
		return integration{slack: &sc, dropped: dropped}, nil
	case "pagerduty":
		var pdc pagerduty.PagerdutyConfig
		if err := yaml.UnmarshalStrict(raw, &pdc); err != nil {
			return integration{}, skip(reasonInvalid, err)
		}
		pdc.HTTPConfig = cmp.Or(pdc.HTTPConfig, copyHTTPConfig(global.HTTPConfig))
		pdc.URL = cmp.Or(pdc.URL, global.PagerdutyURL)
		return integration{pagerduty: &pdc, dropped: dropped}, nil
	default:
		var wh webhook.WebhookConfig
		if err := yaml.UnmarshalStrict(raw, &wh); err != nil {
			return integration{}, skip(reasonInvalid, err)
		}
		wh.HTTPConfig = cmp.Or(wh.HTTPConfig, copyHTTPConfig(global.HTTPConfig))
		return integration{webhook: &wh, dropped: dropped}, nil
	}
}

// sendResolved reads filter_config.send_resolved as the old core's
// targetAcceptsAlertStatus does: bool, a strconv.ParseBool string or a
// number; a missing or unreadable value means true.
func sendResolved(t *core.PublishingTarget) bool {
	switch v := t.FilterConfig["send_resolved"].(type) {
	case bool:
		return v
	case string:
		parsed, err := strconv.ParseBool(strings.TrimSpace(v))
		return err != nil || parsed
	case float64:
		return v != 0
	case int:
		return v != 0
	default:
		return true
	}
}

// pagerdutyRoutingKey takes the routing key out of headers as the old core's
// extractRoutingKey does: headers.routing_key, else the Authorization value
// without "Bearer ". Either way the header is not sent over HTTP; the old
// core's PagerDuty client never sent it.
func pagerdutyRoutingKey(headers map[string]string) string {
	if key, ok := headers["routing_key"]; ok {
		delete(headers, "routing_key")
		return key
	}
	for name, value := range headers {
		if http.CanonicalHeaderKey(name) == "Authorization" {
			delete(headers, name)
			return strings.TrimPrefix(value, "Bearer ")
		}
	}
	return ""
}

// pagerdutyURL is the old core's normalizePagerDutyBaseURL plus the enqueue
// path. Empty stays empty: upstream then applies global.pagerduty_url, whose
// default is the same https://events.pagerduty.com/v2/enqueue.
func pagerdutyURL(raw string) string {
	base := strings.TrimRight(strings.TrimSpace(raw), "/")
	if base == "" {
		return ""
	}
	for _, suffix := range pagerdutyPathSuffixes {
		if trimmed, ok := strings.CutSuffix(base, suffix); ok {
			base = strings.TrimRight(trimmed, "/")
			break
		}
	}
	return base + pagerdutyEnqueuePath
}

func isRootlyREST(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	return slices.Contains(rootlyRESTHosts, strings.ToLower(u.Hostname()))
}

func copyTemplates(frag map[string]any, templates map[string]string, keys []string) {
	for _, k := range keys {
		if v, ok := templates[k]; ok {
			frag[k] = v
		}
	}
}

// httpConfigFragment builds the upstream http_config from the target's
// http_config (its JSON field names are upstream's) and headers. An
// Authorization header becomes http_config.authorization, or basic_auth for
// the Basic scheme, which upstream does not take as an authorization type;
// other headers that upstream reserves are dropped and returned; the rest go
// to http_headers as secrets. A nil fragment means neither is set and the
// global http_config applies.
func httpConfigFragment(hc *core.HTTPClientConfig, headers map[string]string) (map[string]any, []string, error) {
	var frag map[string]any
	if hc != nil {
		raw, err := json.Marshal(hc)
		if err != nil {
			return nil, nil, err
		}
		if err := json.Unmarshal(raw, &frag); err != nil {
			return nil, nil, err
		}
	}
	extra := map[string]any{}
	var dropped []string
	for name, value := range headers {
		canonical := http.CanonicalHeaderKey(name)
		if canonical != "Authorization" {
			if _, reserved := commoncfg.ReservedHeaders[canonical]; reserved {
				dropped = append(dropped, canonical)
				continue
			}
			extra[name] = map[string]any{"secrets": []string{value}}
			continue
		}
		if frag["authorization"] != nil || frag["basic_auth"] != nil {
			return nil, nil, errors.New("both the Authorization header and http_config authorization are set")
		}
		auth, err := authorizationFragment(value)
		if err != nil {
			return nil, nil, err
		}
		if frag == nil {
			frag = map[string]any{}
		}
		for k, v := range auth {
			frag[k] = v
		}
	}
	if len(extra) > 0 {
		if frag == nil {
			frag = map[string]any{}
		}
		frag["http_headers"] = extra
	}
	slices.Sort(dropped)
	return frag, dropped, nil
}

// authorizationFragment turns an Authorization header value into the
// http_config field that sends it.
func authorizationFragment(value string) (map[string]any, error) {
	scheme, credentials, ok := strings.Cut(strings.TrimSpace(value), " ")
	credentials = strings.TrimSpace(credentials)
	if !ok || credentials == "" {
		return nil, errors.New(`the Authorization header must be "<type> <credentials>"`)
	}
	if !strings.EqualFold(scheme, "Basic") {
		return map[string]any{"authorization": map[string]any{"type": scheme, "credentials": credentials}}, nil
	}
	user, password, err := decodeBasic(credentials)
	if err != nil {
		return nil, err
	}
	return map[string]any{"basic_auth": map[string]any{"username": user, "password": password}}, nil
}

func decodeBasic(credentials string) (user, password string, err error) {
	raw, err := base64.StdEncoding.DecodeString(credentials)
	if err != nil {
		return "", "", errors.New("the Basic Authorization header is not base64")
	}
	user, password, ok := strings.Cut(string(raw), ":")
	if !ok {
		return "", "", errors.New(`the Basic Authorization header must encode "<user>:<password>"`)
	}
	return user, password, nil
}

func copyHTTPConfig(hc *commoncfg.HTTPClientConfig) *commoncfg.HTTPClientConfig {
	if hc == nil {
		c := commoncfg.DefaultHTTPClientConfig
		return &c
	}
	c := *hc
	return &c
}

// redact removes the target's credential-bearing values from an error text:
// upstream parse errors may quote the value they reject.
func redact(msg string, t *core.PublishingTarget) string {
	secrets := []string{t.URL}
	for _, v := range t.Headers {
		secrets = append(secrets, v)
		if _, credentials, ok := strings.Cut(v, " "); ok {
			secrets = append(secrets, credentials)
			if _, password, err := decodeBasic(strings.TrimSpace(credentials)); err == nil {
				secrets = append(secrets, password)
			}
		}
	}
	if hc := t.HTTPConfig; hc != nil {
		secrets = append(secrets, hc.ProxyURL)
		if hc.BasicAuth != nil {
			secrets = append(secrets, hc.BasicAuth.Password)
		}
		if hc.Authorization != nil {
			secrets = append(secrets, hc.Authorization.Credentials)
		}
	}
	// Longest first, so a value containing another is replaced whole.
	slices.SortFunc(secrets, func(a, b string) int { return len(b) - len(a) })
	for _, s := range secrets {
		if strings.TrimSpace(s) != "" {
			msg = strings.ReplaceAll(msg, s, "<redacted>")
		}
	}
	return msg
}
