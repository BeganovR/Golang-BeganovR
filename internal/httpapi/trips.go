package httpapi

import (
	"fmt"
	"net/http"

	"github.com/google/uuid"

	"github.com/BeganovR/Golang-BeganovR/api"
	"github.com/BeganovR/Golang-BeganovR/internal/domain"
)

const tripsPath = "/api/v1/trips"

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

	var requestHash []byte
	if params.IdempotencyKey != nil {
		if requestHash, err = hashTripData(data); err != nil {
			s.writeError(w, r, err)
			return
		}
	}

	ctx, cancel := s.withQueryTimeout(r.Context())
	defer cancel()

	trip, replayed, err := s.svc.CreateTrip(ctx, params.IdempotencyKey, requestHash, newTrip(id, data, domain.Now()))
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

func (s *Server) GetTrip(w http.ResponseWriter, r *http.Request, tripId api.TripId) {
	ctx, cancel := s.withQueryTimeout(r.Context())
	defer cancel()

	trip, err := s.svc.GetTrip(ctx, tripId)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	s.writeJSON(w, r, http.StatusOK, toAPITrip(trip))
}

func (s *Server) FinishTrip(w http.ResponseWriter, r *http.Request, tripId api.TripId) {
	ctx, cancel := s.withQueryTimeout(r.Context())
	defer cancel()

	trip, err := s.svc.FinishTrip(ctx, tripId)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	s.writeJSON(w, r, http.StatusOK, toAPITrip(trip))
}
