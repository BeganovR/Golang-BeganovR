package finish

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/google/uuid"

	"github.com/BeganovR/Golang-BeganovR/internal/domain/trip"
)

type Usecase struct {
	txm     txManager
	trips   tripRepository
	history historyRepository
	logger  *slog.Logger
	metrics metrics
}

func New(txm txManager, trips tripRepository, history historyRepository, logger *slog.Logger, metrics metrics) *Usecase {
	return &Usecase{txm: txm, trips: trips, history: history, logger: logger, metrics: metrics}
}

func (u *Usecase) Execute(ctx context.Context, id uuid.UUID) (trip.Trip, error) {
	var finished trip.Trip
	err := u.txm.Do(ctx, func(ctx context.Context) error {
		current, err := u.trips.GetByID(ctx, id)
		if err != nil {
			return fmt.Errorf("get trip: %w", err)
		}
		if err := current.Finish(trip.Now()); err != nil {
			return fmt.Errorf("finish trip: %w", err)
		}
		finished, err = u.trips.Finish(ctx, id, *current.FinishedAt)
		if err != nil {
			return fmt.Errorf("save completed trip: %w", err)
		}
		from := trip.TripStatusActive
		if err := u.history.Add(ctx, id, &from, trip.TripStatusCompleted, "trip finished"); err != nil {
			return fmt.Errorf("save trip history: %w", err)
		}
		return nil
	})
	if err != nil {
		return trip.Trip{}, fmt.Errorf("finish trip transaction: %w", err)
	}
	u.metrics.TripCompleted(ctx)
	u.logger.InfoContext(ctx, "trip completed", "trip_id", id)
	return finished, nil
}
