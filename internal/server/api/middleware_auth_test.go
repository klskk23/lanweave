package api_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"lanweave/internal/server/api"
	"lanweave/internal/server/auth"
)

func okHandler(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }

func TestAuthRequired(t *testing.T) {
	jwt := auth.NewJWTManager(harnessJWTSecret, time.Hour)
	protected := api.AuthRequired(jwt)(http.HandlerFunc(okHandler))

	t.Run("missing token", func(t *testing.T) {
		rec := httptest.NewRecorder()
		protected.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/x", nil))
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status %d, want 401", rec.Code)
		}
	})

	t.Run("malformed token", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/x", nil)
		req.Header.Set("Authorization", "Bearer not.a.jwt")
		rec := httptest.NewRecorder()
		protected.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status %d, want 401", rec.Code)
		}
	})

	t.Run("expired token", func(t *testing.T) {
		expired := auth.NewJWTManager(harnessJWTSecret, -time.Minute)
		tok, _ := expired.Issue(auth.Claims{UserID: 1, Username: "x"})
		req := httptest.NewRequest(http.MethodGet, "/x", nil)
		req.Header.Set("Authorization", "Bearer "+tok)
		rec := httptest.NewRecorder()
		protected.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status %d, want 401", rec.Code)
		}
	})

	t.Run("valid token populates identity", func(t *testing.T) {
		var gotUser string
		var gotAdmin bool
		h := api.AuthRequired(jwt)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id, ok := api.IdentityFrom(r.Context())
			if !ok {
				t.Error("identity missing from context")
			} else {
				gotUser, gotAdmin = id.Username, id.IsAdmin
			}
			w.WriteHeader(http.StatusOK)
		}))
		tok, _ := jwt.Issue(auth.Claims{UserID: 9, Username: "alice", IsAdmin: true})
		req := httptest.NewRequest(http.MethodGet, "/x", nil)
		req.Header.Set("Authorization", "Bearer "+tok)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK || gotUser != "alice" || !gotAdmin {
			t.Fatalf("identity not propagated: code=%d user=%q admin=%v", rec.Code, gotUser, gotAdmin)
		}
	})
}

func TestAdminRequired(t *testing.T) {
	jwt := auth.NewJWTManager(harnessJWTSecret, time.Hour)
	chain := func(claims auth.Claims) *httptest.ResponseRecorder {
		h := api.AuthRequired(jwt)(api.AdminRequired()(http.HandlerFunc(okHandler)))
		tok, _ := jwt.Issue(claims)
		req := httptest.NewRequest(http.MethodGet, "/x", nil)
		req.Header.Set("Authorization", "Bearer "+tok)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}

	if rec := chain(auth.Claims{UserID: 1, Username: "bob", IsAdmin: false}); rec.Code != http.StatusForbidden {
		t.Errorf("non-admin: status %d, want 403", rec.Code)
	}
	if rec := chain(auth.Claims{UserID: 1, Username: "admin", IsAdmin: true}); rec.Code != http.StatusOK {
		t.Errorf("admin: status %d, want 200", rec.Code)
	}
}
