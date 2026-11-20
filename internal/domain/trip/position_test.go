package trip_test

import (
	"errors"
	"math"
	"testing"
	"time"

	"github.com/BeganovR/Golang-BeganovR/internal/domain/trip"
)

func TestValidatePosition(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.November, 19, 14, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name     string
		point    trip.Point
		recorded time.Time
		field    string
	}{
		{name: "old position", recorded: now.Add(-time.Hour)},
		{name: "future boundary", point: trip.Point{Latitude: -90, Longitude: 180}, recorded: now.Add(time.Minute)},
		{name: "too far in future", recorded: now.Add(time.Minute + time.Nanosecond), field: "recorded_at"},
		{name: "latitude", point: trip.Point{Latitude: 91}, recorded: now, field: "coordinates.latitude"},
		{name: "longitude", point: trip.Point{Longitude: -181}, recorded: now, field: "coordinates.longitude"},
		{name: "not a number", point: trip.Point{Latitude: math.NaN()}, recorded: now, field: "coordinates.latitude"},
		{name: "infinity", point: trip.Point{Longitude: math.Inf(1)}, recorded: now, field: "coordinates.longitude"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := trip.Position{Coordinates: tc.point, RecordedAt: tc.recorded}
			err := p.Validate(now)
			if tc.field == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			var invalid *trip.ValidationError
			if !errors.As(err, &invalid) || invalid.Field != tc.field {
				t.Fatalf("validation error = %v, want field %q", err, tc.field)
			}
		})
	}
}

func TestPositionRecordedAtUTCYear(t *testing.T) {
	t.Parallel()
	now := time.Date(9999, time.December, 31, 23, 59, 59, 0, time.UTC)
	for _, tc := range []struct {
		input   string
		wantErr bool
	}{
		{input: "0000-01-01T00:00:00Z"},
		{input: "0000-01-01T00:00:00+01:00", wantErr: true},
		{input: "0001-01-01T00:00:00Z"},
		{input: "9999-12-31T23:59:59Z"},
		{input: "9999-12-31T23:59:59-00:01", wantErr: true},
	} {
		t.Run(tc.input, func(t *testing.T) {
			recordedAt, err := time.Parse(time.RFC3339, tc.input)
			if err != nil {
				t.Fatal(err)
			}
			err = (trip.Position{RecordedAt: recordedAt}).Validate(now)
			if !tc.wantErr {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			var invalid *trip.ValidationError
			if !errors.As(err, &invalid) || invalid.Field != "recorded_at" {
				t.Fatalf("validation error = %v, want recorded_at error", err)
			}
		})
	}
}
