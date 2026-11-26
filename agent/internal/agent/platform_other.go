//go:build !windows

package agent

// Non-Windows build: used to test enroll/heartbeat against the server from Linux.
// No DPAPI here — secrets rely on the 0600 file mode only.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

func DataDir() string {
	if d := os.Getenv("SWIM_AGENT_DIR"); d != "" {
		return d
	}
	return "/var/lib/swim-agent"
}

func Protect(b []byte) ([]byte, error)   { return append([]byte(nil), b...), nil }
func Unprotect(b []byte) ([]byte, error) { return append([]byte(nil), b...), nil }

type Inventory struct {
	SmbiosUUID string `json:"smbios_uuid"`
	Serial     string `json:"serial"`
	Vendor     string `json:"vendor"`
	Model      string `json:"model"`
	Hostname   string `json:"hostname"`
	Arch       string `json:"arch"`
	Firmware   string `json:"firmware"`
	SecureBoot *bool  `json:"secure_boot"`
	TPMVersion string `json:"tpm_version"`
}

func dmi(name string) string {
	b, _ := os.ReadFile(filepath.Join("/sys/class/dmi/id", name))
	return strings.TrimSpace(string(b))
}

func CollectInventory() (Inventory, error) {
	host, _ := os.Hostname()
	inv := Inventory{SmbiosUUID: dmi("product_uuid"), Serial: dmi("product_serial"), Vendor: dmi("sys_vendor"),
		Model: dmi("product_name"), Hostname: host, Arch: runtime.GOARCH, Firmware: "bios"}
	if _, err := os.Stat("/sys/firmware/efi"); err == nil {
		inv.Firmware = "uefi"
	}
	if v := os.Getenv("SWIM_TEST_UUID"); v != "" { // lets one Linux box pose as several machines in tests
		inv.SmbiosUUID = v
	}
	if inv.SmbiosUUID == "" {
		return inv, errors.New("cannot read SMBIOS UUID (run as root)")
	}
	if inv.Serial == "" {
		inv.Serial = "NOSERIAL"
	}
	return inv, nil
}

func firmwareUUID() (string, error) {
	if v := os.Getenv("SWIM_TEST_UUID"); v != "" {
		return strings.ToLower(v), nil
	}
	if u := dmi("product_uuid"); u != "" {
		return strings.ToLower(u), nil
	}
	return "", errors.New("cannot read SMBIOS UUID")
}

func readRegistryBootstrap() *Bootstrap                   { return nil }
func clearRegistryToken() error                           { return nil }
func platformInit(lg interface{ Printf(string, ...any) }) {}

func InstallService() error   { return errors.New("service install is Windows-only") }
func UninstallService() error { return errors.New("service install is Windows-only") }

func RunService(run func(context.Context) error) error {
	ctx, cancel := signalContext()
	defer cancel()
	return run(ctx)
}
