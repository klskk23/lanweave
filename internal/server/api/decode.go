package api

import (
	"encoding/json"
	"errors"
	"net/http"
)

const maxBodyBytes = 16 << 10 // 16 KiB

// decodeJSON reads a single JSON value from the request body, capping its size
// and rejecting unknown fields. It bounds memory at the boundary.
func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return err
	}
	if dec.More() {
		return errors.New("unexpected trailing data in request body")
	}
	return nil
}

// writeJSON writes v as a JSON response with the given status code.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
