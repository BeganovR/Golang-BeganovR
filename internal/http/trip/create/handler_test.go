package create_test

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
	"github.com/BeganovR/Golang-BeganovR/internal/http/request"
	"github.com/BeganovR/Golang-BeganovR/internal/http/trip/create"
)

const validBody = `{"user_id":"5cb72c04-7650-45c9-a79b-bcdba0631e0c","driver_id":"8860b315-ec86-42eb-a17c-7c163d721ff5","start_point":{"latitude":59.9,"longitude":30.3},"end_point":{"latitude":60,"longitude":31},"price":1450}`

func TestHandle(t *testing.T) {
	tests := []struct {
		name     string
		replayed bool
		err      error
		status   int
		code     string
	}{
		{name: "created", status: http.StatusCreated},
		{name: "replayed", replayed: true, status: http.StatusOK},
		{name: "busy", err: trip.ErrDriverBusy, status: http.StatusConflict, code: "driver_busy"},
		{name: "idempotency conflict", err: trip.ErrIdempotencyConflict, status: http.StatusConflict, code: "idempotency_conflict"},
		{name: "repository error", err: errors.New("private database details"), status: http.StatusInternalServerError, code: "internal_error"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			uc := NewMockUsecase(gomock.NewController(t))
			validator, err := request.NewValidator()
			if err != nil {
				t.Fatal(err)
			}
			id := uuid.New()
			key := uuid.New()
			saved := trip.Trip{ID: id, Status: trip.TripStatusActive, StartedAt: time.Now().UTC()}
			uc.EXPECT().Execute(gomock.Any(), &key, gomock.Len(32), gomock.Any()).DoAndReturn(
				func(ctx context.Context, _ *uuid.UUID, _ []byte, input trip.Trip) (trip.Trip, bool, error) {
					if _, ok := ctx.Deadline(); !ok {
						t.Error("missing query deadline")
					}
					if input.UserID.String() != "5cb72c04-7650-45c9-a79b-bcdba0631e0c" || input.Price != 1450 || input.StartPoint.Latitude != 59.9 {
						t.Errorf("incorrect conversion: %+v", input)
					}
					if input.ID != uuid.Nil || !input.StartedAt.IsZero() || input.Status != "" {
						t.Error("handler generated business fields")
					}
					return saved, tt.replayed, tt.err
				},
			)
			r := httptest.NewRequest(http.MethodPost, "/api/v1/trips", strings.NewReader(validBody))
			w := httptest.NewRecorder()
			create.New(uc, time.Second, validator).Handle(w, r, api.CreateTripParams{IdempotencyKey: &key})
			if w.Code != tt.status {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
			if tt.err == nil {
				var body api.Trip
				if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
					t.Fatal(err)
				}
				if body.Id != id || body.Status != api.Active || !body.FinishedAt.IsNull() || !body.LastPositionAt.IsNull() {
					t.Fatalf("incorrect response: %+v", body)
				}
				if w.Header().Get("Location") != "/api/v1/trips/"+id.String() {
					t.Fatal("incorrect Location header")
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

func TestHandleInvalidBody(t *testing.T) {
	validator, err := request.NewValidator()
	if err != nil {
		t.Fatal(err)
	}
	uc := NewMockUsecase(gomock.NewController(t))
	r := httptest.NewRequest(http.MethodPost, "/api/v1/trips", strings.NewReader(`{"price":-1}`))
	w := httptest.NewRecorder()
	create.New(uc, time.Second, validator).Handle(w, r, api.CreateTripParams{})
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), `"code":"invalid_request"`) {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestIdempotencyHash(t *testing.T) {
	validator, err := request.NewValidator()
	if err != nil {
		t.Fatal(err)
	}
	uc := NewMockUsecase(gomock.NewController(t))
	key := uuid.New()
	var hashes [][]byte
	uc.EXPECT().Execute(gomock.Any(), &key, gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, _ *uuid.UUID, hash []byte, _ trip.Trip) (trip.Trip, bool, error) {
			hashes = append(hashes, hash)
			return trip.Trip{ID: uuid.New()}, false, nil
		},
	).Times(2)
	var pretty strings.Builder
	var payload any
	if err := json.Unmarshal([]byte(validBody), &payload); err != nil {
		t.Fatal(err)
	}
	encoder := json.NewEncoder(&pretty)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(payload); err != nil {
		t.Fatal(err)
	}
	h := create.New(uc, time.Second, validator)
	for _, body := range []string{validBody, pretty.String()} {
		r := httptest.NewRequest(http.MethodPost, "/api/v1/trips", strings.NewReader(body))
		h.Handle(httptest.NewRecorder(), r, api.CreateTripParams{IdempotencyKey: &key})
	}
	if string(hashes[0]) != string(hashes[1]) {
		t.Fatal("JSON formatting changed the idempotency hash")
	}
}
