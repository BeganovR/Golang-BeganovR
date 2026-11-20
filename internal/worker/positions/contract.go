//go:generate go tool mockgen -source contract.go -destination mocks_test.go -package positions -mock_names staleRepository=MockStaleRepository,pushGateway=MockPushGateway,metrics=MockMetrics,ticker=MockTicker

package positions

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/BeganovR/Golang-BeganovR/internal/domain/trip"
)

type staleRepository interface {
	ListStale(ctx context.Context, asOf time.Time, staleAfter time.Duration, after *trip.StaleCandidate, limit int) ([]trip.StaleCandidate, error)
}

type pushGateway interface {
	RequestPosition(ctx context.Context, tripID, driverID uuid.UUID) error
}

type metrics interface {
	StaleCheck(ctx context.Context, result string)
	RowsRead(ctx context.Context, count int64)
	QueueSize(ctx context.Context, size int64)
	ActiveWorkers(ctx context.Context, delta int64)
	PositionRequest(ctx context.Context, result string, duration time.Duration)
}

type ticker interface {
	Ticks() <-chan time.Time
	Stop()
}
