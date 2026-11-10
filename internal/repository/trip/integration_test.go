//go:build integration

package trip_test

import (
	"errors"
	"testing"

	"github.com/google/uuid"

	domain "github.com/BeganovR/Golang-BeganovR/internal/domain/trip"
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

func TestRepositoryLifecycle(t *testing.T) {
	t.Parallel()
	pool := testpostgres.NewPool(t)
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

func TestConcurrentCreateAndFinish(t *testing.T) {
	t.Parallel()
	pool := testpostgres.NewPool(t)
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
	if success != 1 || busy != 1 || testpostgres.Count(t, pool, "trips") != 1 {
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
