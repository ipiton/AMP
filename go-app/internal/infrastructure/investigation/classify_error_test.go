package investigation

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/ipiton/AMP/internal/core"
)

func TestClassifyError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want core.InvestigationErrorType
	}{
		{"unsupported provider", fmt.Errorf("%w: llm.provider=%q", core.ErrLLMProviderUnsupported, "proxy"), core.InvestigationErrorPermanent},
		{"unsupported provider, wrapped again", fmt.Errorf("investigate: %w", fmt.Errorf("%w: x", core.ErrLLMProviderUnsupported)), core.InvestigationErrorPermanent},
		{"bad request", errors.New("HTTP 400: LLM investigation error: status 400, body: {}"), core.InvestigationErrorPermanent},
		{"server error", errors.New("HTTP 503: LLM investigation error: status 503, body: {}"), core.InvestigationErrorTransient},
		{"deadline", context.DeadlineExceeded, core.InvestigationErrorTransient},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := classifyError(tc.err); got != tc.want {
				t.Errorf("classifyError(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}
