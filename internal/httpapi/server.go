package httpapi

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	api "github.com/BeganovR/Golang-BeganovR/internal/generated"
	"github.com/BeganovR/Golang-BeganovR/internal/postgres"
)

var _ api.ServerInterface = (*Server)(nil)

type Deps struct {
	Pool         *pgxpool.Pool
	TxManager    postgres.TxManager
	Trips        *postgres.TripRepository
	History      *postgres.StatusHistoryRepository
	QueryTimeout time.Duration
	Logger       *slog.Logger
}

type Server struct {
	pool           *pgxpool.Pool
	txm            postgres.TxManager
	trips          *postgres.TripRepository
	history        *postgres.StatusHistoryRepository
	queryTimeout   time.Duration
	logger         *slog.Logger
	tripDataSchema *openapi3.Schema
}

func NewServer(d Deps) (*Server, error) {
	swagger, err := api.GetSwagger()
	if err != nil {
		return nil, fmt.Errorf("load embedded openapi spec: %w", err)
	}

	tripData, ok := swagger.Components.Schemas["TripData"]
	if !ok {
		return nil, errors.New("openapi spec has no TripData schema")
	}

	return &Server{
		pool:           d.Pool,
		txm:            d.TxManager,
		trips:          d.Trips,
		history:        d.History,
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

	return api.HandlerWithOptions(s, api.ChiServerOptions{
		BaseRouter:       router,
		ErrorHandlerFunc: s.handleBindError,
	})
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
