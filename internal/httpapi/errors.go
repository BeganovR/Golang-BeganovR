package httpapi

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/BeganovR/Golang-BeganovR/internal/domain"
)

var errInvalidRequest = errors.New("invalid request")

func (s *Server) writeError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, errInvalidRequest):
		s.logger.DebugContext(r.Context(), "invalid request", "error", err)
		s.writeProblem(w, r, problemInvalidRequest)
	case errors.Is(err, domain.ErrTripNotFound):
		s.writeProblem(w, r, problemTripNotFound)
	case errors.Is(err, domain.ErrTripCompleted):
		s.writeProblem(w, r, problemTripCompleted)
	case errors.Is(err, domain.ErrDriverBusy):
		s.writeProblem(w, r, problemDriverBusy)
	default:
		s.logger.ErrorContext(r.Context(), "request failed",
			"method", r.Method, "path", r.URL.Path, "error", err)
		s.writeProblem(w, r, problemInternal)
	}
}

func (s *Server) handleBindError(w http.ResponseWriter, r *http.Request, err error) {
	s.writeError(w, r, fmt.Errorf("%w: %w", errInvalidRequest, err))
}
