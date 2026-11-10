package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"go.opentelemetry.io/otel"

	"github.com/BeganovR/Golang-BeganovR/internal/config"
	"github.com/BeganovR/Golang-BeganovR/internal/container"
	"github.com/BeganovR/Golang-BeganovR/internal/infra/telemetry"
)

var version = "dev"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-ctx.Done()
		stop()
	}()
	err := run(ctx)
	stop()
	if err != nil {
		os.Exit(1)
	}
}

func run(ctx context.Context) (err error) {
	level := new(slog.LevelVar)
	logger := telemetry.Logger(os.Stdout, level, "trip-service")
	otel.SetErrorHandler(otel.ErrorHandlerFunc(func(err error) {
		logger.Warn("telemetry error", "error", err)
	}))
	defer func() {
		if err != nil {
			logger.Error("service stopped with error", "error", err)
		}
	}()
	serviceName, err := telemetry.ServiceName(ctx)
	logger = telemetry.Logger(os.Stdout, level, serviceName)
	if err != nil {
		return err
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	level.Set(cfg.LogLevel)
	app, err := container.New(ctx, cfg, version, logger)
	if err != nil {
		return err
	}
	return app.Run(ctx)
}
