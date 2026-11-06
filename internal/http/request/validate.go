package request

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/google/uuid"

	"github.com/BeganovR/Golang-BeganovR/api"
	"github.com/BeganovR/Golang-BeganovR/internal/http/domainerror"
)

const maxBodyBytes = 1 << 20

var canonicalUUID = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

type Validator struct {
	tripData *openapi3.Schema
}

func NewValidator() (*Validator, error) {
	spec, err := api.GetSpec()
	if err != nil {
		return nil, fmt.Errorf("load openapi specification: %w", err)
	}
	return &Validator{tripData: spec.Components.Schemas["TripData"].Value}, nil
}

func UUID(value string) bool {
	return canonicalUUID.MatchString(value)
}

func (v *Validator) TripData(w http.ResponseWriter, r *http.Request) (api.TripData, error) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	if err != nil {
		return api.TripData{}, fmt.Errorf("%w: read body: %w", domainerror.ErrInvalidRequest, err)
	}
	var payload any
	if err := json.Unmarshal(body, &payload); err != nil {
		return api.TripData{}, fmt.Errorf("%w: malformed json", domainerror.ErrInvalidRequest)
	}
	if err := v.tripData.VisitJSON(payload, openapi3.EnableFormatValidation()); err != nil {
		return api.TripData{}, fmt.Errorf("%w: body does not match the schema", domainerror.ErrInvalidRequest)
	}
	object, ok := payload.(map[string]any)
	if !ok {
		return api.TripData{}, domainerror.ErrInvalidRequest
	}
	for _, name := range []string{"user_id", "driver_id"} {
		value, ok := object[name].(string)
		if !ok || !UUID(value) {
			return api.TripData{}, fmt.Errorf("%w: %s must be a canonical UUID", domainerror.ErrInvalidRequest, name)
		}
	}
	var data api.TripData
	if err := json.Unmarshal(body, &data); err != nil {
		return api.TripData{}, fmt.Errorf("%w: invalid field type or value", domainerror.ErrInvalidRequest)
	}
	if data.UserId == uuid.Nil || data.DriverId == uuid.Nil {
		return api.TripData{}, fmt.Errorf("%w: user_id and driver_id must not be empty", domainerror.ErrInvalidRequest)
	}
	return data, nil
}
