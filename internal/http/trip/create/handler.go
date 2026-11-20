package create

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
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

func (h *Handler) Handle(w http.ResponseWriter, r *http.Request, params api.CreateTripParams) {
	data, err := h.validator.TripData(w, r)
	if err != nil {
		response.Error(w, r, err)
		return
	}
	var hash []byte
	if params.IdempotencyKey != nil {
		canonical, err := json.Marshal(data)
		if err != nil {
			response.Error(w, r, fmt.Errorf("marshal trip data: %w", err))
			return
		}
		sum := sha256.Sum256(canonical)
		hash = sum[:]
	}
	t := trip.Trip{
		UserID:     data.UserId,
		DriverID:   data.DriverId,
		StartPoint: trip.Point{Latitude: data.StartPoint.Latitude, Longitude: data.StartPoint.Longitude},
		EndPoint:   trip.Point{Latitude: data.EndPoint.Latitude, Longitude: data.EndPoint.Longitude},
		Price:      data.Price,
	}
	ctx, cancel := context.WithTimeout(r.Context(), h.timeout)
	defer cancel()
	saved, replayed, err := h.usecase.Execute(ctx, params.IdempotencyKey, hash, t)
	if err != nil {
		response.Error(w, r, err)
		return
	}
	status := http.StatusCreated
	if replayed {
		status = http.StatusOK
	}
	w.Header().Set("Location", "/api/v1/trips/"+saved.ID.String())
	response.JSON(w, r, status, response.Trip(saved))
}
