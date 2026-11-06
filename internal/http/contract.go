//go:generate go tool mockgen -source=contract.go -destination=mocks_test.go -package=httptransport_test -mock_names=createUsecase=MockCreateUsecase,tripUsecase=MockTripUsecase,pinger=MockPinger

package httptransport

import (
	"context"

	"github.com/google/uuid"

	"github.com/BeganovR/Golang-BeganovR/internal/domain/trip"
)

type createUsecase interface {
	Execute(ctx context.Context, key *uuid.UUID, hash []byte, t trip.Trip) (trip.Trip, bool, error)
}

type tripUsecase interface {
	Execute(ctx context.Context, id uuid.UUID) (trip.Trip, error)
}

type pinger interface {
	Ping(ctx context.Context) error
}
