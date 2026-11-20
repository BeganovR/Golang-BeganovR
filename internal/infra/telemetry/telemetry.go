package telemetry

import (
	"context"
	"errors"
	"fmt"
	"os"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

type Providers struct {
	Traces      *sdktrace.TracerProvider
	Metrics     *sdkmetric.MeterProvider
	ServiceName string
}

func New(ctx context.Context, version string) (*Providers, error) {
	if err := validateEnvironment(); err != nil {
		return nil, err
	}
	res, err := resource.New(ctx,
		resource.WithAttributes(
			attribute.String("service.name", "trip-service"),
			attribute.String("service.version", version),
			attribute.String("service.namespace", "tripgo"),
			attribute.String("deployment.environment", "local"),
		),
		resource.WithFromEnv(), resource.WithTelemetrySDK(),
	)
	if err != nil {
		return nil, fmt.Errorf("create telemetry resource: %w", err)
	}
	traceExporter, metricExporter, err := exporters(ctx)
	if err != nil {
		return nil, err
	}
	serviceName, _ := res.Set().Value(attribute.Key("service.name"))
	return &Providers{
		ServiceName: serviceName.AsString(),
		Traces: sdktrace.NewTracerProvider(
			sdktrace.WithResource(res),
			sdktrace.WithoutPanicRecording(),
			sdktrace.WithBatcher(traceExporter),
		),
		Metrics: sdkmetric.NewMeterProvider(
			sdkmetric.WithResource(res),
			sdkmetric.WithReader(sdkmetric.NewPeriodicReader(metricExporter)),
		),
	}, nil
}

func exporters(ctx context.Context) (sdktrace.SpanExporter, sdkmetric.Exporter, error) {
	var traces sdktrace.SpanExporter
	var metrics sdkmetric.Exporter
	var err error
	switch protocol := os.Getenv("OTEL_EXPORTER_OTLP_PROTOCOL"); protocol {
	case "", "http/protobuf":
		traces, err = otlptracehttp.New(ctx)
		if err == nil {
			metrics, err = otlpmetrichttp.New(ctx)
		}
	case "grpc":
		traces, err = otlptracegrpc.New(ctx)
		if err == nil {
			metrics, err = otlpmetricgrpc.New(ctx)
		}
	default:
		return nil, nil, fmt.Errorf("unsupported OTLP protocol %q", protocol)
	}
	if err != nil {
		if traces != nil {
			_ = traces.Shutdown(ctx)
		}
		return nil, nil, errors.New("create OTLP exporters: check OTLP configuration")
	}
	return traces, metrics, nil
}

func (p *Providers) Shutdown(ctx context.Context) error {
	return errors.Join(p.Metrics.Shutdown(ctx), p.Traces.Shutdown(ctx))
}
