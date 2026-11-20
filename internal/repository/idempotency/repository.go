package idempotency

import (
	"context"
	"fmt"
	"time"

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

func (r *Repository) Claim(
	ctx context.Context,
	key uuid.UUID,
	requestHash []byte,
	tripID uuid.UUID,
	ttl time.Duration,
) (bool, error) {
	query, args, err := psql.Insert("idempotency_keys").
		Columns("key", "request_hash", "trip_id").
		Values(key, requestHash, tripID).
		Suffix("ON CONFLICT (key) DO UPDATE SET "+
			"request_hash = EXCLUDED.request_hash, trip_id = EXCLUDED.trip_id, created_at = now() "+
			"WHERE idempotency_keys.created_at < now() - (? * interval '1 second')", ttl.Seconds()).
		ToSql()
	if err != nil {
		return false, fmt.Errorf("build claim idempotency key: %w", err)
	}

	tag, err := transaction.ExecutorFrom(ctx, r.pool).Exec(ctx, query, args...)
	if err != nil {
		return false, fmt.Errorf("claim idempotency key: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

func (r *Repository) Get(ctx context.Context, key uuid.UUID) (domain.IdempotencyRecord, error) {
	query, args, err := psql.Select("request_hash", "trip_id").
		From("idempotency_keys").
		Where(squirrel.Eq{"key": key}).
		ToSql()
	if err != nil {
		return domain.IdempotencyRecord{}, fmt.Errorf("build select idempotency key: %w", err)
	}

	var record domain.IdempotencyRecord
	if err := transaction.ExecutorFrom(ctx, r.pool).QueryRow(ctx, query, args...).Scan(&record.RequestHash, &record.TripID); err != nil {
		return domain.IdempotencyRecord{}, fmt.Errorf("select idempotency key: %w", err)
	}
	return record, nil
}

func (r *Repository) DeleteExpired(ctx context.Context, ttl time.Duration) (int64, error) {
	query, args, err := psql.Delete("idempotency_keys").
		Where("created_at < now() - (? * interval '1 second')", ttl.Seconds()).
		ToSql()
	if err != nil {
		return 0, fmt.Errorf("build delete expired idempotency keys: %w", err)
	}

	tag, err := transaction.ExecutorFrom(ctx, r.pool).Exec(ctx, query, args...)
	if err != nil {
		return 0, fmt.Errorf("delete expired idempotency keys: %w", err)
	}
	return tag.RowsAffected(), nil
}
