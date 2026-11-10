//go:build integration

package transaction_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	domain "github.com/BeganovR/Golang-BeganovR/internal/domain/trip"
	"github.com/BeganovR/Golang-BeganovR/internal/infra/postgres/transaction"
	"github.com/BeganovR/Golang-BeganovR/internal/repository/history"
	"github.com/BeganovR/Golang-BeganovR/internal/repository/idempotency"
	"github.com/BeganovR/Golang-BeganovR/internal/repository/trip"
	testpostgres "github.com/BeganovR/Golang-BeganovR/internal/testutil/postgres"
)

func activeTrip() domain.Trip {
	return domain.Trip{
		ID: uuid.New(), UserID: uuid.New(), DriverID: uuid.New(),
		StartPoint: domain.Point{Latitude: 59.9, Longitude: 30.3},
		EndPoint:   domain.Point{Latitude: 60, Longitude: 30.4},
		Price:      15000, Status: domain.TripStatusActive, StartedAt: domain.Now(),
	}
}

func manager(pool *pgxpool.Pool) *transaction.Manager {
	return transaction.New(pool, slog.New(slog.NewJSONHandler(io.Discard, nil)), 5*time.Second)
}

func TestTransactionCommit(t *testing.T) {
	t.Parallel()
	pool := testpostgres.NewPool(t)
	want := activeTrip()
	err := manager(pool).Do(t.Context(), func(ctx context.Context) error {
		if err := trip.New(pool).Create(ctx, want); err != nil {
			return err
		}
		return history.New(pool).Add(ctx, want.ID, nil, domain.TripStatusActive, "trip created")
	})
	if err != nil {
		t.Fatalf("commit: %v", err)
	}
	if testpostgres.Count(t, pool, "trips") != 1 || testpostgres.Count(t, pool, "trip_status_history") != 1 {
		t.Fatal("commit did not persist trip and history")
	}
}

func TestTransactionRollback(t *testing.T) {
	const (
		databaseError = "database"
		panicError    = "panic"
	)
	for _, mode := range []string{"callback", databaseError, "nested", panicError, "canceled"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			pool := testpostgres.NewPool(t)
			txm := manager(pool)
			want := activeTrip()
			rollbackErr := errors.New("rollback requested")
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			var recovered any
			var gotErr error
			func() {
				defer func() { recovered = recover() }()
				gotErr = txm.Do(ctx, func(ctx context.Context) error {
					if err := trip.New(pool).Create(ctx, want); err != nil {
						return err
					}
					if err := history.New(pool).Add(ctx, want.ID, nil, domain.TripStatusActive, "trip created"); err != nil {
						return err
					}
					if mode == databaseError {
						return history.New(pool).Add(ctx, want.ID, nil, "invalid", "invalid transition")
					}
					if mode == "nested" {
						return txm.Do(ctx, func(inner context.Context) error {
							if transaction.ExecutorFrom(inner, pool) != transaction.ExecutorFrom(ctx, pool) {
								return errors.New("nested transaction used another executor")
							}
							return rollbackErr
						})
					}
					if mode == panicError {
						panic(rollbackErr)
					}
					if mode == "canceled" {
						cancel()
					}
					return rollbackErr
				})
			}()
			switch mode {
			case panicError:
				if recovered != rollbackErr {
					t.Fatalf("recovered panic = %v; want %v", recovered, rollbackErr)
				}
			case databaseError:
				if gotErr == nil {
					t.Fatal("invalid history insert succeeded")
				}
			default:
				if !errors.Is(gotErr, rollbackErr) {
					t.Fatalf("rollback error = %v; want %v", gotErr, rollbackErr)
				}
			}
			if testpostgres.Count(t, pool, "trips") != 0 || testpostgres.Count(t, pool, "trip_status_history") != 0 {
				t.Fatal("rollback left trip or history rows")
			}
		})
	}
}

func TestTransactionDeferredConstraint(t *testing.T) {
	t.Parallel()
	pool := testpostgres.NewPool(t)
	err := manager(pool).Do(t.Context(), func(ctx context.Context) error {
		_, err := idempotency.New(pool).Claim(ctx, uuid.New(), []byte("hash"), uuid.New(), time.Hour)
		return err
	})
	if err == nil {
		t.Fatal("commit with missing trip succeeded")
	}
	if testpostgres.Count(t, pool, "idempotency_keys") != 0 {
		t.Fatal("failed commit left an idempotency key")
	}
}
