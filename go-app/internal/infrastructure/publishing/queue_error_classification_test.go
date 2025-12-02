package publishing

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"syscall"
	"testing"
)

// mockHTTPError implements interface{ StatusCode() int } for testing
type mockHTTPError struct {
	statusCode int
	message    string
}

func (e *mockHTTPError) Error() string {
	return e.message
}

func (e *mockHTTPError) StatusCode() int {
	return e.statusCode
}

// TestClassifyPublishingError_HTTPTransient tests transient HTTP errors
func TestClassifyPublishingError_HTTPTransient(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
	}{
		{"408 Request Timeout", http.StatusRequestTimeout},
		{"429 Too Many Requests", http.StatusTooManyRequests},
		{"502 Bad Gateway", http.StatusBadGateway},
		{"503 Service Unavailable", http.StatusServiceUnavailable},
		{"504 Gateway Timeout", http.StatusGatewayTimeout},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := &mockHTTPError{
				statusCode: tt.statusCode,
				message:    fmt.Sprintf("HTTP %d", tt.statusCode),
			}

			errorType := classifyPublishingError(err)

			if errorType != QueueErrorTypeTransient {
				t.Errorf("Expected QueueErrorTypeTransient for %s, got %v", tt.name, errorType)
			}
		})
	}
}

// TestClassifyPublishingError_HTTPPermanent tests permanent HTTP errors
func TestClassifyPublishingError_HTTPPermanent(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
	}{
		{"400 Bad Request", http.StatusBadRequest},
		{"401 Unauthorized", http.StatusUnauthorized},
		{"403 Forbidden", http.StatusForbidden},
		{"404 Not Found", http.StatusNotFound},
		{"405 Method Not Allowed", http.StatusMethodNotAllowed},
		{"409 Conflict", http.StatusConflict},
		{"410 Gone", http.StatusGone},
		{"422 Unprocessable Entity", http.StatusUnprocessableEntity},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := &mockHTTPError{
				statusCode: tt.statusCode,
				message:    fmt.Sprintf("HTTP %d", tt.statusCode),
			}

			errorType := classifyPublishingError(err)

			if errorType != QueueErrorTypePermanent {
				t.Errorf("Expected QueueErrorTypePermanent for %s, got %v", tt.name, errorType)
			}
		})
	}
}

// TestClassifyPublishingError_HTTPUnknown5xx tests unknown 5xx errors
func TestClassifyPublishingError_HTTPUnknown5xx(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
	}{
		{"500 Internal Server Error", http.StatusInternalServerError},
		{"501 Not Implemented", http.StatusNotImplemented},
		{"505 HTTP Version Not Supported", http.StatusHTTPVersionNotSupported},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := &mockHTTPError{
				statusCode: tt.statusCode,
				message:    fmt.Sprintf("HTTP %d", tt.statusCode),
			}

			errorType := classifyPublishingError(err)

			if errorType != QueueErrorTypePermanent {
				t.Errorf("Expected QueueErrorTypePermanent for unknown 5xx %s, got %v", tt.name, errorType)
			}
		})
	}
}

// TestClassifyPublishingError_StringHTTPTransient tests string-based HTTP error parsing (transient)
func TestClassifyPublishingError_StringHTTPTransient(t *testing.T) {
	tests := []struct {
		name    string
		errMsg  string
		wantErr QueueErrorType
	}{
		{"408 in message", "HTTP 408 Request Timeout", QueueErrorTypeTransient},
		{"429 in message", "received 429 Too Many Requests", QueueErrorTypeTransient},
		{"502 in message", "status code: 502", QueueErrorTypeTransient},
		{"503 in message", "HTTP 503 Service Unavailable", QueueErrorTypeTransient},
		{"504 in message", "status code: 504", QueueErrorTypeTransient},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := errors.New(tt.errMsg)

			errorType := classifyPublishingError(err)

			if errorType != tt.wantErr {
				t.Errorf("Expected %v for %s, got %v", tt.wantErr, tt.name, errorType)
			}
		})
	}
}

// TestClassifyPublishingError_StringHTTPPermanent tests string-based HTTP error parsing (permanent)
func TestClassifyPublishingError_StringHTTPPermanent(t *testing.T) {
	tests := []struct {
		name    string
		errMsg  string
		wantErr QueueErrorType
	}{
		{"400 in message", "HTTP 400 Bad Request", QueueErrorTypePermanent},
		{"401 in message", "status code: 401", QueueErrorTypePermanent},
		{"403 in message", "HTTP 403 Forbidden", QueueErrorTypePermanent},
		{"404 in message", "HTTP 404 Not Found", QueueErrorTypePermanent},
		{"405 in message", "status code: 405", QueueErrorTypePermanent},
		{"422 in message", "HTTP 422 Unprocessable Entity", QueueErrorTypePermanent},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := errors.New(tt.errMsg)

			errorType := classifyPublishingError(err)

			if errorType != tt.wantErr {
				t.Errorf("Expected %v for %s, got %v", tt.wantErr, tt.name, errorType)
			}
		})
	}
}

// TestClassifyPublishingError_NetworkTimeout tests network timeout errors
func TestClassifyPublishingError_NetworkTimeout(t *testing.T) {
	// Simulate network timeout
	err := &net.OpError{
		Op:  "dial",
		Err: &timeoutError{},
	}

	errorType := classifyPublishingError(err)

	if errorType != QueueErrorTypeTransient {
		t.Errorf("Expected QueueErrorTypeTransient for network timeout, got %v", errorType)
	}
}

// timeoutError is a mock net.Error that reports timeout
type timeoutError struct{}

func (e *timeoutError) Error() string   { return "i/o timeout" }
func (e *timeoutError) Timeout() bool   { return true }
func (e *timeoutError) Temporary() bool { return true }

// TestClassifyPublishingError_DNSError tests DNS errors
func TestClassifyPublishingError_DNSError(t *testing.T) {
	err := &net.DNSError{
		Err:  "no such host",
		Name: "invalid.example.com",
	}

	errorType := classifyPublishingError(err)

	if errorType != QueueErrorTypeTransient {
		t.Errorf("Expected QueueErrorTypeTransient for DNS error, got %v", errorType)
	}
}

// TestClassifyPublishingError_ConnectionRefused tests connection refused errors
func TestClassifyPublishingError_ConnectionRefused(t *testing.T) {
	err := &net.OpError{
		Op:  "dial",
		Err: syscall.ECONNREFUSED,
	}

	errorType := classifyPublishingError(err)

	if errorType != QueueErrorTypeTransient {
		t.Errorf("Expected QueueErrorTypeTransient for connection refused, got %v", errorType)
	}
}

// TestClassifyPublishingError_ConnectionReset tests connection reset errors
func TestClassifyPublishingError_ConnectionReset(t *testing.T) {
	err := fmt.Errorf("connection reset: %w", syscall.ECONNRESET)

	errorType := classifyPublishingError(err)

	if errorType != QueueErrorTypeTransient {
		t.Errorf("Expected QueueErrorTypeTransient for connection reset, got %v", errorType)
	}
}

// TestClassifyPublishingError_NilError tests nil error handling
func TestClassifyPublishingError_NilError(t *testing.T) {
	errorType := classifyPublishingError(nil)

	if errorType != QueueErrorTypeUnknown {
		t.Errorf("Expected QueueErrorTypeUnknown for nil error, got %v", errorType)
	}
}

// TestClassifyPublishingError_GenericError tests generic unknown errors
func TestClassifyPublishingError_GenericError(t *testing.T) {
	err := errors.New("something went wrong")

	errorType := classifyPublishingError(err)

	if errorType != QueueErrorTypeUnknown {
		t.Errorf("Expected QueueErrorTypeUnknown for generic error, got %v", errorType)
	}
}

// TestClassifyQueueHTTPError_DirectStatusCodes tests direct status code classification
func TestClassifyQueueHTTPError_DirectStatusCodes(t *testing.T) {
	tests := []struct {
		statusCode int
		want       QueueErrorType
		name       string
	}{
		{200, QueueErrorTypeUnknown, "200 OK"},
		{408, QueueErrorTypeTransient, "408 Timeout"},
		{429, QueueErrorTypeTransient, "429 Rate Limit"},
		{400, QueueErrorTypePermanent, "400 Bad Request"},
		{401, QueueErrorTypePermanent, "401 Unauthorized"},
		{502, QueueErrorTypeTransient, "502 Bad Gateway"},
		{503, QueueErrorTypeTransient, "503 Service Unavailable"},
		{504, QueueErrorTypeTransient, "504 Gateway Timeout"},
		{500, QueueErrorTypePermanent, "500 Internal Server Error"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := classifyQueueHTTPError(tt.statusCode)

			if got != tt.want {
				t.Errorf("classifyQueueHTTPError(%d) = %v, want %v", tt.statusCode, got, tt.want)
			}
		})
	}
}

// TestClassifyQueueHTTPErrorString_VariousFormats tests string parsing with various formats
func TestClassifyQueueHTTPErrorString_VariousFormats(t *testing.T) {
	tests := []struct {
		errMsg string
		want   QueueErrorType
		name   string
	}{
		{"HTTP 404 Not Found", QueueErrorTypePermanent, "HTTP format"},
		{"status code: 503", QueueErrorTypeTransient, "status code format"},
		{"received 429 Too Many Requests", QueueErrorTypeTransient, "received format"},
		{"error: 401 Unauthorized", QueueErrorTypePermanent, "error format"},
		{"no status code here", QueueErrorTypeUnknown, "no status code"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := classifyQueueHTTPErrorString(tt.errMsg)

			if got != tt.want {
				t.Errorf("classifyQueueHTTPErrorString(%q) = %v, want %v", tt.errMsg, got, tt.want)
			}
		})
	}
}

// TestClassifyPublishingError_TemporaryNetworkError tests temporary network errors
func TestClassifyPublishingError_TemporaryNetworkError(t *testing.T) {
	err := &net.OpError{
		Op:  "read",
		Err: &temporaryError{},
	}

	errorType := classifyPublishingError(err)

	if errorType != QueueErrorTypeTransient {
		t.Errorf("Expected QueueErrorTypeTransient for temporary network error, got %v", errorType)
	}
}

// temporaryError is a mock net.Error that reports temporary
type temporaryError struct{}

func (e *temporaryError) Error() string   { return "temporary failure" }
func (e *temporaryError) Timeout() bool   { return false }
func (e *temporaryError) Temporary() bool { return true }

// BenchmarkClassifyPublishingError benchmarks error classification
func BenchmarkClassifyPublishingError(b *testing.B) {
	err := &mockHTTPError{
		statusCode: 503,
		message:    "HTTP 503 Service Unavailable",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = classifyPublishingError(err)
	}
}

// BenchmarkClassifyQueueHTTPError benchmarks direct HTTP status code classification
func BenchmarkClassifyQueueHTTPError(b *testing.B) {
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = classifyQueueHTTPError(503)
	}
}
