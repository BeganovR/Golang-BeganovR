package domainerror

import (
	"errors"
	"net/http"

	"github.com/BeganovR/Golang-BeganovR/api"
	"github.com/BeganovR/Golang-BeganovR/internal/domain/trip"
)

var (
	ErrInvalidRequest   = errors.New("invalid request")
	ErrNotFound         = errors.New("resource not found")
	ErrMethodNotAllowed = errors.New("method not allowed")
)

func Problem(err error) (int, api.Problem) {
	var validation *trip.ValidationError
	switch {
	case errors.As(err, &validation):
		return problem(http.StatusBadRequest, "invalid-request", "Invalid request", validation.Error(), "invalid_request")
	case errors.Is(err, ErrInvalidRequest):
		return problem(http.StatusBadRequest, "invalid-request", "Invalid request", "Request validation failed", "invalid_request")
	case errors.Is(err, trip.ErrTripNotFound):
		return problem(http.StatusNotFound, "trip-not-found", "Trip not found", "Trip was not found", "trip_not_found")
	case errors.Is(err, trip.ErrTripCompleted):
		return problem(http.StatusConflict, "trip-completed", "Trip completed", "Operation is not allowed for a completed trip", "trip_completed")
	case errors.Is(err, trip.ErrDriverBusy):
		return problem(http.StatusConflict, "driver-busy", "Driver busy", "Driver already has an active trip", "driver_busy")
	case errors.Is(err, trip.ErrIdempotencyConflict):
		return problem(http.StatusConflict, "idempotency-conflict", "Idempotency conflict", "Idempotency-Key was already used with a different request body", "idempotency_conflict")
	case errors.Is(err, ErrNotFound):
		return problem(http.StatusNotFound, "not-found", "Not found", "Resource was not found", "not_found")
	case errors.Is(err, ErrMethodNotAllowed):
		return problem(http.StatusMethodNotAllowed, "method-not-allowed", "Method not allowed", "Method is not allowed for this resource", "method_not_allowed")
	default:
		return problem(http.StatusInternalServerError, "internal-error", "Internal Server Error", "Internal server error", "internal_error")
	}
}

func problem(status int32, slug, title, detail, code string) (int, api.Problem) {
	return int(status), api.Problem{
		Type:   "https://tripgo.example/problems/" + slug,
		Title:  title,
		Status: status,
		Detail: &detail,
		Code:   code,
	}
}
