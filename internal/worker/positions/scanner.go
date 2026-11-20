package positions

import (
	"context"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/codes"

	"github.com/BeganovR/Golang-BeganovR/internal/domain/trip"
)

type systemTicker struct {
	*time.Ticker
}

func (t systemTicker) Ticks() <-chan time.Time {
	return t.C
}

func newSystemTicker(interval time.Duration) ticker {
	return systemTicker{time.NewTicker(interval)}
}

func (p *Pool) scan(ctx context.Context) {
	p.logger.InfoContext(ctx, "position scanner started")
	defer p.logger.InfoContext(ctx, "position scanner stopped")

	t := p.newTicker(p.cfg.CheckInterval)
	defer t.Stop()
	ticks := t.Ticks()
	for {
		select {
		case <-ctx.Done():
			return
		case asOf, ok := <-ticks:
			if !ok || ctx.Err() != nil {
				return
			}
			p.check(ctx, asOf)
		}
	}
}

func (p *Pool) check(ctx context.Context, asOf time.Time) {
	ctx, span := p.tracer.Start(ctx, "positions.scan")
	defer span.End()

	p.mu.Lock()
	now := time.Now()
	for id, started := range p.lastRequest {
		if now.Sub(started) >= p.cfg.CheckInterval {
			delete(p.lastRequest, id)
		}
	}
	p.mu.Unlock()

	var rowsRead int64
	defer func() { p.metrics.RowsRead(ctx, rowsRead) }()
	var after *trip.StaleCandidate
	for ctx.Err() == nil {
		next, count, err := p.checkBatch(ctx, asOf, after)
		rowsRead += int64(count)
		if err != nil && ctx.Err() == nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, "scan for stale positions failed")
			p.logger.ErrorContext(ctx, "scan for stale positions failed", "error", err)
		}
		if next == nil {
			return
		}
		after = next
	}
}

func (p *Pool) checkBatch(ctx context.Context, asOf time.Time, after *trip.StaleCandidate) (*trip.StaleCandidate, int, error) {
	p.mu.Lock()
	p.batchCancels = make(map[uuid.UUID]struct{})
	p.mu.Unlock()
	defer func() {
		p.mu.Lock()
		p.batchCancels = nil
		p.mu.Unlock()
	}()

	queryCtx, cancel := context.WithTimeout(ctx, p.cfg.QueryTimeout)
	batch, err := p.trips.ListStale(queryCtx, asOf, p.cfg.StaleAfter, after, p.cfg.BatchSize)
	cancel()
	if err != nil {
		return nil, 0, err
	}

	for _, candidate := range batch {
		if ctx.Err() != nil || p.checkCandidate(ctx, candidate) {
			return nil, len(batch), nil
		}
	}
	if len(batch) < p.cfg.BatchSize {
		return nil, len(batch), nil
	}
	return &batch[len(batch)-1], len(batch), nil
}

func (p *Pool) checkCandidate(ctx context.Context, candidate trip.StaleCandidate) bool {
	outcome := p.schedule(ctx, candidate)
	if outcome.result == cancelled {
		return false
	}
	p.metrics.StaleCheck(ctx, outcome.result)
	if outcome.dropped == uuid.Nil {
		return false
	}
	if outcome.result != dropped {
		p.metrics.StaleCheck(ctx, dropped)
	}
	p.logger.WarnContext(ctx, "position queue full", "trip_id", outcome.dropped)
	return true
}
