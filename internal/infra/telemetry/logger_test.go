package telemetry_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"testing"
	"time"

	"go.opentelemetry.io/otel/trace"

	"github.com/BeganovR/Golang-BeganovR/internal/infra/telemetry"
)

func TestLoggerCorrelation(t *testing.T) {
	t.Parallel()
	span := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: trace.TraceID{1, 2, 3}, SpanID: trace.SpanID{4, 5, 6}, TraceFlags: trace.FlagsSampled,
	})
	for _, grouped := range []bool{false, true} {
		name := "attributes"
		if grouped {
			name = "group"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			var out bytes.Buffer
			logger := telemetry.Logger(&out, slog.LevelInfo, "trip-service")
			if grouped {
				logger = logger.WithGroup("request")
			}
			logger.With("operation", "create").InfoContext(trace.ContextWithSpanContext(t.Context(), span), "handled")
			var entry map[string]any
			if err := json.Unmarshal(out.Bytes(), &entry); err != nil {
				t.Fatalf("decode log: %v", err)
			}
			if entry["service"] != "trip-service" || entry["msg"] != "handled" || entry["level"] != "INFO" {
				t.Fatalf("log entry = %+v", entry)
			}
			stamp, ok := entry["time"].(string)
			if !ok {
				t.Fatalf("log timestamp = %v", entry["time"])
			}
			if _, err := time.Parse(time.RFC3339Nano, stamp); err != nil {
				t.Fatalf("log timestamp = %q: %v", stamp, err)
			}
			fields := entry
			if grouped {
				var ok bool
				fields, ok = entry["request"].(map[string]any)
				if !ok {
					t.Fatalf("request group = %v", entry["request"])
				}
			}
			if fields["operation"] != "create" || fields["trace_id"] != span.TraceID().String() || fields["span_id"] != span.SpanID().String() {
				t.Fatalf("correlated fields = %+v", fields)
			}
		})
	}
}

func TestLoggerLevelWithoutSpan(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	logger := telemetry.Logger(&out, slog.LevelWarn, "trip-service")
	ctx := context.Background()
	logger.DebugContext(ctx, "debug")
	logger.InfoContext(ctx, "info")
	if out.Len() != 0 {
		t.Fatalf("messages below configured level were logged: %s", out.String())
	}
	logger.ErrorContext(ctx, "failed")
	var entry map[string]any
	if err := json.Unmarshal(out.Bytes(), &entry); err != nil {
		t.Fatalf("decode log: %v", err)
	}
	if entry["level"] != "ERROR" || entry["msg"] != "failed" || entry["trace_id"] != "" || entry["span_id"] != "" {
		t.Fatalf("log entry = %+v", entry)
	}
}
