//go:generate go tool mockgen -destination=mocks_test.go -package=middleware_test net/http ResponseWriter

package middleware_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.uber.org/mock/gomock"

	"github.com/BeganovR/Golang-BeganovR/internal/http/response"
)

const privateWriteDetails = "private transport details"

func TestResponseWriteFailureSignals(t *testing.T) {
	t.Parallel()
	for _, status := range []int{http.StatusOK, http.StatusCreated, http.StatusInternalServerError} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			t.Parallel()
			original := errors.New("private database details")
			router := chi.NewRouter()
			router.Post(tripRoute, func(w http.ResponseWriter, r *http.Request) {
				if status == http.StatusInternalServerError {
					response.Error(w, r, original)
					return
				}
				response.JSON(w, r, status, map[string]string{"result": "ok"})
			})
			f := newSignals(t, router)
			writer := NewMockResponseWriter(gomock.NewController(t))
			writer.EXPECT().Header().Return(make(http.Header)).AnyTimes()
			gomock.InOrder(
				writer.EXPECT().WriteHeader(status),
				writer.EXPECT().Write(gomock.Any()).Return(0, fmt.Errorf("%s: %w", privateWriteDetails, io.ErrClosedPipe)),
			)
			f.handler.ServeHTTP(writer, httptest.NewRequest(http.MethodPost, tripPath, nil))
			level := "INFO"
			if status == http.StatusInternalServerError {
				level = "ERROR"
			}
			entry := assertAccessLog(t, f, http.MethodPost, tripRoute, status, level)
			writeError, ok := entry["write_error"].(string)
			if !ok || !strings.Contains(writeError, privateWriteDetails) {
				t.Errorf("write error missing from access log: %+v", entry)
			}
			if status == http.StatusInternalServerError && entry["error"] != original.Error() {
				t.Errorf("original error = %v; want %q", entry["error"], original.Error())
			}
			span := f.spans.Ended()[0]
			if span.Status().Code != codes.Error {
				t.Errorf("write failure span status = %v; want Error", span.Status())
			}
			assertAttribute(t, attribute.NewSet(span.Attributes()...), errorAttribute, attribute.StringValue("response_write_error"))
			data, err := json.Marshal(map[string]any{"events": span.Events(), "span_status": span.Status()})
			if err != nil {
				t.Fatalf("encode span: %v", err)
			}
			if strings.Contains(string(data), privateWriteDetails) || strings.Contains(string(data), original.Error()) {
				t.Errorf("internal error details appeared in span: %s", data)
			}
			metrics := f.collect(t)
			assertDuration(t, metrics, http.MethodPost, tripRoute, status, 1)
			assertActive(t, metrics, http.MethodPost, tripRoute, 0)
		})
	}
}

func TestResponseEncodingFailureSignals(t *testing.T) {
	t.Parallel()
	router := chi.NewRouter()
	router.Get(tripRoute, func(w http.ResponseWriter, r *http.Request) {
		response.JSON(w, r, http.StatusOK, math.NaN())
	})
	f := newSignals(t, router)
	writer := NewMockResponseWriter(gomock.NewController(t))
	writer.EXPECT().Header().Return(make(http.Header)).AnyTimes()
	writer.EXPECT().WriteHeader(http.StatusOK)
	f.handler.ServeHTTP(writer, httptest.NewRequest(http.MethodGet, tripPath, nil))
	entry := assertAccessLog(t, f, http.MethodGet, tripRoute, http.StatusOK, "INFO")
	if writeError, ok := entry["write_error"].(string); !ok || writeError == "" {
		t.Errorf("encoding error missing from access log: %+v", entry)
	}
	span := f.spans.Ended()[0]
	if span.Status().Code != codes.Error {
		t.Errorf("encoding failure span status = %v; want Error", span.Status())
	}
	assertAttribute(t, attribute.NewSet(span.Attributes()...), errorAttribute, attribute.StringValue("response_write_error"))
}

func TestUnknownMethodsShareSignals(t *testing.T) {
	t.Parallel()
	const otherMethod = "_OTHER"
	router := chi.NewRouter()
	router.Get(tripRoute, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	f := newSignals(t, router)
	for i := range 10 {
		writer := httptest.NewRecorder()
		method := fmt.Sprintf("METHOD%d", i)
		f.handler.ServeHTTP(writer, httptest.NewRequest(method, tripPath, nil))
		if writer.Code != http.StatusMethodNotAllowed {
			t.Fatalf("%s status = %d; want 405", method, writer.Code)
		}
	}
	entries := f.entries(t)
	if len(entries) != 10 || len(f.spans.Ended()) != 10 {
		t.Fatalf("logs=%d spans=%d; want ten of each", len(entries), len(f.spans.Ended()))
	}
	for i, entry := range entries {
		if entry["method"] != fmt.Sprintf("METHOD%d", i) || entry["route"] != tripRoute {
			t.Errorf("access log lost original method or route: %+v", entry)
		}
	}
	for _, span := range f.spans.Ended() {
		if span.Name() != otherMethod+" "+tripRoute {
			t.Errorf("unknown method span name = %q", span.Name())
		}
	}
	metrics := f.collect(t)
	assertDuration(t, metrics, otherMethod, tripRoute, http.StatusMethodNotAllowed, 10)
	assertActive(t, metrics, otherMethod, tripRoute, 0)
}
