//go:build integration

package trip_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	domain "github.com/BeganovR/Golang-BeganovR/internal/domain/trip"
	"github.com/BeganovR/Golang-BeganovR/internal/infra/postgres/transaction"
	"github.com/BeganovR/Golang-BeganovR/internal/repository/history"
	"github.com/BeganovR/Golang-BeganovR/internal/repository/idempotency"
	"github.com/BeganovR/Golang-BeganovR/internal/repository/trip"
)

func database(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv("INTEGRATION_DATABASE_URL")
	if url == "" {
		url = os.Getenv("DATABASE_URL")
	}
	if url == "" {
		t.Fatal("set INTEGRATION_DATABASE_URL or DATABASE_URL to run integration tests")
	}
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal("invalid integration database configuration")
	}
	cfg.MaxConns = 4
	cfg.MinConns = 0
	cfg.ConnConfig.ConnectTimeout = 5 * time.Second
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	admin, err := pgxpool.NewWithConfig(ctx, cfg.Copy())
	if err != nil {
		t.Fatalf("create admin pool: %v", err)
	}
	schema := "test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize()); err != nil {
		admin.Close()
		t.Fatalf("create test schema: %v", err)
	}
	var pool *pgxpool.Pool
	t.Cleanup(func() {
		if pool != nil {
			pool.Close()
		}
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		if _, err := admin.Exec(cleanupCtx, "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE"); err != nil {
			t.Errorf("drop test schema: %v", err)
		}
		admin.Close()
	})
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err = pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("create test pool: %v", err)
	}
	paths, err := filepath.Glob("../../../migrations/*.sql")
	if err != nil || len(paths) == 0 {
		t.Fatalf("find migrations: %v", err)
	}
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read migration: %v", err)
		}
		up, _, found := strings.Cut(string(data), "-- +goose Down")
		if !found {
			t.Fatalf("migration has no Down marker: %s", path)
		}
		if _, err := pool.Exec(ctx, up); err != nil {
			t.Fatalf("apply migration %s: %v", path, err)
		}
	}
	return pool
}

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

func count(t *testing.T, pool *pgxpool.Pool, table string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(t.Context(), "SELECT count(*) FROM "+pgx.Identifier{table}.Sanitize()).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}

func TestRepositoryLifecycle(t *testing.T) {
	t.Parallel()
	pool := database(t)
	repo := trip.New(pool)
	want := activeTrip()
	if err := repo.Create(t.Context(), want); err != nil {
		t.Fatalf("create trip: %v", err)
	}
	got, err := repo.GetByID(t.Context(), want.ID)
	if err != nil || got != want {
		t.Fatalf("get trip = %+v, %v; want %+v", got, err, want)
	}
	other := activeTrip()
	other.DriverID = want.DriverID
	if err := repo.Create(t.Context(), other); !errors.Is(err, domain.ErrDriverBusy) {
		t.Fatalf("busy driver error = %v", err)
	}
	if n, err := repo.CountActive(t.Context()); err != nil || n != 1 {
		t.Fatalf("active trips = %d, %v; want 1", n, err)
	}
	at := domain.Now()
	finished, err := repo.Finish(t.Context(), want.ID, at)
	if err != nil || finished.Status != domain.TripStatusCompleted || finished.FinishedAt == nil || !finished.FinishedAt.Equal(at) {
		t.Fatalf("finish = %+v, %v", finished, err)
	}
	if _, err := repo.Finish(t.Context(), want.ID, at); !errors.Is(err, domain.ErrTripCompleted) {
		t.Fatalf("double finish error = %v", err)
	}
	if n, err := repo.CountActive(t.Context()); err != nil || n != 0 {
		t.Fatalf("active trips after finish = %d, %v; want 0", n, err)
	}
	if err := repo.Create(t.Context(), other); err != nil {
		t.Fatalf("reuse finished trip driver: %v", err)
	}
	missing := uuid.New()
	if _, err := repo.GetByID(t.Context(), missing); !errors.Is(err, domain.ErrTripNotFound) {
		t.Fatalf("missing trip error = %v", err)
	}
	if _, err := repo.Finish(t.Context(), missing, at); !errors.Is(err, domain.ErrTripNotFound) {
		t.Fatalf("finish missing trip error = %v", err)
	}
}

func TestTransactionCommit(t *testing.T) {
	t.Parallel()
	pool := database(t)
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
	if count(t, pool, "trips") != 1 || count(t, pool, "trip_status_history") != 1 {
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
			pool := database(t)
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
			if count(t, pool, "trips") != 0 || count(t, pool, "trip_status_history") != 0 {
				t.Fatal("rollback left trip or history rows")
			}
		})
	}
}

func TestTransactionDeferredConstraint(t *testing.T) {
	t.Parallel()
	pool := database(t)
	err := manager(pool).Do(t.Context(), func(ctx context.Context) error {
		_, err := idempotency.New(pool).Claim(ctx, uuid.New(), []byte("hash"), uuid.New(), time.Hour)
		return err
	})
	if err == nil {
		t.Fatal("commit with missing trip succeeded")
	}
	if count(t, pool, "idempotency_keys") != 0 {
		t.Fatal("failed commit left an idempotency key")
	}
}

func TestConcurrentCreateAndFinish(t *testing.T) {
	t.Parallel()
	pool := database(t)
	repo := trip.New(pool)
	first, second := activeTrip(), activeTrip()
	second.DriverID = first.DriverID
	results := make(chan error, 2)
	start := make(chan struct{})
	for _, candidate := range []domain.Trip{first, second} {
		go func() {
			<-start
			results <- repo.Create(t.Context(), candidate)
		}()
	}
	close(start)
	var success, busy int
	for range 2 {
		switch err := <-results; {
		case err == nil:
			success++
		case errors.Is(err, domain.ErrDriverBusy):
			busy++
		default:
			t.Fatalf("concurrent create: %v", err)
		}
	}
	if success != 1 || busy != 1 || count(t, pool, "trips") != 1 {
		t.Fatalf("concurrent create success=%d busy=%d", success, busy)
	}
	var id uuid.UUID
	if err := pool.QueryRow(t.Context(), "SELECT id FROM trips").Scan(&id); err != nil {
		t.Fatal(err)
	}
	start = make(chan struct{})
	for range 2 {
		go func() {
			<-start
			_, err := repo.Finish(t.Context(), id, domain.Now())
			results <- err
		}()
	}
	close(start)
	success, busy = 0, 0
	for range 2 {
		switch err := <-results; {
		case err == nil:
			success++
		case errors.Is(err, domain.ErrTripCompleted):
			busy++
		default:
			t.Fatalf("concurrent finish: %v", err)
		}
	}
	if success != 1 || busy != 1 {
		t.Fatalf("concurrent finish success=%d completed=%d", success, busy)
	}
}

func TestIdempotencyLifecycle(t *testing.T) {
	t.Parallel()
	pool := database(t)
	want := activeTrip()
	if err := trip.New(pool).Create(t.Context(), want); err != nil {
		t.Fatal(err)
	}
	repo := idempotency.New(pool)
	key, hash := uuid.New(), []byte("request hash")
	if claimed, err := repo.Claim(t.Context(), key, hash, want.ID, time.Hour); err != nil || !claimed {
		t.Fatalf("claim = %v, %v", claimed, err)
	}
	if claimed, err := repo.Claim(t.Context(), key, []byte("different"), want.ID, time.Hour); err != nil || claimed {
		t.Fatalf("claim existing = %v, %v", claimed, err)
	}
	got, err := repo.Get(t.Context(), key)
	if err != nil || got.TripID != want.ID || !bytes.Equal(got.RequestHash, hash) {
		t.Fatalf("get key = %+v, %v", got, err)
	}
	if n, err := repo.DeleteExpired(t.Context(), time.Hour); err != nil || n != 0 {
		t.Fatalf("purge fresh = %d, %v", n, err)
	}
	if _, err := pool.Exec(t.Context(), "UPDATE idempotency_keys SET created_at = now() - interval '2 hours'"); err != nil {
		t.Fatal(err)
	}
	if claimed, err := repo.Claim(t.Context(), key, []byte("new request"), want.ID, time.Hour); err != nil || !claimed {
		t.Fatalf("reclaim expired = %v, %v", claimed, err)
	}
	if _, err := pool.Exec(t.Context(), "UPDATE idempotency_keys SET created_at = now() - interval '2 hours'"); err != nil {
		t.Fatal(err)
	}
	if n, err := repo.DeleteExpired(t.Context(), time.Hour); err != nil || n != 1 {
		t.Fatalf("purge expired = %d, %v", n, err)
	}
	if _, err := repo.Get(t.Context(), key); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("get deleted key = %v", err)
	}
}

func TestConcurrentIdempotencyClaim(t *testing.T) {
	t.Parallel()
	pool := database(t)
	txm := manager(pool)
	keys := idempotency.New(pool)
	trips := trip.New(pool)
	key := uuid.New()
	type result struct {
		id      uuid.UUID
		claimed bool
		err     error
	}
	results := make(chan result, 2)
	start := make(chan struct{})
	for range 2 {
		go func() {
			<-start
			candidate := activeTrip()
			got := result{id: candidate.ID}
			got.err = txm.Do(t.Context(), func(ctx context.Context) error {
				claimed, err := keys.Claim(ctx, key, []byte("same request"), candidate.ID, time.Hour)
				got.claimed = claimed
				if err != nil {
					return err
				}
				if !claimed {
					record, err := keys.Get(ctx, key)
					got.id = record.TripID
					return err
				}
				if err := trips.Create(ctx, candidate); err != nil {
					return err
				}
				return history.New(pool).Add(ctx, candidate.ID, nil, domain.TripStatusActive, "trip created")
			})
			results <- got
		}()
	}
	close(start)
	first, second := <-results, <-results
	if first.err != nil || second.err != nil || first.id != second.id || first.claimed == second.claimed {
		t.Fatalf("concurrent claims = %+v, %+v", first, second)
	}
	if count(t, pool, "trips") != 1 || count(t, pool, "trip_status_history") != 1 || count(t, pool, "idempotency_keys") != 1 {
		t.Fatal("concurrent claims did not preserve a single trip, history and key")
	}
}
