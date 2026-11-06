//go:generate go tool mockgen -source=contract.go -destination=mocks_test.go -package=create_test -mock_names=usecase=MockUsecase

package create

import (
	"context"

	"github.com/google/uuid"

	"github.com/BeganovR/Golang-BeganovR/internal/domain/trip"
)

type usecase interface {
	Execute(ctx context.Context, key *uuid.UUID, hash []byte, t trip.Trip) (trip.Trip, bool, error)
}
