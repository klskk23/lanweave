package apiclient_test

import (
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"lanweave/internal/client/apiclient"
	"lanweave/pkg/protocol"
)

// certFP is the lowercase-hex SHA-256 of a certificate's DER bytes — the TOFU pin form.
func certFP(c *x509.Certificate) string {
	sum := sha256.Sum256(c.Raw)
	return hex.EncodeToString(sum[:])
}

// testMux returns a handler emulating the server endpoints the client uses, with
// programmable conflict cases.
func testMux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/login", func(w http.ResponseWriter, r *http.Request) {
		var req protocol.LoginRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req.Password != "correct" {
			protocol.WriteJSONError(w, http.StatusUnauthorized, "unauthorized", "bad creds")
			return
		}
		_ = json.NewEncoder(w).Encode(protocol.LoginResponse{Token: "tok-123"})
	})
	mux.HandleFunc("POST /api/v1/register", func(w http.ResponseWriter, r *http.Request) {
		var req protocol.RegisterRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		switch req.InviteCode {
		case "good":
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(protocol.RegisterResponse{Username: req.Username})
		case "taken-user":
			protocol.WriteJSONError(w, http.StatusConflict, "user_exists", "username taken")
		default:
			protocol.WriteJSONError(w, http.StatusBadRequest, "invalid_invite", "bad invite")
		}
	})
	mux.HandleFunc("POST /api/v1/nodes", func(w http.ResponseWriter, r *http.Request) {
		var req protocol.RegisterNodeRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		switch req.Name {
		case "dupname":
			protocol.WriteJSONError(w, http.StatusConflict, "node_name_taken", "name taken")
		case "dupkey":
			protocol.WriteJSONError(w, http.StatusConflict, "pubkey_taken", "key taken")
		case "full":
			protocol.WriteJSONError(w, http.StatusServiceUnavailable, "pool_exhausted", "no addresses")
		case "atlimit":
			protocol.WriteJSONError(w, http.StatusConflict, "device_limit_reached", "device limit")
		default:
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(protocol.NodeResponse{ID: 1, Name: req.Name, IP: "100.127.0.2"})
		}
	})
	mux.HandleFunc("GET /api/v1/nodes", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(protocol.NodeListResponse{Nodes: []protocol.NodeResponse{{ID: 1, Name: "laptop", IP: "100.127.0.2"}}})
	})
	mux.HandleFunc("GET /api/v1/server", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(protocol.ServerInfoResponse{PublicKey: "srv-pub", Endpoint: "vpn:51820", Network: "100.127.0.0/16", MTU: 1420})
	})
	return mux
}

func TestClientHappyAndErrorMapping(t *testing.T) {
	srv := httptest.NewServer(testMux())
	t.Cleanup(srv.Close)
	c := apiclient.New(srv.URL)

	// Login: wrong password → ErrAuthFailed; correct → token set.
	if err := c.Login("alice", "wrong"); !errors.Is(err, apiclient.ErrAuthFailed) {
		t.Errorf("login wrong: got %v, want ErrAuthFailed", err)
	}
	if err := c.Login("alice", "correct"); err != nil || c.Token() != "tok-123" {
		t.Fatalf("login correct: err=%v token=%q", err, c.Token())
	}

	// Register error mapping.
	if err := c.Register("bad", "u", "p"); !errors.Is(err, apiclient.ErrInviteInvalid) {
		t.Errorf("bad invite: got %v, want ErrInviteInvalid", err)
	}
	if err := c.Register("taken-user", "u", "p"); !errors.Is(err, apiclient.ErrUsernameTaken) {
		t.Errorf("taken user: got %v, want ErrUsernameTaken", err)
	}
	if err := c.Register("good", "u", "p"); err != nil {
		t.Errorf("good invite: %v", err)
	}

	// Node registration mapping.
	if _, err := c.RegisterNode("dupname", "pk"); !errors.Is(err, apiclient.ErrNodeNameTaken) {
		t.Errorf("dup name: got %v, want ErrNodeNameTaken", err)
	}
	if _, err := c.RegisterNode("dupkey", "pk"); !errors.Is(err, apiclient.ErrPubKeyTaken) {
		t.Errorf("dup key: got %v, want ErrPubKeyTaken", err)
	}
	if _, err := c.RegisterNode("full", "pk"); !errors.Is(err, apiclient.ErrPoolExhausted) {
		t.Errorf("pool: got %v, want ErrPoolExhausted", err)
	}
	if _, err := c.RegisterNode("atlimit", "pk"); !errors.Is(err, apiclient.ErrDeviceLimitReached) {
		t.Errorf("device limit: got %v, want ErrDeviceLimitReached", err)
	}
	node, err := c.RegisterNode("laptop", "pk")
	if err != nil || node.IP != "100.127.0.2" {
		t.Fatalf("register node: %+v %v", node, err)
	}

	// ListNodes + ServerInfo parse.
	if list, err := c.ListNodes(); err != nil || len(list.Nodes) != 1 || list.Nodes[0].Name != "laptop" {
		t.Fatalf("list nodes: %+v %v", list, err)
	}
	if info, err := c.ServerInfo(); err != nil || info.PublicKey != "srv-pub" || info.Network != "100.127.0.0/16" {
		t.Fatalf("server info: %+v %v", info, err)
	}
}

// TestZoneErrorMapping covers the feature-011 zone/session error mapping.
func TestZoneErrorMapping(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/me", func(w http.ResponseWriter, _ *http.Request) {
		protocol.WriteJSONError(w, http.StatusUnauthorized, "unauthorized", "expired")
	})
	mux.HandleFunc("POST /api/v1/zones", func(w http.ResponseWriter, _ *http.Request) {
		protocol.WriteJSONError(w, http.StatusConflict, "zone_name_taken", "taken")
	})
	mux.HandleFunc("POST /api/v1/zones/{name}/join", func(w http.ResponseWriter, _ *http.Request) {
		protocol.WriteJSONError(w, http.StatusForbidden, "invalid_zone_or_password", "Invalid zone or password.")
	})
	mux.HandleFunc("PATCH /api/v1/zones/{name}", func(w http.ResponseWriter, _ *http.Request) {
		protocol.WriteJSONError(w, http.StatusForbidden, "forbidden", "Only the zone owner...")
	})
	mux.HandleFunc("POST /api/v1/zones/{name}/leave", func(w http.ResponseWriter, _ *http.Request) {
		protocol.WriteJSONError(w, http.StatusNotFound, "not_found", "Not found.")
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	c := apiclient.New(srv.URL)

	if _, err := c.Me(); !errors.Is(err, apiclient.ErrSessionExpired) {
		t.Errorf("me 401: got %v, want ErrSessionExpired", err)
	}
	if _, err := c.CreateZone("dup", 0, "pw"); !errors.Is(err, apiclient.ErrZoneNameTaken) {
		t.Errorf("create dup: got %v, want ErrZoneNameTaken", err)
	}
	if err := c.JoinZone("z", 1, "bad"); !errors.Is(err, apiclient.ErrZoneOrPassword) {
		t.Errorf("join wrong pw: got %v, want ErrZoneOrPassword", err)
	}
	if err := c.ChangeZonePassword("z", "newpassword"); !errors.Is(err, apiclient.ErrNotOwner) {
		t.Errorf("non-owner change: got %v, want ErrNotOwner", err)
	}
	if err := c.LeaveZone("z", 1); !errors.Is(err, apiclient.ErrNotMember) {
		t.Errorf("leave not-member: got %v, want ErrNotMember", err)
	}
}

// TestZoneLimitMapping covers the 023 owned-zone-cap refusal mapping.
func TestZoneLimitMapping(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/zones", func(w http.ResponseWriter, _ *http.Request) {
		protocol.WriteJSONError(w, http.StatusConflict, "zone_limit_reached", "zone limit")
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	if _, err := apiclient.New(srv.URL).CreateZone("z", 0, "zone-strong-pw"); !errors.Is(err, apiclient.ErrOwnedZoneLimitReached) {
		t.Errorf("zone limit: got %v, want ErrOwnedZoneLimitReached", err)
	}
}

// TestCreateZoneSendsNodeID asserts the create request carries node_id so the server can
// auto-join the caller's device in the same operation (feature 015).
func TestCreateZoneSendsNodeID(t *testing.T) {
	var gotBody protocol.CreateZoneRequest
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/zones", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(protocol.ZoneResponse{ID: 1, Name: gotBody.Name, IsOwner: true})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	if _, err := apiclient.New(srv.URL).CreateZone("team", 42, "zone-strong-pw"); err != nil {
		t.Fatalf("create: %v", err)
	}
	if gotBody.NodeID != 42 {
		t.Errorf("request node_id = %d, want 42", gotBody.NodeID)
	}
	if gotBody.Name != "team" {
		t.Errorf("request name = %q, want team", gotBody.Name)
	}
}

func TestUnreachable(t *testing.T) {
	c := apiclient.New("https://127.0.0.1:1") // nothing listening
	if err := c.Login("a", "b"); !errors.Is(err, apiclient.ErrUnreachable) {
		t.Errorf("unreachable: got %v, want ErrUnreachable", err)
	}
}

// TestTLSVerification covers the M1 verify-on path and its untrusted counterpart: a TLS
// server with a self-signed cert is rejected (ErrUntrustedCert) by default, and accepted
// when its certificate is supplied as the trust root — WITHOUT disabling verification.
func TestTLSVerification(t *testing.T) {
	srv := httptest.NewTLSServer(testMux())
	t.Cleanup(srv.Close)

	// Default verify-on, untrusted self-signed cert → ErrUntrustedCert.
	if err := apiclient.New(srv.URL).Login("alice", "correct"); !errors.Is(err, apiclient.ErrUntrustedCert) {
		t.Errorf("untrusted cert: got %v, want ErrUntrustedCert", err)
	}

	// Trust the server's cert explicitly (verify stays ON) → succeeds.
	pool := x509.NewCertPool()
	pool.AddCert(srv.Certificate())
	if err := apiclient.New(srv.URL, apiclient.WithRootCAs(pool)).Login("alice", "correct"); err != nil {
		t.Errorf("trusted-root verify-on login should succeed, got %v", err)
	}

	// The --insecure escape hatch also works.
	if err := apiclient.New(srv.URL, apiclient.WithInsecure()).Login("alice", "correct"); err != nil {
		t.Errorf("insecure login should succeed, got %v", err)
	}
}

// TestTOFUPinning is the US1 acceptance test (FR-001/002/003/005): a real TLS round trip
// against self-signed httptest servers exercises first-trust fingerprint capture, the
// pin-or-CA verifier, and the changed-certificate path — no crypto mocks.
func TestTOFUPinning(t *testing.T) {
	srv := httptest.NewTLSServer(testMux())
	t.Cleanup(srv.Close)
	fp := certFP(srv.Certificate())

	// (a) First trust: a default client fails with a *CertError that Is ErrUntrustedCert and
	// carries the leaf fingerprint, with Changed=false (no pin was configured).
	err := apiclient.New(srv.URL).Login("alice", "correct")
	if !errors.Is(err, apiclient.ErrUntrustedCert) {
		t.Fatalf("first-trust: got %v, want ErrUntrustedCert", err)
	}
	var ce *apiclient.CertError
	if !errors.As(err, &ce) {
		t.Fatalf("first-trust error should be *CertError, got %T", err)
	}
	if ce.Fingerprint != fp {
		t.Errorf("captured fingerprint = %q, want %q", ce.Fingerprint, fp)
	}
	if ce.Changed {
		t.Error("first-trust CertError.Changed should be false")
	}

	// (b) Pinned to that fingerprint → verification passes silently.
	if err := apiclient.New(srv.URL, apiclient.WithPinnedCert(fp)).Login("alice", "correct"); err != nil {
		t.Errorf("pinned login should succeed, got %v", err)
	}

	// (c) A CA-valid certificate passes regardless of a bogus pin (pin OR system/roots).
	pool := x509.NewCertPool()
	pool.AddCert(srv.Certificate())
	if err := apiclient.New(srv.URL, apiclient.WithRootCAs(pool), apiclient.WithPinnedCert("deadbeef")).Login("alice", "correct"); err != nil {
		t.Errorf("CA-valid with bogus pin should succeed, got %v", err)
	}

	// (d) Changed certificate: a client pinned to some OTHER fingerprint connects to a server
	// whose presented (still-untrusted) certificate matches neither the pin nor a system root
	// → ErrCertChanged carrying the actually-presented fingerprint, with Changed=true.
	err = apiclient.New(srv.URL, apiclient.WithPinnedCert("00deadbeef00")).Login("alice", "correct")
	if !errors.Is(err, apiclient.ErrCertChanged) {
		t.Fatalf("changed cert: got %v, want ErrCertChanged", err)
	}
	var ce2 *apiclient.CertError
	if !errors.As(err, &ce2) || ce2.Fingerprint != fp || !ce2.Changed {
		t.Errorf("changed CertError = %+v, want Fingerprint=%s Changed=true", ce2, fp)
	}
}

// TestInsecureGetter covers the getter that drives the persistent "certificate not verified"
// indicator (FR-013/014).
func TestInsecureGetter(t *testing.T) {
	if apiclient.New("https://x.example").Insecure() {
		t.Error("default client should report Insecure()=false")
	}
	if !apiclient.New("https://x.example", apiclient.WithInsecure()).Insecure() {
		t.Error("WithInsecure client should report Insecure()=true")
	}
}

// TestLazyRefreshOn401 covers US1: an authenticated call that gets 401 triggers exactly
// one silent POST /refresh, then the original request is retried once and succeeds.
func TestLazyRefreshOn401(t *testing.T) {
	var refreshCalls, meCalls int
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/me", func(w http.ResponseWriter, r *http.Request) {
		meCalls++
		if r.Header.Get("Authorization") != "Bearer new-tok" {
			protocol.WriteJSONError(w, http.StatusUnauthorized, "unauthorized", "expired")
			return
		}
		_ = json.NewEncoder(w).Encode(protocol.MeResponse{UserID: 1, Username: "alice"})
	})
	mux.HandleFunc("POST /api/v1/refresh", func(w http.ResponseWriter, r *http.Request) {
		refreshCalls++
		var req protocol.RefreshRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req.RefreshToken != "good-rt" {
			protocol.WriteJSONError(w, http.StatusUnauthorized, "unauthorized", "bad rt")
			return
		}
		_ = json.NewEncoder(w).Encode(protocol.RefreshResponse{Token: "new-tok"})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	c := apiclient.New(srv.URL)
	c.SetToken("stale-tok")
	c.SetRefreshToken("good-rt")

	me, err := c.Me()
	if err != nil {
		t.Fatalf("Me with silent refresh: %v", err)
	}
	if me.Username != "alice" {
		t.Errorf("me = %+v, want alice", me)
	}
	if refreshCalls != 1 {
		t.Errorf("refresh called %d times, want exactly 1", refreshCalls)
	}
	if meCalls != 2 {
		t.Errorf("me called %d times, want 2 (initial 401 + retry)", meCalls)
	}
	if c.Token() != "new-tok" {
		t.Errorf("client token = %q, want new-tok (rewritten after refresh)", c.Token())
	}
}

// TestLazyRefreshFailsSurfacesExpired covers US1: when /refresh itself fails, the call
// surfaces ErrSessionExpired and does not loop.
func TestLazyRefreshFailsSurfacesExpired(t *testing.T) {
	var refreshCalls int
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/me", func(w http.ResponseWriter, _ *http.Request) {
		protocol.WriteJSONError(w, http.StatusUnauthorized, "unauthorized", "expired")
	})
	mux.HandleFunc("POST /api/v1/refresh", func(w http.ResponseWriter, _ *http.Request) {
		refreshCalls++
		protocol.WriteJSONError(w, http.StatusUnauthorized, "unauthorized", "rt expired")
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	c := apiclient.New(srv.URL)
	c.SetToken("stale-tok")
	c.SetRefreshToken("bad-rt")

	if _, err := c.Me(); !errors.Is(err, apiclient.ErrSessionExpired) {
		t.Errorf("refresh-fails path: got %v, want ErrSessionExpired", err)
	}
	if refreshCalls != 1 {
		t.Errorf("refresh called %d times, want exactly 1 (no loop)", refreshCalls)
	}
}

// TestLogoutPostsRefreshToken covers US2: Logout POSTs the held refresh token to /logout.
func TestLogoutPostsRefreshToken(t *testing.T) {
	var gotRT string
	var gotMethod, gotPath string
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/logout", func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		var req protocol.LogoutRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		gotRT = req.RefreshToken
		w.WriteHeader(http.StatusNoContent)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	c := apiclient.New(srv.URL)
	c.SetRefreshToken("rt-to-revoke")
	if err := c.Logout(); err != nil {
		t.Fatalf("logout: %v", err)
	}
	if gotMethod != http.MethodPost || gotPath != "/api/v1/logout" {
		t.Errorf("request = %s %s, want POST /api/v1/logout", gotMethod, gotPath)
	}
	if gotRT != "rt-to-revoke" {
		t.Errorf("posted refresh token = %q, want rt-to-revoke", gotRT)
	}
}

// TestDeleteNode covers logout's server call: a 204 returns nil and the wire request is a
// DELETE to /api/v1/nodes/{id} with the bearer token; a non-2xx maps to a non-nil error.
func TestDeleteNode(t *testing.T) {
	var gotMethod, gotPath, gotAuth string
	mux := http.NewServeMux()
	mux.HandleFunc("DELETE /api/v1/nodes/{id}", func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath, gotAuth = r.Method, r.URL.Path, r.Header.Get("Authorization")
		w.WriteHeader(http.StatusNoContent)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	c := apiclient.New(srv.URL)
	c.SetToken("tok-xyz")
	if err := c.DeleteNode(42); err != nil {
		t.Fatalf("delete node 204: %v", err)
	}
	if gotMethod != http.MethodDelete || gotPath != "/api/v1/nodes/42" {
		t.Errorf("request = %s %s, want DELETE /api/v1/nodes/42", gotMethod, gotPath)
	}
	if gotAuth != "Bearer tok-xyz" {
		t.Errorf("auth header = %q, want Bearer tok-xyz", gotAuth)
	}

	// A 5xx → a non-nil error.
	mux2 := http.NewServeMux()
	mux2.HandleFunc("DELETE /api/v1/nodes/{id}", func(w http.ResponseWriter, _ *http.Request) {
		protocol.WriteJSONError(w, http.StatusInternalServerError, "server_error", "boom")
	})
	srv2 := httptest.NewServer(mux2)
	t.Cleanup(srv2.Close)
	if err := apiclient.New(srv2.URL).DeleteNode(7); err == nil {
		t.Error("delete node on 500 should return an error")
	}
}

// TestAnnouncementErrorMapping covers the 030 announcement codes → typed errors
// and the happy round trips of the three announcement methods (feature 032).
func TestAnnouncementErrorMapping(t *testing.T) {
	for _, tc := range []struct {
		code    string
		status  int
		wantErr error
	}{
		{"platform_unsupported", http.StatusConflict, apiclient.ErrPlatformUnsupported},
		{"announce_disabled", http.StatusServiceUnavailable, apiclient.ErrAnnounceDisabled},
		{"subnet_invalid", http.StatusBadRequest, apiclient.ErrSubnetInvalid},
		{"subnet_overlap", http.StatusConflict, apiclient.ErrSubnetOverlap},
		{"announce_limit_reached", http.StatusConflict, apiclient.ErrAnnounceLimit},
		{"synthetic_pool_exhausted", http.StatusServiceUnavailable, apiclient.ErrSyntheticPoolExhausted},
	} {
		t.Run(tc.code, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				_ = json.NewEncoder(w).Encode(protocol.ErrorResponse{Error: tc.code, Message: "x"})
			}))
			defer srv.Close()
			c := apiclient.New(srv.URL)
			c.SetToken("tok")
			if _, err := c.CreateAnnouncement("home", 1, "192.168.1.0/24"); !errors.Is(err, tc.wantErr) {
				t.Errorf("err = %v, want %v", err, tc.wantErr)
			}
		})
	}

	// Happy paths: create echoes the mapping; list returns entries; delete 204.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost:
			var req protocol.CreateAnnouncementRequest
			_ = json.NewDecoder(r.Body).Decode(&req)
			if req.NodeID != 7 || req.Subnet != "192.168.1.0/24" {
				t.Errorf("create body = %+v", req)
			}
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(protocol.AnnouncementResponse{ID: 3, NodeID: 7, Subnet: req.Subnet, Synthetic: "100.100.1.0/24"})
		case r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode(protocol.AnnouncementListResponse{
				Announcements: []protocol.AnnouncementResponse{{ID: 3, NodeID: 7, Subnet: "192.168.1.0/24", Synthetic: "100.100.1.0/24"}},
			})
		case r.Method == http.MethodDelete:
			if r.URL.Path != "/api/v1/zones/home/announcements/3" {
				t.Errorf("delete path = %s", r.URL.Path)
			}
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	defer srv.Close()
	c := apiclient.New(srv.URL)
	c.SetToken("tok")
	ann, err := c.CreateAnnouncement("home", 7, "192.168.1.0/24")
	if err != nil || ann.Synthetic != "100.100.1.0/24" {
		t.Fatalf("create = %+v (%v)", ann, err)
	}
	list, err := c.ListAnnouncements("home")
	if err != nil || len(list.Announcements) != 1 {
		t.Fatalf("list = %+v (%v)", list, err)
	}
	if err := c.DeleteAnnouncement("home", 3); err != nil {
		t.Fatalf("delete: %v", err)
	}
}
