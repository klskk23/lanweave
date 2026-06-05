// Package protocol holds DTOs shared between the lanweave server and clients.
package protocol

// HealthResponse is the body of GET /api/v1/healthz.
type HealthResponse struct {
	Status  string `json:"status"`
	Version string `json:"version"`
}
