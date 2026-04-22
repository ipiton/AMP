package investigation_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/ipiton/AMP/internal/core"
	"github.com/ipiton/AMP/internal/infrastructure/investigation"
	"github.com/prometheus/client_golang/prometheus"
)

// mockRepo satisfies core.InvestigationRepository in memory.
type mockRepo struct {
	mu     sync.Mutex
	rows   map[string]*core.Investigation
	errors map[string]error
}

func newMockRepo() *mockRepo {
	return &mockRepo{
		rows:   make(map[string]*core.Investigation),
		errors: make(map[string]error),
	}
}

func (r *mockRepo) Create(_ context.Context, inv *core.Investigation) error {
	if err := r.errors["create"]; err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	cp := *inv
	r.rows[inv.ID] = &cp
	return nil
}

func (r *mockRepo) UpdateStatus(_ context.Context, id string, status core.InvestigationStatus) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if inv, ok := r.rows[id]; ok {
		inv.Status = status
	}
	return nil
}

func (r *mockRepo) SaveResult(_ context.Context, id string, result *core.InvestigationResult) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if inv, ok := r.rows[id]; ok {
		inv.Status = core.InvestigationCompleted
		inv.Result = result
	}
	return nil
}

func (r *mockRepo) SaveError(_ context.Context, id string, errMsg string, errType core.InvestigationErrorType) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if inv, ok := r.rows[id]; ok {
		inv.Status = core.InvestigationFailed
		inv.ErrorMessage = &errMsg
		inv.RetryCount++
	}
	return nil
}

func (r *mockRepo) GetLatestByFingerprint(_ context.Context, fingerprint string) (*core.Investigation, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, inv := range r.rows {
		if inv.Fingerprint == fingerprint {
			return inv, nil
		}
	}
	return nil, nil
}

func (r *mockRepo) MoveToDLQ(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if inv, ok := r.rows[id]; ok {
		inv.Status = core.InvestigationDLQ
	}
	return nil
}

// mockLLM is a controllable InvestigationLLMClient.
type mockLLM struct {
	result *core.InvestigationResult
	err    error
}

func (m *mockLLM) InvestigateAlert(_ context.Context, _ *core.Alert, _ *core.ClassificationResult) (*core.InvestigationResult, error) {
	return m.result, m.err
}

func makeAlert() *core.Alert {
	return &core.Alert{
		Fingerprint: "abc123",
		AlertName:   "TestAlert",
		Status:      core.StatusFiring,
		StartsAt:    time.Now(),
	}
}

func newQueue(repo core.InvestigationRepository, llm investigation.InvestigationLLMClient) *investigation.InvestigationQueue {
	cfg := investigation.DefaultQueueConfig()
	cfg.WorkerCount = 1
	cfg.MaxRetries = 1
	cfg.RetryInterval = 10 * time.Millisecond
	cfg.LLMTimeout = 5 * time.Second
	return investigation.NewInvestigationQueue(repo, llm, cfg, nil, prometheus.NewRegistry())
}

func TestQueue_SubmitSuccess(t *testing.T) {
	repo := newMockRepo()
	llm := &mockLLM{
		result: &core.InvestigationResult{
			Summary:         "test summary",
			Findings:        map[string]any{"key": "val"},
			Recommendations: []string{"fix it"},
			Confidence:      0.9,
		},
	}

	q := newQueue(repo, llm)
	q.Start()

	alert := makeAlert()
	q.Submit(alert, nil)

	// Wait for worker to process.
	time.Sleep(200 * time.Millisecond)
	_ = q.Stop(2 * time.Second)

	repo.mu.Lock()
	defer repo.mu.Unlock()
	for _, inv := range repo.rows {
		if inv.Fingerprint == alert.Fingerprint {
			if inv.Status != core.InvestigationCompleted {
				t.Errorf("expected status completed, got %s", inv.Status)
			}
			return
		}
	}
	t.Error("no investigation record found for fingerprint")
}

func TestQueue_LLMErrorMovesToDLQ(t *testing.T) {
	repo := newMockRepo()
	llm := &mockLLM{err: errors.New("HTTP 503 service unavailable")}

	q := newQueue(repo, llm)
	q.Start()

	alert := makeAlert()
	q.Submit(alert, nil)

	time.Sleep(300 * time.Millisecond)
	_ = q.Stop(2 * time.Second)

	repo.mu.Lock()
	defer repo.mu.Unlock()
	for _, inv := range repo.rows {
		if inv.Fingerprint == alert.Fingerprint {
			if inv.Status != core.InvestigationDLQ {
				t.Errorf("expected status dlq, got %s", inv.Status)
			}
			return
		}
	}
	t.Error("no investigation record found for fingerprint")
}

func TestQueue_PermanentErrorStaysFailed(t *testing.T) {
	repo := newMockRepo()
	llm := &mockLLM{err: errors.New("HTTP 400 bad request")}

	q := newQueue(repo, llm)
	q.Start()

	alert := makeAlert()
	q.Submit(alert, nil)

	time.Sleep(300 * time.Millisecond)
	_ = q.Stop(2 * time.Second)

	repo.mu.Lock()
	defer repo.mu.Unlock()
	for _, inv := range repo.rows {
		if inv.Fingerprint == alert.Fingerprint {
			if inv.Status != core.InvestigationFailed {
				t.Errorf("expected status failed, got %s", inv.Status)
			}
			return
		}
	}
	t.Error("no investigation record found for fingerprint")
}

func TestQueue_FullQueueDrops(t *testing.T) {
	repo := newMockRepo()
	// LLM takes too long so queue fills up.
	llm := &mockLLM{
		result: &core.InvestigationResult{Summary: "ok", Confidence: 1.0},
	}

	cfg := investigation.DefaultQueueConfig()
	cfg.QueueSize = 1
	cfg.WorkerCount = 1
	cfg.MaxRetries = 0
	cfg.LLMTimeout = 500 * time.Millisecond

	q := investigation.NewInvestigationQueue(repo, llm, cfg, nil, prometheus.NewRegistry())
	// Don't start workers so queue fills up immediately.

	alert := makeAlert()
	q.Submit(alert, nil) // fills queue
	q.Submit(alert, nil) // should be dropped (queue size=1, already one in channel)

	_ = q.Stop(100 * time.Millisecond)
}
