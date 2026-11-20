package push

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

const maxResponseDrain = 4 << 10

type Gateway struct {
	endpoint string
	client   *http.Client
	tracer   trace.Tracer
}

type positionRequest struct {
	RequestID   uuid.UUID         `json:"request_id"`
	RecipientID uuid.UUID         `json:"recipient_id"`
	Kind        string            `json:"kind"`
	Data        map[string]string `json:"data"`
}

type HTTPError struct {
	StatusCode int
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("push service returned HTTP %d", e.StatusCode)
}

type TransportError struct {
	cause error
}

func (e *TransportError) Error() string {
	switch {
	case errors.Is(e.cause, context.DeadlineExceeded):
		return "push service request timed out"
	case errors.Is(e.cause, context.Canceled):
		return "push service request cancelled"
	default:
		return "push service transport failed"
	}
}

func (e *TransportError) Unwrap() error {
	return e.cause
}

func New(baseURL string, timeout time.Duration, provider trace.TracerProvider) (*Gateway, error) {
	base, err := url.Parse(baseURL)
	if err != nil || base.Hostname() == "" || (base.Scheme != "http" && base.Scheme != "https") ||
		base.User != nil || base.RawQuery != "" || base.ForceQuery || base.Fragment != "" {
		return nil, errors.New("PUSH_HTTP_URL must be an HTTP or HTTPS base URL without credentials, query or fragment")
	}
	if timeout <= 0 {
		return nil, errors.New("PUSH_REQUEST_TIMEOUT must be positive")
	}
	return &Gateway{
		endpoint: base.JoinPath("api", "v1", "push").String(),
		client: &http.Client{
			Transport: http.DefaultTransport.(*http.Transport).Clone(),
			Timeout:   timeout,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		tracer: provider.Tracer("tripgo/gateway/push"),
	}, nil
}

func (g *Gateway) CloseIdleConnections() {
	g.client.CloseIdleConnections()
}

func (g *Gateway) RequestPosition(ctx context.Context, tripID, driverID uuid.UUID) error {
	ctx, span := g.tracer.Start(ctx, "push.request_position",
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(attribute.String("http.request.method", http.MethodPost)),
	)
	defer span.End()

	requestID, err := uuid.NewRandom()
	if err != nil {
		return fmt.Errorf("generate push request id: %w", err)
	}
	body, err := json.Marshal(positionRequest{
		RequestID:   requestID,
		RecipientID: driverID,
		Kind:        "REQUEST_POSITION",
		Data:        map[string]string{"trip_id": tripID.String()},
	})
	if err != nil {
		return fmt.Errorf("encode push request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, g.endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build push request: %w", err)
	}
	req.GetBody = nil
	req.Header.Set("Content-Type", "application/json")
	propagation.TraceContext{}.Inject(ctx, propagation.HeaderCarrier(req.Header))

	res, err := g.client.Do(req)
	if err != nil {
		failure := &TransportError{cause: err}
		span.SetAttributes(attribute.String("error.type", "transport"))
		span.SetStatus(codes.Error, failure.Error())
		return failure
	}
	defer res.Body.Close()
	_, _ = io.CopyN(io.Discard, res.Body, maxResponseDrain)
	span.SetAttributes(attribute.Int("http.response.status_code", res.StatusCode))
	if res.StatusCode != http.StatusAccepted {
		failure := &HTTPError{StatusCode: res.StatusCode}
		span.SetAttributes(attribute.String("error.type", strconv.Itoa(res.StatusCode)))
		span.SetStatus(codes.Error, failure.Error())
		return failure
	}
	return nil
}
