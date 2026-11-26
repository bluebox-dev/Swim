package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPinnedClient(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			w.WriteHeader(401)
			_, _ = w.Write([]byte(`{"error":{"code":"SWIM-API-401","message":"nope"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"halt":true,"halt_reason":"global: test"}`))
	}))
	defer srv.Close()
	sum := sha256.Sum256(srv.Certificate().Raw)
	pin := hex.EncodeToString(sum[:])

	var out struct {
		Halt bool `json:"halt"`
	}
	if err := NewClient(srv.URL, pin).PostJSON("/x", "tok", map[string]any{}, &out); err != nil || !out.Halt {
		t.Fatalf("pinned request failed: %v %+v", err, out)
	}
	err := NewClient(srv.URL, pin).PostJSON("/x", "bad", map[string]any{}, nil)
	if ae, ok := err.(*APIError); !ok || ae.Status != 401 || ae.Code != "SWIM-API-401" {
		t.Fatalf("want APIError 401, got %v", err)
	}
	wrong := strings.Repeat("0", 64)
	if err := NewClient(srv.URL, wrong).PostJSON("/x", "tok", map[string]any{}, nil); err == nil || !strings.Contains(err.Error(), "pin") {
		t.Fatalf("want pin mismatch error, got %v", err)
	}
	// No pin → system trust → the test server's self-signed cert must be rejected.
	if err := NewClient(srv.URL, "").PostJSON("/x", "tok", map[string]any{}, nil); err == nil {
		t.Fatal("want TLS verification error without pin")
	}
}
