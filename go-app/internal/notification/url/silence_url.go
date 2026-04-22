package url

import (
	"fmt"
	"net/url"
	"sort"
	"strings"
)

// BuildSilenceURL returns an Alertmanager-compatible silence URL for the given labels.
// Returns "" when externalURL is empty (graceful degradation).
// Format: {externalURL}/#/silences?filter={encodedMatchers}
func BuildSilenceURL(externalURL string, labels map[string]string) string {
	if externalURL == "" {
		return ""
	}

	filter := buildMatcherFilter(labels)
	return fmt.Sprintf("%s/#/silences?filter=%s", strings.TrimRight(externalURL, "/"), url.QueryEscape(filter))
}

// buildMatcherFilter encodes labels as an Alertmanager matcher expression.
// Example: {alertname="HighCPU",namespace="prod"}
func buildMatcherFilter(labels map[string]string) string {
	if len(labels) == 0 {
		return "{}"
	}

	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var b strings.Builder
	b.WriteByte('{')
	for i, k := range keys {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, `%s="%s"`, k, labels[k])
	}
	b.WriteByte('}')
	return b.String()
}
