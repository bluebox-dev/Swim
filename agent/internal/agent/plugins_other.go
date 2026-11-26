//go:build !windows

package agent

import (
	"context"
	"errors"
	"time"
)

func PowerShell(context.Context, time.Duration, string) (string, error) {
	return "", errors.New("PowerShell exists only on Windows")
}

func ConsoleUser(context.Context) (string, int, bool) { return "", 0, false }
