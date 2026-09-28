package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/BeganovR/Golang-BeganovR/internal/domain"
	api "github.com/BeganovR/Golang-BeganovR/internal/generated"
)

const tripsPath = "/api/v1/trips"

func now() time.Time {
	return time.Now().UTC().Truncate(time.Microsecond)
}

func (s *Server) CreateTrip(w http.ResponseWriter, r *http.Request, _ api.CreateTripParams) {
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
	trip := newTrip(id, data, now())

	ctx, cancel := s.withQueryTimeout(r.Context())
	defer cancel()

	err = s.txm.Do(ctx, func(ctx context.Context) error {
		if err := s.trips.Create(ctx, trip); err != nil {
			return err
		}
		return s.history.Add(ctx, trip.ID, nil, domain.TripStatusActive, "trip created")
	})
	if err != nil {
		s.writeError(w, r, err)
		return
	}

	w.Header().Set("Location", tripsPath+"/"+trip.ID.String())
	s.writeJSON(w, r, http.StatusCreated, toAPITrip(trip))
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
