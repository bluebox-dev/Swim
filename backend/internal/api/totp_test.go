package api

import (
	"testing"
	"time"
)

// RFC 6238 appendix B, SHA-1 secret "12345678901234567890" (8-digit values, last 6 digits)
func TestTOTPVectors(t *testing.T) {
	sec := []byte("12345678901234567890")
	for _, c := range []struct {
		t    int64
		want string
	}{{59, "287082"}, {1111111109, "081804"}, {1234567890, "005924"}, {2000000000, "279037"}} {
		if got := totpCode(sec, c.t/30); got != c.want {
			t.Fatalf("t=%d: got %s want %s", c.t, got, c.want)
		}
	}
	now := time.Unix(1234567890, 0)
	if st := totpCheck(sec, "005924", now, 0); st == 0 {
		t.Fatal("valid code rejected")
	}
	if st := totpCheck(sec, "005924", now, now.Unix()/30); st != 0 {
		t.Fatal("reused step accepted")
	}
	if totpCheck(sec, "000000", now, 0) != 0 {
		t.Fatal("wrong code accepted")
	}
}

func TestPasswordHash(t *testing.T) {
	h := hashPassword("correct horse battery")
	if !checkPassword(h, "correct horse battery") || checkPassword(h, "wrong") {
		t.Fatal("argon2id check")
	}
}
