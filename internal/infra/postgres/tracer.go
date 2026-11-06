package postgres

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/exaring/otelpgx"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/trace"
)

var tablePattern = regexp.MustCompile(`(?i)\b(?:FROM|INTO|UPDATE)\s+([a-z_][a-z0-9_]*)`)

type Tracer struct {
	*otelpgx.Tracer
	duration metric.Float64Histogram
}

type queryKey struct{}

type query struct {
	started time.Time
	attrs   []attribute.KeyValue
}

func NewTracer(traces trace.TracerProvider, metrics metric.MeterProvider) (*Tracer, error) {
	duration, err := metrics.Meter("tripgo/postgres").Float64Histogram(
		"db.client.operation.duration", metric.WithUnit("s"),
		metric.WithExplicitBucketBoundaries(0.001, 0.005, 0.01, 0.05, 0.1, 0.5, 1, 5, 10),
	)
	if err != nil {
		return nil, fmt.Errorf("create database duration histogram: %w", err)
	}
	return &Tracer{
		Tracer: otelpgx.NewTracer(
			otelpgx.WithTracerProvider(traces),
			otelpgx.WithMeterProvider(noop.NewMeterProvider()),
			otelpgx.WithDisableSQLStatementInAttributes(),
			otelpgx.WithDisableConnectionDetailsInAttributes(),
			otelpgx.WithDisableAcquireTracer(),
		),
		duration: duration,
	}, nil
}

func (t *Tracer) TraceConnectEnd(ctx context.Context, data pgx.TraceConnectEndData) {
	if data.Err != nil {
		kind := errorType(data.Err)
		trace.SpanFromContext(ctx).SetAttributes(attribute.String("error.type", kind))
		data.Err = errors.New(kind)
	}
	t.Tracer.TraceConnectEnd(ctx, data)
}

func (t *Tracer) TraceQueryStart(ctx context.Context, conn *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	attrs := queryAttributes(ctx, data.SQL)
	started := time.Now()
	ctx = t.Tracer.TraceQueryStart(ctx, conn, data)
	trace.SpanFromContext(ctx).SetAttributes(attrs...)
	return context.WithValue(ctx, queryKey{}, query{started: started, attrs: attrs})
}

func (t *Tracer) TracePrepareStart(ctx context.Context, conn *pgx.Conn, data pgx.TracePrepareStartData) context.Context {
	attrs := queryAttributes(ctx, data.SQL)
	ctx = t.Tracer.TracePrepareStart(ctx, conn, data)
	trace.SpanFromContext(ctx).SetAttributes(attrs...)
	return ctx
}

func (t *Tracer) TracePrepareEnd(ctx context.Context, conn *pgx.Conn, data pgx.TracePrepareEndData) {
	if data.Err != nil {
		kind := errorType(data.Err)
		trace.SpanFromContext(ctx).SetAttributes(attribute.String("error.type", kind))
		data.Err = errors.New(kind)
	}
	t.Tracer.TracePrepareEnd(ctx, conn, data)
}

func queryAttributes(ctx context.Context, sql string) []attribute.KeyValue {
	attrs := []attribute.KeyValue{attribute.String("db.operation.name", otelpgx.SQLOperationName(ctx, sql))}
	if table := collection(sql); table != "" {
		attrs = append(attrs, attribute.String("db.collection.name", table))
	}
	return attrs
}

func (t *Tracer) TraceQueryEnd(ctx context.Context, conn *pgx.Conn, data pgx.TraceQueryEndData) {
	q, ok := ctx.Value(queryKey{}).(query)
	if !ok {
		return
	}
	if data.Err != nil && !errors.Is(data.Err, pgx.ErrNoRows) {
		kind := errorType(data.Err)
		q.attrs = append(q.attrs, attribute.String("error.type", kind))
		span := trace.SpanFromContext(ctx)
		span.SetAttributes(attribute.String("error.type", kind))
		span.SetStatus(codes.Error, kind)
		data.Err = errors.New(kind)
	}
	t.duration.Record(ctx, time.Since(q.started).Seconds(), metric.WithAttributes(q.attrs...))
	t.Tracer.TraceQueryEnd(ctx, conn, data)
}

func collection(sql string) string {
	match := tablePattern.FindStringSubmatch(sql)
	if len(match) < 2 {
		return ""
	}
	switch table := strings.ToLower(match[1]); table {
	case "trips", "trip_status_history", "idempotency_keys":
		return table
	default:
		return ""
	}
}

func errorType(err error) string {
	var pgErr *pgconn.PgError
	switch {
	case errors.As(err, &pgErr):
		return pgErr.Code
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case errors.Is(err, context.Canceled):
		return "canceled"
	default:
		return "database_error"
	}
}
