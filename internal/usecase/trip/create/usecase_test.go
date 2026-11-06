package create_test

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
	"github.com/BeganovR/Golang-BeganovR/internal/usecase/trip/create"
)

type fixture struct {
	txm     *MockTxManager
	trips   *MockTripRepository
	history *MockHistoryRepository
	keys    *MockIdempotencyRepository
	metrics *MockMetrics
	logs    bytes.Buffer
	usecase *create.Usecase
}

const (
	stageBegin    = "begin"
	stageSave     = "save"
	stageBusy     = "busy"
	stageHistory  = "history"
	stageCommit   = "commit"
	stageSuccess  = "success"
	stageConflict = "conflict"
	stageClaim    = "claim"
	stageKey      = "key"
	stageTrip     = "trip"
)

func newFixture(t *testing.T) *fixture {
	t.Helper()
	ctrl := gomock.NewController(t)
	f := &fixture{
		txm: NewMockTxManager(ctrl), trips: NewMockTripRepository(ctrl),
		history: NewMockHistoryRepository(ctrl), keys: NewMockIdempotencyRepository(ctrl), metrics: NewMockMetrics(ctrl),
	}
	f.usecase = create.New(f.txm, f.trips, f.history, f.keys, time.Hour,
		slog.New(slog.NewJSONHandler(&f.logs, nil)), f.metrics)
	return f
}

func validInput() trip.Trip {
	return trip.Trip{
		UserID: uuid.New(), DriverID: uuid.New(), Price: 100,
		StartPoint: trip.Point{Latitude: 59.9, Longitude: 30.3}, EndPoint: trip.Point{Latitude: 60, Longitude: 31},
	}
}

func TestExecuteCreate(t *testing.T) {
	t.Parallel()
	for _, withKey := range []bool{false, true} {
		name := "without key"
		if withKey {
			name = "with key"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t)
			ctx, txCtx := context.Background(), t.Context()
			input := validInput()
			input.ID, input.Status = uuid.New(), trip.TripStatusCompleted
			oldFinish := time.Now()
			input.FinishedAt = &oldFinish
			var key *uuid.UUID
			hash := []byte("request hash")
			committed := false
			f.txm.EXPECT().Do(ctx, gomock.Any()).DoAndReturn(func(_ context.Context, fn func(context.Context) error) error {
				err := fn(txCtx)
				committed = err == nil
				return err
			})
			if withKey {
				id := uuid.New()
				key = &id
				f.keys.EXPECT().Claim(txCtx, id, hash, gomock.Any(), time.Hour).Return(true, nil)
			}
			var saved trip.Trip
			f.trips.EXPECT().Create(txCtx, gomock.Any()).DoAndReturn(func(_ context.Context, v trip.Trip) error {
				saved = v
				return nil
			})
			f.history.EXPECT().Add(txCtx, gomock.Any(), nil, trip.TripStatusActive, "trip created").
				DoAndReturn(func(_ context.Context, id uuid.UUID, _ *trip.TripStatus, _ trip.TripStatus, _ string) error {
					if id != saved.ID {
						t.Fatalf("history trip id = %v, want %v", id, saved.ID)
					}
					return nil
				})
			f.metrics.EXPECT().TripCreated(ctx).Do(func(context.Context) {
				if !committed {
					t.Fatal("TripCreated called before commit")
				}
			})
			before := time.Now().UTC().Truncate(time.Microsecond)
			got, replayed, err := f.usecase.Execute(ctx, key, hash, input)
			if err != nil || replayed || !reflect.DeepEqual(got, saved) {
				t.Fatalf("Execute() = %+v, %v, %v; saved = %+v", got, replayed, err, saved)
			}
			if got.ID == uuid.Nil || got.ID.Version() != 7 || got.ID == input.ID || got.Status != trip.TripStatusActive || got.FinishedAt != nil {
				t.Fatalf("generated fields = %+v", got)
			}
			if got.StartedAt.Before(before) || got.StartedAt.After(time.Now()) || got.StartedAt.Location() != time.UTC {
				t.Fatalf("StartedAt = %v", got.StartedAt)
			}
			input.ID, input.Status, input.StartedAt, input.FinishedAt = got.ID, got.Status, got.StartedAt, got.FinishedAt
			if !reflect.DeepEqual(got, input) {
				t.Fatalf("input fields changed: got %+v, want %+v", got, input)
			}
			if !strings.Contains(f.logs.String(), "trip created") || !strings.Contains(f.logs.String(), got.ID.String()) {
				t.Fatalf("missing business log: %s", f.logs.String())
			}
		})
	}
}

func TestExecuteInvalid(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	got, replayed, err := f.usecase.Execute(t.Context(), nil, nil, trip.Trip{})
	var invalid *trip.ValidationError
	if !errors.As(err, &invalid) || invalid.Field != "user_id" || got != (trip.Trip{}) || replayed {
		t.Fatalf("Execute() = %+v, %v, %v", got, replayed, err)
	}
}

func TestExecuteTransactionError(t *testing.T) {
	t.Parallel()
	storageErr := errors.New("storage unavailable")
	for _, stage := range []string{stageBegin, stageSave, stageBusy, stageHistory, stageCommit} {
		t.Run(stage, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t)
			ctx := t.Context()
			wantErr := storageErr
			if stage == stageBusy {
				wantErr = trip.ErrDriverBusy
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
				var saveErr error
				if stage == stageSave || stage == stageBusy {
					saveErr = wantErr
				}
				f.trips.EXPECT().Create(ctx, gomock.Any()).Return(saveErr)
			}
			if stage == stageHistory || stage == stageCommit {
				var historyErr error
				if stage == stageHistory {
					historyErr = storageErr
				}
				f.history.EXPECT().Add(ctx, gomock.Any(), nil, trip.TripStatusActive, "trip created").Return(historyErr)
			}
			got, replayed, err := f.usecase.Execute(ctx, nil, nil, validInput())
			if !errors.Is(err, wantErr) || got != (trip.Trip{}) || replayed || f.logs.Len() != 0 {
				t.Fatalf("Execute() = %+v, %v, %v; logs = %s", got, replayed, err, f.logs.String())
			}
		})
	}
}

func TestExecuteReplay(t *testing.T) {
	t.Parallel()
	storageErr := errors.New("storage unavailable")
	for _, stage := range []string{stageSuccess, stageConflict, stageClaim, stageKey, stageTrip} {
		t.Run(stage, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t)
			ctx := t.Context()
			key, id := uuid.New(), uuid.New()
			hash := []byte("same body")
			existing := validInput()
			existing.ID, existing.Status = id, trip.TripStatusActive
			f.txm.EXPECT().Do(ctx, gomock.Any()).DoAndReturn(func(_ context.Context, fn func(context.Context) error) error {
				return fn(ctx)
			})
			var claimErr, keyErr, tripErr, wantErr error
			switch stage {
			case stageClaim:
				claimErr, wantErr = storageErr, storageErr
			case stageKey:
				keyErr, wantErr = storageErr, storageErr
			case stageTrip:
				tripErr, wantErr = storageErr, storageErr
			case stageConflict:
				wantErr = trip.ErrIdempotencyConflict
			}
			f.keys.EXPECT().Claim(ctx, key, hash, gomock.Any(), time.Hour).Return(false, claimErr)
			if stage != stageClaim {
				record := trip.IdempotencyRecord{TripID: id, RequestHash: hash}
				if stage == stageConflict {
					record.RequestHash = []byte("different body")
				}
				f.keys.EXPECT().Get(ctx, key).Return(record, keyErr)
			}
			if stage == stageSuccess || stage == stageTrip {
				f.trips.EXPECT().GetByID(ctx, id).Return(existing, tripErr)
			}
			got, replayed, err := f.usecase.Execute(ctx, &key, hash, validInput())
			if !errors.Is(err, wantErr) {
				t.Fatalf("Execute() error = %v, want %v", err, wantErr)
			}
			if wantErr != nil {
				existing = trip.Trip{}
			}
			if !reflect.DeepEqual(got, existing) || replayed != (wantErr == nil) || f.logs.Len() != 0 {
				t.Fatalf("Execute() = %+v, %v, %v; logs = %s", got, replayed, err, f.logs.String())
			}
		})
	}
}
