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
