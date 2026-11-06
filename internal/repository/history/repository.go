package history

import (
	"context"
	"fmt"

	"github.com/Masterminds/squirrel"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	domain "github.com/BeganovR/Golang-BeganovR/internal/domain/trip"
	"github.com/BeganovR/Golang-BeganovR/internal/infra/postgres/transaction"
)

var psql = squirrel.StatementBuilder.PlaceholderFormat(squirrel.Dollar)

type Repository struct {
	pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

func (r *Repository) Add(
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

	if _, err := transaction.ExecutorFrom(ctx, r.pool).Exec(ctx, query, args...); err != nil {
		return fmt.Errorf("insert status history: %w", err)
	}
	return nil
}
