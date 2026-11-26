//go:build !windows

package agent

import (
	"errors"

	"swim/pkg/manifest"
)

func prestageImage(*execCtx, manifest.PrestageArgs) (string, error) {
	return "", errors.New("pre-staging exists only on Windows")
}
