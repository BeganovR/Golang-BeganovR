//go:generate go tool mockgen -source contract.go -destination mocks_test.go -package telemetry_test -mock_names tripRepository=MockTripRepository

package telemetry

import "context"

type tripRepository interface {
	CountActive(context.Context) (int64, error)
}
