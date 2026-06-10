package api

import (
	"net/http"

	"lanweave/internal/server/api/docs"
)

// route pairs a ServeMux pattern with its fully wrapped handler. The slice
// returned by routes is the single source of truth for the control-plane API
// surface: NewRouter registers from it and the OpenAPI consistency test
// compares the embedded document against it, so an endpoint added or removed
// here without a matching openapi.yaml edit fails CI.
type route struct {
	pattern string
	handler http.Handler
}

// routes returns every business endpoint. Patterns are exact net/http ServeMux
// strings ("METHOD /path"). healthz keeps its historical method-agnostic
// registration: the handler itself answers 405 to non-GET, and the consistency
// test normalizes the bare pattern to GET.
func routes(h *handlers, opts Options) []route {
	authed := AuthRequired(opts.JWT)
	admin := AdminRequired()
	return []route{
		{"/api/v1/healthz", http.HandlerFunc(healthz(opts.Version))},

		// Public (rate-limited) endpoints. Refresh and logout authenticate via
		// the refresh token in the body because the access JWT is expired at
		// call time (NO AuthRequired).
		{"POST /api/v1/login", http.HandlerFunc(h.login)},
		{"POST /api/v1/register", http.HandlerFunc(h.register)},
		{"POST /api/v1/refresh", http.HandlerFunc(h.refresh)},
		{"POST /api/v1/logout", http.HandlerFunc(h.logout)},

		// Authenticated endpoints.
		{"GET /api/v1/me", authed(http.HandlerFunc(h.me))},
		{"GET /api/v1/server", authed(http.HandlerFunc(h.serverInfo))},
		{"POST /api/v1/nodes", authed(http.HandlerFunc(h.registerNode))},
		{"GET /api/v1/nodes", authed(http.HandlerFunc(h.listNodes))},
		{"DELETE /api/v1/nodes/{id}", authed(http.HandlerFunc(h.deleteNode))},

		// Zones (any authenticated user).
		{"POST /api/v1/zones", authed(http.HandlerFunc(h.createZone))},
		{"GET /api/v1/zones", authed(http.HandlerFunc(h.listZones))},
		{"POST /api/v1/zones/{name}/join", authed(http.HandlerFunc(h.joinZone))},
		{"POST /api/v1/zones/{name}/leave", authed(http.HandlerFunc(h.leaveZone))},
		{"GET /api/v1/zones/{name}/members", authed(http.HandlerFunc(h.zoneMembers))},

		// Subnet announcements (any member; detach additionally allowed for the
		// zone owner — authorization inside the handlers).
		{"POST /api/v1/zones/{name}/announcements", authed(http.HandlerFunc(h.createAnnouncement))},
		{"GET /api/v1/zones/{name}/announcements", authed(http.HandlerFunc(h.listAnnouncements))},
		{"DELETE /api/v1/zones/{name}/announcements/{id}", authed(http.HandlerFunc(h.deleteAnnouncement))},

		// Zone owner controls (owner only).
		{"PATCH /api/v1/zones/{name}", authed(http.HandlerFunc(h.changeZonePassword))},
		{"DELETE /api/v1/zones/{name}", authed(http.HandlerFunc(h.deleteZone))},
		{"DELETE /api/v1/zones/{name}/members/{node_id}", authed(http.HandlerFunc(h.kickMember))},

		// Admin-only endpoints.
		{"POST /api/v1/admin/invites", authed(admin(http.HandlerFunc(h.createInvite)))},
		{"GET /api/v1/admin/invites", authed(admin(http.HandlerFunc(h.listInvites)))},
		{"DELETE /api/v1/admin/users/{id}", authed(admin(http.HandlerFunc(h.deleteUser)))},
	}
}

// docsRoutes returns the documentation surface, registered only when
// Options.APIDocs is true. It is deliberately a separate table: these routes
// describe the API rather than being part of it, so the OpenAPI consistency
// test compares routes() only.
func docsRoutes() []route {
	serve := func(w http.ResponseWriter, r *http.Request) {
		body, contentType, ok := docs.File(r.PathValue("file"))
		if !ok {
			notFound(w, r)
			return
		}
		w.Header().Set("Content-Type", contentType)
		_, _ = w.Write(body)
	}
	return []route{
		{"GET /api/docs", http.RedirectHandler("/api/docs/", http.StatusMovedPermanently)},
		{"GET /api/docs/{file...}", http.HandlerFunc(serve)},
	}
}
