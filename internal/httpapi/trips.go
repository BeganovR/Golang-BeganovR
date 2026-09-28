package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/BeganovR/Golang-BeganovR/api"
	"github.com/BeganovR/Golang-BeganovR/internal/domain"
)

const tripsPath = "/api/v1/trips"

func now() time.Time {
	return time.Now().UTC().Truncate(time.Microsecond)
}

func (s *Server) CreateTrip(w http.ResponseWriter, r *http.Request, params api.CreateTripParams) {
	data, err := s.decodeTripData(w, r)
	if err != nil {
		s.writeError(w, r, err)
		return
	}

	id, err := uuid.NewV7()
	if err != nil {
		s.writeError(w, r, fmt.Errorf("generate trip id: %w", err))
		return
	}

	ctx, cancel := s.withQueryTimeout(r.Context())
	defer cancel()

	trip, replayed, err := s.saveTrip(ctx, params.IdempotencyKey, data, newTrip(id, data, now()))
	if err != nil {
		s.writeError(w, r, err)
		return
	}

	status := http.StatusCreated
	if replayed {
		status = http.StatusOK
	}
	w.Header().Set("Location", tripsPath+"/"+trip.ID.String())
	s.writeJSON(w, r, status, toAPITrip(trip))
}

func (s *Server) saveTrip(
	ctx context.Context,
	key *api.IdempotencyKey,
	data api.TripData,
	trip domain.Trip,
) (domain.Trip, bool, error) {
	var requestHash []byte
	if key != nil {
		var err error
		if requestHash, err = hashTripData(data); err != nil {
			return domain.Trip{}, false, err
		}
	}

	saved, replayed := trip, false
	err := s.txm.Do(ctx, func(ctx context.Context) error {
		if key != nil {
			claimed, err := s.keys.Claim(ctx, *key, requestHash, trip.ID, s.idempotencyTTL)
			if err != nil {
				return err
			}
			if !claimed {
				existing, err := s.replayTrip(ctx, *key, requestHash)
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

func (s *Server) GetTrip(w http.ResponseWriter, r *http.Request, tripId api.TripId) {
	ctx, cancel := s.withQueryTimeout(r.Context())
	defer cancel()

	trip, err := s.trips.GetByID(ctx, tripId)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	s.writeJSON(w, r, http.StatusOK, toAPITrip(trip))
}

func (s *Server) FinishTrip(w http.ResponseWriter, r *http.Request, tripId api.TripId) {
	ctx, cancel := s.withQueryTimeout(r.Context())
	defer cancel()

	var trip domain.Trip
	err := s.txm.Do(ctx, func(ctx context.Context) error {
		finished, err := s.trips.Finish(ctx, tripId, now())
		if err != nil {
			return err
		}
		trip = finished

		from := domain.TripStatusActive
		return s.history.Add(ctx, trip.ID, &from, domain.TripStatusCompleted, "trip finished")
	})
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	s.writeJSON(w, r, http.StatusOK, toAPITrip(trip))
}
