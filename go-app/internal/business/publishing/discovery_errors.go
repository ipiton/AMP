package publishing

import "fmt"

// ErrTargetNotFound indicates requested target doesn't exist in cache.
//
// This error is returned by GetTarget() when target name is not found.
// It's a typed error so callers can distinguish "not found" from other errors.
//
// Use errors.As() to check error type:
//
//	target, err := manager.GetTarget("nonexistent")
//	if err != nil {
//	    var notFoundErr *ErrTargetNotFound
//	    if errors.As(err, &notFoundErr) {
//	        log.Warn("Target not configured", "name", notFoundErr.TargetName)
//	        // Fallback logic (use default target, skip publishing)
//	    }
//	}
type ErrTargetNotFound struct {
	// TargetName is name of target that was requested but not found.
	TargetName string
}

// Error implements error interface.
func (e *ErrTargetNotFound) Error() string {
	return fmt.Sprintf("target '%s' not found in cache", e.TargetName)
}

// NewTargetNotFoundError creates ErrTargetNotFound with target name.
func NewTargetNotFoundError(targetName string) *ErrTargetNotFound {
	return &ErrTargetNotFound{
		TargetName: targetName,
	}
}

// ErrDiscoveryFailed indicates K8s API failure during target discovery.
//
// This error is returned by DiscoverTargets() when K8s API is unavailable.
// Possible causes:
//   - K8s API unreachable (network issue, pod not in cluster)
//   - RBAC permission denied (missing get/list secrets)
//   - K8s API timeout (slow response, context cancelled)
//   - Invalid namespace (namespace doesn't exist)
//
// Recovery:
//   - Manager keeps old cache (stale targets OK)
//   - Publishing continues with last known targets
//   - Next discovery attempt may succeed
//
// Example:
//
//	err := manager.DiscoverTargets(ctx)
//	if err != nil {
//	    var discoveryErr *ErrDiscoveryFailed
//	    if errors.As(err, &discoveryErr) {
//	        log.Error("K8s API unavailable",
//	            "namespace", discoveryErr.Namespace,
//	            "cause", discoveryErr.Cause)
//	        // Continue with stale cache
//	    }
//	}
type ErrDiscoveryFailed struct {
	// Namespace is K8s namespace where discovery was attempted.
	Namespace string

	// Cause is underlying error from K8s client.
	// May be: ConnectionError, AuthError, TimeoutError (from TN-046).
	Cause error
}

// Error implements error interface.
func (e *ErrDiscoveryFailed) Error() string {
	return fmt.Sprintf("target discovery failed in namespace '%s': %v", e.Namespace, e.Cause)
}

// Unwrap enables error chain traversal with errors.Is/As.
func (e *ErrDiscoveryFailed) Unwrap() error {
	return e.Cause
}

// NewDiscoveryFailedError creates ErrDiscoveryFailed with context.
func NewDiscoveryFailedError(namespace string, cause error) *ErrDiscoveryFailed {
	return &ErrDiscoveryFailed{
		Namespace: namespace,
		Cause:     cause,
	}
}

// ErrInvalidSecretFormat indicates secret parsing failure.
//
// This error is logged (not returned) when secret has malformed data.
// Possible causes:
//   - Missing 'config' field (secret.Data["config"] doesn't exist)
//   - Invalid base64 encoding (corrupt data)
//   - Invalid JSON (malformed syntax, wrong types)
//   - Empty configuration (all fields empty after parse)
//
// Recovery:
//   - Invalid secret is skipped (doesn't block discovery)
//   - Error logged with secret name + reason
//   - Other secrets continue processing (partial success)
//
// Example:
//
//	if err := parseSecret(secret); err != nil {
//	    var formatErr *ErrInvalidSecretFormat
//	    if errors.As(err, &formatErr) {
//	        log.Warn("Skipping invalid secret",
//	            "secret_name", formatErr.SecretName,
//	            "reason", formatErr.Reason)
//	        // Continue with other secrets
//	    }
//	}
type ErrInvalidSecretFormat struct {
	// SecretName is name of secret that failed parsing.
	SecretName string

	// Reason is human-readable explanation of parse failure.
	// Examples: "missing 'config' field", "invalid base64", "invalid JSON".
	Reason string
}

// Error implements error interface.
func (e *ErrInvalidSecretFormat) Error() string {
	return fmt.Sprintf("secret '%s' has invalid format: %s", e.SecretName, e.Reason)
}

// NewInvalidSecretFormatError creates ErrInvalidSecretFormat with context.
func NewInvalidSecretFormatError(secretName string, reason string) *ErrInvalidSecretFormat {
	return &ErrInvalidSecretFormat{
		SecretName: secretName,
		Reason:     reason,
	}
}

// ValidationError represents a field validation error.
//
// This error is returned by validateTarget() when target configuration is invalid.
// Multiple validation errors can occur simultaneously (e.g., missing name + invalid URL).
//
// Fields:
//   - Field: Name of invalid field ("name", "url", "type", "format")
//   - Message: Human-readable error message ("field is required", "must be valid URL")
//   - Value: Actual value that failed validation (for debugging)
//
// Example:
//
//	errs := validateTarget(target)
//	if len(errs) > 0 {
//	    log.Warn("Target validation failed", "target", target.Name)
//	    for _, err := range errs {
//	        log.Warn("Validation error",
//	            "field", err.Field,
//	            "message", err.Message,
//	            "value", err.Value)
//	    }
//	    // Skip invalid target
//	}
type ValidationError struct {
	// Field is name of field that failed validation.
	// Examples: "name", "url", "type", "format", "headers".
	Field string

	// Message is human-readable error message.
	// Examples: "field is required", "must be valid URL", "invalid enum value".
	Message string

	// Value is actual value that failed validation.
	// Used for debugging (show what was provided).
	Value any
}

// Error implements error interface.
func (e ValidationError) Error() string {
	return fmt.Sprintf("field '%s': %s (value: %v)", e.Field, e.Message, e.Value)
}

// NewValidationError creates ValidationError with field/message/value.
func NewValidationError(field, message string, value any) ValidationError {
	return ValidationError{
		Field:   field,
		Message: message,
		Value:   value,
	}
}
