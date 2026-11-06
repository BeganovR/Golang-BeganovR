package transaction

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Executor interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

var (
	_ Executor = (*pgxpool.Pool)(nil)
	_ Executor = pgx.Tx(nil)
)

type txKey struct{}

func ExecutorFrom(ctx context.Context, pool *pgxpool.Pool) Executor {
	if tx, ok := ctx.Value(txKey{}).(pgx.Tx); ok {
		return tx
	}
	return pool
}

type Manager struct {
	pool            *pgxpool.Pool
	logger          *slog.Logger
	rollbackTimeout time.Duration
}

func New(pool *pgxpool.Pool, logger *slog.Logger, rollbackTimeout time.Duration) *Manager {
	return &Manager{pool: pool, logger: logger, rollbackTimeout: rollbackTimeout}
}

func (m *Manager) Do(ctx context.Context, fn func(ctx context.Context) error) (err error) {
	if _, ok := ctx.Value(txKey{}).(pgx.Tx); ok {
		return fn(ctx)
	}

	tx, err := m.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}

	defer func() {
		if p := recover(); p != nil {
			m.rollback(ctx, tx)
			panic(p)
		}
		if err != nil {
			m.rollback(ctx, tx)
			return
		}
		if commitErr := tx.Commit(ctx); commitErr != nil {
			err = fmt.Errorf("commit tx: %w", commitErr)
		}
	}()

	err = fn(context.WithValue(ctx, txKey{}, tx))
	return err
}

func (m *Manager) rollback(ctx context.Context, tx pgx.Tx) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), m.rollbackTimeout)
	defer cancel()

	if err := tx.Rollback(ctx); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
		m.logger.ErrorContext(ctx, "rollback transaction", "error", err)
	}
}
