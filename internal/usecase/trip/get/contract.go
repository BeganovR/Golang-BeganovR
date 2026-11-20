//go:generate go tool mockgen -source contract.go -destination mocks_test.go -package get_test -mock_names tripRepository=MockTripRepository

package get

import (
	"context"

	"github.com/google/uuid"

	"github.com/BeganovR/Golang-BeganovR/internal/domain/trip"
)

type tripRepository interface {
	GetByID(context.Context, uuid.UUID) (trip.Trip, error)
}
