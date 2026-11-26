//go:build windows

package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

const ServiceName = "SWIMAgent"

func DataDir() string {
	if d := os.Getenv("ProgramData"); d != "" {
		return filepath.Join(d, "SWIM")
	}
	return `C:\ProgramData\SWIM`
}

// ───────── DPAPI (machine scope; the folder ACL limits it to SYSTEM + Administrators) ─────────

const (
	cryptprotectUIForbidden  = 0x1
	cryptprotectLocalMachine = 0x4
)

func blob(b []byte) *windows.DataBlob {
	if len(b) == 0 {
		return &windows.DataBlob{}
	}
	return &windows.DataBlob{Size: uint32(len(b)), Data: &b[0]}
}

func outBytes(d windows.DataBlob) []byte {
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(d.Data)))
	return append([]byte(nil), unsafe.Slice(d.Data, d.Size)...)
}

func Protect(b []byte) ([]byte, error) {
	var out windows.DataBlob
	desc, _ := windows.UTF16PtrFromString("SWIM agent secret")
	if err := windows.CryptProtectData(blob(b), desc, nil, 0, nil, cryptprotectUIForbidden|cryptprotectLocalMachine, &out); err != nil {
		return nil, err
	}
	return outBytes(out), nil
}

func Unprotect(b []byte) ([]byte, error) {
	var out windows.DataBlob
	if err := windows.CryptUnprotectData(blob(b), nil, nil, 0, nil, cryptprotectUIForbidden, &out); err != nil {
		return nil, err
	}
	return outBytes(out), nil
}

// ───────── inventory (one PowerShell/CIM call; no text parsing of localized output) ─────────

const invScript = `[Console]::OutputEncoding=[Text.Encoding]::UTF8
$p=Get-CimInstance Win32_ComputerSystemProduct; $c=Get-CimInstance Win32_ComputerSystem; $b=Get-CimInstance Win32_BIOS
$sb=$null; try { $sb=[bool](Confirm-SecureBootUEFI) } catch {}
$t=$null; try { $t=(Get-CimInstance -Namespace root/cimv2/Security/MicrosoftTpm -ClassName Win32_Tpm -ErrorAction Stop).SpecVersion } catch {}
[pscustomobject]@{uuid=$p.UUID; serial=$b.SerialNumber; vendor=$c.Manufacturer; model=$c.Model; hostname=$env:COMPUTERNAME;
  firmware=$env:firmware_type; secure_boot=$sb; tpm=$t} | ConvertTo-Json -Compress`

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

func CollectInventory() (Inventory, error) {
	// WMI can take minutes to come up right after boot; enroll retries on error anyway.
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	out, err := command(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass",
		"-Command", invScript).Output()
	if err != nil {
		return Inventory{}, fmt.Errorf("powershell inventory: %w", err)
	}
	var r struct {
		UUID, Serial, Vendor, Model, Hostname, Firmware, TPM string
		SecureBoot                                           *bool `json:"secure_boot"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(out))), &r); err != nil {
		return Inventory{}, fmt.Errorf("inventory JSON: %w: %s", err, out)
	}
	inv := Inventory{SmbiosUUID: r.UUID, Serial: strings.TrimSpace(r.Serial), Vendor: strings.TrimSpace(r.Vendor),
		Model: strings.TrimSpace(r.Model), Hostname: r.Hostname, Arch: runtime.GOARCH, Firmware: strings.ToLower(r.Firmware),
		SecureBoot: r.SecureBoot, TPMVersion: strings.TrimSpace(strings.Split(r.TPM, ",")[0])}
	if inv.Serial == "" || strings.EqualFold(inv.Serial, "To be filled by O.E.M.") || strings.EqualFold(inv.Serial, "Default string") {
		inv.Serial = "NOSERIAL" // VMs and white-box PCs; the UUID still identifies the machine
	}
	return inv, nil
}

// ───────── MSI bootstrap (HKLM\SOFTWARE\SWIM\Bootstrap) ─────────

const bootstrapKey = `SOFTWARE\SWIM\Bootstrap`

func readRegistryBootstrap() *Bootstrap {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, bootstrapKey, registry.QUERY_VALUE)
	if err != nil {
		return nil
	}
	defer k.Close()
	get := func(n string) string { v, _, _ := k.GetStringValue(n); return strings.TrimSpace(v) }
	return &Bootstrap{Server: get("Server"), Pin: get("Pin"), Token: get("Token")}
}

func clearRegistryToken() error {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, bootstrapKey, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	return k.DeleteValue("Token")
}

// platformInit locks down the data folder and makes SCM restart us on crash —
// an MSI-created service has no recovery actions by default.
func platformInit(lg interface{ Printf(string, ...any) }) {
	_ = os.MkdirAll(DataDir(), 0o700)
	if out, err := exec.Command("icacls", DataDir(), "/inheritance:r", "/grant:r",
		"*S-1-5-18:(OI)(CI)F", "*S-1-5-32-544:(OI)(CI)F").CombinedOutput(); err != nil {
		lg.Printf("icacls %s: %v %s", DataDir(), err, strings.TrimSpace(string(out)))
	}
	m, err := mgr.Connect()
	if err != nil {
		return
	}
	defer m.Disconnect()
	if s, err := m.OpenService(ServiceName); err == nil {
		_ = s.SetRecoveryActions([]mgr.RecoveryAction{
			{Type: mgr.ServiceRestart, Delay: 10 * time.Second},
			{Type: mgr.ServiceRestart, Delay: 30 * time.Second},
			{Type: mgr.ServiceRestart, Delay: 2 * time.Minute},
		}, 86400)
		_ = s.SetRecoveryActionsOnNonCrashFailures(true)
		s.Close()
	}
}

// ───────── Windows service ─────────

func InstallService() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	m, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer m.Disconnect()
	s, err := m.OpenService(ServiceName)
	if err == nil { // already installed: point it at this binary and restart
		cfg, _ := s.Config()
		cfg.BinaryPathName = fmt.Sprintf(`"%s" run`, exe)
		cfg.StartType = mgr.StartAutomatic
		if err := s.UpdateConfig(cfg); err != nil {
			s.Close()
			return err
		}
	} else {
		s, err = m.CreateService(ServiceName, exe, mgr.Config{
			DisplayName: "SWIM Agent",
			Description: "Staged Windows Image Manager agent — reports to the SWIM server and runs approved reimage jobs.",
			StartType:   mgr.StartAutomatic,
		}, "run")
		if err != nil {
			return err
		}
	}
	defer s.Close()
	_ = s.SetRecoveryActions([]mgr.RecoveryAction{
		{Type: mgr.ServiceRestart, Delay: 10 * time.Second},
		{Type: mgr.ServiceRestart, Delay: 30 * time.Second},
		{Type: mgr.ServiceRestart, Delay: 2 * time.Minute},
	}, 86400)
	if st, err := s.Query(); err == nil && st.State == svc.Running {
		if _, err := s.Control(svc.Stop); err == nil {
			waitState(s, svc.Stopped, 20*time.Second)
		}
	}
	if err := s.Start(); err != nil {
		return err
	}
	if !waitState(s, svc.Running, 20*time.Second) {
		return fmt.Errorf("service did not reach Running — see %s", filepath.Join(DataDir(), "logs", "agent.log"))
	}
	return nil
}

func waitState(s *mgr.Service, want svc.State, d time.Duration) bool {
	for end := time.Now().Add(d); time.Now().Before(end); time.Sleep(300 * time.Millisecond) {
		if st, err := s.Query(); err == nil && st.State == want {
			return true
		}
	}
	return false
}

func UninstallService() error {
	m, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer m.Disconnect()
	s, err := m.OpenService(ServiceName)
	if err != nil {
		return fmt.Errorf("service not installed")
	}
	defer s.Close()
	if _, err := s.Control(svc.Stop); err == nil {
		waitState(s, svc.Stopped, 20*time.Second)
	}
	return s.Delete()
}

type handler struct{ run func(context.Context) error }

func (h handler) Execute(_ []string, req <-chan svc.ChangeRequest, st chan<- svc.Status) (bool, uint32) {
	st <- svc.Status{State: svc.StartPending}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- h.run(ctx) }()
	st <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown | svc.AcceptPowerEvent}
	for {
		select {
		case err := <-done: // run ended on its own (e.g. not enrolled) → let SCM recovery restart us
			cancel()
			if err != nil {
				return true, 1
			}
			return false, 0
		case c := <-req:
			switch c.Cmd {
			case svc.Interrogate:
				st <- c.CurrentStatus
			case svc.PowerEvent:
				if c.EventType == 0x4 { // PBT_APMSUSPEND
					select {
					case powerSuspend <- struct{}{}:
					default:
					}
				}
			case svc.Stop, svc.Shutdown:
				st <- svc.Status{State: svc.StopPending}
				cancel()
				select {
				case <-done:
				case <-time.After(15 * time.Second):
				}
				return false, 0
			}
		}
	}
}

// RunService runs under the SCM when started as a service, else in the foreground.
func RunService(run func(context.Context) error) error {
	if is, _ := svc.IsWindowsService(); is {
		return svc.Run(ServiceName, handler{run})
	}
	ctx, cancel := signalContext()
	defer cancel()
	return run(ctx)
}
