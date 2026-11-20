package positions

import (
	"context"

	"github.com/google/uuid"

	"github.com/BeganovR/Golang-BeganovR/internal/domain/trip"
)

type usecase interface {
	Save(context.Context, trip.Position) (trip.Position, error)
	List(context.Context, uuid.UUID) ([]trip.Position, error)
}
