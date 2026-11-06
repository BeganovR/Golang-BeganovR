package container

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BeganovR/Golang-BeganovR/internal/config"
	httptransport "github.com/BeganovR/Golang-BeganovR/internal/http"
	"github.com/BeganovR/Golang-BeganovR/internal/http/middleware"
	"github.com/BeganovR/Golang-BeganovR/internal/infra/postgres"
	"github.com/BeganovR/Golang-BeganovR/internal/infra/postgres/transaction"
	"github.com/BeganovR/Golang-BeganovR/internal/infra/telemetry"
	"github.com/BeganovR/Golang-BeganovR/internal/repository/history"
	"github.com/BeganovR/Golang-BeganovR/internal/repository/idempotency"
	"github.com/BeganovR/Golang-BeganovR/internal/repository/trip"
	"github.com/BeganovR/Golang-BeganovR/internal/usecase/trip/create"
	"github.com/BeganovR/Golang-BeganovR/internal/usecase/trip/finish"
	"github.com/BeganovR/Golang-BeganovR/internal/usecase/trip/get"
	keypurger "github.com/BeganovR/Golang-BeganovR/internal/worker/idempotency"
)

type Container struct {
	server          *http.Server
	pool            *pgxpool.Pool
	purger          *keypurger.Purger
	telemetry       *telemetry.Providers
	logger          *slog.Logger
	shutdownTimeout time.Duration
}

func New(ctx context.Context, cfg config.Config, version string, logger *slog.Logger) (_ *Container, err error) {
	providers, err := telemetry.New(ctx, version)
	if err != nil {
		return nil, err
	}
	c := &Container{telemetry: providers, logger: logger, shutdownTimeout: cfg.ShutdownTimeout}
	defer func() {
		if err != nil {
			cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cfg.ShutdownTimeout)
			defer cancel()
			err = errors.Join(err, c.close(cleanupCtx))
		}
	}()
	poolCfg, err := pgxpool.ParseConfig(cfg.DatabaseURL)
	if err != nil {
		return nil, errors.New("parse database config: invalid DATABASE_URL")
	}
	poolCfg.MaxConns = cfg.DatabaseMaxConns
	poolCfg.MinConns = cfg.DatabaseMinConns
	poolCfg.MaxConnLifetime = cfg.DatabaseMaxConnLifetime
	poolCfg.ConnConfig.ConnectTimeout = cfg.DatabaseConnectTimeout
	poolCfg.ConnConfig.Tracer, err = postgres.NewTracer(providers.Traces, providers.Metrics)
	if err != nil {
		return nil, err
	}
	connectCtx, cancel := context.WithTimeout(ctx, cfg.DatabaseConnectTimeout)
	defer cancel()
	c.pool, err = pgxpool.NewWithConfig(connectCtx, poolCfg)
	if err != nil {
		return nil, fmt.Errorf("create database pool: %w", err)
	}
	if err := c.pool.Ping(connectCtx); err != nil {
		return nil, fmt.Errorf("ping database: %w", err)
	}
	trips, histories, keys := trip.New(c.pool), history.New(c.pool), idempotency.New(c.pool)
	txm := transaction.New(c.pool, logger, cfg.DatabaseQueryTimeout)
	metrics, err := telemetry.NewTrips(providers.Metrics, trips, cfg.DatabaseQueryTimeout)
	if err != nil {
		return nil, err
	}
	api, err := httptransport.NewServer(httptransport.Deps{
		Create:       create.New(txm, trips, histories, keys, cfg.IdempotencyKeyTTL, logger, metrics),
		Get:          get.New(trips),
		Finish:       finish.New(txm, trips, histories, logger, metrics),
		Pinger:       c.pool,
		QueryTimeout: cfg.DatabaseQueryTimeout,
	})
	if err != nil {
		return nil, fmt.Errorf("create HTTP API: %w", err)
	}
	instrumentation, err := middleware.New(logger, providers.Traces, providers.Metrics)
	if err != nil {
		return nil, err
	}
	c.server = &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           instrumentation.Wrap(api.Handler()),
		ReadTimeout:       cfg.HTTPReadTimeout,
		ReadHeaderTimeout: cfg.HTTPReadHeaderTimeout,
		WriteTimeout:      cfg.HTTPWriteTimeout,
		IdleTimeout:       cfg.HTTPIdleTimeout,
		ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelError),
	}
	c.purger = keypurger.New(keys, cfg.IdempotencyKeyTTL, cfg.IdempotencyPurgeInterval, cfg.DatabaseQueryTimeout, logger)
	return c, nil
}

func (c *Container) Run(ctx context.Context) error {
	workerCtx, stopWorker := context.WithCancel(context.WithoutCancel(ctx))
	workerDone := make(chan struct{})
	go func() {
		defer close(workerDone)
		c.purger.Run(workerCtx)
	}()
	serverErr := make(chan error, 1)
	go func() {
		c.logger.Info("HTTP server starting", "addr", c.server.Addr)
		serverErr <- c.server.ListenAndServe()
	}()
	var err error
	select {
	case err = <-serverErr:
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
	case <-ctx.Done():
		c.logger.Info("shutdown signal received")
	}
	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), c.shutdownTimeout)
	defer cancel()
	if shutdownErr := c.server.Shutdown(shutdownCtx); shutdownErr != nil {
		err = errors.Join(err, fmt.Errorf("graceful shutdown: %w", shutdownErr), c.server.Close())
	}
	stopWorker()
	select {
	case <-workerDone:
	case <-shutdownCtx.Done():
		err = errors.Join(err, shutdownCtx.Err())
	}
	err = errors.Join(err, c.close(shutdownCtx))
	if err == nil {
		c.logger.Info("shutdown complete")
	}
	return err
}

func (c *Container) close(ctx context.Context) error {
	if err := c.telemetry.Shutdown(ctx); err != nil {
		c.logger.WarnContext(ctx, "telemetry shutdown failed", "error", err)
	}
	if c.pool != nil {
		closed := make(chan struct{})
		go func() {
			c.pool.Close()
			close(closed)
		}()
		select {
		case <-closed:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return ctx.Err()
}
