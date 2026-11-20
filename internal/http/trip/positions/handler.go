package positions

import (
	"context"
	"net/http"
	"time"

	"github.com/BeganovR/Golang-BeganovR/api"
	"github.com/BeganovR/Golang-BeganovR/internal/domain/trip"
	"github.com/BeganovR/Golang-BeganovR/internal/http/request"
	"github.com/BeganovR/Golang-BeganovR/internal/http/response"
)

type Handler struct {
	usecase   usecase
	timeout   time.Duration
	validator *request.Validator
}

func New(uc usecase, timeout time.Duration, validator *request.Validator) *Handler {
	return &Handler{usecase: uc, timeout: timeout, validator: validator}
}

func (h *Handler) Save(w http.ResponseWriter, r *http.Request, id api.TripId) {
	data, err := h.validator.PositionData(w, r)
	if err != nil {
		response.Error(w, r, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), h.timeout)
	defer cancel()
	position, err := h.usecase.Save(ctx, trip.Position{
		TripID:      id,
		Coordinates: trip.Point{Latitude: data.Coordinates.Latitude, Longitude: data.Coordinates.Longitude},
		RecordedAt:  data.RecordedAt,
	})
	if err != nil {
		response.Error(w, r, err)
		return
	}
	response.JSON(w, r, http.StatusCreated, toAPI(position))
}

func (h *Handler) List(w http.ResponseWriter, r *http.Request, id api.TripId) {
	ctx, cancel := context.WithTimeout(r.Context(), h.timeout)
	defer cancel()
	positions, err := h.usecase.List(ctx, id)
	if err != nil {
		response.Error(w, r, err)
		return
	}
	data := make([]api.TripPosition, 0, len(positions))
	for _, position := range positions {
		data = append(data, toAPI(position))
	}
	response.JSON(w, r, http.StatusOK, api.TripPositionsResponse{Positions: data})
}

func toAPI(p trip.Position) api.TripPosition {
	return api.TripPosition{
		Id:          p.ID,
		TripId:      p.TripID,
		Coordinates: api.Coordinates{Latitude: p.Coordinates.Latitude, Longitude: p.Coordinates.Longitude},
		RecordedAt:  p.RecordedAt,
	}
}
