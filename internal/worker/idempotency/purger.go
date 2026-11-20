package idempotency

import (
	"context"
	"log/slog"
	"time"
)

type Purger struct {
	keys     keyRepository
	ttl      time.Duration
	interval time.Duration
	timeout  time.Duration
	logger   *slog.Logger
}

func New(
	keys keyRepository,
	ttl, interval, timeout time.Duration,
	logger *slog.Logger,
) *Purger {
	return &Purger{keys: keys, ttl: ttl, interval: interval, timeout: timeout, logger: logger}
}

func (p *Purger) Run(ctx context.Context) {
	p.logger.InfoContext(ctx, "idempotency purger started")
	defer p.logger.InfoContext(ctx, "idempotency purger stopped")
	p.purge(ctx)

	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.purge(ctx)
		}
	}
}

func (p *Purger) purge(parent context.Context) {
	ctx, cancel := context.WithTimeout(parent, p.timeout)
	defer cancel()

	removed, err := p.keys.DeleteExpired(ctx, p.ttl)
	if err != nil {
		if parent.Err() == nil {
			p.logger.ErrorContext(ctx, "purge expired idempotency keys", "error", err)
		}
		return
	}
	if removed > 0 {
		p.logger.InfoContext(ctx, "expired idempotency keys removed", "count", removed)
	}
}
