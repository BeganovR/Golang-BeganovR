package telemetry

import (
	"context"
	"fmt"
	"time"

	"go.opentelemetry.io/otel/metric"
)

type Trips struct {
	created   metric.Int64Counter
	completed metric.Int64Counter
}

func NewTrips(provider metric.MeterProvider, repository tripRepository, timeout time.Duration) (*Trips, error) {
	meter := provider.Meter("tripgo/trips")
	created, err := meter.Int64Counter("tripgo.trips.created", metric.WithUnit("{trip}"))
	if err != nil {
		return nil, fmt.Errorf("create trips counter: %w", err)
	}
	completed, err := meter.Int64Counter("tripgo.trips.completed", metric.WithUnit("{trip}"))
	if err != nil {
		return nil, fmt.Errorf("create completed counter: %w", err)
	}
	_, err = meter.Int64ObservableGauge("tripgo.trips.active", metric.WithUnit("{trip}"),
		metric.WithInt64Callback(func(ctx context.Context, observer metric.Int64Observer) error {
			ctx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()
			count, err := repository.CountActive(ctx)
			if err != nil {
				return fmt.Errorf("count active trips: %w", err)
			}
			observer.Observe(count)
			return nil
		}))
	if err != nil {
		return nil, fmt.Errorf("create active trips gauge: %w", err)
	}
	return &Trips{created: created, completed: completed}, nil
}

func (m *Trips) TripCreated(ctx context.Context) {
	m.created.Add(ctx, 1)
}

func (m *Trips) TripCompleted(ctx context.Context) {
	m.completed.Add(ctx, 1)
}
