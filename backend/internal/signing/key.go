// Package signing holds the online manifest-signing key (spec 08 §3).
// The release key for agent builds is NOT here: it stays off the server.
package signing

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
)

type Key struct {
	Kid  string
	Priv ed25519.PrivateKey
	Pub  ed25519.PublicKey
}

// LoadOrCreate reads a PKCS#8 PEM key, creating one (mode 0600) if absent.
func LoadOrCreate(path string) (*Key, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		_, priv, gerr := ed25519.GenerateKey(rand.Reader)
		if gerr != nil {
			return nil, gerr
		}
		der, merr := x509.MarshalPKCS8PrivateKey(priv)
		if merr != nil {
			return nil, merr
		}
		b = pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
		if werr := os.WriteFile(path, b, 0o600); werr != nil {
			return nil, werr
		}
	} else if err != nil {
		return nil, err
	}
	blk, _ := pem.Decode(b)
	if blk == nil {
		return nil, fmt.Errorf("%s: not PEM", path)
	}
	k, err := x509.ParsePKCS8PrivateKey(blk.Bytes)
	if err != nil {
		return nil, err
	}
	priv, ok := k.(ed25519.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("%s: not an Ed25519 key", path)
	}
	pub := priv.Public().(ed25519.PublicKey)
	sum := sha256.Sum256(pub)
	return &Key{Kid: "manifest-" + hex.EncodeToString(sum[:4]), Priv: priv, Pub: pub}, nil
}
