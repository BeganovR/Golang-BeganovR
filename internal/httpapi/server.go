package httpapi

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"
	"strings"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/go-chi/chi/v5"

	"github.com/BeganovR/Golang-BeganovR/api"
)

var _ api.ServerInterface = (*Server)(nil)

type Deps struct {
	Pinger       Pinger
	Service      Service
	QueryTimeout time.Duration
	Logger       *slog.Logger
}

type Server struct {
	pinger         Pinger
	svc            Service
	queryTimeout   time.Duration
	logger         *slog.Logger
	tripDataSchema *openapi3.Schema
}

func NewServer(d Deps) (*Server, error) {
	registerUUIDFormat.Do(func() {
		openapi3.DefineStringFormatValidator("uuid", openapi3.NewRegexpFormatValidator(canonicalUUIDPattern))
	})

	swagger, err := api.GetSpec()
	if err != nil {
		return nil, fmt.Errorf("load embedded openapi spec: %w", err)
	}

	tripData, ok := swagger.Components.Schemas["TripData"]
	if !ok {
		return nil, errors.New("openapi spec has no TripData schema")
	}

	return &Server{
		pinger:         d.Pinger,
		svc:            d.Service,
		queryTimeout:   d.QueryTimeout,
		logger:         d.Logger,
		tripDataSchema: tripData.Value,
	}, nil
}

func (s *Server) Handler() http.Handler {
	router := chi.NewRouter()
	router.Use(s.recoverer)
	router.NotFound(func(w http.ResponseWriter, r *http.Request) {
		s.writeProblem(w, r, problemNotFound)
	})
	router.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Allow", strings.Join(allowedMethods(router, r.URL.Path), ", "))
		s.writeProblem(w, r, problemMethodNotAllowed)
	})

	return api.HandlerWithOptions(s, api.ChiServerOptions{
		BaseRouter:       router,
		Middlewares:      []api.MiddlewareFunc{s.requireCanonicalUUIDs},
		ErrorHandlerFunc: s.handleBindError,
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

func (s *Server) withQueryTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, s.queryTimeout)
}

func (s *Server) recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			rec := recover()
			if rec == nil {
				return
			}
			if err, ok := rec.(error); ok && errors.Is(err, http.ErrAbortHandler) {
				panic(rec)
			}

			s.logger.ErrorContext(r.Context(), "panic in handler",
				"panic", rec, "stack", string(debug.Stack()))
			s.writeProblem(w, r, problemInternal)
		}()

		next.ServeHTTP(w, r)
	})
}
