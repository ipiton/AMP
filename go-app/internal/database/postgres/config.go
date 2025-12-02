package postgres

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

// PostgresConfig содержит все настройки для подключения к PostgreSQL
type PostgresConfig struct {
	// Connection parameters
	Host     string `yaml:"host" env:"DB_HOST"`
	Port     int    `yaml:"port" env:"DB_PORT"`
	Database string `yaml:"database" env:"DB_NAME"`
	User     string `yaml:"user" env:"DB_USER"`
	Password string `yaml:"password" env:"DB_PASSWORD"`

	// SSL configuration
	SSLMode string `yaml:"ssl_mode" env:"DB_SSL_MODE"`

	// Pool configuration
	MaxConns int32 `yaml:"max_conns" env:"DB_MAX_CONNS"`
	MinConns int32 `yaml:"min_conns" env:"DB_MIN_CONNS"`

	// Timeout configuration
	MaxConnLifetime   time.Duration `yaml:"max_conn_lifetime" env:"DB_MAX_CONN_LIFETIME"`
	MaxConnIdleTime   time.Duration `yaml:"max_conn_idle_time" env:"DB_MAX_CONN_IDLE_TIME"`
	HealthCheckPeriod time.Duration `yaml:"health_check_period" env:"DB_HEALTH_CHECK_PERIOD"`
	ConnectTimeout    time.Duration `yaml:"connect_timeout" env:"DB_CONNECT_TIMEOUT"`
}

// DefaultConfig возвращает конфигурацию с настройками по умолчанию
func DefaultConfig() *PostgresConfig {
	return &PostgresConfig{
		Host:              "localhost",
		Port:              5432,
		Database:          "alerthistory",
		User:              "alerthistory",
		Password:          "",
		SSLMode:           "disable",
		MaxConns:          20,
		MinConns:          2,
		MaxConnLifetime:   1 * time.Hour,
		MaxConnIdleTime:   5 * time.Minute,
		HealthCheckPeriod: 30 * time.Second,
		ConnectTimeout:    30 * time.Second,
	}
}

// LoadFromEnv загружает конфигурацию из переменных окружения
func LoadFromEnv() *PostgresConfig {
	config := DefaultConfig()

	if host := os.Getenv("DB_HOST"); host != "" {
		config.Host = host
	}
	if portStr := os.Getenv("DB_PORT"); portStr != "" {
		if port, err := strconv.Atoi(portStr); err == nil {
			config.Port = port
		}
	}
	if database := os.Getenv("DB_NAME"); database != "" {
		config.Database = database
	}
	if user := os.Getenv("DB_USER"); user != "" {
		config.User = user
	}
	if password := os.Getenv("DB_PASSWORD"); password != "" {
		config.Password = password
	}
	if sslMode := os.Getenv("DB_SSL_MODE"); sslMode != "" {
		config.SSLMode = sslMode
	}
	if maxConnsStr := os.Getenv("DB_MAX_CONNS"); maxConnsStr != "" {
		if maxConns, err := strconv.ParseInt(maxConnsStr, 10, 32); err == nil {
			config.MaxConns = int32(maxConns)
		}
	}
	if minConnsStr := os.Getenv("DB_MIN_CONNS"); minConnsStr != "" {
		if minConns, err := strconv.ParseInt(minConnsStr, 10, 32); err == nil {
			config.MinConns = int32(minConns)
		}
	}

	return config
}

// Validate проверяет корректность конфигурации
func (c *PostgresConfig) Validate() error {
	if c.Host == "" {
		return fmt.Errorf("database host is required")
	}
	if c.Port <= 0 || c.Port > 65535 {
		return fmt.Errorf("database port must be between 1 and 65535")
	}
	if c.Database == "" {
		return fmt.Errorf("database name is required")
	}
	if c.User == "" {
		return fmt.Errorf("database user is required")
	}
	if c.MaxConns <= 0 {
		return fmt.Errorf("max connections must be greater than 0")
	}
	if c.MinConns < 0 {
		return fmt.Errorf("min connections cannot be negative")
	}
	if c.MinConns > c.MaxConns {
		return fmt.Errorf("min connections cannot be greater than max connections")
	}
	if c.MaxConnLifetime <= 0 {
		return fmt.Errorf("max connection lifetime must be greater than 0")
	}
	if c.MaxConnIdleTime <= 0 {
		return fmt.Errorf("max connection idle time must be greater than 0")
	}
	if c.HealthCheckPeriod <= 0 {
		return fmt.Errorf("health check period must be greater than 0")
	}

	validSSLModes := map[string]bool{
		"disable":     true,
		"require":     true,
		"verify-ca":   true,
		"verify-full": true,
	}
	if !validSSLModes[c.SSLMode] {
		return fmt.Errorf("invalid SSL mode: %s", c.SSLMode)
	}

	return nil
}

// ConnectionString возвращает строку подключения для PostgreSQL
func (c *PostgresConfig) ConnectionString() string {
	return fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=%s",
		c.Host, c.Port, c.User, c.Password, c.Database, c.SSLMode)
}

// DSN возвращает Data Source Name для pgx
func (c *PostgresConfig) DSN() string {
	return fmt.Sprintf("postgres://%s:%s@%s:%d/%s?sslmode=%s",
		c.User, c.Password, c.Host, c.Port, c.Database, c.SSLMode)
}
