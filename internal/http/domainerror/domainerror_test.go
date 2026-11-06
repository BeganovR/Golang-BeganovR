package domainerror_test

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/BeganovR/Golang-BeganovR/internal/domain/trip"
	"github.com/BeganovR/Golang-BeganovR/internal/http/domainerror"
)

func TestProblem(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{"invalid request", domainerror.ErrInvalidRequest, http.StatusBadRequest, "invalid_request"},
		{"validation", &trip.ValidationError{Field: "price", Message: "must be non-negative"}, http.StatusBadRequest, "invalid_request"},
		{"not found", trip.ErrTripNotFound, http.StatusNotFound, "trip_not_found"},
		{"completed", trip.ErrTripCompleted, http.StatusConflict, "trip_completed"},
		{"busy", trip.ErrDriverBusy, http.StatusConflict, "driver_busy"},
		{"idempotency", trip.ErrIdempotencyConflict, http.StatusConflict, "idempotency_conflict"},
		{"route", domainerror.ErrNotFound, http.StatusNotFound, "not_found"},
		{"method", domainerror.ErrMethodNotAllowed, http.StatusMethodNotAllowed, "method_not_allowed"},
		{"internal", errors.New("private database details"), http.StatusInternalServerError, "internal_error"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status, body := domainerror.Problem(fmt.Errorf("wrapped: %w", tt.err))
			if status != tt.status || int(body.Status) != tt.status || body.Code != tt.code {
				t.Fatalf("got status=%d body=%+v", status, body)
			}
			if !strings.HasPrefix(body.Type, "https://tripgo.example/problems/") || body.Title == "" || body.Detail == nil {
				t.Fatalf("incomplete problem: %+v", body)
			}
			if strings.Contains(*body.Detail, tt.err.Error()) && status == http.StatusInternalServerError {
				t.Fatalf("internal error leaked: %s", *body.Detail)
			}
		})
	}
}
