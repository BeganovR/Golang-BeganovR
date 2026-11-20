package positions

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/BeganovR/Golang-BeganovR/internal/domain/trip"
)

type Usecase struct {
	txm       txManager
	trips     tripRepository
	positions positionRepository
	poller    poller
	metrics   metrics
	now       func() time.Time
}

func New(txm txManager, trips tripRepository, positions positionRepository, poller poller, metrics metrics, now func() time.Time) *Usecase {
	return &Usecase{txm: txm, trips: trips, positions: positions, poller: poller, metrics: metrics, now: now}
}

func (u *Usecase) Save(ctx context.Context, p trip.Position) (trip.Position, error) {
	if err := p.Validate(u.now()); err != nil {
		return trip.Position{}, fmt.Errorf("validate position: %w", err)
	}
	p.RecordedAt = p.RecordedAt.UTC().Truncate(time.Microsecond)
	var saved trip.Position
	err := u.txm.Do(ctx, func(ctx context.Context) error {
		if err := u.trips.RecordPosition(ctx, p.TripID, p.RecordedAt); err != nil {
			return fmt.Errorf("record last position: %w", err)
		}
		var err error
		saved, err = u.positions.Add(ctx, p)
		if err != nil {
			return fmt.Errorf("save position: %w", err)
		}
		return nil
	})
	if err != nil {
		return trip.Position{}, fmt.Errorf("save position transaction: %w", err)
	}
	u.poller.Cancel(ctx, saved.TripID)
	u.metrics.PositionSaved(ctx)
	return saved, nil
}

func (u *Usecase) List(ctx context.Context, id uuid.UUID) ([]trip.Position, error) {
	if err := u.trips.CheckExists(ctx, id); err != nil {
		return nil, fmt.Errorf("get trip for route: %w", err)
	}
	positions, err := u.positions.List(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("get trip route: %w", err)
	}
	return positions, nil
}
