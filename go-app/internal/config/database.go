package config

import "github.com/ipiton/AMP/internal/database/postgres"

// PostgresConfigFrom maps AMP's database config section onto the postgres
// package's own config, applying postgres.DefaultConfig() for every value the
// operator left at zero.
func PostgresConfigFrom(cfg DatabaseConfig) *postgres.PostgresConfig {
	dbCfg := postgres.DefaultConfig()
	dbCfg.Host = cfg.Host
	dbCfg.Port = cfg.Port
	dbCfg.Database = cfg.Database
	dbCfg.User = cfg.Username
	dbCfg.Password = cfg.Password
	dbCfg.SSLMode = cfg.SSLMode
	if cfg.MaxConnections > 0 {
		dbCfg.MaxConns = int32(cfg.MaxConnections) //nolint:gosec // config-bounded, validated by ConfigValidator
	}
	if cfg.MinConnections > 0 {
		dbCfg.MinConns = int32(cfg.MinConnections) //nolint:gosec // config-bounded, validated by ConfigValidator
	}
	if cfg.MaxConnLifetime > 0 {
		dbCfg.MaxConnLifetime = cfg.MaxConnLifetime
	}
	if cfg.MaxConnIdleTime > 0 {
		dbCfg.MaxConnIdleTime = cfg.MaxConnIdleTime
	}
	if cfg.ConnectTimeout > 0 {
		dbCfg.ConnectTimeout = cfg.ConnectTimeout
	}
	return dbCfg
}
