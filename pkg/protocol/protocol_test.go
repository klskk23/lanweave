package protocol_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"lanweave/pkg/protocol"
)

func TestWriteJSONError(t *testing.T) {
	rec := httptest.NewRecorder()
	protocol.WriteJSONError(rec, http.StatusTeapot, "im_a_teapot", "short and stout")

	if rec.Code != http.StatusTeapot {
		t.Fatalf("status = %d, want 418", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("content-type = %q", ct)
	}
	var e protocol.ErrorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &e); err != nil {
		t.Fatalf("body not JSON: %v", err)
	}
	if e.Error != "im_a_teapot" || e.Message != "short and stout" {
		t.Errorf("unexpected envelope: %+v", e)
	}
}

func TestHealthResponseJSON(t *testing.T) {
	b, err := json.Marshal(protocol.HealthResponse{Status: "ok", Version: "1.2.3"})
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != `{"status":"ok","version":"1.2.3"}` {
		t.Errorf("unexpected JSON: %s", b)
	}
}
