//go:generate go tool mockgen -source contract.go -destination mocks_test.go -package create_test -mock_names tripRepository=MockTripRepository,historyRepository=MockHistoryRepository,idempotencyRepository=MockIdempotencyRepository,txManager=MockTxManager,metrics=MockMetrics

package create

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/BeganovR/Golang-BeganovR/internal/domain/trip"
)

type tripRepository interface {
	Create(context.Context, trip.Trip) error
	GetByID(context.Context, uuid.UUID) (trip.Trip, error)
}

type historyRepository interface {
	Add(context.Context, uuid.UUID, *trip.TripStatus, trip.TripStatus, string) error
}

type idempotencyRepository interface {
	Claim(context.Context, uuid.UUID, []byte, uuid.UUID, time.Duration) (bool, error)
	Get(context.Context, uuid.UUID) (trip.IdempotencyRecord, error)
}

type txManager interface {
	Do(context.Context, func(context.Context) error) error
}

type metrics interface {
	TripCreated(context.Context)
}
