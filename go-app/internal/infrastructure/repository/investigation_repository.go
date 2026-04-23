package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/ipiton/AMP/internal/core"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// InvestigationMetrics contains Prometheus metrics for investigation repository operations.
type InvestigationMetrics struct {
	QueryDuration *prometheus.HistogramVec
	QueryErrors   *prometheus.CounterVec
}

// PostgresInvestigationRepository implements core.InvestigationRepository for PostgreSQL.
type PostgresInvestigationRepository struct {
	pool    *pgxpool.Pool
	logger  *slog.Logger
	metrics *InvestigationMetrics
}

// NewPostgresInvestigationRepository creates a new investigation repository.
func NewPostgresInvestigationRepository(pool *pgxpool.Pool, logger *slog.Logger) *PostgresInvestigationRepository {
	return NewPostgresInvestigationRepositoryWithRegisterer(pool, logger, prometheus.DefaultRegisterer)
}

// NewPostgresInvestigationRepositoryWithRegisterer allows injecting a custom Prometheus registerer.
func NewPostgresInvestigationRepositoryWithRegisterer(pool *pgxpool.Pool, logger *slog.Logger, reg prometheus.Registerer) *PostgresInvestigationRepository {
	if logger == nil {
		logger = slog.Default()
	}
	if reg == nil {
		reg = prometheus.DefaultRegisterer
	}

	factory := promauto.With(reg)
	metrics := &InvestigationMetrics{
		QueryDuration: factory.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: "amp",
				Subsystem: "investigation_repository",
				Name:      "query_duration_seconds",
				Help:      "Duration of investigation repository queries",
				Buckets:   []float64{.001, .005, .01, .025, .05, .1, .25, .5, 1},
			},
			[]string{"operation", "status"},
		),
		QueryErrors: factory.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: "amp",
				Subsystem: "investigation_repository",
				Name:      "query_errors_total",
				Help:      "Total number of investigation repository query errors",
			},
			[]string{"operation", "error_type"},
		),
	}

	return &PostgresInvestigationRepository{
		pool:    pool,
		logger:  logger,
		metrics: metrics,
	}
}

// Create inserts a new investigation record with status=queued.
func (r *PostgresInvestigationRepository) Create(ctx context.Context, inv *core.Investigation) error {
	start := time.Now()
	op := "create"

	query := `
		INSERT INTO alert_investigations
			(id, fingerprint, classification_id, status, retry_count, queued_at, created_at, updated_at)
		VALUES
			($1, $2, $3, $4, 0, $5, $5, $5)`

	now := time.Now().UTC()
	_, err := r.pool.Exec(ctx, query,
		inv.ID,
		inv.Fingerprint,
		inv.ClassificationID,
		string(core.InvestigationQueued),
		now,
	)

	r.metrics.QueryDuration.WithLabelValues(op, statusLabel(err)).Observe(time.Since(start).Seconds())
	if err != nil {
		r.metrics.QueryErrors.WithLabelValues(op, "database").Inc()
		return fmt.Errorf("investigation create: %w", err)
	}
	return nil
}

// UpdateStatus sets the lifecycle status (and started_at when transitioning to processing).
func (r *PostgresInvestigationRepository) UpdateStatus(ctx context.Context, id string, status core.InvestigationStatus) error {
	start := time.Now()
	op := "update_status"

	now := time.Now().UTC()
	var err error
	if status == core.InvestigationProcessing {
		_, err = r.pool.Exec(ctx,
			`UPDATE alert_investigations SET status=$1, started_at=$2, updated_at=$2 WHERE id=$3`,
			string(status), now, id,
		)
	} else {
		_, err = r.pool.Exec(ctx,
			`UPDATE alert_investigations SET status=$1, updated_at=$2 WHERE id=$3`,
			string(status), now, id,
		)
	}

	r.metrics.QueryDuration.WithLabelValues(op, statusLabel(err)).Observe(time.Since(start).Seconds())
	if err != nil {
		r.metrics.QueryErrors.WithLabelValues(op, "database").Inc()
		return fmt.Errorf("investigation update status: %w", err)
	}
	return nil
}

// SaveResult stores the LLM findings and sets status=completed.
func (r *PostgresInvestigationRepository) SaveResult(ctx context.Context, id string, result *core.InvestigationResult) error {
	start := time.Now()
	op := "save_result"

	findingsJSON, err := json.Marshal(result.Findings)
	if err != nil {
		return fmt.Errorf("marshal findings: %w", err)
	}
	recsJSON, err := json.Marshal(result.Recommendations)
	if err != nil {
		return fmt.Errorf("marshal recommendations: %w", err)
	}

	now := time.Now().UTC()
	_, err = r.pool.Exec(ctx, `
		UPDATE alert_investigations SET
			status           = 'completed',
			summary          = $1,
			findings         = $2,
			recommendations  = $3,
			confidence       = $4,
			llm_model        = $5,
			prompt_tokens    = $6,
			completion_tokens= $7,
			processing_time  = $8,
			completed_at     = $9,
			updated_at       = $9
		WHERE id = $10`,
		result.Summary,
		findingsJSON,
		recsJSON,
		result.Confidence,
		result.LLMModel,
		result.PromptTokens,
		result.CompletionTokens,
		result.ProcessingTime,
		now,
		id,
	)

	r.metrics.QueryDuration.WithLabelValues(op, statusLabel(err)).Observe(time.Since(start).Seconds())
	if err != nil {
		r.metrics.QueryErrors.WithLabelValues(op, "database").Inc()
		return fmt.Errorf("investigation save result: %w", err)
	}
	return nil
}

// SaveError records failure information and increments retry_count.
func (r *PostgresInvestigationRepository) SaveError(ctx context.Context, id string, errMsg string, errType core.InvestigationErrorType) error {
	start := time.Now()
	op := "save_error"

	now := time.Now().UTC()
	_, err := r.pool.Exec(ctx, `
		UPDATE alert_investigations SET
			status        = 'failed',
			error_message = $1,
			error_type    = $2,
			retry_count   = retry_count + 1,
			updated_at    = $3
		WHERE id = $4`,
		errMsg,
		string(errType),
		now,
		id,
	)

	r.metrics.QueryDuration.WithLabelValues(op, statusLabel(err)).Observe(time.Since(start).Seconds())
	if err != nil {
		r.metrics.QueryErrors.WithLabelValues(op, "database").Inc()
		return fmt.Errorf("investigation save error: %w", err)
	}
	return nil
}

// GetLatestByFingerprint retrieves the most recent investigation for a fingerprint.
func (r *PostgresInvestigationRepository) GetLatestByFingerprint(ctx context.Context, fingerprint string) (*core.Investigation, error) {
	start := time.Now()
	op := "get_latest_by_fingerprint"

	row := r.pool.QueryRow(ctx, `
		SELECT
			id, fingerprint, classification_id, status,
			summary, findings, recommendations, confidence,
			llm_model, prompt_tokens, completion_tokens, processing_time,
			retry_count, error_message, error_type,
			queued_at, started_at, completed_at, created_at, updated_at,
			steps, iterations_count, tool_calls_count
		FROM alert_investigations
		WHERE fingerprint = $1
		ORDER BY queued_at DESC
		LIMIT 1`,
		fingerprint,
	)

	inv, err := scanInvestigation(row)
	r.metrics.QueryDuration.WithLabelValues(op, statusLabel(err)).Observe(time.Since(start).Seconds())
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		r.metrics.QueryErrors.WithLabelValues(op, "database").Inc()
		return nil, fmt.Errorf("investigation get latest: %w", err)
	}
	return inv, nil
}

// SaveAgentResult stores Phase 5B agentic loop output: result + trace (steps, iterations, tool calls).
func (r *PostgresInvestigationRepository) SaveAgentResult(ctx context.Context, id string, result *core.InvestigationResult, agentRun *core.AgentRunSummary) error {
	start := time.Now()
	op := "save_agent_result"

	findingsJSON, err := json.Marshal(result.Findings)
	if err != nil {
		return fmt.Errorf("marshal findings: %w", err)
	}
	recsJSON, err := json.Marshal(result.Recommendations)
	if err != nil {
		return fmt.Errorf("marshal recommendations: %w", err)
	}

	now := time.Now().UTC()
	_, err = r.pool.Exec(ctx, `
		UPDATE alert_investigations SET
			status            = 'completed',
			summary           = $1,
			findings          = $2,
			recommendations   = $3,
			confidence        = $4,
			llm_model         = $5,
			prompt_tokens     = $6,
			completion_tokens = $7,
			processing_time   = $8,
			steps             = $9,
			iterations_count  = $10,
			tool_calls_count  = $11,
			completed_at      = $12,
			updated_at        = $12
		WHERE id = $13`,
		result.Summary,
		findingsJSON,
		recsJSON,
		result.Confidence,
		result.LLMModel,
		result.PromptTokens,
		result.CompletionTokens,
		result.ProcessingTime,
		agentRun.StepsJSON,
		agentRun.IterationsCount,
		agentRun.ToolCallsCount,
		now,
		id,
	)

	r.metrics.QueryDuration.WithLabelValues(op, statusLabel(err)).Observe(time.Since(start).Seconds())
	if err != nil {
		r.metrics.QueryErrors.WithLabelValues(op, "database").Inc()
		return fmt.Errorf("investigation save agent result: %w", err)
	}
	return nil
}

// MoveToDLQ sets status=dlq for the given investigation.
func (r *PostgresInvestigationRepository) MoveToDLQ(ctx context.Context, id string) error {
	start := time.Now()
	op := "move_to_dlq"

	_, err := r.pool.Exec(ctx,
		`UPDATE alert_investigations SET status='dlq', updated_at=$1 WHERE id=$2`,
		time.Now().UTC(), id,
	)

	r.metrics.QueryDuration.WithLabelValues(op, statusLabel(err)).Observe(time.Since(start).Seconds())
	if err != nil {
		r.metrics.QueryErrors.WithLabelValues(op, "database").Inc()
		return fmt.Errorf("investigation move to dlq: %w", err)
	}
	return nil
}

// scanInvestigation reads a row into an Investigation struct.
func scanInvestigation(row pgx.Row) (*core.Investigation, error) {
	inv := &core.Investigation{}
	var (
		findingsJSON []byte
		recsJSON     []byte
		stepsJSON    []byte
		summary      *string
		confidence   *float64
		llmModel     *string
		promptTok    *int
		completeTok  *int
		procTime     *float64
		errMsg       *string
		errType      *string
		status       string
	)

	err := row.Scan(
		&inv.ID,
		&inv.Fingerprint,
		&inv.ClassificationID,
		&status,
		&summary,
		&findingsJSON,
		&recsJSON,
		&confidence,
		&llmModel,
		&promptTok,
		&completeTok,
		&procTime,
		&inv.RetryCount,
		&errMsg,
		&errType,
		&inv.QueuedAt,
		&inv.StartedAt,
		&inv.CompletedAt,
		&inv.CreatedAt,
		&inv.UpdatedAt,
		&stepsJSON,
		&inv.IterationsCount,
		&inv.ToolCallsCount,
	)
	if err != nil {
		return nil, err
	}

	inv.Status = core.InvestigationStatus(status)
	inv.ErrorMessage = errMsg
	if errType != nil {
		et := core.InvestigationErrorType(*errType)
		inv.ErrorType = &et
	}

	if stepsJSON != nil {
		inv.Steps = stepsJSON
	}

	if summary != nil || findingsJSON != nil || recsJSON != nil {
		result := &core.InvestigationResult{}
		if summary != nil {
			result.Summary = *summary
		}
		if findingsJSON != nil {
			if err := json.Unmarshal(findingsJSON, &result.Findings); err != nil {
				result.Findings = nil
			}
		}
		if recsJSON != nil {
			if err := json.Unmarshal(recsJSON, &result.Recommendations); err != nil {
				result.Recommendations = nil
			}
		}
		if confidence != nil {
			result.Confidence = *confidence
		}
		if llmModel != nil {
			result.LLMModel = *llmModel
		}
		if promptTok != nil {
			result.PromptTokens = *promptTok
		}
		if completeTok != nil {
			result.CompletionTokens = *completeTok
		}
		if procTime != nil {
			result.ProcessingTime = *procTime
		}
		inv.Result = result
	}

	return inv, nil
}

func statusLabel(err error) string {
	if err == nil {
		return "success"
	}
	return "error"
}
