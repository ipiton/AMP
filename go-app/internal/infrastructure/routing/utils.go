package routing

import (
	"fmt"
	"net"
	"net/url"
	"strings"
)

// sanitizeURL redacts sensitive URL components.
// Keeps scheme, host, path but removes query parameters and fragments.
//
// Example:
//
//	input:  https://webhook.site/xxx?token=secret#fragment
//	output: https://webhook.site/xxx?[REDACTED]
func sanitizeURL(urlStr string) string {
	if urlStr == "" {
		return ""
	}

	u, err := url.Parse(urlStr)
	if err != nil {
		return "[INVALID_URL]"
	}

	// Keep scheme and host
	sanitized := fmt.Sprintf("%s://%s", u.Scheme, u.Host)

	// Keep path but check for sensitive segments
	path := u.Path
	if containsSensitiveSegment(path) {
		path = "[REDACTED]"
	}
	sanitized += path

	// Redact query parameters (may contain tokens)
	if u.RawQuery != "" {
		sanitized += "?[REDACTED]"
	}

	// Note: fragments are not sent to servers, so not a leak risk

	return sanitized
}

// containsSensitiveSegment checks if a URL path contains sensitive keywords.
func containsSensitiveSegment(path string) bool {
	lower := strings.ToLower(path)
	sensitiveKeywords := []string{"token", "key", "secret", "password", "auth", "api"}
	for _, keyword := range sensitiveKeywords {
		if strings.Contains(lower, keyword) {
			return true
		}
	}
	return false
}

// isSensitiveHeader checks if an HTTP header is sensitive.
func isSensitiveHeader(key string) bool {
	lower := strings.ToLower(key)
	sensitiveHeaders := []string{
		"authorization",
		"api-key",
		"apikey",
		"api_key",
		"x-api-key",
		"token",
		"bearer",
		"password",
		"secret",
	}

	for _, header := range sensitiveHeaders {
		if strings.Contains(lower, header) {
			return true
		}
	}

	return false
}

// isSecretReference checks if a value is a secret reference.
// Recognizes environment variables and K8s secret references.
//
// Formats:
//   - ${VAR_NAME}
//   - secret:namespace/name/key
func isSecretReference(value string) bool {
	if strings.HasPrefix(value, "${") && strings.HasSuffix(value, "}") {
		return true
	}
	if strings.HasPrefix(value, "secret:") {
		return true
	}
	return false
}

// isPrivateIP checks if an IP address is private.
// Includes RFC 1918, localhost, link-local, and IPv6 equivalents.
func isPrivateIP(ip net.IP) bool {
	// Private IPv4 ranges
	privateIPv4 := []string{
		"10.0.0.0/8",       // RFC 1918
		"172.16.0.0/12",    // RFC 1918
		"192.168.0.0/16",   // RFC 1918
		"127.0.0.0/8",      // Localhost
		"169.254.0.0/16",   // Link-local
		"0.0.0.0/8",        // This network
		"100.64.0.0/10",    // Shared address space (RFC 6598)
		"192.0.0.0/24",     // IETF protocol assignments
		"192.0.2.0/24",     // TEST-NET-1
		"198.18.0.0/15",    // Benchmarking
		"198.51.100.0/24",  // TEST-NET-2
		"203.0.113.0/24",   // TEST-NET-3
		"224.0.0.0/4",      // Multicast
		"240.0.0.0/4",      // Reserved
		"255.255.255.255/32", // Broadcast
	}

	// Private IPv6 ranges
	privateIPv6 := []string{
		"::1/128",       // Localhost
		"fc00::/7",      // Unique local
		"fe80::/10",     // Link-local
		"ff00::/8",      // Multicast
		"::ffff:0:0/96", // IPv4-mapped
	}

	// Check IPv4
	for _, cidr := range privateIPv4 {
		_, network, err := net.ParseCIDR(cidr)
		if err != nil {
			continue
		}
		if network.Contains(ip) {
			return true
		}
	}

	// Check IPv6
	for _, cidr := range privateIPv6 {
		_, network, err := net.ParseCIDR(cidr)
		if err != nil {
			continue
		}
		if network.Contains(ip) {
			return true
		}
	}

	return false
}
