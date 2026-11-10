package request

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/google/uuid"

	"github.com/BeganovR/Golang-BeganovR/api"
	"github.com/BeganovR/Golang-BeganovR/internal/domain/trip"
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
		return api.TripData{}, fmt.Errorf("%w: %w", validationError("body", "could not be read"), err)
	}
	var payload any
	if err := json.Unmarshal(body, &payload); err != nil {
		return api.TripData{}, validationError("body", "must be valid JSON")
	}
	if err := v.tripData.VisitJSON(payload, openapi3.EnableFormatValidation()); err != nil {
		return api.TripData{}, schemaValidationError(err)
	}
	object, ok := payload.(map[string]any)
	if !ok {
		return api.TripData{}, validationError("body", "must be an object")
	}
	for _, name := range []string{"user_id", "driver_id"} {
		value, ok := object[name].(string)
		if !ok || !UUID(value) {
			return api.TripData{}, validationError(name, "must be a canonical UUID")
		}
	}
	var data api.TripData
	if err := json.Unmarshal(body, &data); err != nil {
		var fieldError *json.UnmarshalTypeError
		if errors.As(err, &fieldError) {
			return api.TripData{}, validationError(fieldError.Field, "must fit the declared field type")
		}
		return api.TripData{}, validationError("body", "contains an invalid field")
	}
	if data.UserId == uuid.Nil {
		return api.TripData{}, validationError("user_id", "must not be empty")
	}
	if data.DriverId == uuid.Nil {
		return api.TripData{}, validationError("driver_id", "must not be empty")
	}
	return data, nil
}

func validationError(field, message string) error {
	return fmt.Errorf("%w: %w", domainerror.ErrInvalidRequest, &trip.ValidationError{Field: field, Message: message})
}

func schemaValidationError(err error) error {
	var schemaError *openapi3.SchemaError
	if !errors.As(err, &schemaError) {
		return validationError("body", "does not match the schema")
	}
	field := strings.Join(schemaError.JSONPointer(), ".")
	if field == "" {
		field = "body"
	}
	message := "does not match the schema"
	switch schemaError.SchemaField {
	case "required", "nullable", "type", "minimum", "maximum":
		message = schemaError.Reason
	case "format":
		message = "must match format " + schemaError.Schema.Format
	case "properties":
		message = "unknown fields are not allowed"
	}
	return validationError(field, message)
}
