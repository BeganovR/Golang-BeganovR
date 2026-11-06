package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BeganovR/Golang-BeganovR/internal/config"
	"github.com/BeganovR/Golang-BeganovR/internal/httpapi"
	"github.com/BeganovR/Golang-BeganovR/internal/postgres"
	"github.com/BeganovR/Golang-BeganovR/internal/service"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-ctx.Done()
		stop()
	}()

	err := run(ctx)
	stop()

	if err != nil {
		slog.Error("service stopped with error", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.LogLevel}))
	slog.SetDefault(logger)

	poolCfg, err := pgxpool.ParseConfig(cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("parse database url: %w", err)
	}
	poolCfg.MaxConns = cfg.DatabaseMaxConns
	poolCfg.MinConns = cfg.DatabaseMinConns
	poolCfg.MaxConnLifetime = cfg.DatabaseMaxConnLifetime
	poolCfg.ConnConfig.ConnectTimeout = cfg.DatabaseConnectTimeout

	connectCtx, cancel := context.WithTimeout(ctx, cfg.DatabaseConnectTimeout)
	defer cancel()

	pool, err := pgxpool.NewWithConfig(connectCtx, poolCfg)
	if err != nil {
		return fmt.Errorf("create database pool: %w", err)
	}
	defer pool.Close()

	if err := pool.Ping(connectCtx); err != nil {
		return fmt.Errorf("ping database: %w", err)
	}
	logger.Info("database ping ok")

	keys := postgres.NewIdempotencyRepository(pool)

	purger := postgres.NewIdempotencyPurger(
		keys, cfg.IdempotencyKeyTTL, cfg.IdempotencyPurgeInterval, cfg.DatabaseQueryTimeout, logger,
	)
	purgerCtx, stopPurger := context.WithCancel(ctx)
	purgerDone := make(chan struct{})
	go func() {
		defer close(purgerDone)
		purger.Run(purgerCtx)
	}()
	defer func() {
		stopPurger()
		<-purgerDone
	}()

	svc := service.New(
		postgres.NewManager(pool),
		postgres.NewTripRepository(pool),
		postgres.NewStatusHistoryRepository(pool),
		keys,
		cfg.IdempotencyKeyTTL,
	)

	srv, err := httpapi.NewServer(httpapi.Deps{
		Pinger:       pool,
		Service:      svc,
		QueryTimeout: cfg.DatabaseQueryTimeout,
		Logger:       logger,
	})
	if err != nil {
		return fmt.Errorf("create http api: %w", err)
	}

	httpServer := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           srv.Handler(),
		ReadTimeout:       cfg.HTTPReadTimeout,
		ReadHeaderTimeout: cfg.HTTPReadHeaderTimeout,
		WriteTimeout:      cfg.HTTPWriteTimeout,
		IdleTimeout:       cfg.HTTPIdleTimeout,
		ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelError),
	}

	serverErr := make(chan error, 1)
	go func() {
		logger.Info("http server starting", "addr", cfg.HTTPAddr)
		if err := httpServer.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
		}
	}()

	select {
	case err := <-serverErr:
		return fmt.Errorf("http server: %w", err)
	case <-ctx.Done():
		logger.Info("shutdown signal received")
	}

	shutdownCtx, shutdownCancel := context.WithTimeout(context.WithoutCancel(ctx), cfg.ShutdownTimeout)
	defer shutdownCancel()

	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		_ = httpServer.Close()
		return fmt.Errorf("graceful shutdown: %w", err)
	}
	logger.Info("shutdown complete")
	return nil
}
