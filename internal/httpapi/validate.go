package httpapi

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/google/uuid"

	api "github.com/BeganovR/Golang-BeganovR/internal/generated"
)

const maxBodyBytes = 1 << 20

func (s *Server) decodeTripData(w http.ResponseWriter, r *http.Request) (api.TripData, error) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	if err != nil {
		return api.TripData{}, fmt.Errorf("%w: read body: %w", errInvalidRequest, err)
	}

	var payload any
	if err := json.Unmarshal(body, &payload); err != nil {
		return api.TripData{}, fmt.Errorf("%w: parse json: %w", errInvalidRequest, err)
	}
	if err := s.tripDataSchema.VisitJSON(payload); err != nil {
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
