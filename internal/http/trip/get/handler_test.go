package get_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"go.uber.org/mock/gomock"

	"github.com/BeganovR/Golang-BeganovR/api"
	"github.com/BeganovR/Golang-BeganovR/internal/domain/trip"
	"github.com/BeganovR/Golang-BeganovR/internal/http/trip/get"
)

func TestHandle(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{name: "found", status: http.StatusOK},
		{name: "not found", err: trip.ErrTripNotFound, status: http.StatusNotFound, code: "trip_not_found"},
		{name: "repository error", err: errors.New("private database details"), status: http.StatusInternalServerError, code: "internal_error"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			uc := NewMockUsecase(gomock.NewController(t))
			id := uuid.New()
			uc.EXPECT().Execute(gomock.Any(), id).DoAndReturn(func(ctx context.Context, _ uuid.UUID) (trip.Trip, error) {
				if _, ok := ctx.Deadline(); !ok {
					t.Error("missing query deadline")
				}
				return trip.Trip{ID: id, Price: 1450, Status: trip.TripStatusActive}, tt.err
			})
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodGet, "/api/v1/trips/"+id.String(), nil)
			get.New(uc, time.Second).Handle(w, r, id)
			if w.Code != tt.status {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
			if tt.err == nil {
				var body api.Trip
				if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
					t.Fatal(err)
				}
				if body.Id != id || body.Price != 1450 || body.Status != api.Active {
					t.Fatalf("incorrect response: %+v", body)
				}
				return
			}
			var body api.Problem
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if body.Code != tt.code || strings.Contains(w.Body.String(), "private database details") {
				t.Fatalf("incorrect problem: %s", w.Body.String())
			}
		})
	}
}
