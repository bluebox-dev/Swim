// Package manifest implements the signed job-manifest envelope from spec 06 §1.
//
// The signature is detached and covers the payload bytes exactly as received,
// so verifiers never canonicalize JSON: verify first, parse second.
package manifest

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

const AlgEd25519 = "Ed25519"

var (
	ErrMalformed    = errors.New("manifest: malformed envelope")
	ErrUnknownKey   = errors.New("manifest: unknown kid")
	ErrBadAlg       = errors.New("manifest: unsupported alg")
	ErrBadSignature = errors.New("manifest: signature does not verify")
	ErrNotYetValid  = errors.New("manifest: not yet valid")
	ErrExpired      = errors.New("manifest: expired")
	ErrSchema       = errors.New("manifest: unsupported schema")
)

// Envelope is the wire format: {kid, alg, payload(base64url), sig(base64url)}.
type Envelope struct {
	Kid     string `json:"kid"`
	Alg     string `json:"alg"`
	Payload string `json:"payload"`
	Sig     string `json:"sig"`
}

// Keyset maps kid → trusted public key.
type Keyset map[string]ed25519.PublicKey

var b64 = base64.RawURLEncoding

// Sign wraps payload bytes in a signed envelope.
func Sign(priv ed25519.PrivateKey, kid string, payload []byte) Envelope {
	return Envelope{
		Kid:     kid,
		Alg:     AlgEd25519,
		Payload: b64.EncodeToString(payload),
		Sig:     b64.EncodeToString(ed25519.Sign(priv, payload)),
	}
}

// Verify checks the envelope against the keyset and returns the payload bytes.
// It does not parse the payload; call CheckValidity on the result.
func (ks Keyset) Verify(env Envelope) ([]byte, error) {
	if env.Kid == "" || env.Payload == "" || env.Sig == "" {
		return nil, ErrMalformed
	}
	if env.Alg != AlgEd25519 {
		return nil, ErrBadAlg
	}
	pub, ok := ks[env.Kid]
	if !ok {
		return nil, ErrUnknownKey
	}
	payload, err := b64.DecodeString(env.Payload)
	if err != nil {
		return nil, fmt.Errorf("%w: payload: %v", ErrMalformed, err)
	}
	sig, err := b64.DecodeString(env.Sig)
	if err != nil || len(sig) != ed25519.SignatureSize {
		return nil, ErrMalformed
	}
	if !ed25519.Verify(pub, payload, sig) {
		return nil, ErrBadSignature
	}
	return payload, nil
}

// Header holds the fields every verifier checks before trusting a payload.
type Header struct {
	Schema    int       `json:"schema"`
	Kind      string    `json:"kind"`
	JobID     string    `json:"job_id"`
	NotBefore time.Time `json:"not_before"`
	NotAfter  time.Time `json:"not_after"`
	Host      struct {
		HostID     string `json:"host_id"`
		SmbiosUUID string `json:"smbios_uuid"`
		Serial     string `json:"serial"`
	} `json:"host"`
}

// CheckValidity parses the header of verified payload bytes and enforces the
// schema list and the not_before ≤ now ≤ not_after window (spec 06 §1 steps 3–4).
func CheckValidity(payload []byte, now time.Time, schemas ...int) (Header, error) {
	var h Header
	if err := json.Unmarshal(payload, &h); err != nil {
		return h, fmt.Errorf("%w: %v", ErrMalformed, err)
	}
	supported := false
	for _, s := range schemas {
		if h.Schema == s {
			supported = true
			break
		}
	}
	if !supported {
		return h, ErrSchema
	}
	if h.NotAfter.IsZero() {
		return h, fmt.Errorf("%w: not_after missing", ErrMalformed)
	}
	if !h.NotBefore.IsZero() && now.Before(h.NotBefore) {
		return h, ErrNotYetValid
	}
	if now.After(h.NotAfter) {
		return h, ErrExpired
	}
	return h, nil
}
