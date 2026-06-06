package apiclient_test

import (
	"crypto/x509"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"lanweave/internal/client/apiclient"
	"lanweave/pkg/protocol"
)

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
