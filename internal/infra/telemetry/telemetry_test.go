package telemetry_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/BeganovR/Golang-BeganovR/internal/infra/telemetry"
)

func TestUnsupportedProtocol(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_PROTOCOL", "http/json")
	providers, err := telemetry.New(context.Background(), "test-version")
	if providers != nil || err == nil || !strings.Contains(err.Error(), "unsupported OTLP protocol") {
		t.Fatalf("New() = %+v, %v", providers, err)
	}
}

func TestProvidersEnvironment(t *testing.T) {
	for _, sampled := range []bool{true, false} {
		sampler := "always_off"
		if sampled {
			sampler = "always_on"
		}
		t.Run(sampler, func(t *testing.T) {
			var traceRequests, metricRequests atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/v1/traces":
					traceRequests.Add(1)
				case "/v1/metrics":
					metricRequests.Add(1)
				default:
					t.Errorf("unexpected OTLP path %q", r.URL.Path)
				}
				w.Header().Set("Content-Type", "application/x-protobuf")
				w.WriteHeader(http.StatusOK)
			}))
			t.Cleanup(server.Close)
			for key, value := range map[string]string{
				"OTEL_EXPORTER_OTLP_PROTOCOL":         "http/protobuf",
				"OTEL_EXPORTER_OTLP_ENDPOINT":         server.URL,
				"OTEL_EXPORTER_OTLP_TRACES_ENDPOINT":  server.URL + "/v1/traces",
				"OTEL_EXPORTER_OTLP_METRICS_ENDPOINT": server.URL + "/v1/metrics",
				"OTEL_EXPORTER_OTLP_HEADERS":          "",
				"OTEL_EXPORTER_OTLP_TRACES_HEADERS":   "",
				"OTEL_EXPORTER_OTLP_METRICS_HEADERS":  "",
				"OTEL_METRIC_EXPORT_INTERVAL":         "3600000",
				"OTEL_TRACES_SAMPLER":                 sampler,
				"OTEL_SERVICE_NAME":                   "configured-service",
				"OTEL_RESOURCE_ATTRIBUTES":            "service.namespace=configured-namespace,deployment.environment=test",
			} {
				t.Setenv(key, value)
			}
			providers, err := telemetry.New(t.Context(), "test-version")
			if err != nil {
				t.Fatalf("New() = %v", err)
			}
			t.Cleanup(func() {
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()
				if err := providers.Shutdown(ctx); err != nil {
					t.Errorf("shutdown telemetry: %v", err)
				}
			})
			recorder := tracetest.NewSpanRecorder()
			providers.Traces.RegisterSpanProcessor(recorder)
			_, span := providers.Traces.Tracer("test").Start(t.Context(), "request")
			if span.SpanContext().IsSampled() != sampled {
				t.Errorf("sampled = %v, want %v", span.SpanContext().IsSampled(), sampled)
			}
			span.End()
			if sampled {
				spans := recorder.Ended()
				if len(spans) != 1 {
					t.Fatalf("ended spans = %d, want one", len(spans))
				}
				attrs := spans[0].Resource().Set()
				for key, want := range map[string]string{
					"service.name": "configured-service", "service.version": "test-version",
					"service.namespace": "configured-namespace", "deployment.environment": "test", "telemetry.sdk.language": "go",
				} {
					if got, ok := attrs.Value(attribute.Key(key)); !ok || got.AsString() != want {
						t.Errorf("resource %s = %v, want %q", key, got, want)
					}
				}
			}
			counter, err := providers.Metrics.Meter("test").Int64Counter("test.counter")
			if err != nil {
				t.Fatalf("create metric: %v", err)
			}
			counter.Add(t.Context(), 1)
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			if err := providers.Traces.ForceFlush(ctx); err != nil {
				t.Fatalf("flush traces: %v", err)
			}
			if err := providers.Metrics.ForceFlush(ctx); err != nil {
				t.Fatalf("flush metrics: %v", err)
			}
			if traceRequests.Load() != int64(len(recorder.Ended())) || metricRequests.Load() == 0 {
				t.Fatalf("export requests: traces=%d, metrics=%d", traceRequests.Load(), metricRequests.Load())
			}
		})
	}
}

func TestServiceName(t *testing.T) {
	for _, tc := range []struct {
		name               string
		serviceName        string
		resourceAttributes string
		want               string
	}{
		{name: "default", want: "trip-service"},
		{name: "resource override", resourceAttributes: "service.name=custom", want: "custom"},
		{name: "explicit name wins", serviceName: "explicit", resourceAttributes: "service.name=custom", want: "explicit"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/x-protobuf")
				w.WriteHeader(http.StatusOK)
			}))
			t.Cleanup(server.Close)
			for key, value := range map[string]string{
				"OTEL_EXPORTER_OTLP_PROTOCOL":         "http/protobuf",
				"OTEL_EXPORTER_OTLP_ENDPOINT":         server.URL,
				"OTEL_EXPORTER_OTLP_TRACES_ENDPOINT":  server.URL + "/v1/traces",
				"OTEL_EXPORTER_OTLP_METRICS_ENDPOINT": server.URL + "/v1/metrics",
				"OTEL_EXPORTER_OTLP_HEADERS":          "",
				"OTEL_EXPORTER_OTLP_TRACES_HEADERS":   "",
				"OTEL_EXPORTER_OTLP_METRICS_HEADERS":  "",
				"OTEL_METRIC_EXPORT_INTERVAL":         "3600000",
				"OTEL_TRACES_SAMPLER":                 "always_on",
				"OTEL_SERVICE_NAME":                   tc.serviceName,
				"OTEL_RESOURCE_ATTRIBUTES":            tc.resourceAttributes,
			} {
				t.Setenv(key, value)
			}
			if tc.serviceName == "" {
				if err := os.Unsetenv("OTEL_SERVICE_NAME"); err != nil {
					t.Fatalf("unset service name: %v", err)
				}
			}
			providers, err := telemetry.New(t.Context(), "test-version")
			if err != nil {
				t.Fatalf("create telemetry: %v", err)
			}
			t.Cleanup(func() {
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()
				if err := providers.Shutdown(ctx); err != nil {
					t.Errorf("shutdown telemetry: %v", err)
				}
			})
			recorder := tracetest.NewSpanRecorder()
			providers.Traces.RegisterSpanProcessor(recorder)
			ctx, span := providers.Traces.Tracer("test").Start(t.Context(), "request")
			var output bytes.Buffer
			telemetry.Logger(&output, slog.LevelInfo, providers.ServiceName).InfoContext(ctx, "request handled")
			span.End()
			var entry struct {
				Service string `json:"service"`
			}
			if err := json.Unmarshal(output.Bytes(), &entry); err != nil {
				t.Fatalf("decode log: %v", err)
			}
			spans := recorder.Ended()
			if len(spans) != 1 {
				t.Fatalf("ended spans = %d, want one", len(spans))
			}
			resourceName, ok := spans[0].Resource().Set().Value(attribute.Key("service.name"))
			if !ok || resourceName.AsString() != tc.want || entry.Service != tc.want || providers.ServiceName != tc.want {
				t.Fatalf("service name: resource=%q log=%q providers=%q, want %q",
					resourceName.AsString(), entry.Service, providers.ServiceName, tc.want)
			}
		})
	}
}
