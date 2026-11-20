//go:generate go tool mockgen -source=contract.go -destination=mocks_test.go -package=finish_test -mock_names=usecase=MockUsecase

package finish

import (
	"context"

	"github.com/google/uuid"

	"github.com/BeganovR/Golang-BeganovR/internal/domain/trip"
)

type usecase interface {
	Execute(ctx context.Context, id uuid.UUID) (trip.Trip, error)
}
