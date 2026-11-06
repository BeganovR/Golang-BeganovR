package middleware_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/BeganovR/Golang-BeganovR/api"
	"github.com/BeganovR/Golang-BeganovR/internal/domain/trip"
	"github.com/BeganovR/Golang-BeganovR/internal/http/domainerror"
	"github.com/BeganovR/Golang-BeganovR/internal/http/middleware"
	"github.com/BeganovR/Golang-BeganovR/internal/http/response"
	"github.com/BeganovR/Golang-BeganovR/internal/infra/telemetry"
)

const (
	tripRoute       = "/api/v1/trips/{tripId}"
	tripPath        = "/api/v1/trips/0192e1c7-1234-7123-8123-123456789abc"
	durationMetric  = "http.server.request.duration"
	activeMetric    = "http.server.active_requests"
	methodAttribute = "http.request.method"
	routeAttribute  = "http.route"
	statusAttribute = "http.response.status_code"
	errorAttribute  = "error.type"
)

type signals struct {
	reader  *sdkmetric.ManualReader
	spans   *tracetest.SpanRecorder
	logs    bytes.Buffer
	handler http.Handler
}

func newSignals(t *testing.T, router *chi.Mux) *signals {
	t.Helper()
	f := &signals{reader: sdkmetric.NewManualReader(), spans: tracetest.NewSpanRecorder()}
	traces := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(f.spans), sdktrace.WithSampler(sdktrace.AlwaysSample()))
	metrics := sdkmetric.NewMeterProvider(sdkmetric.WithReader(f.reader))
	t.Cleanup(func() {
		if err := metrics.Shutdown(context.Background()); err != nil {
			t.Errorf("shutdown metrics: %v", err)
		}
		if err := traces.Shutdown(context.Background()); err != nil {
			t.Errorf("shutdown traces: %v", err)
		}
	})
	m, err := middleware.New(telemetry.Logger(&f.logs, slog.LevelDebug, "trip-service"), traces, metrics)
	if err != nil {
		t.Fatalf("create middleware: %v", err)
	}
	f.handler = m.Wrap(router)
	return f
}

func (f *signals) entries(t *testing.T) []map[string]any {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(f.logs.Bytes()))
	var entries []map[string]any
	for {
		var entry map[string]any
		err := decoder.Decode(&entry)
		if errors.Is(err, io.EOF) {
			return entries
		}
		if err != nil {
			t.Fatalf("decode log entry: %v", err)
		}
		entries = append(entries, entry)
	}
}

func (f *signals) collect(t *testing.T) metricdata.ResourceMetrics {
	t.Helper()
	var metrics metricdata.ResourceMetrics
	if err := f.reader.Collect(t.Context(), &metrics); err != nil {
		t.Fatalf("collect metrics: %v", err)
	}
	return metrics
}

func findMetric(t *testing.T, metrics metricdata.ResourceMetrics, name string) metricdata.Metrics {
	t.Helper()
	var found []metricdata.Metrics
	for _, scope := range metrics.ScopeMetrics {
		for _, candidate := range scope.Metrics {
			if candidate.Name == name {
				found = append(found, candidate)
			}
		}
	}
	if len(found) != 1 {
		t.Fatalf("metric %s found %d times; want 1", name, len(found))
	}
	return found[0]
}

func assertAttribute(t *testing.T, attrs attribute.Set, key string, want attribute.Value) {
	t.Helper()
	got, ok := attrs.Value(attribute.Key(key))
	if !ok || got != want {
		t.Fatalf("attribute %s = %v, %v; want %v", key, got, ok, want)
	}
}

func assertDuration(t *testing.T, metrics metricdata.ResourceMetrics, method, route string, status int, count uint64) {
	t.Helper()
	m := findMetric(t, metrics, durationMetric)
	histogram, ok := m.Data.(metricdata.Histogram[float64])
	if !ok || m.Unit != "s" || len(histogram.DataPoints) != 1 {
		t.Fatalf("duration metric = %+v; want one histogram in seconds", m)
	}
	point := histogram.DataPoints[0]
	if point.Count != count || point.Sum < 0 || point.Attributes.Len() != 3 {
		t.Fatalf("duration point = %+v; want count %d and three attributes", point, count)
	}
	assertAttribute(t, point.Attributes, methodAttribute, attribute.StringValue(method))
	assertAttribute(t, point.Attributes, routeAttribute, attribute.StringValue(route))
	assertAttribute(t, point.Attributes, statusAttribute, attribute.IntValue(status))
}

func assertActive(t *testing.T, metrics metricdata.ResourceMetrics, method, route string, want int64) {
	t.Helper()
	m := findMetric(t, metrics, activeMetric)
	sum, ok := m.Data.(metricdata.Sum[int64])
	if !ok || sum.IsMonotonic || len(sum.DataPoints) != 1 {
		t.Fatalf("active metric = %+v; want one up-down counter", m)
	}
	point := sum.DataPoints[0]
	if point.Value != want || point.Attributes.Len() != 2 {
		t.Fatalf("active point = %+v; want %d and two attributes", point, want)
	}
	assertAttribute(t, point.Attributes, methodAttribute, attribute.StringValue(method))
	assertAttribute(t, point.Attributes, routeAttribute, attribute.StringValue(route))
}

func assertAccessLog(t *testing.T, f *signals, method, route string, status int, level string) map[string]any {
	t.Helper()
	entries := f.entries(t)
	if len(entries) != 1 {
		t.Fatalf("access log count = %d; want 1", len(entries))
	}
	entry := entries[0]
	if entry["msg"] != "request handled" || entry["level"] != level || entry["service"] != "trip-service" ||
		entry["method"] != method || entry["route"] != route || entry["status"] != float64(status) {
		t.Fatalf("access log = %+v", entry)
	}
	if duration, ok := entry["duration_ms"].(float64); !ok || duration < 0 {
		t.Fatalf("log duration_ms = %v", entry["duration_ms"])
	}
	spans := f.spans.Ended()
	if len(spans) != 1 {
		t.Fatalf("span count = %d; want 1", len(spans))
	}
	span := spans[0].SpanContext()
	if entry["trace_id"] != span.TraceID().String() || entry["span_id"] != span.SpanID().String() {
		t.Fatalf("log correlation = %+v; want span %v", entry, span)
	}
	return entry
}

func assertPanicDetailsHidden(t *testing.T, span sdktrace.ReadOnlySpan) {
	t.Helper()
	data, err := json.Marshal(map[string]any{"events": span.Events(), "status": span.Status()})
	if err != nil {
		t.Fatalf("encode panic span: %v", err)
	}
	if strings.Contains(string(data), "private panic value") {
		t.Fatalf("raw panic value appeared in span: %s", data)
	}
}

func TestRequestSignals(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name   string
		err    error
		status int
	}{
		{name: "created", status: http.StatusCreated},
		{name: "invalid", err: domainerror.ErrInvalidRequest, status: http.StatusBadRequest},
		{name: "missing", err: trip.ErrTripNotFound, status: http.StatusNotFound},
		{name: "busy", err: trip.ErrDriverBusy, status: http.StatusConflict},
		{name: "internal", err: errors.New("database private details"), status: http.StatusInternalServerError},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			router := chi.NewRouter()
			router.Post(tripRoute, func(w http.ResponseWriter, r *http.Request) {
				if tt.err != nil {
					response.Error(w, r, tt.err)
					return
				}
				response.JSON(w, r, tt.status, map[string]string{"value": "private response body"})
			})
			f := newSignals(t, router)
			req := httptest.NewRequest(http.MethodPost, tripPath, strings.NewReader("private request body"))
			recorder := httptest.NewRecorder()
			f.handler.ServeHTTP(recorder, req)
			if recorder.Code != tt.status {
				t.Fatalf("status = %d; want %d", recorder.Code, tt.status)
			}
			level := "INFO"
			if tt.status >= http.StatusInternalServerError {
				level = "ERROR"
			}
			assertAccessLog(t, f, http.MethodPost, tripRoute, tt.status, level)
			for _, private := range []string{tripPath, "private request body", "private response body"} {
				if strings.Contains(f.logs.String(), private) {
					t.Fatalf("private request data appeared in logs: %s", private)
				}
			}
			span := f.spans.Ended()[0]
			if span.Name() != http.MethodPost+" "+tripRoute {
				t.Fatalf("span name = %q", span.Name())
			}
			attrs := attribute.NewSet(span.Attributes()...)
			assertAttribute(t, attrs, routeAttribute, attribute.StringValue(tripRoute))
			assertAttribute(t, attrs, statusAttribute, attribute.IntValue(tt.status))
			if tt.status >= http.StatusInternalServerError {
				if span.Status().Code != codes.Error {
					t.Fatalf("failed request span status = %v", span.Status())
				}
				assertAttribute(t, attrs, errorAttribute, attribute.StringValue("internal_error"))
				if strings.Contains(recorder.Body.String(), tt.err.Error()) {
					t.Fatal("internal error details appeared in response")
				}
			} else if span.Status().Code == codes.Error {
				t.Fatalf("successful or client error request marked failed: %v", span.Status())
			}
			metrics := f.collect(t)
			assertDuration(t, metrics, http.MethodPost, tripRoute, tt.status, 1)
			assertActive(t, metrics, http.MethodPost, tripRoute, 0)
		})
	}
}

func TestRemoteTraceParent(t *testing.T) {
	t.Parallel()
	const (
		traceID = "4bf92f3577b34da6a3ce929d0e0e4736"
		spanID  = "00f067aa0ba902b7"
	)
	router := chi.NewRouter()
	router.Get(tripRoute, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	f := newSignals(t, router)
	req := httptest.NewRequest(http.MethodGet, tripPath, nil)
	req.Header.Set("Traceparent", "00-"+traceID+"-"+spanID+"-01")
	f.handler.ServeHTTP(httptest.NewRecorder(), req)
	entry := assertAccessLog(t, f, http.MethodGet, tripRoute, http.StatusNoContent, "INFO")
	span := f.spans.Ended()[0]
	parent := span.Parent()
	if !parent.IsRemote() || parent.TraceID().String() != traceID || parent.SpanID().String() != spanID ||
		span.SpanContext().TraceID().String() != traceID || entry["trace_id"] != traceID {
		t.Fatalf("remote parent = %v, span = %v, log trace = %v", parent, span.SpanContext(), entry["trace_id"])
	}
}

func TestPanicRecovery(t *testing.T) {
	t.Parallel()
	const panicRoute = "/panic"
	router := chi.NewRouter()
	router.Get(panicRoute, func(http.ResponseWriter, *http.Request) { panic("private panic value") })
	router.Get("/alive", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	f := newSignals(t, router)
	recorder := httptest.NewRecorder()
	f.handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, panicRoute, nil))
	var problem api.Problem
	if err := json.Unmarshal(recorder.Body.Bytes(), &problem); err != nil {
		t.Fatalf("decode panic response: %v", err)
	}
	if recorder.Code != http.StatusInternalServerError || problem.Code != "internal_error" ||
		strings.Contains(recorder.Body.String(), "private panic value") {
		t.Fatalf("panic response = %d %s", recorder.Code, recorder.Body.String())
	}
	entry := assertAccessLog(t, f, http.MethodGet, panicRoute, http.StatusInternalServerError, "ERROR")
	if stack, ok := entry["stack"].(string); !ok || !strings.Contains(stack, "middleware_test.TestPanicRecovery") {
		t.Fatalf("panic stack = %v", entry["stack"])
	}
	span := f.spans.Ended()[0]
	if span.Status().Code != codes.Error || len(span.Events()) == 0 {
		t.Fatalf("panic span = status %v events %v", span.Status(), span.Events())
	}
	assertPanicDetailsHidden(t, span)
	attrs := attribute.NewSet(span.Attributes()...)
	assertAttribute(t, attrs, errorAttribute, attribute.StringValue("panic"))
	assertAttribute(t, attrs, statusAttribute, attribute.IntValue(http.StatusInternalServerError))
	metrics := f.collect(t)
	assertDuration(t, metrics, http.MethodGet, panicRoute, http.StatusInternalServerError, 1)
	assertActive(t, metrics, http.MethodGet, panicRoute, 0)
	recorder = httptest.NewRecorder()
	f.handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/alive", nil))
	if recorder.Code != http.StatusNoContent || len(f.entries(t)) != 2 || len(f.spans.Ended()) != 2 {
		t.Fatal("service did not handle the next request after panic")
	}
}

func TestUnmatchedRouteCardinality(t *testing.T) {
	t.Parallel()
	router := chi.NewRouter()
	router.Get(tripRoute, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	router.NotFound(func(w http.ResponseWriter, r *http.Request) { response.Error(w, r, domainerror.ErrNotFound) })
	f := newSignals(t, router)
	for _, path := range []string{"/private-one/123", "/private-two/456"} {
		recorder := httptest.NewRecorder()
		f.handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		if recorder.Code != http.StatusNotFound {
			t.Fatalf("unknown route status = %d", recorder.Code)
		}
		if strings.Contains(f.logs.String(), path) {
			t.Fatal("unknown request path appeared in access log")
		}
	}
	for _, span := range f.spans.Ended() {
		if span.Name() != "GET /unmatched" {
			t.Fatalf("unmatched span name = %q", span.Name())
		}
	}
	metrics := f.collect(t)
	assertDuration(t, metrics, http.MethodGet, "/unmatched", http.StatusNotFound, 2)
	assertActive(t, metrics, http.MethodGet, "/unmatched", 0)
}

func TestAbortHandlerCompletesObservation(t *testing.T) {
	t.Parallel()
	const abortRoute = "/abort"
	router := chi.NewRouter()
	router.Get(abortRoute, func(http.ResponseWriter, *http.Request) { panic(http.ErrAbortHandler) })
	f := newSignals(t, router)
	var recovered any
	func() {
		defer func() { recovered = recover() }()
		f.handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, abortRoute, nil))
	}()
	if recovered != http.ErrAbortHandler {
		t.Fatalf("recovered = %v; want http.ErrAbortHandler", recovered)
	}
	assertAccessLog(t, f, http.MethodGet, abortRoute, http.StatusInternalServerError, "ERROR")
	metrics := f.collect(t)
	assertActive(t, metrics, http.MethodGet, abortRoute, 0)
	assertDuration(t, metrics, http.MethodGet, abortRoute, http.StatusInternalServerError, 1)
}

func TestPanicAfterResponseStarted(t *testing.T) {
	t.Parallel()
	const (
		partialRoute = "/partial"
		headers      = "headers"
		body         = "body"
	)
	for _, mode := range []string{headers, body, "flushed"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			router := chi.NewRouter()
			router.Get(partialRoute, func(w http.ResponseWriter, _ *http.Request) {
				switch mode {
				case headers:
					w.WriteHeader(http.StatusAccepted)
				case body:
					if _, err := io.WriteString(w, "partial response"); err != nil {
						t.Errorf("write partial response: %v", err)
					}
				case "flushed":
					flusher, ok := w.(http.Flusher)
					if !ok {
						t.Fatal("middleware lost http.Flusher support")
					}
					flusher.Flush()
				}
				panic("private panic value")
			})
			f := newSignals(t, router)
			recorder := httptest.NewRecorder()
			var recovered any
			func() {
				defer func() { recovered = recover() }()
				f.handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, partialRoute, nil))
			}()
			if recovered != http.ErrAbortHandler {
				t.Fatalf("partial response panic = %v; want http.ErrAbortHandler", recovered)
			}
			wantStatus, wantBody := http.StatusOK, ""
			if mode == headers {
				wantStatus = http.StatusAccepted
			}
			if mode == body {
				wantBody = "partial response"
			}
			if recorder.Code != wantStatus || recorder.Body.String() != wantBody {
				t.Fatalf("partial response = %d %q; want %d %q", recorder.Code, recorder.Body.String(), wantStatus, wantBody)
			}
			entry := assertAccessLog(t, f, http.MethodGet, partialRoute, http.StatusInternalServerError, "ERROR")
			if stack, ok := entry["stack"].(string); !ok || stack == "" {
				t.Fatal("partial response panic has no stack trace")
			}
			span := f.spans.Ended()[0]
			if span.Status().Code != codes.Error {
				t.Fatalf("partial response span status = %v", span.Status())
			}
			assertPanicDetailsHidden(t, span)
			assertAttribute(t, attribute.NewSet(span.Attributes()...), errorAttribute, attribute.StringValue("panic"))
			metrics := f.collect(t)
			assertActive(t, metrics, http.MethodGet, partialRoute, 0)
			assertDuration(t, metrics, http.MethodGet, partialRoute, http.StatusInternalServerError, 1)
		})
	}
}

func TestMethodNotAllowedRoute(t *testing.T) {
	t.Parallel()
	router := chi.NewRouter()
	router.Get(tripRoute, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	router.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) { response.Error(w, r, domainerror.ErrMethodNotAllowed) })
	f := newSignals(t, router)
	recorder := httptest.NewRecorder()
	f.handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPatch, tripPath, nil))
	if recorder.Code != http.StatusMethodNotAllowed {
		t.Fatalf("method not allowed status = %d", recorder.Code)
	}
	assertAccessLog(t, f, http.MethodPatch, tripRoute, http.StatusMethodNotAllowed, "INFO")
	if name := f.spans.Ended()[0].Name(); name != http.MethodPatch+" "+tripRoute {
		t.Fatalf("method not allowed span name = %q", name)
	}
	assertDuration(t, f.collect(t), http.MethodPatch, tripRoute, http.StatusMethodNotAllowed, 1)
}

func TestActiveRequestsDuringHandler(t *testing.T) {
	t.Parallel()
	var f *signals
	router := chi.NewRouter()
	router.Get(tripRoute, func(w http.ResponseWriter, _ *http.Request) {
		assertActive(t, f.collect(t), http.MethodGet, tripRoute, 1)
		w.WriteHeader(http.StatusNoContent)
	})
	f = newSignals(t, router)
	f.handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, tripPath, nil))
	metrics := f.collect(t)
	assertActive(t, metrics, http.MethodGet, tripRoute, 0)
	assertDuration(t, metrics, http.MethodGet, tripRoute, http.StatusNoContent, 1)
}

func TestResponseWriter(t *testing.T) {
	t.Parallel()
	const flush = "flush"
	for _, mode := range []string{"empty", "write", flush} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			router := chi.NewRouter()
			router.Get(tripRoute, func(w http.ResponseWriter, _ *http.Request) {
				if mode == flush {
					flusher, ok := w.(http.Flusher)
					if !ok {
						t.Error("middleware lost http.Flusher support")
						return
					}
					flusher.Flush()
				}
				if mode != "empty" {
					if _, err := io.WriteString(w, "response"); err != nil {
						t.Errorf("write response: %v", err)
					}
				}
			})
			f := newSignals(t, router)
			recorder := httptest.NewRecorder()
			f.handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, tripPath, nil))
			if recorder.Code != http.StatusOK || (mode == flush && !recorder.Flushed) {
				t.Fatalf("response writer status=%d flushed=%v", recorder.Code, recorder.Flushed)
			}
			assertAccessLog(t, f, http.MethodGet, tripRoute, http.StatusOK, "INFO")
			metrics := f.collect(t)
			assertDuration(t, metrics, http.MethodGet, tripRoute, http.StatusOK, 1)
			assertActive(t, metrics, http.MethodGet, tripRoute, 0)
		})
	}
}
