package trip

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	domain "github.com/BeganovR/Golang-BeganovR/internal/domain/trip"
	"github.com/BeganovR/Golang-BeganovR/internal/infra/postgres/transaction"
)

const staleTrips = `
SELECT id, driver_id, COALESCE(last_position_at, started_at) AS last_seen_at
FROM trips
WHERE status = 'active'
  AND COALESCE(last_position_at, started_at) < $1::timestamptz - $2::interval`

func (r *Repository) RecordPosition(ctx context.Context, id uuid.UUID, recordedAt time.Time) error {
	const query = `
UPDATE trips
SET last_position_at = GREATEST(last_position_at, $2), updated_at = now()
WHERE id = $1 AND status = 'active'`
	tag, err := transaction.ExecutorFrom(ctx, r.pool).Exec(ctx, query, id, recordedAt)
	if err != nil {
		return fmt.Errorf("update last position: %w", err)
	}
	if tag.RowsAffected() != 0 {
		return nil
	}
	if err := r.CheckExists(ctx, id); err != nil {
		return err
	}
	return domain.ErrTripCompleted
}

func (r *Repository) ListStale(ctx context.Context, asOf time.Time, staleAfter time.Duration, after *domain.StaleCandidate, limit int) ([]domain.StaleCandidate, error) {
	age := pgtype.Interval{Microseconds: staleAfter.Microseconds(), Valid: true}
	query := staleTrips + " ORDER BY last_seen_at, id LIMIT $3"
	args := []any{asOf, age, limit}
	if after != nil {
		query = staleTrips + `
  AND (COALESCE(last_position_at, started_at), id) > ($4, $5)
ORDER BY last_seen_at, id LIMIT $3`
		args = append(args, after.LastSeenAt, after.TripID)
	}
	rows, err := transaction.ExecutorFrom(ctx, r.pool).Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query stale trips: %w", err)
	}
	trips, err := pgx.CollectRows(rows, pgx.RowToStructByPos[domain.StaleCandidate])
	if err != nil {
		return nil, fmt.Errorf("read stale trips: %w", err)
	}
	return trips, nil
}
