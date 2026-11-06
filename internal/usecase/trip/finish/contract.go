//go:generate go tool mockgen -source contract.go -destination mocks_test.go -package finish_test -mock_names tripRepository=MockTripRepository,historyRepository=MockHistoryRepository,txManager=MockTxManager,metrics=MockMetrics

package finish

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/BeganovR/Golang-BeganovR/internal/domain/trip"
)

type tripRepository interface {
	GetByID(context.Context, uuid.UUID) (trip.Trip, error)
	Finish(context.Context, uuid.UUID, time.Time) (trip.Trip, error)
}

type historyRepository interface {
	Add(context.Context, uuid.UUID, *trip.TripStatus, trip.TripStatus, string) error
}

type txManager interface {
	Do(context.Context, func(context.Context) error) error
}

type metrics interface {
	TripCompleted(context.Context)
}
