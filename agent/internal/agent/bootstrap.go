package agent

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

// Bootstrap is one-shot enrollment material. Two producers:
//   - the MSI writes it to HKLM\SOFTWARE\SWIM\Bootstrap (Server, Pin, Token)
//   - swim-pe writes <DataDir>/bootstrap.json into the freshly applied Windows
//     during a reimage, with a reimage Ticket instead of a token, so the new OS
//     comes back as the same host_id (spec 05 post-deploy, D-18)
//
// After a successful enroll the secret part is deleted.
type Bootstrap struct {
	Server string `json:"server"`
	Pin    string `json:"cert_sha256"`
	Token  string `json:"token,omitempty"`
	Ticket string `json:"reimage_ticket,omitempty"`
	Source string `json:"-"` // "file" | "registry"
}

func (b *Bootstrap) valid() bool {
	return b != nil && b.Server != "" && (b.Token != "" || b.Ticket != "")
}

func bootstrapFile() string { return filepath.Join(DataDir(), "bootstrap.json") }

// FindBootstrap prefers the file (reimage ticket) over the registry (MSI token).
func FindBootstrap() *Bootstrap {
	if b, err := os.ReadFile(bootstrapFile()); err == nil {
		var bs Bootstrap
		if json.Unmarshal(b, &bs) == nil && bs.valid() {
			bs.Source = "file"
			return &bs
		}
	}
	if bs := readRegistryBootstrap(); bs.valid() {
		bs.Source = "registry"
		return bs
	}
	return nil
}

// ClearBootstrap removes the secret after use. Server/Pin stay in the
// registry (harmless) so an MSI repair does not look like a fresh install.
func ClearBootstrap(b *Bootstrap) error {
	switch b.Source {
	case "file":
		err := os.Remove(bootstrapFile())
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	case "registry":
		return clearRegistryToken()
	}
	return nil
}

// WriteBootstrapFile is used by tests and by `swim-agent stage-bootstrap`.
func WriteBootstrapFile(b Bootstrap) error {
	if err := os.MkdirAll(DataDir(), 0o700); err != nil {
		return err
	}
	data, _ := json.MarshalIndent(b, "", "  ")
	return os.WriteFile(bootstrapFile(), data, 0o600)
}
