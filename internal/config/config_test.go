package config_test

import (
	"testing"
	"time"

	"github.com/BeganovR/Golang-BeganovR/internal/config"
)

func TestLoad(t *testing.T) {
	const minConnsEnv = "DATABASE_MIN_CONNS"
	for _, tc := range []struct {
		name  string
		key   string
		value string
	}{
		{name: "valid"},
		{name: "empty database", key: "DATABASE_URL"},
		{name: "empty address", key: "HTTP_ADDR"},
		{name: "invalid log level", key: "LOG_LEVEL", value: "verbose"},
		{name: "zero shutdown timeout", key: "SHUTDOWN_TIMEOUT", value: "0s"},
		{name: "invalid connection timeout", key: "DATABASE_CONNECT_TIMEOUT", value: "bad"},
		{name: "zero query timeout", key: "DATABASE_QUERY_TIMEOUT", value: "0s"},
		{name: "zero pool size", key: "DATABASE_MAX_CONNS", value: "0"},
		{name: "negative min connections", key: minConnsEnv, value: "-1"},
		{name: "min exceeds max", key: minConnsEnv, value: "11"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for key, value := range map[string]string{
				"HTTP_ADDR": ":8080", "LOG_LEVEL": "info", "SHUTDOWN_TIMEOUT": "10s",
				"DATABASE_URL":       "postgres://localhost/tripgo?sslmode=disable",
				"DATABASE_MAX_CONNS": "10", minConnsEnv: "2",
				"DATABASE_MAX_CONN_LIFETIME": "30m", "DATABASE_CONNECT_TIMEOUT": "5s", "DATABASE_QUERY_TIMEOUT": "3s",
				"HTTP_READ_TIMEOUT": "5s", "HTTP_READ_HEADER_TIMEOUT": "5s", "HTTP_WRITE_TIMEOUT": "10s", "HTTP_IDLE_TIMEOUT": "60s",
				"IDEMPOTENCY_KEY_TTL": "24h", "IDEMPOTENCY_PURGE_INTERVAL": "1h", "OTEL_SERVICE_NAME": "trip-service",
			} {
				t.Setenv(key, value)
			}
			if tc.key != "" {
				t.Setenv(tc.key, tc.value)
			}
			cfg, err := config.Load()
			if tc.key != "" {
				if err == nil {
					t.Fatalf("expected configuration error for %s, got %v", tc.key, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("load valid configuration: %v", err)
			}
			if cfg.DatabaseQueryTimeout != 3*time.Second || cfg.ShutdownTimeout != 10*time.Second || cfg.DatabaseMaxConns != 10 {
				t.Fatal("configuration values were not loaded")
			}
		})
	}
}
