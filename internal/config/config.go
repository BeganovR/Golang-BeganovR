package config

import (
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/caarlos0/env/v11"
)

type Config struct {
	HTTPAddr        string        `env:"HTTP_ADDR,required,notEmpty"`
	LogLevel        slog.Level    `env:"LOG_LEVEL,required,notEmpty"`
	ShutdownTimeout time.Duration `env:"SHUTDOWN_TIMEOUT,required,notEmpty"`

	HTTPReadTimeout       time.Duration `env:"HTTP_READ_TIMEOUT" envDefault:"5s"`
	HTTPReadHeaderTimeout time.Duration `env:"HTTP_READ_HEADER_TIMEOUT" envDefault:"5s"`
	HTTPWriteTimeout      time.Duration `env:"HTTP_WRITE_TIMEOUT" envDefault:"10s"`
	HTTPIdleTimeout       time.Duration `env:"HTTP_IDLE_TIMEOUT" envDefault:"60s"`

	IdempotencyKeyTTL        time.Duration `env:"IDEMPOTENCY_KEY_TTL" envDefault:"24h"`
	IdempotencyPurgeInterval time.Duration `env:"IDEMPOTENCY_PURGE_INTERVAL" envDefault:"1h"`

	DatabaseURL             string        `env:"DATABASE_URL,required,notEmpty"`
	DatabaseMaxConns        int32         `env:"DATABASE_MAX_CONNS,required,notEmpty"`
	DatabaseMinConns        int32         `env:"DATABASE_MIN_CONNS,required,notEmpty"`
	DatabaseMaxConnLifetime time.Duration `env:"DATABASE_MAX_CONN_LIFETIME,required,notEmpty"`
	DatabaseConnectTimeout  time.Duration `env:"DATABASE_CONNECT_TIMEOUT,required,notEmpty"`
	DatabaseQueryTimeout    time.Duration `env:"DATABASE_QUERY_TIMEOUT,required,notEmpty"`
}

func Load() (Config, error) {
	cfg, err := env.ParseAs[Config]()
	if err != nil {
		return Config{}, err
	}
	if err := cfg.validate(); err != nil {
		return Config{}, fmt.Errorf("invalid config: %w", err)
	}
	return cfg, nil
}

func (c Config) validate() error {
	durations := []struct {
		name  string
		value time.Duration
	}{
		{"SHUTDOWN_TIMEOUT", c.ShutdownTimeout},
		{"HTTP_READ_TIMEOUT", c.HTTPReadTimeout},
		{"HTTP_READ_HEADER_TIMEOUT", c.HTTPReadHeaderTimeout},
		{"HTTP_WRITE_TIMEOUT", c.HTTPWriteTimeout},
		{"HTTP_IDLE_TIMEOUT", c.HTTPIdleTimeout},
		{"IDEMPOTENCY_KEY_TTL", c.IdempotencyKeyTTL},
		{"IDEMPOTENCY_PURGE_INTERVAL", c.IdempotencyPurgeInterval},
		{"DATABASE_MAX_CONN_LIFETIME", c.DatabaseMaxConnLifetime},
		{"DATABASE_CONNECT_TIMEOUT", c.DatabaseConnectTimeout},
		{"DATABASE_QUERY_TIMEOUT", c.DatabaseQueryTimeout},
	}

	var errs []error
	for _, d := range durations {
		if d.value <= 0 {
			errs = append(errs, fmt.Errorf("%s must be positive, got %s", d.name, d.value))
		}
	}
	if c.DatabaseMaxConns < 1 {
		errs = append(errs, fmt.Errorf("DATABASE_MAX_CONNS must be at least 1, got %d", c.DatabaseMaxConns))
	}
	if c.DatabaseMinConns < 0 || c.DatabaseMinConns > c.DatabaseMaxConns {
		errs = append(errs, fmt.Errorf("DATABASE_MIN_CONNS must be between 0 and DATABASE_MAX_CONNS, got %d", c.DatabaseMinConns))
	}
	return errors.Join(errs...)
}
