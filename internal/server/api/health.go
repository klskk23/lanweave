package api

import (
	"encoding/json"
	"net/http"

	"lanweave/pkg/protocol"
)

// healthz handles GET /api/v1/healthz, responding 405 for other methods so the
// shared JSON error envelope is used consistently.
func healthz(version string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			protocol.WriteJSONError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed.")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(protocol.HealthResponse{Status: "ok", Version: version})
	}
}
