// Package api builds the HTTP routing and middleware for the control plane.
package api

import (
	"log/slog"
	"net/http"

	"golang.org/x/time/rate"

	"lanweave/pkg/protocol"
)

// Options configures the router.
type Options struct {
	Version string
	Limiter *rate.Limiter
	Logger  *slog.Logger
}

// NewRouter returns the fully wrapped handler. Middleware order, outermost first:
// RequestLogger -> Recoverer -> RateLimit -> mux.
func NewRouter(opts Options) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/healthz", healthz(opts.Version))
	mux.HandleFunc("/", notFound)

	var h http.Handler = mux
	h = RateLimit(opts.Limiter)(h)
	h = Recoverer(opts.Logger)(h)
	h = RequestLogger(opts.Logger)(h)
	return h
}

func notFound(w http.ResponseWriter, _ *http.Request) {
	protocol.WriteJSONError(w, http.StatusNotFound, "not_found", "The requested resource was not found.")
}
