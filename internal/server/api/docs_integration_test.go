package api_test

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"golang.org/x/time/rate"

	"lanweave/internal/server/api"
	"lanweave/internal/server/auth"
)

// docsRouter builds a store-less router: the docs surface and the notFound
// fallback never touch the database, so this is enough to probe both toggle
// states. Business-endpoint behavior under the enabled toggle is covered by
// the whole existing suite (the shared harness runs with APIDocs on).
func docsRouter(t *testing.T, enabled bool, limiter *rate.Limiter) http.Handler {
	t.Helper()
	return api.NewRouter(api.Options{
		Version: "test",
		Limiter: limiter,
		Logger:  slog.New(slog.NewJSONHandler(io.Discard, nil)),
		JWT:     auth.NewJWTManager(harnessJWTSecret, time.Hour),
		APIDocs: enabled,
	})
}

func get(t *testing.T, router http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

func wideLimiter() *rate.Limiter { return rate.NewLimiter(rate.Limit(10000), 10000) }

// TestDocsEnabledSurface covers the documented /api/docs surface
// (contracts/docs-endpoints.md): UI page, spec file, vendored assets,
// no-trailing-slash redirect, and the unknown-asset 404 shape.
func TestDocsEnabledSurface(t *testing.T) {
	router := docsRouter(t, true, wideLimiter())

	t.Run("index page", func(t *testing.T) {
		rec := get(t, router, "/api/docs/")
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
		}
		if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
			t.Errorf("Content-Type = %q, want text/html", ct)
		}
		if body := rec.Body.String(); !strings.Contains(body, "swagger-ui-bundle.js") {
			t.Errorf("index page does not reference the bundled UI script")
		}
	})

	t.Run("no-trailing-slash redirect", func(t *testing.T) {
		rec := get(t, router, "/api/docs")
		if rec.Code != http.StatusMovedPermanently {
			t.Fatalf("status = %d, want 301", rec.Code)
		}
		if loc := rec.Header().Get("Location"); loc != "/api/docs/" {
			t.Errorf("Location = %q, want /api/docs/", loc)
		}
	})

	t.Run("openapi.yaml", func(t *testing.T) {
		rec := get(t, router, "/api/docs/openapi.yaml")
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/yaml") {
			t.Errorf("Content-Type = %q, want application/yaml", ct)
		}
		if !strings.HasPrefix(rec.Body.String(), "openapi:") {
			t.Errorf("spec body does not start with an openapi version marker")
		}
	})

	t.Run("static assets", func(t *testing.T) {
		for path, wantCT := range map[string]string{
			"/api/docs/swagger-ui.css":       "text/css",
			"/api/docs/swagger-ui-bundle.js": "text/javascript",
		} {
			rec := get(t, router, path)
			if rec.Code != http.StatusOK {
				t.Errorf("GET %s status = %d, want 200", path, rec.Code)
				continue
			}
			if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, wantCT) {
				t.Errorf("GET %s Content-Type = %q, want %s", path, ct, wantCT)
			}
			if rec.Body.Len() == 0 {
				t.Errorf("GET %s returned an empty body", path)
			}
		}
	})

	t.Run("unknown asset matches global 404 shape", func(t *testing.T) {
		got := get(t, router, "/api/docs/no-such.js")
		want := get(t, router, "/api/v1/does-not-exist")
		assertSameResponse(t, got, want)
	})
}

// TestDocsRateLimited proves the docs surface sits behind the same global
// limiter as the business API (FR-012).
func TestDocsRateLimited(t *testing.T) {
	router := docsRouter(t, true, rate.NewLimiter(rate.Limit(0.0001), 1))
	if rec := get(t, router, "/api/docs/"); rec.Code != http.StatusOK {
		t.Fatalf("first request status = %d, want 200", rec.Code)
	}
	rec := get(t, router, "/api/docs/openapi.yaml")
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("second request status = %d, want 429", rec.Code)
	}
	if e := decodeError(t, rec); e.Error != "rate_limited" {
		t.Errorf("error code = %q, want rate_limited", e.Error)
	}
}

// TestDocsDisabledIndistinguishable proves the off switch leaves no trace: every
// docs path answers byte-for-byte like any unknown path (FR-006 / SC-003), and a
// business endpoint on the same router still works (FR-011).
func TestDocsDisabledIndistinguishable(t *testing.T) {
	router := docsRouter(t, false, wideLimiter())
	want := get(t, router, "/api/v1/does-not-exist")

	for _, path := range []string{
		"/api/docs",
		"/api/docs/",
		"/api/docs/openapi.yaml",
		"/api/docs/swagger-ui-bundle.js",
	} {
		t.Run(path, func(t *testing.T) {
			assertSameResponse(t, get(t, router, path), want)
		})
	}

	t.Run("business endpoint unaffected", func(t *testing.T) {
		if rec := get(t, router, "/api/v1/healthz"); rec.Code != http.StatusOK {
			t.Errorf("healthz status = %d, want 200", rec.Code)
		}
	})
}

// assertSameResponse compares status, Content-Type and body byte-for-byte —
// the "indistinguishable from a path that never existed" bar.
func assertSameResponse(t *testing.T, got, want *httptest.ResponseRecorder) {
	t.Helper()
	if got.Code != want.Code {
		t.Errorf("status = %d, want %d", got.Code, want.Code)
	}
	if g, w := got.Header().Get("Content-Type"), want.Header().Get("Content-Type"); g != w {
		t.Errorf("Content-Type = %q, want %q", g, w)
	}
	if g, w := got.Body.String(), want.Body.String(); g != w {
		t.Errorf("body = %q, want %q", g, w)
	}
}
