//go:build !windows

package agent

import (
	"context"
	"errors"

	"swim/pkg/manifest"
)

func remoteSession(context.Context, *State, manifest.RemoteSessionArgs, interface{ Printf(string, ...any) }) (string, any, error) {
	return "", nil, errors.New("remote sessions exist only on Windows")
}

func disableRemoteAccount(interface{ Printf(string, ...any) }) {}
