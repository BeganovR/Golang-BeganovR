package position

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BeganovR/Golang-BeganovR/internal/domain/trip"
	"github.com/BeganovR/Golang-BeganovR/internal/infra/postgres/transaction"
)

type Repository struct {
	pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

func (r *Repository) Add(ctx context.Context, p trip.Position) (trip.Position, error) {
	const query = `
INSERT INTO trip_positions (trip_id, latitude, longitude, recorded_at)
VALUES ($1, $2, $3, $4)
RETURNING id`
	err := transaction.ExecutorFrom(ctx, r.pool).
		QueryRow(ctx, query, p.TripID, p.Coordinates.Latitude, p.Coordinates.Longitude, p.RecordedAt).Scan(&p.ID)
	if err != nil {
		return trip.Position{}, fmt.Errorf("insert trip position: %w", err)
	}
	return p, nil
}

func (r *Repository) List(ctx context.Context, id uuid.UUID) ([]trip.Position, error) {
	const query = `
SELECT id, trip_id, latitude, longitude, recorded_at
FROM trip_positions
WHERE trip_id = $1
ORDER BY recorded_at, id`
	rows, err := transaction.ExecutorFrom(ctx, r.pool).Query(ctx, query, id)
	if err != nil {
		return nil, fmt.Errorf("query trip positions: %w", err)
	}
	positions, err := pgx.CollectRows(rows, scanPosition)
	if err != nil {
		return nil, fmt.Errorf("read trip positions: %w", err)
	}
	return positions, nil
}

func scanPosition(row pgx.CollectableRow) (trip.Position, error) {
	var p trip.Position
	err := row.Scan(&p.ID, &p.TripID, &p.Coordinates.Latitude, &p.Coordinates.Longitude, &p.RecordedAt)
	p.RecordedAt = p.RecordedAt.UTC()
	return p, err
}
