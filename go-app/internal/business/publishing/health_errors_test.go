package publishing

import (
	"errors"
	"net"
	"testing"
)

// TestClassifyNetworkError_Timeout tests timeout error classification.
func TestClassifyNetworkError_Timeout(t *testing.T) {
	// Create timeout error
	err := &net.OpError{
		Op:  "dial",
		Net: "tcp",
		Err: &timeoutError{},
	}

	errType := classifyNetworkError(err)
	if errType != ErrorTypeTimeout {
		t.Errorf("Expected ErrorTypeTimeout, got %s", errType)
	}
}

// TestClassifyNetworkError_DNS tests DNS error classification.
func TestClassifyNetworkError_DNS(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want ErrorType
	}{
		{
			name: "no such host",
			err:  errors.New("dial tcp: lookup invalid-host.example.com: no such host"),
			want: ErrorTypeDNS,
		},
		{
			name: "dns error",
			err:  errors.New("dns resolution failed"),
			want: ErrorTypeDNS,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errType := classifyNetworkError(tt.err)
			if errType != tt.want {
				t.Errorf("Expected %s, got %s", tt.want, errType)
			}
		})
	}
}

// TestClassifyNetworkError_Refused tests connection refused error.
func TestClassifyNetworkError_Refused(t *testing.T) {
	err := errors.New("dial tcp 127.0.0.1:12345: connection refused")

	errType := classifyNetworkError(err)
	if errType != ErrorTypeRefused {
		t.Errorf("Expected ErrorTypeRefused, got %s", errType)
	}
}

// TestClassifyNetworkError_TLS tests TLS error classification.
func TestClassifyNetworkError_TLS(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want ErrorType
	}{
		{
			name: "tls handshake",
			err:  errors.New("tls: handshake failure"),
			want: ErrorTypeTLS,
		},
		{
			name: "certificate error",
			err:  errors.New("x509: certificate has expired"),
			want: ErrorTypeTLS,
		},
		{
			name: "x509 error",
			err:  errors.New("x509: certificate signed by unknown authority"),
			want: ErrorTypeTLS,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errType := classifyNetworkError(tt.err)
			if errType != tt.want {
				t.Errorf("Expected %s, got %s", tt.want, errType)
			}
		})
	}
}

// TestClassifyNetworkError_Unknown tests unknown error classification.
func TestClassifyNetworkError_Unknown(t *testing.T) {
	tests := []struct {
		name string
		err  error
	}{
		{
			name: "nil error",
			err:  nil,
		},
		{
			name: "generic error",
			err:  errors.New("some unexpected error"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errType := classifyNetworkError(tt.err)
			if errType != ErrorTypeUnknown {
				t.Errorf("Expected ErrorTypeUnknown, got %s", errType)
			}
		})
	}
}

// TestClassifyHTTPError_Timeout tests HTTP timeout error.
func TestClassifyHTTPError_Timeout(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want ErrorType
	}{
		{
			name: "deadline exceeded",
			err:  errors.New("context deadline exceeded"),
			want: ErrorTypeTimeout,
		},
		{
			name: "timeout",
			err:  errors.New("http request timeout"),
			want: ErrorTypeTimeout,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errType := classifyHTTPError(tt.err)
			if errType != tt.want {
				t.Errorf("Expected %s, got %s", tt.want, errType)
			}
		})
	}
}

// TestClassifyHTTPError_TLS tests HTTP TLS error.
func TestClassifyHTTPError_TLS(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want ErrorType
	}{
		{
			name: "tls error",
			err:  errors.New("tls: bad certificate"),
			want: ErrorTypeTLS,
		},
		{
			name: "certificate error",
			err:  errors.New("certificate verify failed"),
			want: ErrorTypeTLS,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errType := classifyHTTPError(tt.err)
			if errType != tt.want {
				t.Errorf("Expected %s, got %s", tt.want, errType)
			}
		})
	}
}

// TestClassifyHTTPError_DNS tests HTTP DNS error.
func TestClassifyHTTPError_DNS(t *testing.T) {
	err := errors.New("Get https://invalid-host: no such host")

	errType := classifyHTTPError(err)
	if errType != ErrorTypeDNS {
		t.Errorf("Expected ErrorTypeDNS, got %s", errType)
	}
}

// TestClassifyHTTPError_Unknown tests HTTP unknown error.
func TestClassifyHTTPError_Unknown(t *testing.T) {
	tests := []struct {
		name string
		err  error
	}{
		{
			name: "nil error",
			err:  nil,
		},
		{
			name: "generic error",
			err:  errors.New("unexpected http error"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errType := classifyHTTPError(tt.err)
			if errType != ErrorTypeUnknown {
				t.Errorf("Expected ErrorTypeUnknown, got %s", errType)
			}
		})
	}
}

// TestSanitizeErrorMessage tests error message sanitization.
func TestSanitizeErrorMessage(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "Authorization header",
			input: "HTTP request failed: Authorization: Bearer secret123\nother data",
			want:  "HTTP request failed: Authorization: [REDACTED]\nother data",
		},
		{
			name:  "API key header",
			input: "Request error: X-API-Key: apikey123\nmore info",
			want:  "Request error: X-API-Key: [REDACTED]\nmore info",
		},
		{
			name:  "Bearer token",
			input: "Auth error: Bearer token123 is invalid",
			want:  "Auth error: Bearer [REDACTED] is invalid",
		},
		{
			name:  "token parameter",
			input: "URL: https://api.example.com?token=secret123&other=value",
			want:  "URL: https://api.example.com?token= [REDACTED]&other=value",
		},
		{
			name:  "api_key parameter",
			input: "URL: https://api.example.com?api_key=secret&param=value",
			want:  "URL: https://api.example.com?api_key= [REDACTED]&param=value",
		},
		{
			name:  "no sensitive data",
			input: "Simple error message",
			want:  "Simple error message",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sanitizeErrorMessage(tt.input)
			if got != tt.want {
				t.Errorf("sanitizeErrorMessage() =\n%q\nwant:\n%q", got, tt.want)
			}
		})
	}
}

// timeoutError implements net.Error with Timeout() = true
type timeoutError struct{}

func (e *timeoutError) Error() string   { return "timeout" }
func (e *timeoutError) Timeout() bool   { return true }
func (e *timeoutError) Temporary() bool { return true }
