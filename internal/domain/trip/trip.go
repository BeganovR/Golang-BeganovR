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
	ID         uuid.UUID
	UserID     uuid.UUID
	DriverID   uuid.UUID
	StartPoint Point
	EndPoint   Point
	Price      int64
	Status     TripStatus
	StartedAt  time.Time
	FinishedAt *time.Time
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
		if math.IsNaN(p.point.Latitude) || math.IsInf(p.point.Latitude, 0) || p.point.Latitude < -90 || p.point.Latitude > 90 {
			return &ValidationError{Field: p.field + ".latitude", Message: "must be between -90 and 90"}
		}
		if math.IsNaN(p.point.Longitude) || math.IsInf(p.point.Longitude, 0) || p.point.Longitude < -180 || p.point.Longitude > 180 {
			return &ValidationError{Field: p.field + ".longitude", Message: "must be between -180 and 180"}
		}
	}
	if t.Price < 0 {
		return &ValidationError{Field: "price", Message: "must be non-negative"}
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
