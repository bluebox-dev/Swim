//go:build windows

package agent

import (
	"context"
	"time"
)

// PowerShell runs a script as the agent does (SYSTEM, no profile, errors stop it).
func PowerShell(ctx context.Context, timeout time.Duration, script string) (string, error) {
	return ps(ctx, timeout, script)
}

// ConsoleUser reports who is signed in at the machine's own screen and in which session.
func ConsoleUser(ctx context.Context) (user string, session int, ok bool) {
	return consoleUser(ctx)
}
