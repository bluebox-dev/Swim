package manifest

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"strings"
	"testing"
	"time"
)

func newKey(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return pub, priv
}

const samplePayload = `{"schema":1,"kind":"deploy","job_id":"job_1",
 "not_before":"2026-11-23T09:00:00Z","not_after":"2026-11-24T09:00:00Z",
 "host":{"host_id":"h_1","smbios_uuid":"4C4C4544-0042","serial":"8XK2Q73"}}`

func TestRoundTrip(t *testing.T) {
	pub, priv := newKey(t)
	env := Sign(priv, "k1", []byte(samplePayload))
	got, err := Keyset{"k1": pub}.Verify(env)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if string(got) != samplePayload {
		t.Fatal("payload bytes changed")
	}
	h, err := CheckValidity(got, time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC), 1)
	if err != nil {
		t.Fatalf("validity: %v", err)
	}
	if h.JobID != "job_1" || h.Host.Serial != "8XK2Q73" {
		t.Fatalf("header parsed wrong: %+v", h)
	}
}

func TestTamperedPayloadRejected(t *testing.T) {
	pub, priv := newKey(t)
	env := Sign(priv, "k1", []byte(samplePayload))
	env2 := Sign(priv, "k1", []byte(strings.Replace(samplePayload, "job_1", "job_2", 1)))
	env.Payload = env2.Payload // payload of one, signature of the other
	if _, err := (Keyset{"k1": pub}).Verify(env); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("want ErrBadSignature, got %v", err)
	}
}

func TestUnknownKidAndWrongKey(t *testing.T) {
	pub, priv := newKey(t)
	otherPub, _ := newKey(t)
	env := Sign(priv, "k1", []byte(samplePayload))
	if _, err := (Keyset{"k2": pub}).Verify(env); !errors.Is(err, ErrUnknownKey) {
		t.Fatalf("want ErrUnknownKey, got %v", err)
	}
	if _, err := (Keyset{"k1": otherPub}).Verify(env); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("want ErrBadSignature, got %v", err)
	}
}

func TestBadAlg(t *testing.T) {
	pub, priv := newKey(t)
	env := Sign(priv, "k1", []byte(samplePayload))
	env.Alg = "none"
	if _, err := (Keyset{"k1": pub}).Verify(env); !errors.Is(err, ErrBadAlg) {
		t.Fatalf("want ErrBadAlg, got %v", err)
	}
}

func TestValidityWindow(t *testing.T) {
	p := []byte(samplePayload)
	if _, err := CheckValidity(p, time.Date(2026, 11, 22, 0, 0, 0, 0, time.UTC), 1); !errors.Is(err, ErrNotYetValid) {
		t.Fatalf("want ErrNotYetValid, got %v", err)
	}
	if _, err := CheckValidity(p, time.Date(2026, 11, 25, 0, 0, 0, 0, time.UTC), 1); !errors.Is(err, ErrExpired) {
		t.Fatalf("want ErrExpired, got %v", err)
	}
	if _, err := CheckValidity(p, time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC), 2); !errors.Is(err, ErrSchema) {
		t.Fatalf("want ErrSchema, got %v", err)
	}
}
