package protocol

// CreateZoneRequest is the body of POST /api/v1/zones.
type CreateZoneRequest struct {
	Name     string `json:"name"`
	Password string `json:"password"`
}

// ZoneResponse describes a zone the caller participates in.
type ZoneResponse struct {
	ID      int64  `json:"id"`
	Name    string `json:"name"`
	IsOwner bool   `json:"is_owner"`
}

// ZoneListResponse wraps the caller's zones.
type ZoneListResponse struct {
	Zones []ZoneResponse `json:"zones"`
}

// JoinZoneRequest is the body of POST /api/v1/zones/{name}/join.
type JoinZoneRequest struct {
	NodeID   int64  `json:"node_id"`
	Password string `json:"password"`
}

// ChangeZonePasswordRequest is the body of PATCH /api/v1/zones/{name}.
type ChangeZonePasswordRequest struct {
	Password string `json:"password"`
}

// LeaveZoneRequest is the body of POST /api/v1/zones/{name}/leave.
type LeaveZoneRequest struct {
	NodeID int64 `json:"node_id"`
}

// ZoneMemberResponse is one member of a zone (full transparency within the zone).
type ZoneMemberResponse struct {
	NodeID   int64  `json:"node_id"`
	NodeName string `json:"node_name"`
	IP       string `json:"ip"`
	Owner    string `json:"owner"`
}

// ZoneMembersResponse wraps a zone's members.
type ZoneMembersResponse struct {
	Members []ZoneMemberResponse `json:"members"`
}
