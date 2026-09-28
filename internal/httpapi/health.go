package httpapi

import (
	"net/http"

	"github.com/BeganovR/Golang-BeganovR/api"
)

func (s *Server) Health(w http.ResponseWriter, r *http.Request) {
	s.writeJSON(w, r, http.StatusOK, api.HealthResponse{Status: api.Ok})
}

func (s *Server) Ready(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := s.withQueryTimeout(r.Context())
	defer cancel()

	if err := s.pinger.Ping(ctx); err != nil {
		s.logger.WarnContext(r.Context(), "readiness check failed", "error", err)
		s.writeJSON(w, r, http.StatusServiceUnavailable, api.HealthResponse{Status: api.Unavailable})
		return
	}
	s.writeJSON(w, r, http.StatusOK, api.HealthResponse{Status: api.Ok})
}
