package httptransport_test

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
	httptransport "github.com/BeganovR/Golang-BeganovR/internal/http"
	"github.com/BeganovR/Golang-BeganovR/internal/http/request"
)

const (
	validBody          = `{"user_id":"5cb72c04-7650-45c9-a79b-bcdba0631e0c","driver_id":"8860b315-ec86-42eb-a17c-7c163d721ff5","start_point":{"latitude":59.9,"longitude":30.3},"end_point":{"latitude":60,"longitude":31},"price":1450}`
	tripsPath          = "/api/v1/trips"
	readyPath          = "/ready"
	invalidRequestCode = "invalid_request"
	invalidKey         = "invalid"
)

func TestRouterTripRouting(t *testing.T) {
	ctrl := gomock.NewController(t)
	create := NewMockCreateUsecase(ctrl)
	get := NewMockTripUsecase(ctrl)
	finish := NewMockTripUsecase(ctrl)
	id := uuid.New()
	created := trip.Trip{ID: id, Status: trip.TripStatusActive}
	create.EXPECT().Execute(gomock.Any(), nil, nil, gomock.Any()).Return(created, false, nil)
	get.EXPECT().Execute(gomock.Any(), id).Return(created, nil)
	finish.EXPECT().Execute(gomock.Any(), id).Return(trip.Trip{ID: id, Status: trip.TripStatusCompleted}, nil)
	srv, err := httptransport.NewServer(httptransport.Deps{Create: create, Get: get, Finish: finish, QueryTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	router := srv.Handler()
	for _, tt := range []struct {
		method     string
		path       string
		status     int
		tripStatus api.TripStatus
	}{
		{http.MethodPost, tripsPath, http.StatusCreated, api.Active},
		{http.MethodGet, "/api/v1/trips/" + id.String(), http.StatusOK, api.Active},
		{http.MethodPost, "/api/v1/trips/" + id.String() + "/finish", http.StatusOK, api.Completed},
	} {
		t.Run(tt.method+" "+tt.path, func(t *testing.T) {
			w := httptest.NewRecorder()
			r := httptest.NewRequest(tt.method, tt.path, strings.NewReader(validBody))
			if tt.path != tripsPath {
				r.Header.Set("Idempotency-Key", invalidKey)
			}
			router.ServeHTTP(w, r)
			if w.Code != tt.status || w.Header().Get("Content-Type") != "application/json" {
				t.Fatalf("status=%d content-type=%s body=%s", w.Code, w.Header().Get("Content-Type"), w.Body.String())
			}
			var body api.Trip
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if body.Id != id || body.Status != tt.tripStatus {
				t.Fatalf("incorrect response: %+v", body)
			}
		})
	}
}

func TestRouterInvalidRequests(t *testing.T) {
	ctrl := gomock.NewController(t)
	srv, err := httptransport.NewServer(httptransport.Deps{
		Create: NewMockCreateUsecase(ctrl), Get: NewMockTripUsecase(ctrl), Finish: NewMockTripUsecase(ctrl), QueryTimeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	router := srv.Handler()
	for _, tt := range []struct {
		name   string
		method string
		path   string
		body   string
		keys   []string
		status int
		code   string
		allow  string
	}{
		{name: "missing route", method: http.MethodGet, path: "/missing", status: http.StatusNotFound, code: "not_found"},
		{name: "wrong method", method: http.MethodGet, path: tripsPath, status: http.StatusMethodNotAllowed, code: "method_not_allowed", allow: http.MethodPost},
		{name: "malformed ID", method: http.MethodGet, path: "/api/v1/trips/private_value", status: http.StatusBadRequest, code: invalidRequestCode},
		{name: "compact ID", method: http.MethodGet, path: "/api/v1/trips/5cb72c04765045c9a79bbcdba0631e0c", status: http.StatusBadRequest, code: invalidRequestCode},
		{name: "finish malformed ID", method: http.MethodPost, path: "/api/v1/trips/private_value/finish", status: http.StatusBadRequest, code: invalidRequestCode},
		{name: "malformed body", method: http.MethodPost, path: tripsPath, body: `{"private_value":"sensitive"}`, status: http.StatusBadRequest, code: invalidRequestCode},
		{name: "malformed key", method: http.MethodPost, path: tripsPath, body: validBody, keys: []string{"private_value"}, status: http.StatusBadRequest, code: invalidRequestCode},
		{name: "compact key", method: http.MethodPost, path: tripsPath, body: validBody, keys: []string{"5cb72c04765045c9a79bbcdba0631e0c"}, status: http.StatusBadRequest, code: invalidRequestCode},
		{name: "braced key", method: http.MethodPost, path: tripsPath, body: validBody, keys: []string{"{5cb72c04-7650-45c9-a79b-bcdba0631e0c}"}, status: http.StatusBadRequest, code: invalidRequestCode},
		{name: "URN key", method: http.MethodPost, path: tripsPath, body: validBody, keys: []string{"urn:uuid:5cb72c04-7650-45c9-a79b-bcdba0631e0c"}, status: http.StatusBadRequest, code: invalidRequestCode},
		{name: "duplicate key", method: http.MethodPost, path: tripsPath, body: validBody, keys: []string{uuid.NewString(), uuid.NewString()}, status: http.StatusBadRequest, code: invalidRequestCode},
	} {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest(tt.method, tt.path, strings.NewReader(tt.body))
			for _, key := range tt.keys {
				r.Header.Add("Idempotency-Key", key)
			}
			r, state := request.WithState(r)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, r)
			if w.Code != tt.status || w.Header().Get("Content-Type") != "application/problem+json" {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
			var body api.Problem
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if body.Code != tt.code || body.Instance == nil || *body.Instance != tt.path || int(body.Status) != tt.status {
				t.Fatalf("incorrect problem: %+v", body)
			}
			if w.Header().Get("Allow") != tt.allow {
				t.Fatalf("Allow=%s, want=%s", w.Header().Get("Allow"), tt.allow)
			}
			if state.Err == nil || strings.Contains(state.Err.Error(), "private_value") || strings.Contains(state.Err.Error(), "sensitive") {
				t.Fatalf("unsafe error in request state: %v", state.Err)
			}
		})
	}
}

func TestServiceRoutes(t *testing.T) {
	for _, tt := range []struct {
		name       string
		path       string
		key        string
		err        error
		status     int
		bodyStatus api.HealthResponseStatus
	}{
		{name: "health", path: "/health", status: http.StatusOK, bodyStatus: api.Ok},
		{name: "ready", path: readyPath, status: http.StatusOK, bodyStatus: api.Ok},
		{name: "unavailable", path: readyPath, err: errors.New("database unavailable"), status: http.StatusServiceUnavailable, bodyStatus: api.Unavailable},
		{name: "health with unrelated key", path: "/health", key: invalidKey, status: http.StatusOK, bodyStatus: api.Ok},
		{name: "ready with unrelated key", path: readyPath, key: invalidKey, status: http.StatusOK, bodyStatus: api.Ok},
		{name: "unavailable with unrelated key", path: readyPath, key: invalidKey, err: errors.New("database unavailable"), status: http.StatusServiceUnavailable, bodyStatus: api.Unavailable},
	} {
		t.Run(tt.name, func(t *testing.T) {
			pinger := NewMockPinger(gomock.NewController(t))
			if tt.path == readyPath {
				pinger.EXPECT().Ping(gomock.Any()).DoAndReturn(func(ctx context.Context) error {
					if _, ok := ctx.Deadline(); !ok {
						t.Error("missing readiness deadline")
					}
					return tt.err
				})
			}
			srv, err := httptransport.NewServer(httptransport.Deps{Pinger: pinger, QueryTimeout: time.Second})
			if err != nil {
				t.Fatal(err)
			}
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodGet, tt.path, nil)
			if tt.key != "" {
				r.Header.Set("Idempotency-Key", tt.key)
			}
			srv.Handler().ServeHTTP(w, r)
			var body api.HealthResponse
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if w.Code != tt.status || body.Status != tt.bodyStatus {
				t.Fatalf("status=%d body=%+v", w.Code, body)
			}
		})
	}
}
