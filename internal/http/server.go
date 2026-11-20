package httptransport

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/BeganovR/Golang-BeganovR/api"
	"github.com/BeganovR/Golang-BeganovR/internal/http/domainerror"
	"github.com/BeganovR/Golang-BeganovR/internal/http/request"
	"github.com/BeganovR/Golang-BeganovR/internal/http/response"
	"github.com/BeganovR/Golang-BeganovR/internal/http/trip/create"
	"github.com/BeganovR/Golang-BeganovR/internal/http/trip/finish"
	"github.com/BeganovR/Golang-BeganovR/internal/http/trip/get"
)

var _ api.ServerInterface = (*Server)(nil)

type Deps struct {
	Create       createUsecase
	Get          tripUsecase
	Finish       tripUsecase
	Pinger       pinger
	QueryTimeout time.Duration
}

type Server struct {
	create  *create.Handler
	get     *get.Handler
	finish  *finish.Handler
	pinger  pinger
	timeout time.Duration
}

func NewServer(d Deps) (*Server, error) {
	validator, err := request.NewValidator()
	if err != nil {
		return nil, err
	}
	return &Server{
		create:  create.New(d.Create, d.QueryTimeout, validator),
		get:     get.New(d.Get, d.QueryTimeout),
		finish:  finish.New(d.Finish, d.QueryTimeout),
		pinger:  d.Pinger,
		timeout: d.QueryTimeout,
	}, nil
}

func (s *Server) Handler() *chi.Mux {
	router := chi.NewRouter()
	router.NotFound(func(w http.ResponseWriter, r *http.Request) {
		response.Error(w, r, domainerror.ErrNotFound)
	})
	router.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Allow", strings.Join(allowedMethods(router, r.URL.Path), ", "))
		response.Error(w, r, domainerror.ErrMethodNotAllowed)
	})
	api.HandlerWithOptions(s, api.ChiServerOptions{
		BaseRouter:  router,
		Middlewares: []api.MiddlewareFunc{requireCanonicalTripID},
		ErrorHandlerFunc: func(w http.ResponseWriter, r *http.Request, _ error) {
			response.Error(w, r, domainerror.ErrInvalidRequest)
		},
	})
	return router
}

func (s *Server) CreateTrip(w http.ResponseWriter, r *http.Request, params api.CreateTripParams) {
	if key := r.Header.Get("Idempotency-Key"); key != "" && !request.UUID(key) {
		response.Error(w, r, domainerror.ErrInvalidRequest)
		return
	}
	s.create.Handle(w, r, params)
}

func (s *Server) GetTrip(w http.ResponseWriter, r *http.Request, id api.TripId) {
	s.get.Handle(w, r, id)
}

func (s *Server) FinishTrip(w http.ResponseWriter, r *http.Request, id api.TripId) {
	s.finish.Handle(w, r, id)
}

func (s *Server) Health(w http.ResponseWriter, r *http.Request) {
	response.JSON(w, r, http.StatusOK, api.HealthResponse{Status: api.Ok})
}

func (s *Server) Ready(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), s.timeout)
	defer cancel()
	if err := s.pinger.Ping(ctx); err != nil {
		request.SetError(r, fmt.Errorf("readiness check: %w", err))
		response.JSON(w, r, http.StatusServiceUnavailable, api.HealthResponse{Status: api.Unavailable})
		return
	}
	response.JSON(w, r, http.StatusOK, api.HealthResponse{Status: api.Ok})
}

func requireCanonicalTripID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if id := chi.URLParam(r, "tripId"); id != "" && !request.UUID(id) {
			response.Error(w, r, domainerror.ErrInvalidRequest)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func allowedMethods(routes chi.Routes, path string) []string {
	methods := []string{
		http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut,
		http.MethodPatch, http.MethodDelete, http.MethodOptions,
	}
	var allowed []string
	for _, method := range methods {
		if routes.Match(chi.NewRouteContext(), method, path) {
			allowed = append(allowed, method)
		}
	}
	return allowed
}
