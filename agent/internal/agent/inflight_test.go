package agent

import "testing"

func TestSafeID(t *testing.T) {
	for id, want := range map[string]bool{"cmd_01m88hr63887st79fwdq63ms4s": true, "": false, "../x": false, `a\b`: false, "x.json": false} {
		if got := safeID(id); got != want {
			t.Errorf("safeID(%q) = %v, want %v", id, got, want)
		}
	}
}
