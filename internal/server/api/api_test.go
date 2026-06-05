package api_test

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"golang.org/x/time/rate"

	"lanweave/internal/server/api"
	"lanweave/pkg/protocol"
)

func testRouter(limiter *rate.Limiter) http.Handler {
	if limiter == nil {
		limiter = rate.NewLimiter(rate.Limit(1000), 1000)
	}
	return api.NewRouter(api.Options{
		Version: "test-version",
		Limiter: limiter,
		Logger:  slog.New(slog.NewJSONHandler(io.Discard, nil)),
	})
}

func TestHealthzOK(t *testing.T) {
	h := testRouter(nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/healthz", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("content-type = %q", ct)
	}
	var body protocol.HealthResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("body not JSON: %v", err)
	}
	if body.Status != "ok" || body.Version != "test-version" {
		t.Errorf("unexpected body: %+v", body)
	}
}

func TestHealthzMethodNotAllowed(t *testing.T) {
	h := testRouter(nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/healthz", nil))

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rec.Code)
	}
	assertErrorEnvelope(t, rec.Body.Bytes(), "method_not_allowed")
}

func TestNotFound(t *testing.T) {
	h := testRouter(nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/does-not-exist", nil))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	assertErrorEnvelope(t, rec.Body.Bytes(), "not_found")
}

func assertErrorEnvelope(t *testing.T, body []byte, wantCode string) {
	t.Helper()
	var e protocol.ErrorResponse
	if err := json.Unmarshal(body, &e); err != nil {
		t.Fatalf("error body not JSON: %v", err)
	}
	if e.Error != wantCode {
		t.Errorf("error code = %q, want %q", e.Error, wantCode)
	}
	if e.Message == "" {
		t.Error("error message is empty")
	}
}
