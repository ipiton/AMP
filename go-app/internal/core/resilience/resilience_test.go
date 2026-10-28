package resilience

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestWithRetryFunc_MaxRetries(t *testing.T) {
	errBoom := errors.New("boom")
	for _, tc := range []struct {
		maxRetries int
		wantCalls  int
	}{
		{maxRetries: 0, wantCalls: 1}, // 0 = no retries, not "default 3"
		{maxRetries: -1, wantCalls: 1},
		{maxRetries: 2, wantCalls: 3},
	} {
		calls := 0
		_, err := WithRetryFunc(context.Background(), &RetryPolicy{
			MaxRetries: tc.maxRetries,
			BaseDelay:  time.Millisecond,
		}, func() (int, error) {
			calls++
			return 0, errBoom
		})
		if !errors.Is(err, errBoom) {
			t.Errorf("max_retries %d: err = %v, want %v", tc.maxRetries, err, errBoom)
		}
		if calls != tc.wantCalls {
			t.Errorf("max_retries %d: calls = %d, want %d", tc.maxRetries, calls, tc.wantCalls)
		}
	}
}
