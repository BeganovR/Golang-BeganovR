package idempotency

import (
	"context"
	"time"
)

type keyRepository interface {
	DeleteExpired(ctx context.Context, ttl time.Duration) (int64, error)
}
