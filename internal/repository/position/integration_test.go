//go:build integration

package position_test

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/BeganovR/Golang-BeganovR/internal/domain/trip"
	"github.com/BeganovR/Golang-BeganovR/internal/infra/postgres/transaction"
	"github.com/BeganovR/Golang-BeganovR/internal/repository/position"
	triprepo "github.com/BeganovR/Golang-BeganovR/internal/repository/trip"
	testpostgres "github.com/BeganovR/Golang-BeganovR/internal/testutil/postgres"
)

func TestSaveAndListPositions(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	pool := testpostgres.NewPool(t)
	trips, positions, txm := triprepo.New(pool), position.New(pool), transaction.New(pool, slog.Default(), time.Second)
	started := time.Date(2026, time.November, 19, 13, 0, 0, 0, time.UTC)
	id := createTrip(ctx, t, trips, started)
	latest := started.Add(time.Minute)
	p := trip.Position{TripID: id, RecordedAt: latest, Coordinates: trip.Point{Latitude: 59.9, Longitude: 30.3}}
	save := func(p trip.Position) (saved trip.Position, err error) {
		err = txm.Do(ctx, func(ctx context.Context) error {
			if err := trips.RecordPosition(ctx, id, p.RecordedAt); err != nil {
				return err
			}
			var err error
			saved, err = positions.Add(ctx, p)
			return err
		})
		return saved, err
	}

	first, err := save(p)
	if err != nil {
		t.Fatal(err)
	}
	requireLastPosition(ctx, t, trips, id, latest)
	p.RecordedAt = started.Add(10 * time.Second)
	second, err := save(p)
	if err != nil {
		t.Fatal(err)
	}
	requireLastPosition(ctx, t, trips, id, latest)
	p.Coordinates = trip.Point{Latitude: 60, Longitude: 31}
	third, err := save(p)
	if err != nil {
		t.Fatal(err)
	}

	p.RecordedAt = latest.Add(time.Minute)
	p.Coordinates.Latitude = 91
	_, err = save(p)
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23514" {
		t.Fatalf("insert invalid position = %v, want check violation", err)
	}
	requireLastPosition(ctx, t, trips, id, latest)

	if _, err := trips.Finish(ctx, id, latest.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := save(p); !errors.Is(err, trip.ErrTripCompleted) {
		t.Fatalf("save after finish = %v, want completed", err)
	}
	got, err := positions.List(ctx, id)
	want := []trip.Position{second, third, first}
	if err != nil || !slices.Equal(got, want) {
		t.Fatalf("route = %+v, %v; want %+v", got, err, want)
	}
	if err := trips.RecordPosition(ctx, uuid.New(), latest); !errors.Is(err, trip.ErrTripNotFound) {
		t.Fatalf("save missing trip = %v, want not found", err)
	}
}

func createTrip(ctx context.Context, t *testing.T, trips *triprepo.Repository, started time.Time) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if err := trips.Create(ctx, trip.Trip{
		ID: id, UserID: uuid.New(), DriverID: uuid.New(), Status: trip.TripStatusActive, StartedAt: started,
	}); err != nil {
		t.Fatal(err)
	}
	return id
}

func requireLastPosition(ctx context.Context, t *testing.T, trips *triprepo.Repository, id uuid.UUID, want time.Time) {
	t.Helper()
	state, err := trips.GetByID(ctx, id)
	if err != nil || state.LastPositionAt == nil || !state.LastPositionAt.Equal(want) {
		t.Fatalf("last_position_at = %v, %v; want %v", state.LastPositionAt, err, want)
	}
}
