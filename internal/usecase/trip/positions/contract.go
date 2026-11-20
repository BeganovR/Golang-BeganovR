//go:generate go tool mockgen -source contract.go -destination mocks_test.go -package positions_test -mock_names tripRepository=MockTripRepository,positionRepository=MockPositionRepository,txManager=MockTxManager,poller=MockPoller,metrics=MockMetrics

package positions

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/BeganovR/Golang-BeganovR/internal/domain/trip"
)

type tripRepository interface {
	CheckExists(context.Context, uuid.UUID) error
	RecordPosition(context.Context, uuid.UUID, time.Time) error
}

type positionRepository interface {
	Add(context.Context, trip.Position) (trip.Position, error)
	List(context.Context, uuid.UUID) ([]trip.Position, error)
}

type txManager interface {
	Do(context.Context, func(context.Context) error) error
}

type poller interface {
	Cancel(context.Context, uuid.UUID)
}

type metrics interface {
	PositionSaved(context.Context)
}
