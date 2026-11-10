package trip

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Masterminds/squirrel"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	domain "github.com/BeganovR/Golang-BeganovR/internal/domain/trip"
	"github.com/BeganovR/Golang-BeganovR/internal/infra/postgres/transaction"
)

const (
	pgUniqueViolation = "23505"
	activeDriverIndex = "one_active_driver_idx"
	statusColumn      = "status"
)

var psql = squirrel.StatementBuilder.PlaceholderFormat(squirrel.Dollar)

var tripColumns = []string{
	"id", "user_id", "driver_id",
	"start_latitude", "start_longitude", "end_latitude", "end_longitude",
	"price", statusColumn, "started_at", "finished_at",
}

type Repository struct {
	pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

func (r *Repository) Create(ctx context.Context, t domain.Trip) error {
	query, args, err := psql.Insert("trips").
		Columns("id", "user_id", "driver_id",
			"start_latitude", "start_longitude", "end_latitude", "end_longitude",
			"price", statusColumn, "started_at").
		Values(t.ID, t.UserID, t.DriverID,
			t.StartPoint.Latitude, t.StartPoint.Longitude, t.EndPoint.Latitude, t.EndPoint.Longitude,
			t.Price, t.Status, t.StartedAt).
		ToSql()
	if err != nil {
		return fmt.Errorf("build insert trip: %w", err)
	}

	if _, err := transaction.ExecutorFrom(ctx, r.pool).Exec(ctx, query, args...); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == pgUniqueViolation && pgErr.ConstraintName == activeDriverIndex {
			return domain.ErrDriverBusy
		}
		return fmt.Errorf("insert trip: %w", err)
	}
	return nil
}

func (r *Repository) GetByID(ctx context.Context, id uuid.UUID) (domain.Trip, error) {
	query, args, err := psql.Select(tripColumns...).
		From("trips").
		Where(squirrel.Eq{"id": id}).
		ToSql()
	if err != nil {
		return domain.Trip{}, fmt.Errorf("build select trip: %w", err)
	}

	t, err := scanTrip(transaction.ExecutorFrom(ctx, r.pool).QueryRow(ctx, query, args...))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Trip{}, domain.ErrTripNotFound
	}
	if err != nil {
		return domain.Trip{}, fmt.Errorf("select trip: %w", err)
	}
	return t, nil
}

func (r *Repository) Finish(ctx context.Context, id uuid.UUID, finishedAt time.Time) (domain.Trip, error) {
	query, args, err := psql.Update("trips").
		Set(statusColumn, domain.TripStatusCompleted).
		Set("finished_at", finishedAt).
		Set("updated_at", finishedAt).
		Where(squirrel.Eq{"id": id, statusColumn: domain.TripStatusActive}).
		Suffix("RETURNING " + strings.Join(tripColumns, ", ")).
		ToSql()
	if err != nil {
		return domain.Trip{}, fmt.Errorf("build finish trip: %w", err)
	}

	t, err := scanTrip(transaction.ExecutorFrom(ctx, r.pool).QueryRow(ctx, query, args...))
	if err == nil {
		return t, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return domain.Trip{}, fmt.Errorf("finish trip: %w", err)
	}

	if _, err := r.GetByID(ctx, id); err != nil {
		return domain.Trip{}, err
	}
	return domain.Trip{}, domain.ErrTripCompleted
}

func (r *Repository) CountActive(ctx context.Context) (int64, error) {
	query, args, err := psql.Select("count(*)").From("trips").
		Where(squirrel.Eq{statusColumn: domain.TripStatusActive}).ToSql()
	if err != nil {
		return 0, fmt.Errorf("build count active trips: %w", err)
	}
	var count int64
	if err := transaction.ExecutorFrom(ctx, r.pool).QueryRow(ctx, query, args...).Scan(&count); err != nil {
		return 0, fmt.Errorf("count active trips: %w", err)
	}
	return count, nil
}

func scanTrip(row pgx.Row) (domain.Trip, error) {
	var t domain.Trip
	err := row.Scan(
		&t.ID, &t.UserID, &t.DriverID,
		&t.StartPoint.Latitude, &t.StartPoint.Longitude,
		&t.EndPoint.Latitude, &t.EndPoint.Longitude,
		&t.Price, &t.Status, &t.StartedAt, &t.FinishedAt,
	)
	if err != nil {
		return domain.Trip{}, fmt.Errorf("scan trip: %w", err)
	}

	t.StartedAt = t.StartedAt.UTC()
	if t.FinishedAt != nil {
		finished := t.FinishedAt.UTC()
		t.FinishedAt = &finished
	}
	return t, nil
}
