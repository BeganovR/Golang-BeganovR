package postgres

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BeganovR/Golang-BeganovR/internal/domain"
)

type StatusHistoryRepository struct {
	pool *pgxpool.Pool
}

func NewStatusHistoryRepository(pool *pgxpool.Pool) *StatusHistoryRepository {
	return &StatusHistoryRepository{pool: pool}
}

func (r *StatusHistoryRepository) Add(
	ctx context.Context,
	tripID uuid.UUID,
	from *domain.TripStatus,
	to domain.TripStatus,
	reason string,
) error {
	query, args, err := psql.Insert("trip_status_history").
		Columns("trip_id", "from_status", "to_status", "reason").
		Values(tripID, from, to, reason).
		ToSql()
	if err != nil {
		return fmt.Errorf("build insert status history: %w", err)
	}

	if _, err := ExecutorFrom(ctx, r.pool).Exec(ctx, query, args...); err != nil {
		return fmt.Errorf("insert status history: %w", err)
	}
	return nil
}
