//go:build windows

package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/windows/registry"
)

func osReboot(delayS int, msg string) (string, error) {
	out, err := exec.Command("shutdown.exe", "/r", "/t", strconv.Itoa(delayS), "/c", msg, "/d", "p:4:1").CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("shutdown.exe: %v", err)
	}
	return fmt.Sprintf("restart scheduled in %d s — the signed-in user sees: %q", delayS, msg), nil
}

// runScript writes the approved script (UTF-8 with BOM so Windows PowerShell 5.1
// reads Thai correctly) and runs it as SYSTEM with a hard timeout.
func runScript(ctx context.Context, id, body string, timeout time.Duration) (string, int, error) {
	dir := filepath.Join(DataDir(), "scripts")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", -1, err
	}
	path := filepath.Join(dir, id+".ps1")
	prelude := "[Console]::OutputEncoding=[Text.Encoding]::UTF8\r\n$ProgressPreference='SilentlyContinue'\r\n"
	if err := os.WriteFile(path, append([]byte("\xEF\xBB\xBF"+prelude), []byte(body)...), 0o600); err != nil {
		return "", -1, err
	}
	defer os.Remove(path)
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	out, err := command(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass",
		"-File", path).CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		return string(out), -1, fmt.Errorf("script timed out after %s", timeout)
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return string(out), ee.ExitCode(), nil
	}
	if err != nil {
		return string(out), -1, err
	}
	return string(out), 0, nil
}

// msiProductCode finds the installed "SWIM Agent" MSI, if the agent came from one.
func msiProductCode() string {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall`, registry.ENUMERATE_SUB_KEYS)
	if err != nil {
		return ""
	}
	defer k.Close()
	names, _ := k.ReadSubKeyNames(-1)
	for _, n := range names {
		sk, err := registry.OpenKey(k, n, registry.QUERY_VALUE)
		if err != nil {
			continue
		}
		dn, _, _ := sk.GetStringValue("DisplayName")
		wi, _, _ := sk.GetIntegerValue("WindowsInstaller")
		sk.Close()
		if dn == "SWIM Agent" && wi == 1 && strings.HasPrefix(n, "{") {
			return n
		}
	}
	return ""
}

// osUninstall launches a detached cleanup script: it outlives this process,
// removes the MSI product (if any), the service, the binaries, data and registry.
func osUninstall() error {
	exe, _ := os.Executable()
	binDir := filepath.Dir(exe)
	script := filepath.Join(os.TempDir(), "swim-uninstall.cmd")
	pc := msiProductCode()
	lines := []string{
		"@echo off",
		`set LOG=%WINDIR%\Temp\swim-uninstall.log`,
		`echo %date% %time% SWIM uninstall started (msi=` + pc + `) > "%LOG%"`,
		"ping -n 6 127.0.0.1 >nul",
	}
	if pc != "" {
		lines = append(lines, `msiexec /x `+pc+` /qn /norestart /l*v "%WINDIR%\Temp\swim-uninstall-msi.log"`, `echo msiexec exit %errorlevel% >> "%LOG%"`)
	}
	lines = append(lines,
		`sc stop SWIMAgent >> "%LOG%" 2>&1`,
		"ping -n 6 127.0.0.1 >nul",
		`sc delete SWIMAgent >> "%LOG%" 2>&1`,
		`rmdir /s /q "`+binDir+`"`,
		`rmdir /s /q "`+DataDir()+`"`,
		`reg delete HKLM\SOFTWARE\SWIM /f >> "%LOG%" 2>&1`,
		`echo %date% %time% SWIM agent removed >> "%LOG%"`,
		`(goto) 2>nul & del "%~f0"`,
	)
	if err := os.WriteFile(script, []byte(strings.Join(lines, "\r\n")+"\r\n"), 0o600); err != nil {
		return err
	}
	cmd := exec.Command("cmd.exe", "/c", script)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x00000008 | 0x00000200} // DETACHED_PROCESS | CREATE_NEW_PROCESS_GROUP
	return cmd.Start()
}

const sysInfoScript = `[Console]::OutputEncoding=[Text.Encoding]::UTF8
$o=Get-CimInstance Win32_OperatingSystem; $c=Get-CimInstance Win32_ComputerSystem
"==== System ===="
"Computer : $($env:COMPUTERNAME)  ($($c.Manufacturer) $($c.Model))"
"Windows  : $($o.Caption) $($o.Version) build $($o.BuildNumber)"
"Boot     : $($o.LastBootUpTime)  (uptime $([int]((Get-Date)-$o.LastBootUpTime).TotalHours) h)"
"Memory   : $([math]::Round($o.FreePhysicalMemory/1MB,1)) GB free of $([math]::Round($o.TotalVisibleMemorySize/1MB,1)) GB"
Get-CimInstance Win32_LogicalDisk -Filter 'DriveType=3' | ForEach-Object { "Disk $($_.DeviceID) : $([math]::Round($_.FreeSpace/1GB,1)) GB free of $([math]::Round($_.Size/1GB,1)) GB" }
Get-NetIPAddress -AddressFamily IPv4 -ErrorAction SilentlyContinue | Where-Object { $_.IPAddress -ne '127.0.0.1' } | ForEach-Object { "IP       : $($_.IPAddress) ($($_.InterfaceAlias))" }
"Service  : SWIMAgent $((Get-Service SWIMAgent -ErrorAction SilentlyContinue).Status)"
""
"==== Windows System log: errors in the last 24 h (max 15) ===="
Get-WinEvent -FilterHashtable @{LogName='System';Level=1,2;StartTime=(Get-Date).AddDays(-1)} -MaxEvents 15 -ErrorAction SilentlyContinue |
  ForEach-Object { "$($_.TimeCreated.ToString('yyyy-MM-dd HH:mm')) [$($_.ProviderName) $($_.Id)] $((($_.Message) -split "\r?\n")[0])" }`

func sysInfo(ctx context.Context) string {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	out, err := command(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass",
		"-Command", sysInfoScript).CombinedOutput()
	if err != nil {
		return fmt.Sprintf("(system info failed: %v)\n%s", err, out)
	}
	return strings.ReplaceAll(string(out), "\r\n", "\n")
}

// osApplyUpdate hands the swap to a copy of the current (known-good) binary, so a
// broken new version can never prevent its own rollback. The copy runs detached
// as "apply-update" and drives the service through the SCM API.
func osApplyUpdate(staged string) error {
	if staged == "" {
		return errors.New("nothing staged")
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	updater := filepath.Join(DataDir(), "swim-updater.exe")
	if err := copyFile(exe, updater); err != nil {
		return fmt.Errorf("prepare updater: %w", err)
	}
	cmd := exec.Command(updater, "apply-update", "--staged", staged, "--exe", exe)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x00000008 | 0x00000200} // DETACHED_PROCESS | CREATE_NEW_PROCESS_GROUP
	return cmd.Start()
}

func copyFile(src, dst string) error {
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, b, 0o700)
}
