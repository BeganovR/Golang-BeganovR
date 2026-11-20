package telemetry_test

import (
	"context"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/BeganovR/Golang-BeganovR/internal/infra/telemetry"
)

func TestPositionMetrics(t *testing.T) {
	t.Parallel()
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	t.Cleanup(func() {
		if err := provider.Shutdown(context.Background()); err != nil {
			t.Errorf("shutdown meter provider: %v", err)
		}
	})
	positions, err := telemetry.NewPositions(provider)
	if err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	positions.PositionSaved(ctx)
	positions.PositionSaved(ctx)
	positions.StaleCheck(ctx, "queued")
	positions.StaleCheck(ctx, "queued")
	positions.StaleCheck(ctx, "duplicate")
	positions.StaleCheck(ctx, "dropped")
	positions.RowsRead(ctx, 100)
	positions.RowsRead(ctx, 15)
	positions.PositionRequest(ctx, "success", 500*time.Millisecond)
	positions.PositionRequest(ctx, "error", time.Second)
	positions.PositionRequest(ctx, "cancelled", 0)
	positions.QueueSize(ctx, 5)
	positions.ActiveWorkers(ctx, 1)
	positions.ActiveWorkers(ctx, 1)
	positions.ActiveWorkers(ctx, -1)

	var resource metricdata.ResourceMetrics
	if err := reader.Collect(ctx, &resource); err != nil {
		t.Fatal(err)
	}
	metrics := tripMetrics(resource)
	checkPositionCounter(t, metrics["tripgo.positions.saved"], map[string]int64{"": 2})
	checkPositionCounter(t, metrics["tripgo.stale_checks"], map[string]int64{"queued": 2, "duplicate": 1, "dropped": 1})
	checkPositionCounter(t, metrics["tripgo.position_requests"], map[string]int64{"success": 1, "error": 1, "cancelled": 1})
	checkPositionGauge(t, metrics["tripgo.position_requests.queue.size"], 5)
	checkPositionGauge(t, metrics["tripgo.position_requests.workers.active"], 1)

	rows, ok := metrics["tripgo.stale_checks.rows_read"].Data.(metricdata.Histogram[int64])
	if !ok || len(rows.DataPoints) != 1 || rows.DataPoints[0].Count != 2 || rows.DataPoints[0].Sum != 115 ||
		rows.DataPoints[0].Attributes.Len() != 0 {
		t.Fatalf("rows_read = %+v", rows)
	}
	durations := metrics["tripgo.position_requests.duration"]
	histogram, ok := durations.Data.(metricdata.Histogram[float64])
	if !ok || durations.Unit != "s" || len(histogram.DataPoints) != 2 {
		t.Fatalf("position request duration = %+v", durations)
	}
	wantDurations := map[string]float64{"success": 0.5, "error": 1}
	for i := range histogram.DataPoints {
		point := &histogram.DataPoints[i]
		result, ok := point.Attributes.Value(attribute.Key("result"))
		if !ok || point.Attributes.Len() != 1 || point.Count != 1 || point.Sum != wantDurations[result.AsString()] {
			t.Errorf("duration for %q = %+v", result.AsString(), point)
		}
		delete(wantDurations, result.AsString())
	}
	if len(wantDurations) > 0 {
		t.Errorf("missing durations: %v", wantDurations)
	}

	positions.QueueSize(ctx, 0)
	positions.ActiveWorkers(ctx, -1)
	if err := reader.Collect(ctx, &resource); err != nil {
		t.Fatal(err)
	}
	metrics = tripMetrics(resource)
	checkPositionGauge(t, metrics["tripgo.position_requests.queue.size"], 0)
	checkPositionGauge(t, metrics["tripgo.position_requests.workers.active"], 0)
}

func checkPositionCounter(t *testing.T, m metricdata.Metrics, want map[string]int64) {
	t.Helper()
	sum, ok := m.Data.(metricdata.Sum[int64])
	if !ok || !sum.IsMonotonic || len(sum.DataPoints) != len(want) {
		t.Fatalf("%s = %+v, want counters %v", m.Name, m.Data, want)
	}
	for _, point := range sum.DataPoints {
		result, found := point.Attributes.Value(attribute.Key("result"))
		key := result.AsString()
		labels := 0
		if found {
			labels = 1
		}
		if value, ok := want[key]; !ok || point.Value != value || point.Attributes.Len() != labels {
			t.Errorf("%s %q = %+v, want %d", m.Name, key, point, value)
		}
		delete(want, key)
	}
	if len(want) > 0 {
		t.Errorf("%s missing %v", m.Name, want)
	}
}

func checkPositionGauge(t *testing.T, m metricdata.Metrics, want int64) {
	t.Helper()
	gauge, ok := m.Data.(metricdata.Gauge[int64])
	if !ok || len(gauge.DataPoints) != 1 || gauge.DataPoints[0].Value != want || gauge.DataPoints[0].Attributes.Len() != 0 {
		t.Fatalf("%s = %+v, want %d", m.Name, m.Data, want)
	}
}
