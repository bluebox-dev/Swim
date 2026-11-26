//go:build !windows

package agent

import (
	"context"
	"errors"

	"swim/pkg/manifest"
)

func setPolicy(context.Context, manifest.SetPolicyArgs) (string, any, error) {
	return "", nil, errors.New("install lock exists only on Windows")
}
