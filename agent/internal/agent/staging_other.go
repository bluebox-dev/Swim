//go:build !windows

package agent

import (
	"context"
	"errors"
)

type StagingInfo struct{}

func stagingInfo() *StagingInfo { return nil }

func deleteStagedImage(string) (string, error) {
	return "", errors.New("stored images exist only on Windows")
}

func releaseStaging(context.Context) (string, error) {
	return "", errors.New("staging exists only on Windows")
}
