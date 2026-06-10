package protocol

// CreateAnnouncementRequest is the body of POST /api/v1/zones/{name}/announcements.
// Subnet is the real LAN CIDR behind the node (text form, e.g. "192.168.1.0/24").
type CreateAnnouncementRequest struct {
	NodeID int64  `json:"node_id"`
	Subnet string `json:"subnet"`
}

// AnnouncementResponse describes one announced subnet and its synthetic mapping.
// Members dial addresses inside Synthetic; the real Subnet never enters any tunnel.
type AnnouncementResponse struct {
	ID        int64  `json:"id"`
	NodeID    int64  `json:"node_id"`
	NodeName  string `json:"node_name"`
	Owner     string `json:"owner"`
	Subnet    string `json:"subnet"`
	Synthetic string `json:"synthetic"`
}

// AnnouncementListResponse wraps a zone's announcements.
type AnnouncementListResponse struct {
	Announcements []AnnouncementResponse `json:"announcements"`
}
