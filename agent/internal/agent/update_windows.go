//go:build windows

package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

// ApplyUpdate runs in the detached updater (a copy of the previous agent):
// stop service → keep old exe → put new exe → start → watch 40 s → roll back
// if the new version is not running steadily. Log: %WINDIR%\Temp\swim-update.log
func ApplyUpdate(staged, exe string) error {
	logPath := filepath.Join(os.Getenv("WINDIR"), "Temp", "swim-update.log")
	lf, _ := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	logf := func(f string, a ...any) {
		if lf != nil {
			fmt.Fprintf(lf, "%s  %s\r\n", time.Now().Format("2006-01-02 15:04:05"), fmt.Sprintf(f, a...))
		}
	}
	defer func() {
		if lf != nil {
			lf.Close()
		}
	}()
	old := strings.TrimSuffix(exe, ".exe") + ".old.exe"
	logf("update: %s -> %s", exe, staged)
	time.Sleep(3 * time.Second) // let the agent report "done" first

	m, err := mgr.Connect()
	if err != nil {
		logf("SCM connect: %v", err)
		return err
	}
	defer m.Disconnect()
	s, err := m.OpenService(ServiceName)
	if err != nil {
		logf("open service: %v", err)
		return err
	}
	defer s.Close()

	stop := func() {
		if st, err := s.Query(); err == nil && st.State != svc.Stopped {
			_, _ = s.Control(svc.Stop)
			if !waitState(s, svc.Stopped, 30*time.Second) {
				if st, err := s.Query(); err == nil && st.ProcessId != 0 {
					killPID(st.ProcessId)
					logf("service did not stop, killed pid %d", st.ProcessId)
				}
				waitState(s, svc.Stopped, 10*time.Second)
			}
		}
	}
	// steady = RUNNING with the same PID at +20 s and +40 s
	steady := func() (bool, string) {
		if err := s.Start(); err != nil {
			return false, "start: " + err.Error()
		}
		var pid uint32
		for _, at := range []time.Duration{20 * time.Second, 20 * time.Second} {
			time.Sleep(at)
			st, err := s.Query()
			if err != nil {
				return false, err.Error()
			}
			if st.State != svc.Running || (pid != 0 && st.ProcessId != pid) {
				return false, fmt.Sprintf("state %d pid %d", st.State, st.ProcessId)
			}
			pid = st.ProcessId
		}
		return true, ""
	}

	stop()
	_ = os.Remove(old)
	if err := os.Rename(exe, old); err != nil {
		logf("keep old exe: %v — aborting, restarting current", err)
		_ = s.Start()
		return err
	}
	if err := os.Rename(staged, exe); err != nil {
		logf("install new exe: %v — restoring", err)
		_ = os.Rename(old, exe)
		_ = s.Start()
		return err
	}
	if ok, why := steady(); ok {
		logf("update ok")
		return nil
	} else {
		logf("new agent not running steadily (%s) — rolling back", why)
	}
	stop()
	_ = os.Remove(exe)
	if err := os.Rename(old, exe); err != nil {
		logf("ROLLBACK FAILED: %v", err)
		return err
	}
	if ok, why := steady(); ok {
		logf("rolled back to previous agent")
	} else {
		logf("previous agent also not running (%s) — SWIMRescue / technician needed", why)
	}
	return nil
}

func killPID(pid uint32) {
	h, err := windows.OpenProcess(windows.PROCESS_TERMINATE, false, pid)
	if err != nil {
		return
	}
	defer windows.CloseHandle(h)
	_ = windows.TerminateProcess(h, 1)
}
