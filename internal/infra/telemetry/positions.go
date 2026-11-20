package telemetry

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

type Positions struct {
	saved    metric.Int64Counter
	checks   metric.Int64Counter
	rows     metric.Int64Histogram
	requests metric.Int64Counter
	duration metric.Float64Histogram
	queue    atomic.Int64
	active   atomic.Int64
}

func NewPositions(provider metric.MeterProvider) (*Positions, error) {
	meter := provider.Meter("tripgo/positions")
	m := &Positions{}
	var err error
	m.saved, err = meter.Int64Counter("tripgo.positions.saved", metric.WithUnit("{position}"))
	if err != nil {
		return nil, fmt.Errorf("create saved positions counter: %w", err)
	}
	m.checks, err = meter.Int64Counter("tripgo.stale_checks", metric.WithUnit("{check}"))
	if err != nil {
		return nil, fmt.Errorf("create stale checks counter: %w", err)
	}
	m.rows, err = meter.Int64Histogram("tripgo.stale_checks.rows_read", metric.WithUnit("{row}"),
		metric.WithExplicitBucketBoundaries(0, 10, 50, 100, 250, 500, 1000, 5000),
	)
	if err != nil {
		return nil, fmt.Errorf("create stale checks rows histogram: %w", err)
	}
	m.requests, err = meter.Int64Counter("tripgo.position_requests", metric.WithUnit("{request}"))
	if err != nil {
		return nil, fmt.Errorf("create position requests counter: %w", err)
	}
	m.duration, err = meter.Float64Histogram("tripgo.position_requests.duration", metric.WithUnit("s"),
		metric.WithExplicitBucketBoundaries(0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10),
	)
	if err != nil {
		return nil, fmt.Errorf("create position request duration histogram: %w", err)
	}
	if err := observeGauge(meter, "tripgo.position_requests.queue.size", "{request}", &m.queue); err != nil {
		return nil, err
	}
	if err := observeGauge(meter, "tripgo.position_requests.workers.active", "{worker}", &m.active); err != nil {
		return nil, err
	}
	return m, nil
}

func observeGauge(meter metric.Meter, name, unit string, value *atomic.Int64) error {
	_, err := meter.Int64ObservableGauge(name, metric.WithUnit(unit),
		metric.WithInt64Callback(func(_ context.Context, observer metric.Int64Observer) error {
			observer.Observe(value.Load())
			return nil
		}),
	)
	if err != nil {
		return fmt.Errorf("create %s gauge: %w", name, err)
	}
	return nil
}

func (m *Positions) PositionSaved(ctx context.Context) {
	m.saved.Add(ctx, 1)
}

func (m *Positions) StaleCheck(ctx context.Context, result string) {
	m.checks.Add(ctx, 1, metric.WithAttributes(attribute.String("result", result)))
}

func (m *Positions) RowsRead(ctx context.Context, count int64) {
	m.rows.Record(ctx, count)
}

func (m *Positions) QueueSize(_ context.Context, size int64) {
	m.queue.Store(size)
}

func (m *Positions) ActiveWorkers(_ context.Context, delta int64) {
	m.active.Add(delta)
}

func (m *Positions) PositionRequest(ctx context.Context, result string, duration time.Duration) {
	attrs := metric.WithAttributes(attribute.String("result", result))
	m.requests.Add(ctx, 1, attrs)
	if result != "cancelled" {
		m.duration.Record(ctx, duration.Seconds(), attrs)
	}
}
