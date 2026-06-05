package api

import (
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"
	"time"

	"golang.org/x/time/rate"

	"lanweave/pkg/protocol"
)

// statusRecorder captures the response status code for logging.
type statusRecorder struct {
	http.ResponseWriter
	status int
	wrote  bool
}

func (r *statusRecorder) WriteHeader(code int) {
	if !r.wrote {
		r.status = code
		r.wrote = true
	}
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	if !r.wrote {
		r.status = http.StatusOK
		r.wrote = true
	}
	return r.ResponseWriter.Write(b)
}

// RequestLogger logs one structured line per request with method, path, status,
// and duration. It is the outermost middleware so it observes the final status,
// including rate-limited (429) and recovered (500) responses.
func RequestLogger(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(rec, r)
			log.Info("http request",
				"method", r.Method,
				"path", r.URL.Path,
				"status", rec.status,
				"duration_ms", time.Since(start).Milliseconds(),
				"remote", r.RemoteAddr,
			)
		})
	}
}

// Recoverer turns a panic into a 500 with the generic error envelope. The stack
// goes to the ERROR log only; the client never sees internal detail.
func Recoverer(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rec := recover(); rec != nil {
					log.Error("panic recovered",
						"error", fmt.Sprintf("%v", rec),
						"stack", string(debug.Stack()),
						"path", r.URL.Path,
					)
					protocol.WriteJSONError(w, http.StatusInternalServerError, "internal_error", "An internal error occurred.")
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

// RateLimit rejects requests with 429 when the shared token bucket is empty.
// keyFn is an extension point for future per-key limiting; this feature wires
// only the single global limiter.
func RateLimit(limiter *rate.Limiter) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !limiter.Allow() {
				w.Header().Set("Retry-After", "1")
				protocol.WriteJSONError(w, http.StatusTooManyRequests, "rate_limited", "Too many requests. Please retry shortly.")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
