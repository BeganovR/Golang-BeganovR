package trip

import (
	"math"
	"time"

	"github.com/google/uuid"
)

type TripStatus string

const (
	TripStatusActive    TripStatus = "active"
	TripStatusCompleted TripStatus = "completed"
)

type Point struct {
	Latitude  float64
	Longitude float64
}

type Trip struct {
	ID             uuid.UUID
	UserID         uuid.UUID
	DriverID       uuid.UUID
	StartPoint     Point
	EndPoint       Point
	Price          int64
	Status         TripStatus
	StartedAt      time.Time
	FinishedAt     *time.Time
	LastPositionAt *time.Time
}

type IdempotencyRecord struct {
	RequestHash []byte
	TripID      uuid.UUID
}

func (t Trip) Validate() error {
	if t.UserID == uuid.Nil {
		return &ValidationError{Field: "user_id", Message: "must not be empty"}
	}
	if t.DriverID == uuid.Nil {
		return &ValidationError{Field: "driver_id", Message: "must not be empty"}
	}
	points := []struct {
		field string
		point Point
	}{
		{field: "start_point", point: t.StartPoint},
		{field: "end_point", point: t.EndPoint},
	}
	for _, p := range points {
		if err := p.point.validate(p.field); err != nil {
			return err
		}
	}
	if t.Price < 0 {
		return &ValidationError{Field: "price", Message: "must be non-negative"}
	}
	return nil
}

func (p Point) validate(field string) error {
	if math.IsNaN(p.Latitude) || p.Latitude < -90 || p.Latitude > 90 {
		return &ValidationError{Field: field + ".latitude", Message: "must be between -90 and 90"}
	}
	if math.IsNaN(p.Longitude) || p.Longitude < -180 || p.Longitude > 180 {
		return &ValidationError{Field: field + ".longitude", Message: "must be between -180 and 180"}
	}
	return nil
}

func (t *Trip) Finish(at time.Time) error {
	if t.Status != TripStatusActive {
		return ErrTripCompleted
	}
	t.Status = TripStatusCompleted
	at = at.UTC().Truncate(time.Microsecond)
	t.FinishedAt = &at
	return nil
}

func Now() time.Time {
	return time.Now().UTC().Truncate(time.Microsecond)
}
