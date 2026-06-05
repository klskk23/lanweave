package protocol

// RegisterNodeRequest is the body of POST /api/v1/nodes.
type RegisterNodeRequest struct {
	Name     string `json:"name"`
	WGPubKey string `json:"wg_pubkey"`
}

// NodeResponse describes a node in API responses (address in dotted form).
type NodeResponse struct {
	ID            int64  `json:"id"`
	Name          string `json:"name"`
	IP            string `json:"ip"`
	CreatedAt     string `json:"created_at,omitempty"`
	Online        bool   `json:"online"`
	LastHandshake string `json:"last_handshake,omitempty"`
}

// NodeListResponse wraps a user's nodes.
type NodeListResponse struct {
	Nodes []NodeResponse `json:"nodes"`
}

// ServerInfoResponse is the body of GET /api/v1/server: everything a client needs
// to configure its side of the tunnel.
type ServerInfoResponse struct {
	PublicKey string `json:"public_key"`
	Endpoint  string `json:"endpoint"`
	Network   string `json:"network"`
	MTU       int    `json:"mtu"`
}
