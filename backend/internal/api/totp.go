package api

import (
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"strings"
	"time"
)

// RFC 6238 TOTP as used by Google Authenticator: SHA-1, 6 digits, 30-second steps.

var b32 = base32.StdEncoding.WithPadding(base32.NoPadding)

func totpCode(secret []byte, step int64) string {
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], uint64(step))
	m := hmac.New(sha1.New, secret)
	m.Write(msg[:])
	sum := m.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	v := binary.BigEndian.Uint32(sum[off:off+4]) & 0x7fffffff
	return fmt.Sprintf("%06d", v%1000000)
}

// totpCheck returns the matching step (now ±1) or 0. Steps at or before lastStep are refused.
func totpCheck(secret []byte, code string, now time.Time, lastStep int64) int64 {
	code = strings.ReplaceAll(strings.TrimSpace(code), " ", "")
	if len(code) != 6 {
		return 0
	}
	cur := now.Unix() / 30
	for _, st := range []int64{cur - 1, cur, cur + 1} {
		if st > lastStep && hmac.Equal([]byte(totpCode(secret, st)), []byte(code)) {
			return st
		}
	}
	return 0
}
