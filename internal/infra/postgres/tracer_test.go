package postgres_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric/noop"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"github.com/BeganovR/Golang-BeganovR/internal/infra/postgres"
)

const (
	timeoutError  = "timeout"
	canceledError = "canceled"
)

func TestTracerConnect(t *testing.T) {
	t.Parallel()
	for _, withParent := range []bool{false, true} {
		for _, tc := range []struct {
			name      string
			err       error
			errorType string
		}{
			{name: "success"},
			{name: "authentication error", err: &pgconn.PgError{Code: "28P01", Message: "private authentication details"}, errorType: "28P01"},
			{name: timeoutError, err: fmt.Errorf("private connection details: %w", context.DeadlineExceeded), errorType: timeoutError},
			{name: canceledError, err: fmt.Errorf("private connection details: %w", context.Canceled), errorType: canceledError},
			{name: "unknown error", err: errors.New("private connection details"), errorType: "database_error"},
		} {
			t.Run(fmt.Sprintf("%s/parent=%t", tc.name, withParent), func(t *testing.T) {
				t.Parallel()
				recorder := tracetest.NewSpanRecorder()
				traces := sdktrace.NewTracerProvider(sdktrace.WithSampler(sdktrace.AlwaysSample()), sdktrace.WithSpanProcessor(recorder))
				t.Cleanup(func() {
					if err := traces.Shutdown(context.Background()); err != nil {
						t.Errorf("shutdown traces: %v", err)
					}
				})
				tracer, err := postgres.NewTracer(traces, noop.NewMeterProvider())
				if err != nil {
					t.Fatalf("NewTracer() = %v", err)
				}
				ctx := t.Context()
				var parent trace.Span
				if withParent {
					ctx, parent = traces.Tracer("test").Start(ctx, "request")
					defer parent.End()
				}
				connectCtx := tracer.TraceConnectStart(ctx, pgx.TraceConnectStartData{
					ConnConfig: &pgx.ConnConfig{Config: pgconn.Config{
						Host: "private-host", User: "private-user", Password: "private-password", Database: "private-database",
					}},
				})
				tracer.TraceConnectEnd(connectCtx, pgx.TraceConnectEndData{Err: tc.err})
				spans := recorder.Ended()
				if !withParent {
					if len(spans) != 0 {
						t.Fatalf("connection without parent emitted %d spans", len(spans))
					}
					return
				}
				if len(spans) != 1 {
					t.Fatalf("ended spans = %d, want one connection span", len(spans))
				}
				span := spans[0]
				if span.Name() != "connect" || span.SpanKind() != trace.SpanKindClient || span.Parent().SpanID() != parent.SpanContext().SpanID() {
					t.Fatalf("connection span is not a child of request: %+v", span)
				}
				assertQueryAttributes(t, attribute.NewSet(span.Attributes()...), "", "", tc.errorType)
				wantStatus := codes.Unset
				if tc.err != nil {
					wantStatus = codes.Error
				}
				if span.Status().Code != wantStatus || span.Status().Description != tc.errorType {
					t.Errorf("connection status = %+v, want %v %q", span.Status(), wantStatus, tc.errorType)
				}
				if strings.Contains(fmt.Sprint(span.Attributes(), span.Events(), span.Status()), "private") {
					t.Fatal("connection span contains private details")
				}
			})
		}
	}
}

func TestTracerQuery(t *testing.T) {
	t.Parallel()
	const (
		selectOperation = "SELECT"
		tripsCollection = "trips"
	)
	tests := []struct {
		name       string
		sql        string
		operation  string
		collection string
		err        error
		errorType  string
	}{
		{name: "select", sql: "SELECT * FROM TRIPS WHERE id = $1", operation: selectOperation, collection: tripsCollection},
		{
			name: "insert", sql: "INSERT INTO trip_status_history (trip_id) VALUES ($1)", operation: "INSERT", collection: "trip_status_history",
			err: &pgconn.PgError{Code: "23505", Message: "private database details"}, errorType: "23505",
		},
		{
			name: "update timeout", sql: "UPDATE idempotency_keys SET request_hash = $1", operation: "UPDATE", collection: "idempotency_keys",
			err: fmt.Errorf("private database details: %w", context.DeadlineExceeded), errorType: timeoutError,
		},
		{
			name: "delete canceled", sql: "DELETE FROM trips WHERE id = $1", operation: "DELETE", collection: tripsCollection,
			err: context.Canceled, errorType: canceledError,
		},
		{
			name: "unknown table", sql: "SELECT * FROM private_table WHERE id = $1", operation: selectOperation,
			err: errors.New("private database details"), errorType: "database_error",
		},
		{name: "no rows", sql: "SELECT * FROM trips WHERE id = $1", operation: selectOperation, collection: tripsCollection, err: pgx.ErrNoRows},
		{name: "no table", sql: "SELECT 1", operation: selectOperation},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			recorder := tracetest.NewSpanRecorder()
			traces := sdktrace.NewTracerProvider(sdktrace.WithSampler(sdktrace.AlwaysSample()), sdktrace.WithSpanProcessor(recorder))
			reader := sdkmetric.NewManualReader()
			metrics := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
			t.Cleanup(func() {
				if err := errors.Join(traces.Shutdown(context.Background()), metrics.Shutdown(context.Background())); err != nil {
					t.Errorf("shutdown providers: %v", err)
				}
			})
			tracer, err := postgres.NewTracer(traces, metrics)
			if err != nil {
				t.Fatalf("NewTracer() = %v", err)
			}
			ctx, parent := traces.Tracer("test").Start(t.Context(), "request")
			defer parent.End()
			queryCtx := tracer.TraceQueryStart(ctx, nil, pgx.TraceQueryStartData{SQL: tt.sql, Args: []any{"private argument"}})
			tracer.TraceQueryEnd(queryCtx, nil, pgx.TraceQueryEndData{Err: tt.err, CommandTag: pgconn.NewCommandTag("SELECT 1")})
			spans := recorder.Ended()
			if len(spans) != 1 {
				t.Fatalf("ended spans = %d, want one database span", len(spans))
			}
			span := spans[0]
			if span.SpanKind() != trace.SpanKindClient || span.Parent().SpanID() != parent.SpanContext().SpanID() || span.SpanContext().TraceID() != parent.SpanContext().TraceID() {
				t.Fatalf("database span is not a child of request: %+v", span)
			}
			attrs := attribute.NewSet(span.Attributes()...)
			assertQueryAttributes(t, attrs, tt.operation, tt.collection, tt.errorType)
			wantStatus := codes.Unset
			if tt.errorType != "" {
				wantStatus = codes.Error
			}
			if span.Status().Code != wantStatus {
				t.Fatalf("span status = %+v, want %v", span.Status(), wantStatus)
			}
			if strings.Contains(fmt.Sprint(span.Attributes(), span.Events(), span.Status()), "private") {
				t.Fatalf("span contains private database details: %+v", span)
			}
			var resource metricdata.ResourceMetrics
			if err := reader.Collect(t.Context(), &resource); err != nil {
				t.Fatalf("collect metrics: %v", err)
			}
			histogram := queryHistogram(t, resource)
			if len(histogram.DataPoints) != 1 || histogram.DataPoints[0].Count != 1 || histogram.DataPoints[0].Sum < 0 {
				t.Fatalf("database duration histogram = %+v", histogram)
			}
			assertQueryAttributes(t, histogram.DataPoints[0].Attributes, tt.operation, tt.collection, tt.errorType)
		})
	}
}

func TestTracerEndWithoutStart(t *testing.T) {
	t.Parallel()
	reader := sdkmetric.NewManualReader()
	metrics := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	traces := sdktrace.NewTracerProvider()
	t.Cleanup(func() {
		if err := errors.Join(traces.Shutdown(context.Background()), metrics.Shutdown(context.Background())); err != nil {
			t.Errorf("shutdown providers: %v", err)
		}
	})
	tracer, err := postgres.NewTracer(traces, metrics)
	if err != nil {
		t.Fatalf("NewTracer() = %v", err)
	}
	tracer.TraceQueryEnd(t.Context(), nil, pgx.TraceQueryEndData{Err: context.Canceled})
	var resource metricdata.ResourceMetrics
	if err := reader.Collect(t.Context(), &resource); err != nil {
		t.Fatalf("collect metrics: %v", err)
	}
	for _, scope := range resource.ScopeMetrics {
		if len(scope.Metrics) != 0 {
			t.Fatalf("unstarted query emitted metrics: %+v", scope.Metrics)
		}
	}
}

func TestTracerPrepareInsideQuery(t *testing.T) {
	t.Parallel()
	for _, failed := range []bool{false, true} {
		name := "success"
		if failed {
			name = "prepare error"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			recorder := tracetest.NewSpanRecorder()
			traces := sdktrace.NewTracerProvider(sdktrace.WithSampler(sdktrace.AlwaysSample()), sdktrace.WithSpanProcessor(recorder))
			reader := sdkmetric.NewManualReader()
			metrics := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
			t.Cleanup(func() {
				if err := errors.Join(traces.Shutdown(context.Background()), metrics.Shutdown(context.Background())); err != nil {
					t.Errorf("shutdown providers: %v", err)
				}
			})
			tracer, err := postgres.NewTracer(traces, metrics)
			if err != nil {
				t.Fatalf("NewTracer() = %v", err)
			}
			ctx, parent := traces.Tracer("test").Start(t.Context(), "request")
			defer parent.End()
			const sql = "INSERT INTO trips (user_id) VALUES ($1)"
			queryCtx := tracer.TraceQueryStart(ctx, nil, pgx.TraceQueryStartData{SQL: sql, Args: []any{"private argument"}})
			prepareCtx := tracer.TracePrepareStart(queryCtx, nil, pgx.TracePrepareStartData{Name: "stmt_1", SQL: sql})
			var prepareErr error
			wantErrorType := ""
			wantStatus := codes.Unset
			if failed {
				prepareErr = &pgconn.PgError{Code: "42601", Message: "private database details"}
				wantErrorType, wantStatus = "42601", codes.Error
			}
			tracer.TracePrepareEnd(prepareCtx, nil, pgx.TracePrepareEndData{Err: prepareErr})
			tracer.TraceQueryEnd(queryCtx, nil, pgx.TraceQueryEndData{Err: prepareErr})
			spans := recorder.Ended()
			if len(spans) != 2 {
				t.Fatalf("ended spans = %d, want query and prepare", len(spans))
			}
			for _, span := range spans {
				assertQueryAttributes(t, attribute.NewSet(span.Attributes()...), "INSERT", "trips", wantErrorType)
				if span.Status().Code != wantStatus || strings.Contains(fmt.Sprint(span.Attributes(), span.Events(), span.Status()), "private") {
					t.Fatalf("prepare/query span contains wrong status or private details: %+v", span)
				}
			}
			if spans[0].Parent().SpanID() != spans[1].SpanContext().SpanID() || spans[1].Parent().SpanID() != parent.SpanContext().SpanID() {
				t.Fatal("prepare span is not nested inside query span")
			}
			var resource metricdata.ResourceMetrics
			if err := reader.Collect(t.Context(), &resource); err != nil {
				t.Fatalf("collect metrics: %v", err)
			}
			histogram := queryHistogram(t, resource)
			if len(histogram.DataPoints) != 1 || histogram.DataPoints[0].Count != 1 {
				t.Fatalf("prepare double-counted query duration: %+v", histogram)
			}
		})
	}
}

func assertQueryAttributes(t *testing.T, attrs attribute.Set, operation, collection, errorType string) {
	t.Helper()
	for key, want := range map[string]string{"db.operation.name": operation, "db.collection.name": collection, "error.type": errorType} {
		value, ok := attrs.Value(attribute.Key(key))
		if want == "" {
			if ok {
				t.Errorf("unexpected %s = %v", key, value)
			}
			continue
		}
		if !ok || value.AsString() != want {
			t.Errorf("%s = %v, want %q", key, value, want)
		}
	}
	for _, key := range []attribute.Key{"db.query.text", "db.statement", "server.address", "user.name"} {
		if value, ok := attrs.Value(key); ok {
			t.Errorf("unexpected private attribute %s = %v", key, value)
		}
	}
}

func queryHistogram(t *testing.T, resource metricdata.ResourceMetrics) metricdata.Histogram[float64] {
	t.Helper()
	for _, scope := range resource.ScopeMetrics {
		for _, metric := range scope.Metrics {
			if metric.Name != "db.client.operation.duration" {
				continue
			}
			histogram, ok := metric.Data.(metricdata.Histogram[float64])
			if !ok || metric.Unit != "s" {
				t.Fatalf("database duration metric = %+v", metric)
			}
			return histogram
		}
	}
	t.Fatal("missing database duration histogram")
	return metricdata.Histogram[float64]{}
}
