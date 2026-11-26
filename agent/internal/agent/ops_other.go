//go:build !windows

package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"time"
)

// Non-Windows implementations exist so the command pipeline can be tested from Linux.

func osReboot(int, string) (string, error) {
	return "", errors.New("reboot is only supported on Windows")
}

func runScript(ctx context.Context, _ string, body string, timeout time.Duration) (string, int, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	out, err := command(ctx, "/bin/sh", "-c", body).CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		return string(out), -1, fmt.Errorf("script timed out after %s", timeout)
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return string(out), ee.ExitCode(), nil
	}
	return string(out), 0, err
}

func osUninstall() error { return os.RemoveAll(DataDir()) }

func sysInfo(ctx context.Context) string {
	out, _ := command(ctx, "sh", "-c", "uname -a; uptime; df -h /").CombinedOutput()
	return "==== System ====\n" + string(out)
}

func osApplyUpdate(staged string) error {
	if staged != "" {
		_ = os.Remove(staged) // Linux test build: verify only, never swap
	}
	return nil
}

func ApplyUpdate(staged, exe string) error { return errors.New("apply-update is Windows-only") }
