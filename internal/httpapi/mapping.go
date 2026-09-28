package httpapi

import (
	"time"

	"github.com/google/uuid"

	"github.com/BeganovR/Golang-BeganovR/internal/domain"
	api "github.com/BeganovR/Golang-BeganovR/internal/generated"
)

func newTrip(id uuid.UUID, data api.TripData, startedAt time.Time) domain.Trip {
	return domain.Trip{
		ID:         id,
		UserID:     data.UserId,
		DriverID:   data.DriverId,
		StartPoint: domain.Point{Latitude: data.StartPoint.Latitude, Longitude: data.StartPoint.Longitude},
		EndPoint:   domain.Point{Latitude: data.EndPoint.Latitude, Longitude: data.EndPoint.Longitude},
		Price:      data.Price,
		Status:     domain.TripStatusActive,
		StartedAt:  startedAt,
	}
}

func toAPITrip(t domain.Trip) api.Trip {
	return api.Trip{
		Id:         t.ID,
		UserId:     t.UserID,
		DriverId:   t.DriverID,
		StartPoint: api.Coordinates{Latitude: t.StartPoint.Latitude, Longitude: t.StartPoint.Longitude},
		EndPoint:   api.Coordinates{Latitude: t.EndPoint.Latitude, Longitude: t.EndPoint.Longitude},
		Price:      t.Price,
		Status:     api.TripStatus(t.Status),
		StartedAt:  t.StartedAt,
		FinishedAt: t.FinishedAt,
	}
}
