package validators

import (
	"regexp"
	"sync"
)

// ================================================================================
// TN-156: Template Validator - Security Patterns
// ================================================================================
// Security patterns for detecting hardcoded secrets and sensitive data.
//
// Features:
// - 15+ regex patterns for secrets detection
// - API keys, passwords, tokens, AWS keys, etc.
// - Severity levels (critical, high, medium)
// - Compiled once using sync.Once for performance
//
// Quality Target: 150% (Grade A+ EXCEPTIONAL)
// Author: AI Assistant
// Date: 2025-11-25

// SecretPattern represents a pattern for detecting secrets
type SecretPattern struct {
	// Name is the secret type name
	Name string

	// Pattern is the compiled regex pattern
	Pattern *regexp.Regexp

	// Severity is the severity level (critical, high, medium, low)
	Severity string

	// Message is the error message template
	Message string

	// Example is an example match (for documentation)
	Example string
}

// ================================================================================

var (
	// secretPatternsOnce ensures patterns are compiled only once
	secretPatternsOnce sync.Once

	// cachedSecretPatterns stores compiled patterns
	cachedSecretPatterns []SecretPattern
)

// GetSecretPatterns returns all secret detection patterns
//
// Patterns are compiled once on first call (sync.Once) for performance.
// Subsequent calls return cached patterns.
//
// Returns 15+ secret patterns covering:
// - API keys
// - Passwords
// - Tokens
// - AWS credentials
// - Database URLs
// - Private keys
// - etc.
func GetSecretPatterns() []SecretPattern {
	secretPatternsOnce.Do(func() {
		cachedSecretPatterns = []SecretPattern{
			// 1. Generic API Keys
			{
				Name:     "API Key",
				Pattern:  regexp.MustCompile(`(?i)(api[-_]?key|apikey)\s*[:=]\s*[\"\']?[a-zA-Z0-9_-]{16,}[\"\']?`),
				Severity: "critical",
				Message:  "Hardcoded API key detected. Use environment variables or secret management.",
				Example:  "api_key: sk-1234567890abcdef",
			},

			// 2. Generic Passwords
			{
				Name:     "Password",
				Pattern:  regexp.MustCompile(`(?i)(password|passwd|pwd)\s*[:=]\s*[\"\'][^\"\']{8,}[\"\']`),
				Severity: "critical",
				Message:  "Hardcoded password detected. Never store passwords in templates.",
				Example:  "password: \"mysecretpassword123\"",
			},

			// 3. Generic Tokens
			{
				Name:     "Token",
				Pattern:  regexp.MustCompile(`(?i)(token|secret)\s*[:=]\s*[\"\']?[a-zA-Z0-9_-]{20,}[\"\']?`),
				Severity: "high",
				Message:  "Hardcoded token/secret detected. Use secret management.",
				Example:  "token: abc123xyz789...",
			},

			// 4. Bearer Tokens
			{
				Name:     "Bearer Token",
				Pattern:  regexp.MustCompile(`(?i)bearer\s+[a-zA-Z0-9_-]{20,}`),
				Severity: "high",
				Message:  "Bearer token detected in template. Use Authorization header from environment.",
				Example:  "Authorization: Bearer abc123xyz789...",
			},

			// 5. AWS Access Key ID
			{
				Name:     "AWS Access Key ID",
				Pattern:  regexp.MustCompile(`(?i)(aws_access_key_id|aws[-_]?access[-_]?key)\s*[:=]\s*[\"\']?AKIA[A-Z0-9]{16}[\"\']?`),
				Severity: "critical",
				Message:  "AWS Access Key ID detected. Use IAM roles or environment variables.",
				Example:  "aws_access_key_id: AKIAIOSFODNN7EXAMPLE",
			},

			// 6. AWS Secret Access Key
			{
				Name:     "AWS Secret Access Key",
				Pattern:  regexp.MustCompile(`(?i)(aws_secret_access_key|aws[-_]?secret)\s*[:=]\s*[\"\']?[A-Za-z0-9/+=]{40}[\"\']?`),
				Severity: "critical",
				Message:  "AWS Secret Access Key detected. Use IAM roles or AWS Secrets Manager.",
				Example:  "aws_secret_access_key: wJalrXUtnFEMI/K7MDENG/...",
			},

			// 7. GitHub Personal Access Token
			{
				Name:     "GitHub Token",
				Pattern:  regexp.MustCompile(`ghp_[a-zA-Z0-9]{36}`),
				Severity: "critical",
				Message:  "GitHub Personal Access Token detected. Revoke immediately and use GitHub Secrets.",
				Example:  "ghp_1234567890abcdefghijklmnopqrstuv",
			},

			// 8. Slack Token
			{
				Name:     "Slack Token",
				Pattern:  regexp.MustCompile(`xox[baprs]-[0-9]{10,12}-[0-9]{10,12}-[a-zA-Z0-9]{24,32}`),
				Severity: "critical",
				Message:  "Slack API token detected. Use Slack App configuration or environment variables.",
				Example:  "xoxb-1234567890-1234567890-abc123xyz789...",
			},

			// 9. Slack Webhook URL
			{
				Name:     "Slack Webhook URL",
				Pattern:  regexp.MustCompile(`https://hooks\.slack\.com/services/T[A-Z0-9]{8,10}/B[A-Z0-9]{8,10}/[a-zA-Z0-9]{24}`),
				Severity: "high",
				Message:  "Slack Webhook URL detected. Store in secrets, not in templates.",
				Example:  "https://hooks.slack.com/services/T.../B.../abc123...",
			},

			// 10. PagerDuty API Key
			{
				Name:     "PagerDuty API Key",
				Pattern:  regexp.MustCompile(`(?i)(pagerduty|pd)[-_]?(api[-_]?)?key\s*[:=]\s*[\"\']?[a-zA-Z0-9_-]{20,}[\"\']?`),
				Severity: "critical",
				Message:  "PagerDuty API key detected. Use K8s secrets for routing keys.",
				Example:  "pagerduty_key: abc123xyz789...",
			},

			// 11. Private SSH Keys
			{
				Name:     "SSH Private Key",
				Pattern:  regexp.MustCompile(`-----BEGIN\s+(RSA|DSA|EC|OPENSSH)\s+PRIVATE KEY-----`),
				Severity: "critical",
				Message:  "SSH private key detected. Never store private keys in templates.",
				Example:  "-----BEGIN RSA PRIVATE KEY-----",
			},

			// 12. JWT Tokens
			{
				Name:     "JWT Token",
				Pattern:  regexp.MustCompile(`eyJ[a-zA-Z0-9_-]{10,}\.[a-zA-Z0-9_-]{10,}\.[a-zA-Z0-9_-]{10,}`),
				Severity: "high",
				Message:  "JWT token detected. Use short-lived tokens from auth service.",
				Example:  "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9...",
			},

			// 13. Database URLs with credentials
			{
				Name:     "Database URL with Credentials",
				Pattern:  regexp.MustCompile(`(?i)(postgres|mysql|mongodb)://[^:]+:[^@]+@`),
				Severity: "critical",
				Message:  "Database URL with embedded credentials detected. Use connection secrets.",
				Example:  "postgres://user:password@host:5432/db",
			},

			// 14. Generic Secrets (key=value with secret in key name)
			{
				Name:     "Generic Secret",
				Pattern:  regexp.MustCompile(`(?i)(secret|key|credential|auth)_?[a-z]*\s*[:=]\s*[\"\'][^\"\']{10,}[\"\']`),
				Severity: "medium",
				Message:  "Potential secret detected. Review if this should be stored as secret.",
				Example:  "secret_value: \"some_secret_string\"",
			},

			// 15. Hardcoded Email with Password
			{
				Name:     "Email Credentials",
				Pattern:  regexp.MustCompile(`(?i)(smtp|email)[-_]?(password|pass|pwd)\s*[:=]\s*[\"\'][^\"\']{6,}[\"\']`),
				Severity: "critical",
				Message:  "Email/SMTP password detected. Use secret management.",
				Example:  "smtp_password: \"secret123\"",
			},

			// 16. Generic Base64 Secrets (long base64 strings)
			{
				Name:     "Base64 Secret",
				Pattern:  regexp.MustCompile(`(?i)(secret|token|key)\s*[:=]\s*[\"\']?[A-Za-z0-9+/]{40,}={0,2}[\"\']?`),
				Severity: "medium",
				Message:  "Potential Base64-encoded secret detected. Review if sensitive.",
				Example:  "secret: YWJjMTIzZGVmNDU2Z2hpNzg5...",
			},
		}
	})

	return cachedSecretPatterns
}

// ================================================================================

// GetSensitiveFieldNames returns field names that may contain sensitive data
//
// Returns slice of field names that should not be logged or exposed.
// Used for detecting sensitive data exposure in templates.
func GetSensitiveFieldNames() []string {
	return []string{
		// Personal Identifiable Information (PII)
		"password",
		"passwd",
		"pwd",
		"secret",
		"token",
		"api_key",
		"apikey",
		"access_key",
		"private_key",
		"credit_card",
		"ssn",
		"social_security",
		"passport",
		"driver_license",

		// Financial
		"credit_card_number",
		"card_number",
		"cvv",
		"pin",
		"account_number",
		"routing_number",

		// Authentication
		"auth_token",
		"bearer_token",
		"refresh_token",
		"session_token",
		"csrf_token",

		// Database
		"database_password",
		"db_password",
		"connection_string",
		"jdbc_url",

		// Cloud Credentials
		"aws_access_key_id",
		"aws_secret_access_key",
		"azure_client_secret",
		"gcp_service_account",

		// Email/Communication
		"smtp_password",
		"email_password",
	}
}

// ================================================================================
