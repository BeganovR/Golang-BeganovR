package middleware

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"
	"time"

	"github.com/go-chi/chi/v5"
	chimiddleware "github.com/go-chi/chi/v5/middleware"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"

	"github.com/BeganovR/Golang-BeganovR/internal/http/domainerror"
	"github.com/BeganovR/Golang-BeganovR/internal/http/request"
	"github.com/BeganovR/Golang-BeganovR/internal/http/response"
)

type observation struct {
	writer   chimiddleware.WrapResponseWriter
	route    string
	stack    []byte
	aborted  bool
	complete []func()
}

type observationKey struct{}

type Middleware struct {
	logger   *slog.Logger
	traces   trace.TracerProvider
	duration metric.Float64Histogram
	active   metric.Int64UpDownCounter
}

func New(logger *slog.Logger, traces trace.TracerProvider, metrics metric.MeterProvider) (*Middleware, error) {
	meter := metrics.Meter("tripgo/http")
	duration, err := meter.Float64Histogram("http.server.request.duration", metric.WithUnit("s"),
		metric.WithExplicitBucketBoundaries(0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10))
	if err != nil {
		return nil, fmt.Errorf("create request duration histogram: %w", err)
	}
	active, err := meter.Int64UpDownCounter("http.server.active_requests", metric.WithUnit("{request}"))
	if err != nil {
		return nil, fmt.Errorf("create active request counter: %w", err)
	}
	return &Middleware{logger: logger, traces: traces, duration: duration, active: active}, nil
}

func (m *Middleware) Wrap(router *chi.Mux) http.Handler {
	return m.recoverer(router, m.tracing(m.logging(m.metrics(router))))
}

func (m *Middleware) recoverer(routes chi.Routes, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r, _ = request.WithState(r)
		o := &observation{writer: chimiddleware.NewWrapResponseWriter(w, r.ProtoMajor), route: route(routes, r)}
		r = r.WithContext(context.WithValue(r.Context(), observationKey{}, o))
		defer func() {
			for _, complete := range o.complete {
				complete()
			}
		}()
		defer func() {
			if recovered := recover(); recovered != nil {
				if err, ok := recovered.(error); ok && errors.Is(err, http.ErrAbortHandler) {
					o.aborted = true
					panic(recovered)
				}
				if o.writer.Status() != 0 {
					o.aborted = true
					panic(http.ErrAbortHandler)
				}
				response.Error(o.writer, r, errors.New("handler panic"))
			}
		}()
		next.ServeHTTP(o.writer, r)
	})
}

func (m *Middleware) tracing(next http.Handler) http.Handler {
	annotate := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		o := observe(r)
		span := trace.SpanFromContext(r.Context())
		span.SetAttributes(attribute.String("http.route", o.route))
		defer func() {
			status := o.status()
			if recovered := recover(); recovered != nil {
				o.stack = debug.Stack()
				span.SetAttributes(attribute.Int("http.response.status_code", http.StatusInternalServerError),
					attribute.String("error.type", "panic"))
				span.SetStatus(codes.Error, "panic")
				span.RecordError(errors.New("handler panic"))
				if err, ok := recovered.(error); ok && errors.Is(err, http.ErrAbortHandler) {
					panic(http.ErrAbortHandler)
				}
				panic(errors.New("handler panic"))
			}
			var errorType string
			if status >= http.StatusInternalServerError {
				_, body := domainerror.Problem(request.Error(r))
				errorType = body.Code
			}
			if request.WriteError(r) != nil {
				errorType = "response_write_error"
			}
			if errorType != "" {
				span.SetAttributes(attribute.String("error.type", errorType))
				span.SetStatus(codes.Error, errorType)
				span.RecordError(errors.New(errorType))
			}
		}()
		next.ServeHTTP(w, r)
	})
	return otelhttp.NewHandler(annotate, "http",
		otelhttp.WithTracerProvider(m.traces),
		otelhttp.WithMeterProvider(noop.NewMeterProvider()),
		otelhttp.WithPropagators(propagation.TraceContext{}),
		otelhttp.WithSpanNameFormatter(func(_ string, r *http.Request) string {
			return knownMethod(r.Method) + " " + observe(r).route
		}),
	)
}

func (m *Middleware) logging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started, o := time.Now(), observe(r)
		o.complete = append(o.complete, func() {
			status, level := o.status(), slog.LevelInfo
			attrs := []any{
				"method", r.Method, "route", o.route, "status", status,
				"duration_ms", float64(time.Since(started).Microseconds()) / 1000,
			}
			if status >= http.StatusInternalServerError {
				level = slog.LevelError
				if err := request.Error(r); err != nil {
					attrs = append(attrs, "error", err)
				}
			}
			if err := request.WriteError(r); err != nil {
				attrs = append(attrs, "write_error", err)
			}
			if len(o.stack) > 0 {
				attrs = append(attrs, "stack", string(o.stack))
			}
			m.logger.Log(r.Context(), level, "request handled", attrs...)
		})
		next.ServeHTTP(w, r)
	})
}

func (m *Middleware) metrics(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started, o := time.Now(), observe(r)
		attrs := []attribute.KeyValue{attribute.String("http.request.method", knownMethod(r.Method)), attribute.String("http.route", o.route)}
		m.active.Add(r.Context(), 1, metric.WithAttributes(attrs...))
		o.complete = append(o.complete, func() {
			m.active.Add(r.Context(), -1, metric.WithAttributes(attrs...))
			attrs = append(attrs, attribute.Int("http.response.status_code", o.status()))
			m.duration.Record(r.Context(), time.Since(started).Seconds(), metric.WithAttributes(attrs...))
		})
		next.ServeHTTP(w, r)
	})
}

func knownMethod(method string) string {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut, http.MethodDelete,
		http.MethodConnect, http.MethodOptions, http.MethodTrace, http.MethodPatch:
		return method
	default:
		return "_OTHER"
	}
}

func observe(r *http.Request) *observation {
	return r.Context().Value(observationKey{}).(*observation)
}

func (o *observation) status() int {
	if o.aborted {
		return http.StatusInternalServerError
	}
	if status := o.writer.Status(); status != 0 {
		return status
	}
	return http.StatusOK
}

func route(routes chi.Routes, r *http.Request) string {
	for _, method := range []string{r.Method, http.MethodGet, http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch} {
		ctx := chi.NewRouteContext()
		if routes.Match(ctx, method, r.URL.Path) {
			return ctx.RoutePattern()
		}
	}
	return "/unmatched"
}
