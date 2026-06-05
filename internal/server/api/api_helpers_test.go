package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/time/rate"

	"lanweave/internal/server/api"
	"lanweave/internal/server/auth"
	"lanweave/internal/server/store"
	"lanweave/pkg/protocol"
)

const harnessJWTSecret = "0123456789abcdef0123456789abcdef"

type harness struct {
	t       *testing.T
	router  http.Handler
	store   *store.Store
	jwt     *auth.JWTManager
	logBuf  *bytes.Buffer
	adminPW string
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "db.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(nil); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	adminPW := "admin-password-123"
	hash, err := auth.HashPassword(adminPW)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if _, err := st.Users().CreateAdmin(context.Background(), "admin", hash); err != nil {
		t.Fatalf("seed admin: %v", err)
	}

	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	jwtMgr := auth.NewJWTManager(harnessJWTSecret, time.Hour)
	router := api.NewRouter(api.Options{
		Version: "test",
		Limiter: rate.NewLimiter(rate.Limit(10000), 10000),
		Logger:  logger,
		Store:   st,
		JWT:     jwtMgr,
	})
	return &harness{t: t, router: router, store: st, jwt: jwtMgr, logBuf: &buf, adminPW: adminPW}
}

func (h *harness) do(method, path, token string, body any) *httptest.ResponseRecorder {
	h.t.Helper()
	var req *http.Request
	if body != nil {
		b, _ := json.Marshal(body)
		req = httptest.NewRequest(method, path, bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
	} else {
		req = httptest.NewRequest(method, path, nil)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	h.router.ServeHTTP(rec, req)
	return rec
}

func (h *harness) loginToken(username, password string) string {
	h.t.Helper()
	rec := h.do(http.MethodPost, "/api/v1/login", "", protocol.LoginRequest{Username: username, Password: password})
	if rec.Code != http.StatusOK {
		h.t.Fatalf("login(%s) status %d: %s", username, rec.Code, rec.Body.String())
	}
	var resp protocol.LoginResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		h.t.Fatalf("login decode: %v", err)
	}
	return resp.Token
}

func (h *harness) createInviteCode(adminToken string) string {
	h.t.Helper()
	rec := h.do(http.MethodPost, "/api/v1/admin/invites", adminToken, nil)
	if rec.Code != http.StatusCreated {
		h.t.Fatalf("create invite status %d: %s", rec.Code, rec.Body.String())
	}
	var resp protocol.CreateInviteResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		h.t.Fatalf("invite decode: %v", err)
	}
	return resp.Code
}

func decodeError(t *testing.T, rec *httptest.ResponseRecorder) protocol.ErrorResponse {
	t.Helper()
	var e protocol.ErrorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &e); err != nil {
		t.Fatalf("decode error envelope: %v (%s)", err, rec.Body.String())
	}
	return e
}

func decodeJSONBody(t *testing.T, body []byte, dst any) {
	t.Helper()
	if err := json.Unmarshal(body, dst); err != nil {
		t.Fatalf("decode JSON body: %v (%s)", err, string(body))
	}
}
