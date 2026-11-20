package trip

import (
	"time"

	"github.com/google/uuid"
)

type Position struct {
	ID          int64
	TripID      uuid.UUID
	Coordinates Point
	RecordedAt  time.Time
}

type StaleCandidate struct {
	TripID     uuid.UUID
	DriverID   uuid.UUID
	LastSeenAt time.Time
}

func (p Position) Validate(now time.Time) error {
	if err := p.Coordinates.validate("coordinates"); err != nil {
		return err
	}
	if year := p.RecordedAt.UTC().Year(); year < 0 || year > 9999 {
		return &ValidationError{Field: "recorded_at", Message: "UTC year must be between 0 and 9999"}
	}
	if p.RecordedAt.After(now.Add(time.Minute)) {
		return &ValidationError{Field: "recorded_at", Message: "must be no more than one minute in the future"}
	}
	return nil
}
