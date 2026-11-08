package main

import (
	"database/sql"
	"log/slog"

	_ "github.com/jackc/pgx/v5/stdlib" // "pgx" driver for the database_query tool

	appconfig "github.com/ipiton/AMP/internal/config"
	coreinv "github.com/ipiton/AMP/internal/core/investigation"
	invrunbooks "github.com/ipiton/AMP/internal/infrastructure/investigation/runbooks"
	invtools "github.com/ipiton/AMP/internal/infrastructure/investigation/tools"
)

// newAgentLoop builds the agentic investigation loop for llm.agent_mode: the
// tools configured under investigation.tools and the runbooks. A tool that
// fails to initialise is skipped with a warning; investigations still run.
// closeTools releases the database_query tool's handle; call it after the
// queue has stopped.
func newAgentLoop(logger *slog.Logger, cfg *appconfig.Config, client coreinv.AgentLLMClient) (loop *coreinv.AgentLoop, closeTools func()) {
	registry, closeTools := newToolRegistry(logger, cfg.Investigation.Tools)
	loop = coreinv.NewAgentLoop(client, registry, coreinv.DefaultAgentLoopConfig())
	configureRunbooks(logger, cfg.Investigation.Runbooks, loop)
	logger.Info("Agentic investigation loop enabled")
	return loop, closeTools
}

// newToolRegistry registers the tools configured under investigation.tools.
// closeTools releases the database_query tool's handle.
func newToolRegistry(logger *slog.Logger, toolsCfg appconfig.InvestigationToolsConfig) (registry *coreinv.ToolRegistry, closeTools func()) {
	closeTools = func() {}
	registry = coreinv.NewToolRegistry()

	if toolsCfg.Prometheus != nil && toolsCfg.Prometheus.Endpoint != "" {
		registry.Register(invtools.NewPrometheusTool(toolsCfg.Prometheus))
		logger.Info("Prometheus investigation tool registered", "endpoint", toolsCfg.Prometheus.Endpoint)
	}

	if toolsCfg.Loki != nil && toolsCfg.Loki.Endpoint != "" {
		registry.Register(invtools.NewLokiTool(toolsCfg.Loki))
		logger.Info("Loki investigation tool registered", "endpoint", toolsCfg.Loki.Endpoint)
	}

	if toolsCfg.Kubernetes != nil && toolsCfg.Kubernetes.Enabled {
		allowed := toolsCfg.Kubernetes.AllowedNamespaces
		k8sTool, err := invtools.NewKubernetesToolFromConfig(toolsCfg.Kubernetes.Kubeconfig, allowed)
		if err != nil {
			logger.Warn("Kubernetes investigation tool init failed, skipping", "error", err)
		} else {
			registry.Register(k8sTool)
			if len(allowed) == 0 {
				// The namespace label is written by whoever sends the alert,
				// so the default bounds an honest alert, not an attacker.
				logger.Warn("Kubernetes investigation tool registered with an empty allowed_namespaces: "+
					"it reads the namespace named in the alert's `namespace` label, which any alert sender controls; "+
					"set investigation.tools.kubernetes.allowed_namespaces", "allowed_namespaces", "alert namespace only")
			} else {
				logger.Info("Kubernetes investigation tool registered", "allowed_namespaces", allowed)
			}
		}
	}

	if toolsCfg.Database != nil && toolsCfg.Database.Enabled {
		// Its own database, never AMP's pool (L-9): AMP's pg_stat_activity
		// says nothing about the alerting service and leaks AMP's queries to
		// the LLM provider. sql.Open does not connect; a bad DSN shows up as
		// a tool error.
		if toolsCfg.Database.DSN == "" {
			logger.Warn("database_query investigation tool not registered: investigation.tools.database.dsn is empty")
		} else if db, err := sql.Open("pgx", toolsCfg.Database.DSN); err != nil {
			logger.Warn("database_query investigation tool init failed, skipping", "error", err)
		} else {
			db.SetMaxOpenConns(2)
			registry.Register(invtools.NewDatabaseTool(db))
			closeTools = func() { _ = db.Close() }
			logger.Info("Database investigation tool registered")
		}
	}

	return registry, closeTools
}

// configureRunbooks loads investigation runbooks into loop. A missing or
// unreadable runbooks directory disables runbooks with a warning instead of
// failing startup.
func configureRunbooks(logger *slog.Logger, rbCfg appconfig.InvestigationRunbooksConfig, loop *coreinv.AgentLoop) {
	if !rbCfg.Enabled {
		return
	}
	set, err := invrunbooks.LoadDir(rbCfg.Path, logger)
	if err != nil {
		logger.Warn("Investigation runbooks unavailable, continuing without runbooks", "path", rbCfg.Path, "error", err)
		return
	}
	if set.Len() == 0 {
		logger.Info("No investigation runbooks loaded, runbook injection inactive", "path", rbCfg.Path)
		return
	}
	loop.SetRunbooks(set, rbCfg.EffectiveMaxRunbooks(), rbCfg.EffectiveMaxChars())
	logger.Info("Investigation runbooks enabled",
		"path", rbCfg.Path,
		"runbooks", set.Len(),
		"max_runbooks", rbCfg.EffectiveMaxRunbooks(),
		"max_chars", rbCfg.EffectiveMaxChars(),
	)
}
