package httpapi

import (
	"encoding/json"
	"net/http"

	"github.com/BeganovR/Golang-BeganovR/api"
)

const (
	problemTypeBase    = "https://tripgo.example/problems/"
	contentTypeJSON    = "application/json"
	contentTypeProblem = "application/problem+json"
)

type problemSpec struct {
	status int32
	slug   string
	title  string
	detail string
	code   string
}

var (
	problemInvalidRequest = problemSpec{
		status: http.StatusBadRequest,
		slug:   "invalid-request",
		title:  "Invalid request",
		detail: "Request validation failed",
		code:   "invalid_request",
	}
	problemTripNotFound = problemSpec{
		status: http.StatusNotFound,
		slug:   "trip-not-found",
		title:  "Trip not found",
		detail: "Trip was not found",
		code:   "trip_not_found",
	}
	problemTripCompleted = problemSpec{
		status: http.StatusConflict,
		slug:   "trip-completed",
		title:  "Trip completed",
		detail: "Operation is not allowed for a completed trip",
		code:   "trip_completed",
	}
	problemDriverBusy = problemSpec{
		status: http.StatusConflict,
		slug:   "driver-busy",
		title:  "Driver busy",
		detail: "Driver already has an active trip",
		code:   "driver_busy",
	}
	problemIdempotencyConflict = problemSpec{
		status: http.StatusConflict,
		slug:   "idempotency-conflict",
		title:  "Idempotency conflict",
		detail: "Idempotency-Key was already used with a different request body",
		code:   "idempotency_conflict",
	}
	problemMethodNotAllowed = problemSpec{
		status: http.StatusMethodNotAllowed,
		slug:   "method-not-allowed",
		title:  "Method not allowed",
		detail: "Method is not allowed for this resource",
		code:   "method_not_allowed",
	}
	problemNotFound = problemSpec{
		status: http.StatusNotFound,
		slug:   "not-found",
		title:  "Not found",
		detail: "Resource was not found",
		code:   "not_found",
	}
	problemInternal = problemSpec{
		status: http.StatusInternalServerError,
		slug:   "internal-error",
		title:  "Internal Server Error",
		detail: "Internal server error",
		code:   "internal_error",
	}
)

func (s *Server) writeProblem(w http.ResponseWriter, r *http.Request, p problemSpec) {
	detail := p.detail
	instance := r.URL.Path

	s.writeBody(w, r, int(p.status), contentTypeProblem, api.Problem{
		Type:     problemTypeBase + p.slug,
		Title:    p.title,
		Status:   p.status,
		Detail:   &detail,
		Instance: &instance,
		Code:     p.code,
	})
}

func (s *Server) writeJSON(w http.ResponseWriter, r *http.Request, status int, v any) {
	s.writeBody(w, r, status, contentTypeJSON, v)
}

func (s *Server) writeBody(w http.ResponseWriter, r *http.Request, status int, contentType string, v any) {
	w.Header().Set("Content-Type", contentType)
	w.WriteHeader(status)

	if err := json.NewEncoder(w).Encode(v); err != nil {
		s.logger.ErrorContext(r.Context(), "write response", "error", err)
	}
}
