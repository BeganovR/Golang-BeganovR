package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/Masterminds/squirrel"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BeganovR/Golang-BeganovR/internal/domain"
)

type IdempotencyRepository struct {
	pool *pgxpool.Pool
}

func NewIdempotencyRepository(pool *pgxpool.Pool) *IdempotencyRepository {
	return &IdempotencyRepository{pool: pool}
}

func (r *IdempotencyRepository) Claim(
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
			"WHERE idempotency_keys.created_at < now() - ?::interval", ttl).
		ToSql()
	if err != nil {
		return false, fmt.Errorf("build claim idempotency key: %w", err)
	}

	tag, err := ExecutorFrom(ctx, r.pool).Exec(ctx, query, args...)
	if err != nil {
		return false, fmt.Errorf("claim idempotency key: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

func (r *IdempotencyRepository) Get(ctx context.Context, key uuid.UUID) (domain.IdempotencyRecord, error) {
	query, args, err := psql.Select("request_hash", "trip_id").
		From("idempotency_keys").
		Where(squirrel.Eq{"key": key}).
		ToSql()
	if err != nil {
		return domain.IdempotencyRecord{}, fmt.Errorf("build select idempotency key: %w", err)
	}

	var record domain.IdempotencyRecord
	if err := ExecutorFrom(ctx, r.pool).QueryRow(ctx, query, args...).Scan(&record.RequestHash, &record.TripID); err != nil {
		return domain.IdempotencyRecord{}, fmt.Errorf("select idempotency key: %w", err)
	}
	return record, nil
}

func (r *IdempotencyRepository) DeleteExpired(ctx context.Context, ttl time.Duration) (int64, error) {
	query, args, err := psql.Delete("idempotency_keys").
		Where("created_at < now() - ?::interval", ttl).
		ToSql()
	if err != nil {
		return 0, fmt.Errorf("build delete expired idempotency keys: %w", err)
	}

	tag, err := ExecutorFrom(ctx, r.pool).Exec(ctx, query, args...)
	if err != nil {
		return 0, fmt.Errorf("delete expired idempotency keys: %w", err)
	}
	return tag.RowsAffected(), nil
}
