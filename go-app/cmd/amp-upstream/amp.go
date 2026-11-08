package main

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/prometheus/alertmanager/provider/mem"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/common/model"

	appconfig "github.com/ipiton/AMP/internal/config"
	"github.com/ipiton/AMP/internal/core"
	coreinv "github.com/ipiton/AMP/internal/core/investigation"
	"github.com/ipiton/AMP/internal/core/services"
	dbmigrations "github.com/ipiton/AMP/internal/database"
	"github.com/ipiton/AMP/internal/database/postgres"
	"github.com/ipiton/AMP/internal/infrastructure"
	"github.com/ipiton/AMP/internal/infrastructure/cache"
	investigationinfra "github.com/ipiton/AMP/internal/infrastructure/investigation"
	"github.com/ipiton/AMP/internal/infrastructure/llm"
	investigationrepo "github.com/ipiton/AMP/internal/infrastructure/repository"
	"github.com/ipiton/AMP/internal/upstreamai"
	"github.com/ipiton/AMP/internal/upstreamcore"
)

const (
	// investigationStopTimeout bounds the drain of in-flight investigations
	// on shutdown.
	investigationStopTimeout = 5 * time.Second
	// migrationTimeout bounds the migrations at start, the advisory lock
	// wait included: delivery starts on time even with the database stuck.
	migrationTimeout = time.Minute
)

// newAMPLayer builds AMP's own processing from the AMP config and hooks it
// into opts (ADR-019): the alert history in PostgreSQL with
// profile standard, and with llm.enabled the classification and, with
// investigation.enabled and the database up, the investigation queue. The
// delivery never depends on it: a database that is down at start leaves
// the history and investigations off. stop tears the layer down; call it
// after the core's Stop.
func newAMPLayer(logger *slog.Logger, cfg *appconfig.Config, opts *upstreamcore.Options) (stop func(), err error) {
	var stops []func()
	stop = func() {
		for i := len(stops) - 1; i >= 0; i-- {
			stops[i]()
		}
	}

	pool := connectDatabase(logger, cfg)
	var history *upstreamai.History
	if pool != nil {
		stops = append(stops, func() { _ = pool.Close() })
		history, err = newHistory(logger, pool, opts.Registerer)
		if err != nil {
			stop()
			return nil, err
		}
		stops = append(stops, history.Stop)
	}

	var classifier *upstreamai.Classifier
	if cfg.LLM.Enabled {
		llmClient := llm.NewHTTPLLMClient(llmConfig(cfg.LLM), logger)
		var investigations upstreamai.Submitter
		if queue, closeTools := newInvestigations(logger, cfg, pool, llmClient, opts.Registerer); queue != nil {
			// The tools outlive the queue's workers.
			stops = append(stops, closeTools)
			// Stopped after the classifier, the queue's only sender (its
			// workers and PostStore, which stops submitting at Stop):
			// Submit on a stopped queue panics.
			stops = append(stops, func() {
				if err := queue.Stop(investigationStopTimeout); err != nil {
					logger.Warn("investigation queue did not stop in time", "err", err)
				}
			})
			investigations = queue
		}
		classifier, err = newClassifier(logger, cfg.LLM, llmClient, investigations, opts.Registerer)
		if err != nil {
			stop()
			return nil, err
		}
		stops = append(stops, classifier.Stop)
	} else {
		logger.Info("alert classification and investigation disabled (llm.enabled=false)")
	}

	if history != nil || classifier != nil {
		opts.NewAlertCallback = func(mutes func(context.Context, model.LabelSet) bool) mem.AlertStoreCallback {
			var callbacks []mem.AlertStoreCallback
			if history != nil {
				callbacks = append(callbacks, history)
			}
			if classifier != nil {
				callbacks = append(callbacks, classifier.AlertCallback(mutes))
			}
			return upstreamai.Chain(callbacks...)
		}
	}
	if classifier != nil {
		opts.NotifyStage = classifier.NotifyStage(cfg.LLM.NotifyWait, cfg.LLM.DropNoise)
	}
	return stop, nil
}

// connectDatabase connects to the history database and migrates it. It returns nil, with the reason logged, when the profile
// has none or the database cannot be reached.
func connectDatabase(logger *slog.Logger, cfg *appconfig.Config) *postgres.PostgresPool {
	if cfg.Profile != appconfig.ProfileStandard {
		logger.Info("alert history disabled: it needs PostgreSQL (profile standard)", "profile", cfg.Profile)
		return nil
	}
	// The pool's health checks run with the connect's context until
	// Disconnect, so it is not cancelled; the connect itself is bounded by
	// database.connect_timeout. A database that comes up later is not
	// picked up before a restart (TECH-DEBT UPSTREAM-HISTORY-PG-RECONNECT).
	pool := postgres.NewPostgresPool(appconfig.PostgresConfigFrom(cfg.Database), logger)
	if err := pool.Connect(context.Background()); err != nil {
		logger.Error("alert history and investigation disabled until restart: PostgreSQL unavailable",
			"host", cfg.Database.Host, "database", cfg.Database.Database, "err", err)
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), migrationTimeout)
	defer cancel()
	if err := dbmigrations.RunMigrations(ctx, pool, logger); err != nil {
		_ = pool.Close()
		logger.Error("alert history and investigation disabled until restart: database migrations failed", "err", err)
		return nil
	}
	return pool
}

// newHistory writes alert events through the deduplication service.
func newHistory(logger *slog.Logger, pool *postgres.PostgresPool, reg prometheus.Registerer) (*upstreamai.History, error) {
	storage, err := infrastructure.NewPostgresStorageAdapter(pool.SharePool(), logger)
	if err != nil {
		return nil, fmt.Errorf("failed to create history storage: %w", err)
	}
	// The service logs every processed alert at Info; on the ingest of
	// every Put that drowns the log, so only its warnings and errors pass.
	dedup, err := services.NewDeduplicationService(&services.DeduplicationConfig{
		Storage: storage,
		Logger:  slog.New(warnOnly{logger.Handler()}),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create history deduplication: %w", err)
	}
	history := upstreamai.NewHistory(upstreamai.HistoryConfig{
		Record: func(ctx context.Context, a *core.Alert) error {
			_, err := dedup.ProcessAlert(ctx, a)
			return err
		},
		Logger:     logger,
		Registerer: reg,
	})
	logger.Info("alert history enabled")
	return history, nil
}

// newInvestigations starts the investigation queue, or returns nil, with the
// reason logged, when it is off. The queue dedups per replica: the shared
// claim needs Redis (UPSTREAM-AI-HA-DEDUP). With llm.agent_mode it runs the
// agentic loop (agent.go); closeTools releases the loop's tools after the
// queue has stopped.
func newInvestigations(logger *slog.Logger, cfg *appconfig.Config, pool *postgres.PostgresPool, client *llm.HTTPLLMClient, reg prometheus.Registerer) (queue *investigationinfra.InvestigationQueue, closeTools func()) {
	inv := cfg.Investigation
	switch {
	case !inv.Enabled:
		logger.Info("alert investigation disabled (investigation.enabled=false)")
		return nil, nil
	case pool == nil:
		logger.Warn("alert investigation disabled: it stores its results in PostgreSQL, which is unavailable")
		return nil, nil
	}

	qCfg := investigationinfra.DefaultQueueConfig()
	if inv.WorkerCount > 0 {
		qCfg.WorkerCount = inv.WorkerCount
	}
	if inv.QueueSize > 0 {
		qCfg.QueueSize = inv.QueueSize
	}
	if inv.MaxRetries > 0 {
		qCfg.MaxRetries = inv.MaxRetries
	}
	if inv.RetryInterval > 0 {
		qCfg.RetryInterval = inv.RetryInterval
	}
	if inv.LLMTimeout > 0 {
		qCfg.LLMTimeout = inv.LLMTimeout
	}
	qCfg.OnlyFiring = inv.OnlyFiring
	if !inv.OnlyFiring {
		logger.Info("investigation.only_firing=false has no effect on amp-upstream: only firing alerts are classified, so only they are investigated")
	}

	repo := investigationrepo.NewPostgresInvestigationRepositoryWithRegisterer(pool.SharePool(), logger, reg)
	queue = investigationinfra.NewInvestigationQueue(repo, client, qCfg, logger, reg)
	closeTools = func() {}
	if cfg.LLM.AgentMode {
		var loop *coreinv.AgentLoop
		loop, closeTools = newAgentLoop(logger, cfg, client)
		queue.SetAgentLoop(loop)
	} else if inv.Runbooks.Enabled {
		logger.Warn("investigation.runbooks.enabled is set but runbooks require llm.agent_mode=true; runbooks disabled")
	}
	queue.Start()
	logger.Info("alert investigation enabled", "workers", qCfg.WorkerCount, "queue_size", qCfg.QueueSize, "agent_mode", cfg.LLM.AgentMode)
	return queue, closeTools
}

// warnOnly passes a handler's records from Warn up.
type warnOnly struct{ slog.Handler }

func (w warnOnly) Enabled(ctx context.Context, l slog.Level) bool {
	return l >= slog.LevelWarn && w.Handler.Enabled(ctx, l)
}

func (w warnOnly) WithAttrs(attrs []slog.Attr) slog.Handler {
	return warnOnly{w.Handler.WithAttrs(attrs)}
}

func (w warnOnly) WithGroup(name string) slog.Handler {
	return warnOnly{w.Handler.WithGroup(name)}
}

func llmConfig(c appconfig.LLMConfig) llm.Config {
	llmCfg := llm.DefaultConfig()
	llmCfg.Provider = c.Provider
	llmCfg.BaseURL = c.BaseURL
	llmCfg.APIKey = c.APIKey
	llmCfg.Model = c.Model
	llmCfg.MaxTokens = c.MaxTokens
	llmCfg.Temperature = c.Temperature
	llmCfg.Timeout = c.Timeout
	llmCfg.MaxRetries = c.MaxRetries
	return llmCfg
}

// newClassifier builds the LLM classification; with investigations set,
// every classified alert is queued for investigation.
func newClassifier(logger *slog.Logger, c appconfig.LLMConfig, client *llm.HTTPLLMClient, investigations upstreamai.Submitter, reg prometheus.Registerer) (*upstreamai.Classifier, error) {
	classCfg := services.DefaultClassificationConfig()
	classCfg.EnableLLM = true
	if c.Timeout > 0 {
		classCfg.LLMTimeout = c.Timeout
	}
	// In-memory L2 cache: a single instance until the gossip cluster
	// (UPSTREAM-HA-MEMBERLIST) decides what stays in Redis.
	svc, err := services.NewClassificationService(services.ClassificationServiceConfig{
		LLMClient: client,
		Cache:     cache.NewMemoryCache(logger),
		Config:    classCfg,
		Logger:    logger,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create classification service: %w", err)
	}

	classifier := upstreamai.New(upstreamai.Config{
		Service:        svc,
		Investigations: investigations,
		Logger:         logger,
		Registerer:     reg,
	})
	logger.Info("alert classification enabled",
		"provider", c.Provider,
		"model", c.Model,
		"notify_wait", c.NotifyWait,
		"drop_noise", c.DropNoise)
	return classifier, nil
}
