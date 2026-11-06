package finish_test

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
	"github.com/BeganovR/Golang-BeganovR/internal/http/trip/finish"
)

func TestHandle(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{name: "finished", status: http.StatusOK},
		{name: "completed", err: trip.ErrTripCompleted, status: http.StatusConflict, code: "trip_completed"},
		{name: "not found", err: trip.ErrTripNotFound, status: http.StatusNotFound, code: "trip_not_found"},
		{name: "repository error", err: errors.New("private database details"), status: http.StatusInternalServerError, code: "internal_error"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			uc := NewMockUsecase(gomock.NewController(t))
			id := uuid.New()
			at := time.Now().UTC().Truncate(time.Microsecond)
			uc.EXPECT().Execute(gomock.Any(), id).DoAndReturn(func(ctx context.Context, _ uuid.UUID) (trip.Trip, error) {
				if _, ok := ctx.Deadline(); !ok {
					t.Error("missing query deadline")
				}
				return trip.Trip{ID: id, Status: trip.TripStatusCompleted, FinishedAt: &at}, tt.err
			})
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodPost, "/api/v1/trips/"+id.String()+"/finish", nil)
			finish.New(uc, time.Second).Handle(w, r, id)
			if w.Code != tt.status {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
			if tt.err == nil {
				var body api.Trip
				if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
					t.Fatal(err)
				}
				if body.Id != id || body.Status != api.Completed || !body.FinishedAt.MustGet().Equal(at) {
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
