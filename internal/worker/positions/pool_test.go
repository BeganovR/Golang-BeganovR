package positions

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"testing/synctest"
	"time"

	"github.com/google/uuid"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace/noop"
	"go.uber.org/goleak"
	"go.uber.org/mock/gomock"

	"github.com/BeganovR/Golang-BeganovR/internal/domain/trip"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

func TestScanBatches(t *testing.T) {
	for _, tc := range []struct {
		name      string
		capacity  int
		wantReads int64
	}{
		{name: "all pages", capacity: 300, wantReads: 250},
		{name: "stop at first drop", capacity: 50, wantReads: 100},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, 1, tc.capacity)
			now := time.Now()
			all := candidates(now, 250)
			h.trips.EXPECT().ListStale(gomock.Any(), now, time.Minute, nil, 100).Return(all[:100], nil)
			if tc.capacity == 300 {
				h.trips.EXPECT().ListStale(gomock.Any(), now, time.Minute, &all[99], 100).Return(all[100:200], nil)
				h.trips.EXPECT().ListStale(gomock.Any(), now, time.Minute, &all[199], 100).Return(all[200:], nil)
			} else {
				h.metrics.EXPECT().StaleCheck(gomock.Any(), dropped)
			}
			wantQueued := min(len(all), tc.capacity)
			h.metrics.EXPECT().StaleCheck(gomock.Any(), queued).Times(wantQueued)
			h.metrics.EXPECT().RowsRead(gomock.Any(), tc.wantReads)

			h.pool.check(t.Context(), now)
			if len(h.pool.queue) != wantQueued {
				t.Fatalf("queued %d tasks, want %d", len(h.pool.queue), wantQueued)
			}
		})
	}
}

func TestQueuePrioritizesOldestAndEvictsNewest(t *testing.T) {
	h := newHarness(t, 1, 3)
	ctx := t.Context()
	now := time.Now()
	oldest, older, newer, newest := timedCandidate(1, now.Add(-4*time.Minute)),
		timedCandidate(2, now.Add(-3*time.Minute)),
		timedCandidate(3, now.Add(-2*time.Minute)),
		timedCandidate(4, now.Add(-time.Minute))
	for _, candidate := range []trip.StaleCandidate{newer, newest, oldest} {
		h.metrics.EXPECT().StaleCheck(gomock.Any(), queued)
		if h.pool.checkCandidate(ctx, candidate) {
			t.Fatalf("dropped %s with space in queue", candidate.TripID)
		}
	}
	h.metrics.EXPECT().StaleCheck(gomock.Any(), queued)
	h.metrics.EXPECT().StaleCheck(gomock.Any(), dropped).Times(2)
	for _, candidate := range []trip.StaleCandidate{older, newest} {
		if !h.pool.checkCandidate(ctx, candidate) {
			t.Fatal("expected a drop with a full queue")
		}
	}
	if _, found := h.pool.pending[newest.TripID]; found {
		t.Fatal("evicted task stayed in the deduplication set")
	}
	for _, want := range []trip.StaleCandidate{oldest, older, newer} {
		got := h.pool.take(ctx)
		if got.TripID != want.TripID {
			t.Fatalf("took %s, want %s", got.TripID, want.TripID)
		}
	}
}

func TestScannerDeduplicatesAndDropsWhilePushIsBlocked(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t, 1, 1)
		first, second, third := timedCandidate(1, time.Now().Add(-3*time.Minute)),
			timedCandidate(2, time.Now().Add(-2*time.Minute)),
			timedCandidate(3, time.Now().Add(-time.Minute))
		release := make(chan struct{})
		h.push.EXPECT().RequestPosition(gomock.Any(), first.TripID, first.DriverID).
			DoAndReturn(func(ctx context.Context, _, _ uuid.UUID) error {
				select {
				case <-release:
					return nil
				case <-ctx.Done():
					return ctx.Err()
				}
			})
		h.expectActive(1)
		h.metrics.EXPECT().PositionRequest(gomock.Any(), "success", gomock.Any())
		h.metrics.EXPECT().PositionRequest(gomock.Any(), cancelled, time.Duration(0))
		h.metrics.EXPECT().StaleCheck(gomock.Any(), queued).Times(2)
		h.metrics.EXPECT().StaleCheck(gomock.Any(), duplicate).Times(3)
		h.metrics.EXPECT().StaleCheck(gomock.Any(), dropped)

		now := time.Now()
		ticks := h.start(t.Context())
		for i, batch := range [][]trip.StaleCandidate{{first}, {first, second}, {first, second, third}} {
			tick := now.Add(time.Duration(i) * h.pool.cfg.CheckInterval)
			h.expectScan(tick, batch)
			ticks <- tick
			synctest.Wait()
		}
		h.pool.Cancel(t.Context(), second.TripID)
		if len(h.pool.queue) != 0 {
			t.Fatal("cancelled task still takes space in the queue")
		}
		close(release)
		if err := h.pool.Shutdown(t.Context()); err != nil {
			t.Fatal(err)
		}
	})
}

func TestScannerSkipsCandidateCancelledWhileReadingBatch(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t, 1, 1)
		now := time.Now()
		batch := candidates(now, 1)
		read := make(chan struct{})
		release := make(chan struct{})
		h.trips.EXPECT().ListStale(gomock.Any(), now, h.pool.cfg.StaleAfter, nil, h.pool.cfg.BatchSize).
			DoAndReturn(func(context.Context, time.Time, time.Duration, *trip.StaleCandidate, int) ([]trip.StaleCandidate, error) {
				close(read)
				<-release
				return batch, nil
			})
		h.metrics.EXPECT().RowsRead(gomock.Any(), int64(1))

		ticks := h.start(t.Context())
		ticks <- now
		<-read
		h.pool.Cancel(t.Context(), batch[0].TripID)
		close(release)
		synctest.Wait()
	})
}

func TestPoolWaitsForCheckIntervalAfterLatePush(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t, 1, 2)
		first, second := timedCandidate(1, time.Now().Add(-3*time.Minute)),
			timedCandidate(2, time.Now().Add(-2*time.Minute))
		release := make(chan struct{})
		h.push.EXPECT().RequestPosition(gomock.Any(), first.TripID, first.DriverID).
			DoAndReturn(func(context.Context, uuid.UUID, uuid.UUID) error {
				<-release
				return nil
			})
		h.push.EXPECT().RequestPosition(gomock.Any(), second.TripID, second.DriverID).Return(nil).Times(2)
		h.expectActive(3)
		h.metrics.EXPECT().PositionRequest(gomock.Any(), "success", gomock.Any()).Times(3)
		h.metrics.EXPECT().StaleCheck(gomock.Any(), queued).Times(3)
		h.metrics.EXPECT().StaleCheck(gomock.Any(), duplicate)

		ticks := h.start(t.Context())
		scan := func(batch []trip.StaleCandidate) {
			tick := time.Now()
			h.expectScan(tick, batch)
			ticks <- tick
			synctest.Wait()
		}
		scan([]trip.StaleCandidate{first, second})
		<-time.After(29 * time.Second)
		close(release)
		synctest.Wait()
		<-time.After(time.Second)
		scan([]trip.StaleCandidate{second})
		<-time.After(29 * time.Second)
		scan([]trip.StaleCandidate{second})
	})
}

func TestPoolUsesFixedWorkersAndDrainsOnCancel(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t, 2, 10)
		now := time.Now()
		batch := candidates(now, 5)
		started := make(chan struct{}, len(batch))
		release := make(chan struct{})
		pushErr := errors.New("push unavailable")
		for i, candidate := range batch {
			var result error
			if i == 0 {
				result = pushErr
			}
			h.push.EXPECT().RequestPosition(gomock.Any(), candidate.TripID, candidate.DriverID).
				DoAndReturn(func(ctx context.Context, _, _ uuid.UUID) error {
					started <- struct{}{}
					select {
					case <-release:
						return result
					case <-ctx.Done():
						return ctx.Err()
					}
				})
		}
		h.expectActive(len(batch))
		h.expectScan(now, batch)
		h.metrics.EXPECT().StaleCheck(gomock.Any(), queued).Times(len(batch))
		h.metrics.EXPECT().PositionRequest(gomock.Any(), "error", gomock.Any())
		h.metrics.EXPECT().PositionRequest(gomock.Any(), "success", gomock.Any()).Times(len(batch) - 1)

		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		ticks := h.start(ctx)
		ticks <- now
		synctest.Wait()
		if len(started) != h.pool.cfg.Workers {
			t.Fatalf("%d concurrent requests, want %d", len(started), h.pool.cfg.Workers)
		}

		cancel()
		synctest.Wait()
		select {
		case <-h.pool.done:
			t.Fatal("pool stopped before draining the queue")
		default:
		}
		close(release)
		<-h.pool.done
		if len(started) != len(batch) {
			t.Fatalf("processed %d tasks, want %d", len(started), len(batch))
		}
	})
}

func TestShutdownDeadlineCancelsPush(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t, 1, 10)
		now := time.Now()
		batch := candidates(now, 1)
		h.expectScan(now, batch)
		h.expectActive(1)
		h.metrics.EXPECT().StaleCheck(gomock.Any(), queued)
		h.metrics.EXPECT().PositionRequest(gomock.Any(), "error", gomock.Any())
		h.push.EXPECT().RequestPosition(gomock.Any(), batch[0].TripID, batch[0].DriverID).
			DoAndReturn(func(ctx context.Context, _, _ uuid.UUID) error {
				<-ctx.Done()
				if !errors.Is(ctx.Err(), context.Canceled) {
					t.Errorf("push context error = %v, want context.Canceled", ctx.Err())
				}
				<-time.After(10 * time.Millisecond)
				return ctx.Err()
			})

		ticks := h.start(t.Context())
		ticks <- now
		synctest.Wait()
		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		defer cancel()
		if err := h.pool.Shutdown(ctx); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("shutdown error = %v, want context.DeadlineExceeded", err)
		}
		select {
		case <-h.pool.done:
		default:
			t.Fatal("shutdown returned before the cancelled worker exited")
		}
	})
}

func TestRequestSpanLinksToScan(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t, 1, 1)
		recorder := tracetest.NewSpanRecorder()
		provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
		defer func() {
			if err := provider.Shutdown(t.Context()); err != nil {
				t.Error(err)
			}
		}()
		h.pool.tracer = provider.Tracer("positions-test")
		now := time.Now()
		batch := candidates(now, 1)
		h.expectScan(now, batch)
		h.expectActive(1)
		h.metrics.EXPECT().StaleCheck(gomock.Any(), queued)
		h.metrics.EXPECT().PositionRequest(gomock.Any(), "success", gomock.Any())
		h.push.EXPECT().RequestPosition(gomock.Any(), batch[0].TripID, batch[0].DriverID).Return(nil)
		ticks := h.start(t.Context())
		ticks <- now
		synctest.Wait()
		if err := h.pool.Shutdown(t.Context()); err != nil {
			t.Fatal(err)
		}

		spans := make(map[string]sdktrace.ReadOnlySpan)
		for _, span := range recorder.Ended() {
			spans[span.Name()] = span
		}
		scan, request := spans["positions.scan"], spans["positions.request"]
		if scan == nil || request == nil {
			t.Fatal("expected scan and request spans")
		}
		if request.Parent().IsValid() || request.SpanContext().TraceID() == scan.SpanContext().TraceID() {
			t.Fatal("request must start a separate trace")
		}
		if links := request.Links(); len(links) != 1 || !links[0].SpanContext.Equal(scan.SpanContext()) {
			t.Fatalf("request span links = %v, want one link to scan", links)
		}
	})
}

type harness struct {
	t       *testing.T
	ctrl    *gomock.Controller
	pool    *Pool
	trips   *MockStaleRepository
	push    *MockPushGateway
	metrics *MockMetrics
}

func newHarness(t *testing.T, workers, capacity int) harness {
	t.Helper()
	ctrl := gomock.NewController(t)
	trips, push, metrics := NewMockStaleRepository(ctrl), NewMockPushGateway(ctrl), NewMockMetrics(ctrl)
	cfg := Config{
		CheckInterval:  30 * time.Second,
		StaleAfter:     time.Minute,
		QueryTimeout:   time.Second,
		RequestTimeout: time.Minute,
		Workers:        workers,
		QueueSize:      capacity,
		BatchSize:      100,
	}
	metrics.EXPECT().QueueSize(gomock.Any(), gomock.Any()).AnyTimes()
	pool := New(cfg, trips, push, metrics, noop.NewTracerProvider(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	return harness{t: t, ctrl: ctrl, pool: pool, trips: trips, push: push, metrics: metrics}
}

func (h harness) start(ctx context.Context) chan<- time.Time {
	h.t.Helper()
	ticks := make(chan time.Time)
	mockTicker := NewMockTicker(h.ctrl)
	mockTicker.EXPECT().Ticks().Return(ticks)
	mockTicker.EXPECT().Stop()
	h.pool.newTicker = func(time.Duration) ticker { return mockTicker }
	h.pool.Start(ctx)
	h.t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		defer cancel()
		if err := h.pool.Shutdown(ctx); err != nil {
			<-h.pool.done
		}
	})
	return ticks
}

func (h harness) expectScan(asOf time.Time, batch []trip.StaleCandidate) {
	h.trips.EXPECT().ListStale(gomock.Any(), asOf, h.pool.cfg.StaleAfter, nil, h.pool.cfg.BatchSize).Return(batch, nil)
	h.metrics.EXPECT().RowsRead(gomock.Any(), int64(len(batch)))
}

func (h harness) expectActive(count int) {
	h.metrics.EXPECT().ActiveWorkers(gomock.Any(), int64(1)).Times(count)
	h.metrics.EXPECT().ActiveWorkers(gomock.Any(), int64(-1)).Times(count)
}

func candidates(now time.Time, count int) []trip.StaleCandidate {
	batch := make([]trip.StaleCandidate, count)
	for i := range batch {
		batch[i] = timedCandidate(byte(i+1), now.Add(-2*time.Minute))
	}
	return batch
}

func timedCandidate(id byte, lastSeenAt time.Time) trip.StaleCandidate {
	return trip.StaleCandidate{
		TripID:     uuid.UUID{15: id},
		DriverID:   uuid.UUID{0: 1, 15: id},
		LastSeenAt: lastSeenAt,
	}
}
