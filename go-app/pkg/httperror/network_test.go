package httperror

import (
	"context"
	"errors"
	"net"
	"syscall"
	"testing"
)

func TestIsRetryableNetworkError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "nil error",
			err:      nil,
			expected: false,
		},
		{
			name:     "context deadline exceeded",
			err:      context.DeadlineExceeded,
			expected: true,
		},
		{
			name:     "context canceled",
			err:      context.Canceled,
			expected: false,
		},
		{
			name:     "connection refused",
			err:      &net.OpError{Err: syscall.ECONNREFUSED},
			expected: true,
		},
		{
			name:     "connection reset",
			err:      &net.OpError{Err: syscall.ECONNRESET},
			expected: true,
		},
		{
			name:     "connection aborted",
			err:      &net.OpError{Err: syscall.ECONNABORTED},
			expected: true,
		},
		{
			name:     "network unreachable",
			err:      &net.OpError{Err: syscall.ENETUNREACH},
			expected: true,
		},
		{
			name:     "host unreachable",
			err:      &net.OpError{Err: syscall.EHOSTUNREACH},
			expected: true,
		},
		{
			name:     "broken pipe",
			err:      &net.OpError{Err: syscall.EPIPE},
			expected: true,
		},
		{
			name:     "generic error",
			err:      errors.New("some error"),
			expected: false,
		},
		{
			name: "wrapped connection refused",
			err: errors.Join(
				errors.New("wrapper"),
				&net.OpError{Err: syscall.ECONNREFUSED},
			),
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsRetryableNetworkError(tt.err); got != tt.expected {
				t.Errorf("IsRetryableNetworkError() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestIsNetworkTimeout(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "nil",
			err:      nil,
			expected: false,
		},
		{
			name:     "context deadline exceeded",
			err:      context.DeadlineExceeded,
			expected: true,
		},
		{
			name:     "timeout net.Error",
			err:      &timeoutError{timeout: true},
			expected: true,
		},
		{
			name:     "non-timeout net.Error",
			err:      &timeoutError{timeout: false},
			expected: false,
		},
		{
			name:     "generic error",
			err:      errors.New("error"),
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsNetworkTimeout(tt.err); got != tt.expected {
				t.Errorf("IsNetworkTimeout() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestIsConnectionRefused(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "nil",
			err:      nil,
			expected: false,
		},
		{
			name:     "connection refused",
			err:      &net.OpError{Err: syscall.ECONNREFUSED},
			expected: true,
		},
		{
			name:     "connection reset",
			err:      &net.OpError{Err: syscall.ECONNRESET},
			expected: false,
		},
		{
			name:     "generic error",
			err:      errors.New("error"),
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsConnectionRefused(tt.err); got != tt.expected {
				t.Errorf("IsConnectionRefused() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestIsConnectionReset(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "nil",
			err:      nil,
			expected: false,
		},
		{
			name:     "connection reset",
			err:      &net.OpError{Err: syscall.ECONNRESET},
			expected: true,
		},
		{
			name:     "connection refused",
			err:      &net.OpError{Err: syscall.ECONNREFUSED},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsConnectionReset(tt.err); got != tt.expected {
				t.Errorf("IsConnectionReset() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestIsDNSError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "nil",
			err:      nil,
			expected: false,
		},
		{
			name:     "DNS error",
			err:      &net.DNSError{Err: "no such host", Name: "example.com"},
			expected: true,
		},
		{
			name:     "non-DNS error",
			err:      errors.New("error"),
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsDNSError(tt.err); got != tt.expected {
				t.Errorf("IsDNSError() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestClassifyNetworkError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected string
	}{
		{
			name:     "nil",
			err:      nil,
			expected: "unknown",
		},
		{
			name:     "context deadline exceeded",
			err:      context.DeadlineExceeded,
			expected: "timeout",
		},
		{
			name:     "context canceled",
			err:      context.Canceled,
			expected: "canceled",
		},
		{
			name:     "timeout error",
			err:      &timeoutError{timeout: true},
			expected: "timeout",
		},
		{
			name:     "DNS error",
			err:      &net.DNSError{Err: "no such host"},
			expected: "dns_error",
		},
		{
			name:     "connection refused",
			err:      &net.OpError{Err: syscall.ECONNREFUSED},
			expected: "connection_refused",
		},
		{
			name:     "connection reset",
			err:      &net.OpError{Err: syscall.ECONNRESET},
			expected: "connection_reset",
		},
		{
			name:     "connection aborted",
			err:      &net.OpError{Err: syscall.ECONNABORTED},
			expected: "connection_aborted",
		},
		{
			name:     "network unreachable",
			err:      &net.OpError{Err: syscall.ENETUNREACH},
			expected: "network_unreachable",
		},
		{
			name:     "host unreachable",
			err:      &net.OpError{Err: syscall.EHOSTUNREACH},
			expected: "host_unreachable",
		},
		{
			name:     "broken pipe",
			err:      &net.OpError{Err: syscall.EPIPE},
			expected: "broken_pipe",
		},
		{
			name:     "generic net.OpError",
			err:      &net.OpError{Err: errors.New("other")},
			expected: "network_error",
		},
		{
			name:     "generic error",
			err:      errors.New("error"),
			expected: "unknown",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ClassifyNetworkError(tt.err); got != tt.expected {
				t.Errorf("ClassifyNetworkError() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestWrapNetworkError(t *testing.T) {
	tests := []struct {
		name             string
		provider         string
		err              error
		expectedNil      bool
		expectedStatus   int
		expectedProvider string
	}{
		{
			name:        "nil error",
			provider:    "slack",
			err:         nil,
			expectedNil: true,
		},
		{
			name:             "connection refused",
			provider:         "pagerduty",
			err:              &net.OpError{Err: syscall.ECONNREFUSED},
			expectedStatus:   0,
			expectedProvider: "pagerduty",
		},
		{
			name:             "timeout",
			provider:         "rootly",
			err:              context.DeadlineExceeded,
			expectedStatus:   0,
			expectedProvider: "rootly",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := WrapNetworkError(tt.provider, tt.err)

			if tt.expectedNil {
				if result != nil {
					t.Errorf("Expected nil, got %v", result)
				}
				return
			}

			if result == nil {
				t.Fatal("Expected non-nil result")
			}

			if result.StatusCode != tt.expectedStatus {
				t.Errorf("StatusCode = %d, want %d", result.StatusCode, tt.expectedStatus)
			}

			if result.Provider != tt.expectedProvider {
				t.Errorf("Provider = %q, want %q", result.Provider, tt.expectedProvider)
			}

			if result.Cause != tt.err {
				t.Errorf("Cause not preserved")
			}
		})
	}
}

func TestDNSTemporaryError(t *testing.T) {
	// Test that temporary DNS errors are retryable
	tempDNSErr := &net.DNSError{
		Err:         "temporary failure",
		IsTemporary: true,
	}

	if !IsRetryableNetworkError(tempDNSErr) {
		t.Error("Temporary DNS error should be retryable")
	}

	// Non-temporary DNS error should NOT be retryable
	permDNSErr := &net.DNSError{
		Err:         "no such host",
		IsTemporary: false,
	}

	if IsRetryableNetworkError(permDNSErr) {
		t.Error("Permanent DNS error should not be retryable")
	}
}

// timeoutError is a test helper implementing net.Error
type timeoutError struct {
	timeout bool
}

func (e *timeoutError) Error() string   { return "timeout error" }
func (e *timeoutError) Timeout() bool   { return e.timeout }
func (e *timeoutError) Temporary() bool { return e.timeout }
