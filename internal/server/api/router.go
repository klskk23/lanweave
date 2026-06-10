// Package api builds the HTTP routing and middleware for the control plane.
package api

import (
	"log/slog"
	"net/http"
	"time"

	"golang.org/x/time/rate"

	"lanweave/internal/server/auth"
	"lanweave/internal/server/config"
	"lanweave/internal/server/netfw"
	"lanweave/internal/server/store"
	"lanweave/internal/server/wg"
	"lanweave/pkg/protocol"
)

// Options configures the router.
type Options struct {
	Version  string
	Limiter  *rate.Limiter
	Logger   *slog.Logger
	Store    *store.Store
	JWT      *auth.JWTManager
	WG       *wg.Server
	NetFW    *netfw.Manager
	WGConfig config.WireGuardConfig
	Status   statusProvider
	// MaxDevicesPerUser / MaxOwnedZonesPerUser are the already-resolved per-user
	// caps (0 = unlimited). Plain ints, not the config pointer type, so a zero-value
	// Options (e.g. a test harness that omits them) reads as unlimited.
	MaxDevicesPerUser    int
	MaxOwnedZonesPerUser int
	// InviteTTL is the already-resolved window stamped onto new invite codes
	// (0 = never expire). A zero-value Options (e.g. a test harness) issues
	// never-expiring codes.
	InviteTTL time.Duration
	// APIDocs exposes the Swagger UI / OpenAPI document under /api/docs/ when
	// true. When false the docs routes are simply not registered, so they fall
	// through to the API-wide notFound and stay indistinguishable from paths
	// that never existed.
	APIDocs bool
}

// NewRouter returns the fully wrapped handler. Middleware order, outermost first:
// RequestLogger -> Recoverer -> RateLimit -> mux. Protected routes additionally
// opt into AuthRequired (and AdminRequired) wrappers.
func NewRouter(opts Options) http.Handler {
	h := &handlers{
		store:                opts.Store,
		jwt:                  opts.JWT,
		log:                  opts.Logger,
		version:              opts.Version,
		wg:                   opts.WG,
		netfw:                opts.NetFW,
		wgConfig:             opts.WGConfig,
		status:               opts.Status,
		maxDevicesPerUser:    opts.MaxDevicesPerUser,
		maxOwnedZonesPerUser: opts.MaxOwnedZonesPerUser,
		inviteTTL:            opts.InviteTTL,
	}

	mux := http.NewServeMux()
	for _, rt := range routes(h, opts) {
		mux.Handle(rt.pattern, rt.handler)
	}
	if opts.APIDocs {
		for _, rt := range docsRoutes() {
			mux.Handle(rt.pattern, rt.handler)
		}
	}

	mux.HandleFunc("/", notFound)

	var handler http.Handler = mux
	handler = RateLimit(opts.Limiter)(handler)
	handler = Recoverer(opts.Logger)(handler)
	handler = RequestLogger(opts.Logger)(handler)
	return handler
}

func notFound(w http.ResponseWriter, _ *http.Request) {
	protocol.WriteJSONError(w, http.StatusNotFound, "not_found", "The requested resource was not found.")
}
