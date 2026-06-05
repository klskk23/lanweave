// Package api builds the HTTP routing and middleware for the control plane.
package api

import (
	"log/slog"
	"net/http"

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
}

// NewRouter returns the fully wrapped handler. Middleware order, outermost first:
// RequestLogger -> Recoverer -> RateLimit -> mux. Protected routes additionally
// opt into AuthRequired (and AdminRequired) wrappers.
func NewRouter(opts Options) http.Handler {
	h := &handlers{
		store:    opts.Store,
		jwt:      opts.JWT,
		log:      opts.Logger,
		version:  opts.Version,
		wg:       opts.WG,
		netfw:    opts.NetFW,
		wgConfig: opts.WGConfig,
		status:   opts.Status,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/healthz", healthz(opts.Version))

	// Public (rate-limited) endpoints.
	mux.HandleFunc("POST /api/v1/login", h.login)
	mux.HandleFunc("POST /api/v1/register", h.register)

	// Authenticated endpoints.
	mux.Handle("GET /api/v1/me", AuthRequired(opts.JWT)(http.HandlerFunc(h.me)))
	mux.Handle("GET /api/v1/server", AuthRequired(opts.JWT)(http.HandlerFunc(h.serverInfo)))
	mux.Handle("POST /api/v1/nodes", AuthRequired(opts.JWT)(http.HandlerFunc(h.registerNode)))
	mux.Handle("GET /api/v1/nodes", AuthRequired(opts.JWT)(http.HandlerFunc(h.listNodes)))
	mux.Handle("DELETE /api/v1/nodes/{id}", AuthRequired(opts.JWT)(http.HandlerFunc(h.deleteNode)))

	// Zones (any authenticated user).
	mux.Handle("POST /api/v1/zones", AuthRequired(opts.JWT)(http.HandlerFunc(h.createZone)))
	mux.Handle("GET /api/v1/zones", AuthRequired(opts.JWT)(http.HandlerFunc(h.listZones)))
	mux.Handle("POST /api/v1/zones/{name}/join", AuthRequired(opts.JWT)(http.HandlerFunc(h.joinZone)))
	mux.Handle("POST /api/v1/zones/{name}/leave", AuthRequired(opts.JWT)(http.HandlerFunc(h.leaveZone)))
	mux.Handle("GET /api/v1/zones/{name}/members", AuthRequired(opts.JWT)(http.HandlerFunc(h.zoneMembers)))

	// Zone owner controls (owner only).
	mux.Handle("PATCH /api/v1/zones/{name}", AuthRequired(opts.JWT)(http.HandlerFunc(h.changeZonePassword)))
	mux.Handle("DELETE /api/v1/zones/{name}", AuthRequired(opts.JWT)(http.HandlerFunc(h.deleteZone)))
	mux.Handle("DELETE /api/v1/zones/{name}/members/{node_id}", AuthRequired(opts.JWT)(http.HandlerFunc(h.kickMember)))

	// Admin-only endpoints.
	mux.Handle("POST /api/v1/admin/invites",
		AuthRequired(opts.JWT)(AdminRequired()(http.HandlerFunc(h.createInvite))))
	mux.Handle("GET /api/v1/admin/invites",
		AuthRequired(opts.JWT)(AdminRequired()(http.HandlerFunc(h.listInvites))))
	mux.Handle("DELETE /api/v1/admin/users/{id}",
		AuthRequired(opts.JWT)(AdminRequired()(http.HandlerFunc(h.deleteUser))))

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
