//go:build integration

package idempotency_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
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

func TestIdempotencyLifecycle(t *testing.T) {
	t.Parallel()
	pool := testpostgres.NewPool(t)
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
	pool := testpostgres.NewPool(t)
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
	if testpostgres.Count(t, pool, "trips") != 1 || testpostgres.Count(t, pool, "trip_status_history") != 1 || testpostgres.Count(t, pool, "idempotency_keys") != 1 {
		t.Fatal("concurrent claims did not preserve a single trip, history and key")
	}
}
