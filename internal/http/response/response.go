package response

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/oapi-codegen/nullable"

	"github.com/BeganovR/Golang-BeganovR/api"
	"github.com/BeganovR/Golang-BeganovR/internal/domain/trip"
	"github.com/BeganovR/Golang-BeganovR/internal/http/domainerror"
	"github.com/BeganovR/Golang-BeganovR/internal/http/request"
)

func JSON(w http.ResponseWriter, r *http.Request, status int, body any) {
	write(w, r, status, "application/json", body)
}

func Error(w http.ResponseWriter, r *http.Request, err error) {
	request.SetError(r, err)
	status, body := domainerror.Problem(err)
	instance := r.URL.Path
	body.Instance = &instance
	write(w, r, status, "application/problem+json", body)
}

func write(w http.ResponseWriter, r *http.Request, status int, contentType string, body any) {
	w.Header().Set("Content-Type", contentType)
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		request.SetWriteError(r, err)
	}
}

func Trip(t trip.Trip) api.Trip {
	finishedAt := nullable.NewNullNullable[time.Time]()
	if t.FinishedAt != nil {
		finishedAt = nullable.NewNullableWithValue(*t.FinishedAt)
	}
	return api.Trip{
		Id:             t.ID,
		UserId:         t.UserID,
		DriverId:       t.DriverID,
		StartPoint:     api.Coordinates{Latitude: t.StartPoint.Latitude, Longitude: t.StartPoint.Longitude},
		EndPoint:       api.Coordinates{Latitude: t.EndPoint.Latitude, Longitude: t.EndPoint.Longitude},
		Price:          t.Price,
		Status:         api.TripStatus(t.Status),
		StartedAt:      t.StartedAt,
		FinishedAt:     finishedAt,
		LastPositionAt: nullable.NewNullNullable[time.Time](),
	}
}
