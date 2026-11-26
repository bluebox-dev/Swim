//go:build !windows

package agent

import (
	"io"
	"log"
	"os"
	"path/filepath"
	"testing"
)

func TestCloneGuardDiscardsForeignState(t *testing.T) {
	t.Setenv("SWIM_AGENT_DIR", t.TempDir())
	lg := log.New(io.Discard, "", 0)
	tok, _ := seal([]byte("swim_ht_x"))
	st := &State{Server: "https://s", HostID: "h_1", SmbiosUUID: "aaaa", SealedToken: tok}
	if err := st.Save(); err != nil {
		t.Fatal(err)
	}
	if got := usableState(lg, "aaaa"); got == nil || got.HostID != "h_1" {
		t.Fatal("own state should be kept")
	}
	if got := usableState(lg, "bbbb"); got != nil {
		t.Fatal("state of another machine must be discarded")
	}
	if _, err := os.Stat(StatePath()); !os.IsNotExist(err) {
		t.Fatal("state.json should have been moved aside")
	}
	m, _ := filepath.Glob(filepath.Join(DataDir(), "state.json.discarded-*"))
	if len(m) != 1 {
		t.Fatalf("want 1 discarded file, got %v", m)
	}
}

func TestBootstrapFileLifecycle(t *testing.T) {
	t.Setenv("SWIM_AGENT_DIR", t.TempDir())
	if FindBootstrap() != nil {
		t.Fatal("no bootstrap expected")
	}
	if err := WriteBootstrapFile(Bootstrap{Server: "https://s", Pin: "ab", Ticket: "swim_rt_1"}); err != nil {
		t.Fatal(err)
	}
	b := FindBootstrap()
	if b == nil || b.Ticket != "swim_rt_1" || b.Source != "file" {
		t.Fatalf("bootstrap not found: %+v", b)
	}
	if err := ClearBootstrap(b); err != nil || FindBootstrap() != nil {
		t.Fatal("bootstrap should be gone after clear")
	}
}
