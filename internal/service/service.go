package service

import (
	"bytes"
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/BeganovR/Golang-BeganovR/internal/domain"
	"github.com/BeganovR/Golang-BeganovR/internal/postgres"
)

type Service struct {
	txm            postgres.TxManager
	trips          *postgres.TripRepository
	history        *postgres.StatusHistoryRepository
	keys           *postgres.IdempotencyRepository
	idempotencyTTL time.Duration
}

func New(
	txm postgres.TxManager,
	trips *postgres.TripRepository,
	history *postgres.StatusHistoryRepository,
	keys *postgres.IdempotencyRepository,
	idempotencyTTL time.Duration,
) *Service {
	return &Service{
		txm:            txm,
		trips:          trips,
		history:        history,
		keys:           keys,
		idempotencyTTL: idempotencyTTL,
	}
}

func (s *Service) CreateTrip(
	ctx context.Context,
	idempotencyKey *uuid.UUID,
	requestHash []byte,
	trip domain.Trip,
) (domain.Trip, bool, error) {
	saved, replayed := trip, false
	err := s.txm.Do(ctx, func(ctx context.Context) error {
		if idempotencyKey != nil {
			claimed, err := s.keys.Claim(ctx, *idempotencyKey, requestHash, trip.ID, s.idempotencyTTL)
			if err != nil {
				return err
			}
			if !claimed {
				existing, err := s.replayTrip(ctx, *idempotencyKey, requestHash)
				if err != nil {
					return err
				}
				saved, replayed = existing, true
				return nil
			}
		}

		if err := s.trips.Create(ctx, trip); err != nil {
			return err
		}
		return s.history.Add(ctx, trip.ID, nil, domain.TripStatusActive, "trip created")
	})
	return saved, replayed, err
}

func (s *Service) replayTrip(ctx context.Context, key uuid.UUID, requestHash []byte) (domain.Trip, error) {
	record, err := s.keys.Get(ctx, key)
	if err != nil {
		return domain.Trip{}, err
	}
	if !bytes.Equal(record.RequestHash, requestHash) {
		return domain.Trip{}, domain.ErrIdempotencyConflict
	}
	return s.trips.GetByID(ctx, record.TripID)
}

func (s *Service) GetTrip(ctx context.Context, id uuid.UUID) (domain.Trip, error) {
	return s.trips.GetByID(ctx, id)
}

func (s *Service) FinishTrip(ctx context.Context, id uuid.UUID) (domain.Trip, error) {
	var trip domain.Trip
	err := s.txm.Do(ctx, func(ctx context.Context) error {
		finished, err := s.trips.Finish(ctx, id, domain.Now())
		if err != nil {
			return err
		}
		trip = finished

		from := domain.TripStatusActive
		return s.history.Add(ctx, trip.ID, &from, domain.TripStatusCompleted, "trip finished")
	})
	return trip, err
}
