package positions_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"go.uber.org/mock/gomock"

	"github.com/BeganovR/Golang-BeganovR/internal/domain/trip"
	"github.com/BeganovR/Golang-BeganovR/internal/usecase/trip/positions"
)

type fixture struct {
	txm    *MockTxManager
	trips  *MockTripRepository
	points *MockPositionRepository
	poller *MockPoller
	metric *MockMetrics
	uc     *positions.Usecase
	now    time.Time
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	ctrl := gomock.NewController(t)
	f := fixture{
		txm: NewMockTxManager(ctrl), trips: NewMockTripRepository(ctrl),
		points: NewMockPositionRepository(ctrl), poller: NewMockPoller(ctrl), metric: NewMockMetrics(ctrl),
		now: time.Date(2026, time.November, 19, 14, 0, 0, 0, time.UTC),
	}
	f.uc = positions.New(f.txm, f.trips, f.points, f.poller, f.metric, func() time.Time { return f.now })
	return f
}

func TestSavePosition(t *testing.T) {
	t.Parallel()
	commitErr := errors.New("commit failed")
	insertErr := errors.New("insert failed")
	for _, tc := range []struct {
		name      string
		recordErr error
		insertErr error
		commitErr error
	}{
		{name: "committed"},
		{name: "missing trip", recordErr: trip.ErrTripNotFound},
		{name: "completed trip", recordErr: trip.ErrTripCompleted},
		{name: "insert failed", insertErr: insertErr},
		{name: "commit failed", commitErr: commitErr},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			ctx := t.Context()
			txCtx, cancel := context.WithCancel(ctx)
			defer cancel()
			before := f.now.Add(-time.Second)
			p := trip.Position{
				TripID:      uuid.New(),
				Coordinates: trip.Point{Latitude: 59.9, Longitude: 30.3},
				RecordedAt:  before.In(time.FixedZone("UTC+3", 3*60*60)).Add(time.Nanosecond),
			}
			normalized := p
			normalized.RecordedAt = before
			saved := normalized
			saved.ID = 23
			committed := false
			f.txm.EXPECT().Do(ctx, gomock.Any()).DoAndReturn(func(_ context.Context, fn func(context.Context) error) error {
				if err := fn(txCtx); err != nil {
					return err
				}
				committed = tc.commitErr == nil
				return tc.commitErr
			})
			record := f.trips.EXPECT().RecordPosition(txCtx, p.TripID, before).Return(tc.recordErr)
			if tc.recordErr == nil {
				insert := f.points.EXPECT().Add(txCtx, normalized).Return(saved, tc.insertErr)
				gomock.InOrder(record, insert)
			}
			wantErr := tc.recordErr
			if wantErr == nil {
				wantErr = tc.insertErr
			}
			if wantErr == nil {
				wantErr = tc.commitErr
			}
			if wantErr == nil {
				f.poller.EXPECT().Cancel(ctx, p.TripID).Do(func(context.Context, uuid.UUID) {
					if !committed {
						t.Error("poll cancelled before commit")
					}
				})
				f.metric.EXPECT().PositionSaved(ctx).Do(func(context.Context) {
					if !committed {
						t.Error("metric emitted before commit")
					}
				})
			} else {
				saved = trip.Position{}
			}
			got, err := f.uc.Save(ctx, p)
			if got != saved || !errors.Is(err, wantErr) {
				t.Fatalf("save = %+v, %v; want %+v, %v", got, err, saved, wantErr)
			}
		})
	}
}

func TestSavePositionRejectsFutureTimestamp(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	_, err := f.uc.Save(t.Context(), trip.Position{RecordedAt: f.now.Add(2 * time.Minute)})
	var invalid *trip.ValidationError
	if !errors.As(err, &invalid) || invalid.Field != "recorded_at" {
		t.Fatalf("save error = %v, want recorded_at validation error", err)
	}
}

func TestListPositions(t *testing.T) {
	t.Parallel()
	for _, tripErr := range []error{nil, trip.ErrTripNotFound} {
		f := newFixture(t)
		id := uuid.New()
		ctx := t.Context()
		exists := f.trips.EXPECT().CheckExists(ctx, id).Return(tripErr)
		if tripErr == nil {
			gomock.InOrder(exists, f.points.EXPECT().List(ctx, id).Return(nil, nil))
		}
		got, err := f.uc.List(ctx, id)
		if !errors.Is(err, tripErr) || len(got) != 0 {
			t.Fatalf("list = %+v, %v; want no points, %v", got, err, tripErr)
		}
	}
}
