//go:build integration

package trip_test

import (
	"bytes"
	"testing"
	"time"

	"github.com/google/uuid"

	domain "github.com/BeganovR/Golang-BeganovR/internal/domain/trip"
	"github.com/BeganovR/Golang-BeganovR/internal/repository/trip"
	testpostgres "github.com/BeganovR/Golang-BeganovR/internal/testutil/postgres"
)

func TestListStaleTripBatches(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	pool := testpostgres.NewPool(t)
	repo := trip.New(pool)
	now := time.Date(2026, time.November, 19, 14, 0, 0, 0, time.UTC)
	oldest, oldPosition, fresh, boundary, completed := activeTrip(), activeTrip(), activeTrip(), activeTrip(), activeTrip()
	for _, candidate := range []*domain.Trip{&oldest, &oldPosition, &fresh, &boundary, &completed} {
		candidate.StartedAt = now.Add(-5 * time.Minute)
	}
	boundary.StartedAt = now.Add(-time.Minute)
	for _, candidate := range []domain.Trip{oldest, oldPosition, fresh, boundary, completed} {
		if err := repo.Create(ctx, candidate); err != nil {
			t.Fatal(err)
		}
	}
	oldSeenAt := now.Add(-3 * time.Minute)
	if err := repo.RecordPosition(ctx, oldPosition.ID, oldSeenAt); err != nil {
		t.Fatal(err)
	}
	if err := repo.RecordPosition(ctx, fresh.ID, now.Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Finish(ctx, completed.ID, now); err != nil {
		t.Fatal(err)
	}
	const seed = `
INSERT INTO trips (id, user_id, driver_id, start_latitude, start_longitude, end_latitude, end_longitude, price, status, started_at)
SELECT gen_random_uuid(), gen_random_uuid(), gen_random_uuid(), 0, 0, 0, 0, 0, 'active', $1
FROM generate_series(1, 248)`
	tiedAt := now.Add(-2 * time.Minute)
	if _, err := pool.Exec(ctx, seed, tiedAt); err != nil {
		t.Fatal(err)
	}

	seen := make(map[uuid.UUID]bool, 250)
	var cursor *domain.StaleCandidate
	for _, want := range []int{100, 100, 50} {
		batch, err := repo.ListStale(ctx, now, time.Minute, cursor, 100)
		if err != nil || len(batch) != want {
			t.Fatalf("batch has %d rows, want %d, err = %v", len(batch), want, err)
		}
		if cursor == nil {
			if batch[0].TripID != oldest.ID || batch[0].DriverID != oldest.DriverID || !batch[0].LastSeenAt.Equal(oldest.StartedAt) {
				t.Fatalf("oldest trip without coordinates should be first: %+v", batch[0])
			}
			if batch[1].TripID != oldPosition.ID || !batch[1].LastSeenAt.Equal(oldSeenAt) {
				t.Fatalf("older position should use recorded_at: %+v", batch[1])
			}
		}
		for _, candidate := range batch {
			if seen[candidate.TripID] {
				t.Fatalf("repeated trip %s across pages", candidate.TripID)
			}
			if cursor != nil && (candidate.LastSeenAt.Before(cursor.LastSeenAt) ||
				candidate.LastSeenAt.Equal(cursor.LastSeenAt) && bytes.Compare(candidate.TripID[:], cursor.TripID[:]) <= 0) {
				t.Fatalf("trip %+v is out of order after %+v", candidate, *cursor)
			}
			seen[candidate.TripID] = true
			cursor = &candidate
		}
	}
	for _, excluded := range []domain.Trip{fresh, boundary, completed} {
		if seen[excluded.ID] {
			t.Errorf("unexpected trip %s at %s with status %s", excluded.ID, excluded.StartedAt, excluded.Status)
		}
	}
}
