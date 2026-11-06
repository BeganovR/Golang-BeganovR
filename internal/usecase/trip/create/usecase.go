package create

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/BeganovR/Golang-BeganovR/internal/domain/trip"
)

type Usecase struct {
	txm            txManager
	trips          tripRepository
	history        historyRepository
	keys           idempotencyRepository
	idempotencyTTL time.Duration
	logger         *slog.Logger
	metrics        metrics
}

func New(txm txManager, trips tripRepository, history historyRepository, keys idempotencyRepository,
	idempotencyTTL time.Duration, logger *slog.Logger, metrics metrics,
) *Usecase {
	return &Usecase{
		txm: txm, trips: trips, history: history, keys: keys,
		idempotencyTTL: idempotencyTTL, logger: logger, metrics: metrics,
	}
}

func (u *Usecase) Execute(ctx context.Context, key *uuid.UUID, requestHash []byte, input trip.Trip) (trip.Trip, bool, error) {
	if err := input.Validate(); err != nil {
		return trip.Trip{}, false, fmt.Errorf("validate trip: %w", err)
	}
	id, err := uuid.NewV7()
	if err != nil {
		return trip.Trip{}, false, fmt.Errorf("generate trip id: %w", err)
	}
	input.ID = id
	input.Status = trip.TripStatusActive
	input.StartedAt = trip.Now()
	input.FinishedAt = nil
	saved, replayed := input, false
	err = u.txm.Do(ctx, func(ctx context.Context) error {
		if key != nil {
			claimed, err := u.keys.Claim(ctx, *key, requestHash, input.ID, u.idempotencyTTL)
			if err != nil {
				return fmt.Errorf("claim idempotency key: %w", err)
			}
			if !claimed {
				existing, err := u.replay(ctx, *key, requestHash)
				if err != nil {
					return err
				}
				saved, replayed = existing, true
				return nil
			}
		}
		if err := u.trips.Create(ctx, input); err != nil {
			return fmt.Errorf("save trip: %w", err)
		}
		if err := u.history.Add(ctx, input.ID, nil, trip.TripStatusActive, "trip created"); err != nil {
			return fmt.Errorf("save trip history: %w", err)
		}
		return nil
	})
	if err != nil {
		return trip.Trip{}, false, fmt.Errorf("create trip transaction: %w", err)
	}
	if !replayed {
		u.metrics.TripCreated(ctx)
		u.logger.InfoContext(ctx, "trip created", "trip_id", saved.ID)
	}
	return saved, replayed, nil
}

func (u *Usecase) replay(ctx context.Context, key uuid.UUID, requestHash []byte) (trip.Trip, error) {
	record, err := u.keys.Get(ctx, key)
	if err != nil {
		return trip.Trip{}, fmt.Errorf("get idempotency key: %w", err)
	}
	if !bytes.Equal(record.RequestHash, requestHash) {
		return trip.Trip{}, trip.ErrIdempotencyConflict
	}
	existing, err := u.trips.GetByID(ctx, record.TripID)
	if err != nil {
		return trip.Trip{}, fmt.Errorf("get replayed trip: %w", err)
	}
	return existing, nil
}
