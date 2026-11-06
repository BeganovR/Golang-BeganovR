package httpapi

import (
	"context"

	"github.com/google/uuid"

	"github.com/BeganovR/Golang-BeganovR/internal/domain"
)

type Service interface {
	CreateTrip(ctx context.Context, idempotencyKey *uuid.UUID, requestHash []byte, trip domain.Trip) (domain.Trip, bool, error)
	GetTrip(ctx context.Context, id uuid.UUID) (domain.Trip, error)
	FinishTrip(ctx context.Context, id uuid.UUID) (domain.Trip, error)
}

type Pinger interface {
	Ping(ctx context.Context) error
}
