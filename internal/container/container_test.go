//go:generate go tool mockgen -source ../worker/idempotency/contract.go -destination mocks_test.go -package container -mock_names keyRepository=MockKeyRepository

package container

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/puddle/v2"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.uber.org/mock/gomock"

	"github.com/BeganovR/Golang-BeganovR/internal/infra/telemetry"
	keypurger "github.com/BeganovR/Golang-BeganovR/internal/worker/idempotency"
)

const lifecycleTimeout = 5 * time.Second

type purgerLifecycle struct {
	started chan context.Context
	stopped chan struct{}
}

func lifecycleContainer(t *testing.T, handler http.Handler, timeout time.Duration) (*Container, <-chan string, *purgerLifecycle, *sdkmetric.ManualReader) {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	lifecycle := &purgerLifecycle{started: make(chan context.Context, 1), stopped: make(chan struct{})}
	keys := NewMockKeyRepository(gomock.NewController(t))
	keys.EXPECT().DeleteExpired(gomock.Any(), time.Hour).DoAndReturn(func(ctx context.Context, _ time.Duration) (int64, error) {
		lifecycle.started <- ctx
		<-ctx.Done()
		close(lifecycle.stopped)
		return 0, ctx.Err()
	})
	reader := sdkmetric.NewManualReader()
	ready := make(chan string, 1)
	c := &Container{
		server: &http.Server{
			Addr: "127.0.0.1:0", Handler: handler, ReadHeaderTimeout: time.Second,
			BaseContext: func(listener net.Listener) context.Context {
				ready <- listener.Addr().String()
				return context.Background()
			},
		},
		purger: keypurger.New(keys, time.Hour, time.Hour, time.Hour, logger),
		telemetry: &telemetry.Providers{
			Traces:  sdktrace.NewTracerProvider(sdktrace.WithSampler(sdktrace.AlwaysSample())),
			Metrics: sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)),
		},
		logger: logger, shutdownTimeout: timeout,
	}
	return c, ready, lifecycle, reader
}

func startContainer(t *testing.T, c *Container) (context.CancelFunc, <-chan error) {
	t.Helper()
	ctx, stop := context.WithCancel(t.Context())
	done := make(chan error, 1)
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		done <- c.Run(ctx)
		close(done)
	}()
	t.Cleanup(func() {
		stop()
		if err := c.server.Close(); err != nil {
			t.Errorf("close test server: %v", err)
		}
		waitResult(t, finished)
	})
	return stop, done
}

func waitResult[T any](t *testing.T, results <-chan T) T {
	t.Helper()
	select {
	case result := <-results:
		return result
	case <-time.After(lifecycleTimeout):
		t.Fatal("timed out waiting for lifecycle operation")
		var zero T
		return zero
	}
}

type response struct {
	status int
	err    error
}

func request(t *testing.T, address string) <-chan response {
	t.Helper()
	results := make(chan response, 1)
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://"+address, http.NoBody)
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	go func() {
		client := &http.Client{Timeout: lifecycleTimeout}
		res, err := client.Do(req)
		if err != nil {
			results <- response{err: err}
			return
		}
		results <- response{status: res.StatusCode, err: res.Body.Close()}
	}()
	return results
}

func assertProvidersStopped(t *testing.T, c *Container, reader *sdkmetric.ManualReader) {
	t.Helper()
	var resource metricdata.ResourceMetrics
	if err := reader.Collect(t.Context(), &resource); !errors.Is(err, sdkmetric.ErrReaderShutdown) {
		t.Errorf("metric reader after shutdown = %v, want ErrReaderShutdown", err)
	}
	_, span := c.telemetry.Traces.Tracer("test").Start(t.Context(), "after shutdown")
	defer span.End()
	if span.IsRecording() {
		t.Error("trace provider is still recording after shutdown")
	}
}

func TestRunDrainsRequestsBeforeStoppingDependencies(t *testing.T) {
	t.Parallel()
	entered, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	releaseHandler := func() { releaseOnce.Do(func() { close(release) }) }
	c, ready, keys, reader := lifecycleContainer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(entered)
		<-release
		w.WriteHeader(http.StatusNoContent)
	}), time.Second)
	poolConfig, err := pgxpool.ParseConfig("postgres://localhost/test?sslmode=disable")
	if err != nil {
		t.Fatalf("parse test pool config: %v", err)
	}
	poolConfig.MinConns, poolConfig.MinIdleConns = 0, 0
	c.pool, err = pgxpool.NewWithConfig(t.Context(), poolConfig)
	if err != nil {
		t.Fatalf("create test pool: %v", err)
	}
	t.Cleanup(c.pool.Close)
	shutdownStarted := make(chan struct{})
	c.server.RegisterOnShutdown(func() { close(shutdownStarted) })
	stop, done := startContainer(t, c)
	t.Cleanup(releaseHandler)
	purgeContext := waitResult(t, keys.started)
	result := request(t, waitResult(t, ready))
	waitResult(t, entered)
	stop()
	waitResult(t, shutdownStarted)
	if err := purgeContext.Err(); err != nil {
		t.Fatalf("worker stopped while HTTP request was draining: %v", err)
	}
	var resource metricdata.ResourceMetrics
	if err := reader.Collect(t.Context(), &resource); err != nil {
		t.Fatalf("metrics stopped while HTTP request was draining: %v", err)
	}
	releaseHandler()
	if res := waitResult(t, result); res.err != nil || res.status != http.StatusNoContent {
		t.Fatalf("in-flight response = %+v", res)
	}
	if err := waitResult(t, done); err != nil {
		t.Fatalf("Run() = %v", err)
	}
	waitResult(t, keys.stopped)
	assertProvidersStopped(t, c, reader)
	if conn, err := c.pool.Acquire(t.Context()); !errors.Is(err, puddle.ErrClosedPool) {
		if conn != nil {
			conn.Release()
		}
		t.Fatalf("pool after shutdown error = %v, want ErrClosedPool", err)
	}
}

func TestRunForcesInFlightRequestClosedAtDeadline(t *testing.T) {
	t.Parallel()
	entered, canceled := make(chan struct{}), make(chan struct{})
	c, ready, keys, reader := lifecycleContainer(t, http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		close(entered)
		<-r.Context().Done()
		close(canceled)
	}), 20*time.Millisecond)
	stop, done := startContainer(t, c)
	waitResult(t, keys.started)
	result := request(t, waitResult(t, ready))
	waitResult(t, entered)
	stop()
	if err := waitResult(t, done); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Run() = %v, want DeadlineExceeded", err)
	}
	waitResult(t, canceled)
	waitResult(t, keys.stopped)
	if res := waitResult(t, result); res.err == nil {
		t.Fatalf("expected interrupted request, got status %d", res.status)
	}
	assertProvidersStopped(t, c, reader)
}

func TestRunBindFailureStopsDependencies(t *testing.T) {
	t.Parallel()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve occupied address: %v", err)
	}
	t.Cleanup(func() {
		if err := listener.Close(); err != nil {
			t.Errorf("close reserved listener: %v", err)
		}
	})
	c, _, keys, reader := lifecycleContainer(t, http.NotFoundHandler(), time.Second)
	c.server.Addr = listener.Addr().String()
	_, done := startContainer(t, c)
	if err := waitResult(t, done); !errors.Is(err, syscall.EADDRINUSE) {
		t.Fatalf("Run() = %v, want EADDRINUSE", err)
	}
	waitResult(t, keys.stopped)
	assertProvidersStopped(t, c, reader)
}
