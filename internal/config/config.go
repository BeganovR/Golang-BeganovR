package config

import (
	"log/slog"
	"time"

	"github.com/caarlos0/env/v11"
)

type Config struct {
	HTTPAddr        string        `env:"HTTP_ADDR,required"`
	LogLevel        slog.Level    `env:"LOG_LEVEL,required"`
	ShutdownTimeout time.Duration `env:"SHUTDOWN_TIMEOUT,required"`

	HTTPReadTimeout       time.Duration `env:"HTTP_READ_TIMEOUT" envDefault:"5s"`
	HTTPReadHeaderTimeout time.Duration `env:"HTTP_READ_HEADER_TIMEOUT" envDefault:"5s"`
	HTTPWriteTimeout      time.Duration `env:"HTTP_WRITE_TIMEOUT" envDefault:"10s"`
	HTTPIdleTimeout       time.Duration `env:"HTTP_IDLE_TIMEOUT" envDefault:"60s"`

	DatabaseURL             string        `env:"DATABASE_URL,required"`
	DatabaseMaxConns        int32         `env:"DATABASE_MAX_CONNS,required"`
	DatabaseMinConns        int32         `env:"DATABASE_MIN_CONNS,required"`
	DatabaseMaxConnLifetime time.Duration `env:"DATABASE_MAX_CONN_LIFETIME,required"`
	DatabaseConnectTimeout  time.Duration `env:"DATABASE_CONNECT_TIMEOUT,required"`
	DatabaseQueryTimeout    time.Duration `env:"DATABASE_QUERY_TIMEOUT,required"`
}

func Load() (Config, error) {
	return env.ParseAs[Config]()
}
