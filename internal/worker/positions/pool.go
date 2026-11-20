package positions

import (
	"container/heap"
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/BeganovR/Golang-BeganovR/internal/domain/trip"
)

const (
	queued    = "queued"
	duplicate = "duplicate"
	dropped   = "dropped"
	cancelled = "cancelled"
)

type Config struct {
	CheckInterval  time.Duration
	StaleAfter     time.Duration
	QueryTimeout   time.Duration
	RequestTimeout time.Duration
	Workers        int
	QueueSize      int
	BatchSize      int
}

type Pool struct {
	cfg       Config
	trips     staleRepository
	push      pushGateway
	metrics   metrics
	tracer    trace.Tracer
	logger    *slog.Logger
	newTicker func(time.Duration) ticker

	mu           sync.Mutex
	ready        *sync.Cond
	queue        priorityQueue
	pending      map[uuid.UUID]*task
	lastRequest  map[uuid.UUID]time.Time
	batchCancels map[uuid.UUID]struct{}
	closed       bool

	stopScanner  context.CancelFunc
	stopRequests context.CancelFunc
	done         chan struct{}
}

func New(
	cfg Config,
	trips staleRepository,
	push pushGateway,
	metrics metrics,
	provider trace.TracerProvider,
	logger *slog.Logger,
) *Pool {
	p := &Pool{
		cfg:         cfg,
		trips:       trips,
		push:        push,
		metrics:     metrics,
		tracer:      provider.Tracer("tripgo/worker/positions"),
		logger:      logger,
		newTicker:   newSystemTicker,
		queue:       make(priorityQueue, 0, cfg.QueueSize),
		pending:     make(map[uuid.UUID]*task),
		lastRequest: make(map[uuid.UUID]time.Time),
		done:        make(chan struct{}),
	}
	p.ready = sync.NewCond(&p.mu)
	return p
}

func (p *Pool) Start(ctx context.Context) {
	scanCtx, stopScanner := context.WithCancel(ctx)
	workCtx, stopRequests := context.WithCancel(context.WithoutCancel(ctx))
	p.stopScanner = stopScanner
	p.stopRequests = stopRequests

	p.logger.InfoContext(ctx, "position worker pool started", "workers", p.cfg.Workers)
	var workers sync.WaitGroup
	for range p.cfg.Workers {
		workers.Go(func() { p.work(workCtx) })
	}
	go func() {
		defer close(p.done)
		defer stopRequests()
		defer stopScanner()
		defer func() {
			p.mu.Lock()
			p.closed = true
			p.ready.Broadcast()
			p.mu.Unlock()
			workers.Wait()
			p.logger.InfoContext(workCtx, "position worker pool stopped")
		}()
		p.scan(scanCtx)
	}()
}

func (p *Pool) Shutdown(ctx context.Context) error {
	p.stopScanner()

	drainCtx := ctx
	if deadline, ok := ctx.Deadline(); ok {
		remaining := max(time.Until(deadline), 0)
		cleanupTime := min(remaining/10, 100*time.Millisecond)
		waitCtx, cancel := context.WithDeadline(ctx, deadline.Add(-cleanupTime))
		defer cancel()
		drainCtx = waitCtx
	}

	select {
	case <-p.done:
		return nil
	case <-drainCtx.Done():
		p.stopRequests()
		select {
		case <-p.done:
		case <-ctx.Done():
		}
		return drainCtx.Err()
	}
}

func (p *Pool) Cancel(ctx context.Context, tripID uuid.UUID) {
	p.mu.Lock()
	if p.batchCancels != nil {
		p.batchCancels[tripID] = struct{}{}
	}
	t, found := p.pending[tripID]
	if !found || t.index < 0 {
		p.mu.Unlock()
		return
	}
	heap.Remove(&p.queue, t.index)
	delete(p.pending, tripID)
	p.metrics.QueueSize(ctx, int64(len(p.queue)))
	p.mu.Unlock()
	p.metrics.PositionRequest(ctx, cancelled, 0)
}

func (p *Pool) schedule(ctx context.Context, candidate trip.StaleCandidate) scheduled {
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, found := p.batchCancels[candidate.TripID]; found {
		return scheduled{result: cancelled}
	}
	if _, found := p.pending[candidate.TripID]; found {
		return scheduled{result: duplicate}
	}
	if started, found := p.lastRequest[candidate.TripID]; found && time.Since(started) < p.cfg.CheckInterval {
		return scheduled{result: duplicate}
	}

	t := &task{StaleCandidate: candidate, source: trace.SpanContextFromContext(ctx)}
	outcome := scheduled{result: queued}
	if len(p.queue) == p.cfg.QueueSize {
		newest := p.queue.latest()
		if !t.before(newest) {
			return scheduled{result: dropped, dropped: t.TripID}
		}
		heap.Remove(&p.queue, newest.index)
		delete(p.pending, newest.TripID)
		outcome.dropped = newest.TripID
	}
	heap.Push(&p.queue, t)
	p.pending[t.TripID] = t
	p.metrics.QueueSize(ctx, int64(len(p.queue)))
	p.ready.Signal()
	return outcome
}

func (p *Pool) take(ctx context.Context) *task {
	p.mu.Lock()
	defer p.mu.Unlock()
	for len(p.queue) == 0 && !p.closed && ctx.Err() == nil {
		p.ready.Wait()
	}
	if len(p.queue) == 0 || ctx.Err() != nil {
		return nil
	}
	t := heap.Pop(&p.queue).(*task)
	p.metrics.QueueSize(ctx, int64(len(p.queue)))
	return t
}

func (p *Pool) work(ctx context.Context) {
	for {
		t := p.take(ctx)
		if t == nil {
			return
		}
		started := p.requestPosition(ctx, t)
		p.mu.Lock()
		p.lastRequest[t.TripID] = started
		delete(p.pending, t.TripID)
		p.mu.Unlock()
	}
}

func (p *Pool) requestPosition(ctx context.Context, t *task) time.Time {
	ctx, span := p.tracer.Start(ctx, "positions.request", trace.WithNewRoot(), trace.WithLinks(trace.Link{
		SpanContext: t.source,
	}))
	defer span.End()

	p.metrics.ActiveWorkers(ctx, 1)
	defer p.metrics.ActiveWorkers(ctx, -1)

	requestCtx, cancel := context.WithTimeout(ctx, p.cfg.RequestTimeout)
	defer cancel()
	started := time.Now()
	err := p.push.RequestPosition(requestCtx, t.TripID, t.DriverID)
	duration := time.Since(started)
	result := "success"
	if err != nil {
		result = "error"
		span.RecordError(err)
		span.SetStatus(codes.Error, "position request failed")
		p.logger.WarnContext(ctx, "position request failed", "trip_id", t.TripID, "error", err)
	}
	p.metrics.PositionRequest(ctx, result, duration)
	return started
}
