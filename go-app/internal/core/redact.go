package core

import "net/url"

// RedactURL returns a target URL safe to log: scheme and host only, with
// "/***" standing in for any path, query, fragment or userinfo
// (PUBLISH-SECRETS-IN-LOGS). For Teams, Discord, Google Chat, Slack and the
// Telegram Bot API the token IS part of the path, so the path is never
// logged. A string that does not parse as an absolute URL becomes "***".
func RedactURL(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		if raw == "" {
			return ""
		}
		return "***"
	}

	redacted := parsed.Scheme + "://" + parsed.Host
	if (parsed.Path != "" && parsed.Path != "/") || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.User != nil || parsed.Opaque != "" {
		redacted += "/***"
	}
	return redacted
}
