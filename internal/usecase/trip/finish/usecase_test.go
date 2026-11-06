package finish_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"go.uber.org/mock/gomock"

	"github.com/BeganovR/Golang-BeganovR/internal/domain/trip"
	"github.com/BeganovR/Golang-BeganovR/internal/usecase/trip/finish"
)

type fixture struct {
	txm     *MockTxManager
	trips   *MockTripRepository
	history *MockHistoryRepository
	metrics *MockMetrics
	logs    bytes.Buffer
	usecase *finish.Usecase
}

const (
	stageBegin            = "begin"
	stageGet              = "get"
	stageNotFound         = "not found"
	stageCompleted        = "completed"
	stageFinish           = "finish"
	stageConcurrentFinish = "concurrent finish"
	stageHistory          = "history"
	stageCommit           = "commit"
)

func newFixture(t *testing.T) *fixture {
	t.Helper()
	ctrl := gomock.NewController(t)
	f := &fixture{
		txm: NewMockTxManager(ctrl), trips: NewMockTripRepository(ctrl),
		history: NewMockHistoryRepository(ctrl), metrics: NewMockMetrics(ctrl),
	}
	f.usecase = finish.New(f.txm, f.trips, f.history,
		slog.New(slog.NewJSONHandler(&f.logs, nil)), f.metrics)
	return f
}

func TestExecuteSuccess(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx, txCtx := context.Background(), t.Context()
	id := uuid.New()
	current := trip.Trip{
		ID: id, UserID: uuid.New(), DriverID: uuid.New(), Price: 100,
		Status: trip.TripStatusActive, StartedAt: trip.Now().Add(-time.Minute),
	}
	var saved trip.Trip
	committed := false
	f.txm.EXPECT().Do(ctx, gomock.Any()).DoAndReturn(func(_ context.Context, fn func(context.Context) error) error {
		err := fn(txCtx)
		committed = err == nil
		return err
	})
	f.trips.EXPECT().GetByID(txCtx, id).Return(current, nil)
	f.trips.EXPECT().Finish(txCtx, id, gomock.Any()).
		DoAndReturn(func(_ context.Context, _ uuid.UUID, at time.Time) (trip.Trip, error) {
			if at.Before(current.StartedAt) || at.After(time.Now()) || at.Location() != time.UTC || at.Nanosecond()%1000 != 0 {
				t.Fatalf("Finish timestamp = %v", at)
			}
			saved = current
			saved.Status, saved.FinishedAt = trip.TripStatusCompleted, &at
			return saved, nil
		})
	from := trip.TripStatusActive
	f.history.EXPECT().Add(txCtx, id, &from, trip.TripStatusCompleted, "trip finished").Return(nil)
	f.metrics.EXPECT().TripCompleted(ctx).Do(func(context.Context) {
		if !committed {
			t.Fatal("TripCompleted called before commit")
		}
	})
	got, err := f.usecase.Execute(ctx, id)
	if err != nil || !reflect.DeepEqual(got, saved) || got.FinishedAt == nil {
		t.Fatalf("Execute() = %+v, %v; want %+v", got, err, saved)
	}
	if !strings.Contains(f.logs.String(), "trip completed") || !strings.Contains(f.logs.String(), id.String()) {
		t.Fatalf("missing business log: %s", f.logs.String())
	}
}

func TestExecuteError(t *testing.T) {
	t.Parallel()
	storageErr := errors.New("storage unavailable")
	for _, stage := range []string{stageBegin, stageGet, stageNotFound, stageCompleted, stageFinish, stageConcurrentFinish, stageHistory, stageCommit} {
		t.Run(stage, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t)
			ctx := t.Context()
			id := uuid.New()
			current := trip.Trip{ID: id, Status: trip.TripStatusActive}
			wantErr := storageErr
			switch stage {
			case stageCompleted, stageConcurrentFinish:
				wantErr = trip.ErrTripCompleted
			case stageNotFound:
				wantErr = trip.ErrTripNotFound
			}
			f.txm.EXPECT().Do(ctx, gomock.Any()).DoAndReturn(func(_ context.Context, fn func(context.Context) error) error {
				if stage == stageBegin {
					return storageErr
				}
				err := fn(ctx)
				if err == nil && stage == stageCommit {
					return storageErr
				}
				return err
			})
			if stage != stageBegin {
				var getErr error
				if stage == stageGet || stage == stageNotFound {
					getErr = wantErr
				}
				if stage == stageCompleted {
					current.Status = trip.TripStatusCompleted
				}
				f.trips.EXPECT().GetByID(ctx, id).Return(current, getErr)
			}
			if stage == stageFinish || stage == stageConcurrentFinish || stage == stageHistory || stage == stageCommit {
				var finishErr error
				if stage == stageFinish || stage == stageConcurrentFinish {
					finishErr = wantErr
				}
				f.trips.EXPECT().Finish(ctx, id, gomock.Any()).Return(trip.Trip{ID: id}, finishErr)
			}
			if stage == stageHistory || stage == stageCommit {
				var historyErr error
				if stage == stageHistory {
					historyErr = storageErr
				}
				from := trip.TripStatusActive
				f.history.EXPECT().Add(ctx, id, &from, trip.TripStatusCompleted, "trip finished").Return(historyErr)
			}
			got, err := f.usecase.Execute(ctx, id)
			if !errors.Is(err, wantErr) || got != (trip.Trip{}) || f.logs.Len() != 0 {
				t.Fatalf("Execute() = %+v, %v; logs = %s", got, err, f.logs.String())
			}
		})
	}
}
