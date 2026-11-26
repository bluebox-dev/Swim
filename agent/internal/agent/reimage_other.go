//go:build !windows

package agent

import (
	"context"
	"errors"

	"swim/pkg/manifest"
)

func startReimage(context.Context, *execCtx, manifest.Command) (string, error) {
	return "", errors.New("reimage is only supported on Windows")
}

func reportFinishedJob(*Client, string, interface{ Printf(string, ...any) }) {}

func clearAbortedJob() {}

func rehandoff(context.Context, *execCtx, string) (string, error) {
	return "", errors.New("reimage exists only on Windows")
}
