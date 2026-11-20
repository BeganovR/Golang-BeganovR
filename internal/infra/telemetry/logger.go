package telemetry

import (
	"context"
	"errors"
	"io"
	"log/slog"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/resource"
	"go.opentelemetry.io/otel/trace"
)

type traceHandler struct {
	slog.Handler
}

func Logger(out io.Writer, level slog.Leveler, service string) *slog.Logger {
	handler := slog.NewJSONHandler(out, &slog.HandlerOptions{Level: level})
	return slog.New(traceHandler{Handler: handler}).With("service", service)
}

func ServiceName(ctx context.Context) (string, error) {
	res, err := resource.New(ctx, resource.WithFromEnv())
	name := "trip-service"
	if value, ok := res.Set().Value(attribute.Key("service.name")); ok {
		name = value.AsString()
	}
	if err != nil {
		return name, errors.New("invalid OTEL_RESOURCE_ATTRIBUTES")
	}
	return name, nil
}

func (h traceHandler) Handle(ctx context.Context, record slog.Record) error {
	span := trace.SpanContextFromContext(ctx)
	traceID, spanID := "", ""
	if span.IsValid() {
		traceID, spanID = span.TraceID().String(), span.SpanID().String()
	}
	record.AddAttrs(slog.String("trace_id", traceID), slog.String("span_id", spanID))
	return h.Handler.Handle(ctx, record)
}

func (h traceHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return traceHandler{Handler: h.Handler.WithAttrs(attrs)}
}

func (h traceHandler) WithGroup(name string) slog.Handler {
	return traceHandler{Handler: h.Handler.WithGroup(name)}
}
