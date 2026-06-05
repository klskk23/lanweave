package protocol

import (
	"encoding/json"
	"net/http"
)

// ErrorResponse is the single JSON error envelope used by every endpoint.
type ErrorResponse struct {
	Error   string `json:"error"`
	Message string `json:"message"`
}

// WriteJSONError writes an ErrorResponse with the given HTTP status. The message
// must be human-safe: no secrets, stack traces, or internal IDs.
func WriteJSONError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(ErrorResponse{Error: code, Message: message})
}
