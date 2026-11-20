package httptransport_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"go.uber.org/mock/gomock"

	"github.com/BeganovR/Golang-BeganovR/api"
	"github.com/BeganovR/Golang-BeganovR/internal/domain/trip"
	httptransport "github.com/BeganovR/Golang-BeganovR/internal/http"
)

const (
	validPosition = `{"coordinates":{"latitude":59.935,"longitude":30.335},"recorded_at":"2026-11-19T14:00:00Z"}`
	positionsPath = "/positions"
)

func TestCreatePositionRoute(t *testing.T) {
	t.Parallel()
	mock := NewMockPositionUsecase(gomock.NewController(t))
	router := positionRouter(t, mock)
	p := trip.Position{
		TripID: uuid.New(), RecordedAt: time.Date(2026, time.November, 19, 14, 0, 0, 0, time.UTC),
		Coordinates: trip.Point{Latitude: 59.935, Longitude: 30.335},
	}
	saved := p
	saved.ID = 42
	mock.EXPECT().Save(gomock.Any(), p).Return(saved, nil)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, tripsPath+"/"+p.TripID.String()+positionsPath, strings.NewReader(validPosition))
	r.Header.Set("Idempotency-Key", invalidKey)
	router.ServeHTTP(w, r)
	if w.Code != http.StatusCreated || w.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("response = %d %s", w.Code, w.Body.String())
	}
	got := decode[api.TripPosition](t, w)
	want := api.TripPosition{
		Id: saved.ID, TripId: p.TripID, RecordedAt: p.RecordedAt,
		Coordinates: api.Coordinates{Latitude: p.Coordinates.Latitude, Longitude: p.Coordinates.Longitude},
	}
	if got != want {
		t.Fatalf("position = %+v, want %+v", got, want)
	}
}

func TestListPositionsRoute(t *testing.T) {
	t.Parallel()
	mock := NewMockPositionUsecase(gomock.NewController(t))
	router := positionRouter(t, mock)
	id := uuid.New()
	path := tripsPath + "/" + id.String() + positionsPath
	at := time.Date(2026, time.November, 19, 14, 0, 0, 0, time.UTC)
	gotPositions := []trip.Position{
		{TripID: id, ID: 8, RecordedAt: at, Coordinates: trip.Point{Latitude: 1}},
		{TripID: id, ID: 9, RecordedAt: at, Coordinates: trip.Point{Longitude: 2}},
	}
	tests := []struct {
		name  string
		input []trip.Position
		want  []api.TripPosition
	}{
		{name: "empty", want: []api.TripPosition{}},
		{
			name: "ordered", input: gotPositions,
			want: []api.TripPosition{
				{TripId: id, Id: 8, RecordedAt: at, Coordinates: api.Coordinates{Latitude: 1}},
				{TripId: id, Id: 9, RecordedAt: at, Coordinates: api.Coordinates{Longitude: 2}},
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mock.EXPECT().List(gomock.Any(), id).Return(tc.input, nil)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
			if w.Code != http.StatusOK {
				t.Fatalf("response = %d %s", w.Code, w.Body.String())
			}
			if got := decode[api.TripPositionsResponse](t, w); !reflect.DeepEqual(got.Positions, tc.want) {
				t.Fatalf("route = %+v, want %+v", got.Positions, tc.want)
			}
		})
	}
}

func TestPositionRouteProblems(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name      string
		method    string
		input     string
		badID     bool
		domainErr error
		status    int
		code      string
	}{
		{name: "invalid UUID", method: http.MethodGet, badID: true, status: http.StatusBadRequest, code: invalidRequestCode},
		{name: "invalid JSON", method: http.MethodPost, input: "{", status: http.StatusBadRequest, code: invalidRequestCode},
		{name: "latitude out of range", method: http.MethodPost, input: strings.Replace(validPosition, "59.935", "91", 1), status: http.StatusBadRequest, code: invalidRequestCode},
		{name: "invalid date", method: http.MethodPost, input: strings.Replace(validPosition, "2026-11-19", "2026-02-30", 1), status: http.StatusBadRequest, code: invalidRequestCode},
		{name: "missing date", method: http.MethodPost, input: `{"coordinates":{"latitude":0,"longitude":0}}`, status: http.StatusBadRequest, code: invalidRequestCode},
		{name: "missing trip on add", method: http.MethodPost, input: validPosition, domainErr: trip.ErrTripNotFound, status: http.StatusNotFound, code: "trip_not_found"},
		{name: "missing trip on list", method: http.MethodGet, domainErr: trip.ErrTripNotFound, status: http.StatusNotFound, code: "trip_not_found"},
		{name: "completed trip", method: http.MethodPost, input: validPosition, domainErr: trip.ErrTripCompleted, status: http.StatusConflict, code: "trip_completed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock := NewMockPositionUsecase(gomock.NewController(t))
			router := positionRouter(t, mock)
			id := uuid.New()
			rawID := id.String()
			if tc.badID {
				rawID = strings.ReplaceAll(rawID, "-", "")
			}
			if tc.domainErr != nil {
				if tc.method == http.MethodGet {
					mock.EXPECT().List(gomock.Any(), id).Return(nil, tc.domainErr)
				} else {
					mock.EXPECT().Save(gomock.Any(), gomock.Any()).Return(trip.Position{}, tc.domainErr)
				}
			}
			w := httptest.NewRecorder()
			r := httptest.NewRequest(tc.method, tripsPath+"/"+rawID+positionsPath, strings.NewReader(tc.input))
			router.ServeHTTP(w, r)
			if w.Code != tc.status || w.Header().Get("Content-Type") != "application/problem+json" {
				t.Fatalf("response = %d %s, want %d", w.Code, w.Body.String(), tc.status)
			}
			if got := decode[api.Problem](t, w); got.Code != tc.code {
				t.Fatalf("code = %q, want %q", got.Code, tc.code)
			}
		})
	}
}

func positionRouter(t *testing.T, positions *MockPositionUsecase) http.Handler {
	t.Helper()
	server, err := httptransport.NewServer(httptransport.Deps{Positions: positions, QueryTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	return server.Handler()
}

func decode[T any](t *testing.T, w *httptest.ResponseRecorder) T {
	t.Helper()
	var data T
	if err := json.Unmarshal(w.Body.Bytes(), &data); err != nil {
		t.Fatal(err)
	}
	return data
}
