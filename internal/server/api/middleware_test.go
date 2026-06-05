package api_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"golang.org/x/time/rate"

	"lanweave/internal/server/api"
	"lanweave/pkg/protocol"
)

func TestRequestLoggerEmitsFields(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	h := api.RequestLogger(logger)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))

	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/v1/healthz", nil))

	var entry map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &entry); err != nil {
		t.Fatalf("log line not JSON: %v (%s)", err, buf.String())
	}
	for _, k := range []string{"time", "level", "method", "path", "status", "duration_ms"} {
		if _, ok := entry[k]; !ok {
			t.Errorf("log entry missing field %q: %v", k, entry)
		}
	}
	if entry["status"].(float64) != http.StatusTeapot {
		t.Errorf("status = %v, want 418", entry["status"])
	}
}

func TestRecovererReturns500(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	h := api.Recoverer(logger)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("boom")
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	var e protocol.ErrorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &e); err != nil {
		t.Fatalf("body not JSON: %v", err)
	}
	if e.Error != "internal_error" {
		t.Errorf("error code = %q, want internal_error", e.Error)
	}
	// The panic detail must stay out of the response body.
	if bytes.Contains(rec.Body.Bytes(), []byte("boom")) {
		t.Error("panic detail leaked into response body")
	}
	// ...but be present in the log.
	if !bytes.Contains(buf.Bytes(), []byte("boom")) {
		t.Error("panic detail missing from logs")
	}
}

func TestRateLimit429(t *testing.T) {
	limiter := rate.NewLimiter(rate.Limit(1), 1) // 1 token capacity
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	h := api.RateLimit(limiter)(ok)

	// First request consumes the only token.
	rec1 := httptest.NewRecorder()
	h.ServeHTTP(rec1, httptest.NewRequest(http.MethodGet, "/api/v1/healthz", nil))
	if rec1.Code != http.StatusOK {
		t.Fatalf("first request status = %d, want 200", rec1.Code)
	}

	// Second is rejected.
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, httptest.NewRequest(http.MethodGet, "/api/v1/healthz", nil))
	if rec2.Code != http.StatusTooManyRequests {
		t.Fatalf("second request status = %d, want 429", rec2.Code)
	}
	if rec2.Header().Get("Retry-After") == "" {
		t.Error("429 response missing Retry-After header")
	}
	var e protocol.ErrorResponse
	if err := json.Unmarshal(rec2.Body.Bytes(), &e); err != nil {
		t.Fatalf("429 body not JSON: %v", err)
	}
	if e.Error != "rate_limited" {
		t.Errorf("error code = %q, want rate_limited", e.Error)
	}
}
