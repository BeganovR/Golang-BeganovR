package telemetry_test

import (
	"context"
	"errors"
	"testing"
	"time"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.uber.org/mock/gomock"

	"github.com/BeganovR/Golang-BeganovR/internal/infra/telemetry"
)

func TestTripsMetrics(t *testing.T) {
	t.Parallel()
	repository := NewMockTripRepository(gomock.NewController(t))
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	t.Cleanup(func() {
		if err := provider.Shutdown(context.Background()); err != nil {
			t.Errorf("shutdown meter provider: %v", err)
		}
	})
	active := int64(7)
	repository.EXPECT().CountActive(gomock.Any()).DoAndReturn(func(ctx context.Context) (int64, error) {
		if _, ok := ctx.Deadline(); !ok {
			t.Error("active trips query has no deadline")
		}
		return active, nil
	}).Times(2)
	trips, err := telemetry.NewTrips(provider, repository, time.Second)
	if err != nil {
		t.Fatalf("NewTrips() = %v", err)
	}
	trips.TripCreated(t.Context())
	trips.TripCreated(t.Context())
	trips.TripCompleted(t.Context())
	for _, wantActive := range []int64{7, 4} {
		active = wantActive
		var resource metricdata.ResourceMetrics
		if err := reader.Collect(t.Context(), &resource); err != nil {
			t.Fatalf("collect metrics: %v", err)
		}
		metrics := tripMetrics(resource)
		for name, want := range map[string]int64{"tripgo.trips.created": 2, "tripgo.trips.completed": 1} {
			metric, ok := metrics[name]
			if !ok {
				t.Fatalf("missing %s", name)
			}
			sum, ok := metric.Data.(metricdata.Sum[int64])
			if !ok || !sum.IsMonotonic || sum.Temporality != metricdata.CumulativeTemporality || len(sum.DataPoints) != 1 {
				t.Fatalf("%s aggregation = %+v", name, metric.Data)
			}
			if sum.DataPoints[0].Value != want || sum.DataPoints[0].Attributes.Len() != 0 || metric.Unit != "{trip}" {
				t.Fatalf("%s = %+v, want %d trips without labels", name, metric, want)
			}
		}
		metric := metrics["tripgo.trips.active"]
		gauge, ok := metric.Data.(metricdata.Gauge[int64])
		if !ok || len(gauge.DataPoints) != 1 || gauge.DataPoints[0].Value != wantActive || gauge.DataPoints[0].Attributes.Len() != 0 || metric.Unit != "{trip}" {
			t.Fatalf("active trips gauge = %+v, want %d", metric, wantActive)
		}
	}
}

func TestTripsGaugeError(t *testing.T) {
	t.Parallel()
	storageErr := errors.New("storage unavailable")
	for _, timeout := range []bool{false, true} {
		name := "repository error"
		if timeout {
			name = "deadline"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			repository := NewMockTripRepository(gomock.NewController(t))
			reader := sdkmetric.NewManualReader()
			provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
			t.Cleanup(func() {
				if err := provider.Shutdown(context.Background()); err != nil {
					t.Errorf("shutdown meter provider: %v", err)
				}
			})
			wantErr := storageErr
			if timeout {
				wantErr = context.DeadlineExceeded
			}
			repository.EXPECT().CountActive(gomock.Any()).DoAndReturn(func(ctx context.Context) (int64, error) {
				if timeout {
					<-ctx.Done()
					return 0, ctx.Err()
				}
				return 0, storageErr
			})
			_, err := telemetry.NewTrips(provider, repository, time.Millisecond)
			if err != nil {
				t.Fatalf("NewTrips() = %v", err)
			}
			var resource metricdata.ResourceMetrics
			if err := reader.Collect(t.Context(), &resource); !errors.Is(err, wantErr) {
				t.Fatalf("Collect() error = %v, want %v", err, wantErr)
			}
			if metric, ok := tripMetrics(resource)["tripgo.trips.active"]; ok {
				t.Fatalf("failed observation emitted a value: %+v", metric)
			}
		})
	}
}

func tripMetrics(resource metricdata.ResourceMetrics) map[string]metricdata.Metrics {
	result := make(map[string]metricdata.Metrics)
	for _, scope := range resource.ScopeMetrics {
		for _, metric := range scope.Metrics {
			result[metric.Name] = metric
		}
	}
	return result
}
