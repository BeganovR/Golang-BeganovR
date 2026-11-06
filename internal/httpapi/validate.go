package httpapi

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"sync"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/BeganovR/Golang-BeganovR/api"
)

const (
	maxBodyBytes         = 1 << 20
	canonicalUUIDPattern = `^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`
)

var (
	canonicalUUID      = regexp.MustCompile(canonicalUUIDPattern)
	registerUUIDFormat sync.Once
)

func (s *Server) requireCanonicalUUIDs(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if id := chi.URLParam(r, "tripId"); id != "" && !canonicalUUID.MatchString(id) {
			s.writeError(w, r, fmt.Errorf("%w: tripId is not a canonical uuid", errInvalidRequest))
			return
		}
		if key := r.Header.Get("Idempotency-Key"); key != "" && !canonicalUUID.MatchString(key) {
			s.writeError(w, r, fmt.Errorf("%w: Idempotency-Key is not a canonical uuid", errInvalidRequest))
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) decodeTripData(w http.ResponseWriter, r *http.Request) (api.TripData, error) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	if err != nil {
		return api.TripData{}, fmt.Errorf("%w: read body: %w", errInvalidRequest, err)
	}

	var payload any
	if err := json.Unmarshal(body, &payload); err != nil {
		return api.TripData{}, fmt.Errorf("%w: parse json: %w", errInvalidRequest, err)
	}
	if err := s.tripDataSchema.VisitJSON(payload, openapi3.EnableFormatValidation()); err != nil {
		return api.TripData{}, fmt.Errorf("%w: contract schema: %w", errInvalidRequest, err)
	}

	var data api.TripData
	if err := json.Unmarshal(body, &data); err != nil {
		return api.TripData{}, fmt.Errorf("%w: decode body: %w", errInvalidRequest, err)
	}
	if data.UserId == uuid.Nil || data.DriverId == uuid.Nil {
		return api.TripData{}, fmt.Errorf("%w: user_id and driver_id must not be empty", errInvalidRequest)
	}
	return data, nil
}
