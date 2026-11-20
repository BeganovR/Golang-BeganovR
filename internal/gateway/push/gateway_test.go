package push_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"

	"github.com/BeganovR/Golang-BeganovR/internal/gateway/push"
)

func TestRequestPosition(t *testing.T) {
	t.Parallel()
	tripID, driverID := uuid.New(), uuid.New()
	received := make(chan trace.SpanContext, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/prefix/api/v1/push" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Errorf("Content-Type = %q", got)
		}
		var body struct {
			RequestID   uuid.UUID         `json:"request_id"`
			RecipientID uuid.UUID         `json:"recipient_id"`
			Kind        string            `json:"kind"`
			Data        map[string]string `json:"data"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode push: %v", err)
		}
		if body.RequestID == uuid.Nil || body.RecipientID != driverID || body.Kind != "REQUEST_POSITION" ||
			len(body.Data) != 1 || body.Data["trip_id"] != tripID.String() {
			t.Errorf("payload = %+v", body)
		}
		ctx := propagation.TraceContext{}.Extract(r.Context(), propagation.HeaderCarrier(r.Header))
		received <- trace.SpanContextFromContext(ctx)
		w.WriteHeader(http.StatusAccepted)
	}))
	t.Cleanup(server.Close)
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	t.Cleanup(func() {
		if err := provider.Shutdown(context.Background()); err != nil {
			t.Errorf("shutdown provider: %v", err)
		}
	})
	gateway, err := push.New(server.URL+"/prefix/", time.Second, provider)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(gateway.CloseIdleConnections)
	ctx, worker := provider.Tracer("test").Start(t.Context(), "worker")
	defer worker.End()
	if err := gateway.RequestPosition(ctx, tripID, driverID); err != nil {
		t.Fatalf("RequestPosition() = %v", err)
	}

	spans := recorder.Ended()
	if len(spans) != 1 {
		t.Fatalf("got %d ended spans, want 1", len(spans))
	}
	span := spans[0]
	if span.SpanKind() != trace.SpanKindClient || span.Parent().SpanID() != worker.SpanContext().SpanID() {
		t.Fatalf("span kind %s, parent %s", span.SpanKind(), span.Parent().SpanID())
	}
	select {
	case got := <-received:
		want := span.SpanContext()
		if got.TraceID() != want.TraceID() || got.SpanID() != want.SpanID() || !got.IsRemote() {
			t.Fatalf("received trace context = %v, want %v", got, want)
		}
	default:
		t.Fatal("server did not receive a trace context")
	}
}

func TestHTTPFailure(t *testing.T) {
	t.Parallel()
	for _, status := range []int{http.StatusTemporaryRedirect, http.StatusBadRequest, http.StatusTooManyRequests, http.StatusServiceUnavailable} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			t.Parallel()
			var calls atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				w.Header().Set("Location", "/next")
				w.WriteHeader(status)
				_, _ = w.Write([]byte("private upstream details"))
			}))
			t.Cleanup(server.Close)
			recorder := tracetest.NewSpanRecorder()
			provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
			t.Cleanup(func() {
				if err := provider.Shutdown(context.Background()); err != nil {
					t.Errorf("shutdown provider: %v", err)
				}
			})
			gateway, err := push.New(server.URL, time.Second, provider)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(gateway.CloseIdleConnections)
			err = gateway.RequestPosition(t.Context(), uuid.New(), uuid.New())
			var failure *push.HTTPError
			if !errors.As(err, &failure) || failure.StatusCode != status {
				t.Fatalf("RequestPosition() = %v, want HTTP %d", err, status)
			}
			if calls.Load() != 1 {
				t.Errorf("sent %d requests, want 1", calls.Load())
			}
			spans := recorder.Ended()
			if len(spans) != 1 || spans[0].Status().Code != codes.Error {
				t.Fatalf("failure spans = %v", spans)
			}
			if strings.Contains(err.Error(), "private upstream details") ||
				strings.Contains(spans[0].Status().Description, "private upstream details") {
				t.Fatal("upstream response body leaked")
			}
		})
	}
}

func TestRequestTimeout(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		<-r.Context().Done()
	}))
	t.Cleanup(server.Close)
	gateway, err := push.New(server.URL, 100*time.Millisecond, noop.NewTracerProvider())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(gateway.CloseIdleConnections)
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	err = gateway.RequestPosition(ctx, uuid.New(), uuid.New())
	var failure *push.TransportError
	if !errors.As(err, &failure) || !errors.Is(err, context.DeadlineExceeded) || ctx.Err() != nil {
		t.Fatalf("RequestPosition() = %v, context = %v", err, ctx.Err())
	}
}

func TestNewRejectsInvalidConfig(t *testing.T) {
	t.Parallel()
	const private = "secret-from-push-url"
	for _, endpoint := range []string{"", "localhost:8090", "ftp://localhost", "http://", "https://user:" + private + "@localhost", "http://localhost?key=" + private, "http://localhost#" + private, "://%" + private} {
		_, err := push.New(endpoint, time.Second, noop.NewTracerProvider())
		if err == nil || !strings.Contains(err.Error(), "PUSH_HTTP_URL") || strings.Contains(err.Error(), private) {
			t.Errorf("unexpected URL validation result: %v", err)
		}
	}
	_, err := push.New("http://localhost", 0, noop.NewTracerProvider())
	if err == nil || !strings.Contains(err.Error(), "PUSH_REQUEST_TIMEOUT") {
		t.Errorf("unexpected timeout validation result: %v", err)
	}
}
