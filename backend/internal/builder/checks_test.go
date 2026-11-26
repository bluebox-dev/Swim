package builder

import (
	"os"
	"strings"
	"testing"
)

// SWIM_CHECK_ROOT=/path/to/mounted/windows go test -run TestChecksOnMount -v ./backend/internal/builder/
func TestChecksOnMount(t *testing.T) {
	root := os.Getenv("SWIM_CHECK_ROOT")
	if root == "" {
		t.Skip("set SWIM_CHECK_ROOT to a mounted Windows volume")
	}
	for _, r := range runChecks(root) {
		t.Logf("%s passed=%v critical=%v %s — %s", r.ID, r.Passed, r.Critical, r.Name, r.Detail)
	}
}

func TestReadTextUTF16(t *testing.T) {
	f := t.TempDir() + "/s.ini"
	_ = os.WriteFile(f, []byte{0xFF, 0xFE, 'A', 0, '=', 0, 'B', 0, '\r', 0, '\n', 0}, 0o644)
	if got := readText(f); got != "A=B\n" {
		t.Fatalf("got %q", got)
	}
}

func TestCopyProgressThrottle(t *testing.T) {
	in := "1 GiB scanned (10 files, 2 directories)\r1 GiB scanned (11 files, 2 directories)\r2 GiB scanned (20 files, 3 directories)\nArchiving file data: 0 GiB of 21 GiB (0%) done\rArchiving file data: 0 GiB of 21 GiB (1%) done\rArchiving file data: 1 GiB of 21 GiB (5%) done\n"
	var out strings.Builder
	copyProgress(strings.NewReader(in), &out)
	if n := strings.Count(out.String(), "\n"); n != 4 {
		t.Fatalf("want 4 lines, got %d:\n%s", n, out.String())
	}
}
