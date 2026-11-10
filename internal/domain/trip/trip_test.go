package trip_test

import (
	"errors"
	"math"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/BeganovR/Golang-BeganovR/internal/domain/trip"
)

func TestTripValidate(t *testing.T) {
	t.Parallel()
	const (
		startLatitude  = "start_point.latitude"
		startLongitude = "start_point.longitude"
		endLatitude    = "end_point.latitude"
		endLongitude   = "end_point.longitude"
	)
	tests := []struct {
		name   string
		change func(*trip.Trip)
		field  string
	}{
		{name: "valid", change: func(*trip.Trip) {}},
		{name: "missing user", change: func(v *trip.Trip) { v.UserID = uuid.Nil }, field: "user_id"},
		{name: "missing driver", change: func(v *trip.Trip) { v.DriverID = uuid.Nil }, field: "driver_id"},
		{name: "negative price", change: func(v *trip.Trip) { v.Price = -1 }, field: "price"},
		{name: "zero price", change: func(v *trip.Trip) { v.Price = 0 }},
		{name: "start latitude low", change: func(v *trip.Trip) { v.StartPoint.Latitude = -91 }, field: startLatitude},
		{name: "start latitude high", change: func(v *trip.Trip) { v.StartPoint.Latitude = 91 }, field: startLatitude},
		{name: "start longitude low", change: func(v *trip.Trip) { v.StartPoint.Longitude = -181 }, field: startLongitude},
		{name: "start longitude high", change: func(v *trip.Trip) { v.StartPoint.Longitude = 181 }, field: startLongitude},
		{name: "end latitude low", change: func(v *trip.Trip) { v.EndPoint.Latitude = -91 }, field: endLatitude},
		{name: "end latitude high", change: func(v *trip.Trip) { v.EndPoint.Latitude = 91 }, field: endLatitude},
		{name: "end longitude low", change: func(v *trip.Trip) { v.EndPoint.Longitude = -181 }, field: endLongitude},
		{name: "end longitude high", change: func(v *trip.Trip) { v.EndPoint.Longitude = 181 }, field: endLongitude},
		{name: "nan latitude", change: func(v *trip.Trip) { v.StartPoint.Latitude = math.NaN() }, field: startLatitude},
		{name: "infinite latitude", change: func(v *trip.Trip) { v.EndPoint.Latitude = math.Inf(1) }, field: endLatitude},
		{name: "nan longitude", change: func(v *trip.Trip) { v.EndPoint.Longitude = math.NaN() }, field: endLongitude},
		{name: "infinite longitude", change: func(v *trip.Trip) { v.StartPoint.Longitude = math.Inf(-1) }, field: startLongitude},
		{name: "boundaries", change: func(v *trip.Trip) {
			v.StartPoint = trip.Point{Latitude: -90, Longitude: -180}
			v.EndPoint = trip.Point{Latitude: 90, Longitude: 180}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			v := trip.Trip{UserID: uuid.New(), DriverID: uuid.New(), Price: 100}
			tt.change(&v)
			err := v.Validate()
			if tt.field == "" {
				if err != nil {
					t.Fatalf("Validate() = %v", err)
				}
				return
			}
			var invalid *trip.ValidationError
			if !errors.As(err, &invalid) {
				t.Fatalf("Validate() error = %v, want ValidationError", err)
			}
			if invalid.Field != tt.field || invalid.Message == "" {
				t.Fatalf("validation details = %+v, want field %q and message", invalid, tt.field)
			}
			if invalid.Error() != invalid.Field+": "+invalid.Message {
				t.Fatalf("Error() = %q", invalid.Error())
			}
		})
	}
}

func TestTripFinish(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, time.October, 1, 12, 0, 0, 123456789, time.FixedZone("offset", 3*60*60))
	for _, status := range []trip.TripStatus{trip.TripStatusActive, trip.TripStatusCompleted, ""} {
		t.Run(string(status), func(t *testing.T) {
			t.Parallel()
			v := trip.Trip{ID: uuid.New(), Status: status}
			before := v
			err := v.Finish(at)
			if status != trip.TripStatusActive {
				if !errors.Is(err, trip.ErrTripCompleted) || v != before {
					t.Fatalf("Finish() = %v, trip = %+v, want unchanged %+v", err, v, before)
				}
				return
			}
			if err != nil || v.Status != trip.TripStatusCompleted || v.ID != before.ID {
				t.Fatalf("Finish() = %v, trip = %+v", err, v)
			}
			if v.FinishedAt == nil || !v.FinishedAt.Equal(at.Truncate(time.Microsecond)) || v.FinishedAt.Location() != time.UTC {
				t.Fatalf("FinishedAt = %v, want UTC microsecond timestamp", v.FinishedAt)
			}
			finishedAt := *v.FinishedAt
			if err := v.Finish(at.Add(time.Hour)); !errors.Is(err, trip.ErrTripCompleted) || *v.FinishedAt != finishedAt {
				t.Fatalf("second Finish() = %v, FinishedAt = %v", err, v.FinishedAt)
			}
		})
	}
}

func TestNow(t *testing.T) {
	t.Parallel()
	before := time.Now().UTC().Truncate(time.Microsecond)
	at := trip.Now()
	after := time.Now().UTC()
	if at.Before(before) || at.After(after) || at.Location() != time.UTC || at.Nanosecond()%1000 != 0 {
		t.Fatalf("Now() = %v, want UTC microsecond time in [%v, %v]", at, before, after)
	}
}
