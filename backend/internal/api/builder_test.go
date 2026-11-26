package api

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"testing"
)

// TestGuacSignRoundTrip decrypts the way guacamole-auth-json does and checks the HMAC.
func TestGuacSignRoundTrip(t *testing.T) {
	secret := "4c0b569e4c96df157eee1b65dd0e4d41"
	payload := []byte(`{"username":"admin1","expires":1,"connections":{"gm":{"protocol":"vnc","parameters":{"hostname":"127.0.0.1","port":"5900"}}}}`)
	data, err := guacSign(secret, payload)
	if err != nil {
		t.Fatal(err)
	}
	enc, _ := base64.StdEncoding.DecodeString(data)
	key, _ := hex.DecodeString(secret)
	block, _ := aes.NewCipher(key)
	plain := make([]byte, len(enc))
	cipher.NewCBCDecrypter(block, make([]byte, 16)).CryptBlocks(plain, enc)
	plain = plain[:len(plain)-int(plain[len(plain)-1])] // strip PKCS#5
	sig, body := plain[:32], plain[32:]
	mac := hmac.New(sha256.New, key)
	mac.Write(body)
	if !hmac.Equal(sig, mac.Sum(nil)) || string(body) != string(payload) {
		t.Fatal("signature or payload mismatch")
	}
	if _, err := guacSign("short", payload); err == nil {
		t.Fatal("want error for bad secret")
	}
}

func TestVersionLess(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{{"0.3.0", "0.4.0", true}, {"0.4.0", "0.4.0", false}, {"0.10.0", "0.4.0", false}, {"", "0.4.0", true}, {"1.0", "0.9.9", false}} {
		if got := versionLess(c.a, c.b); got != c.want {
			t.Errorf("versionLess(%q,%q)=%v want %v", c.a, c.b, got, c.want)
		}
	}
}
