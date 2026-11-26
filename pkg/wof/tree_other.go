//go:build !linux

package wof

import "errors"

// Stats of an Uncompress run (see tree_linux.go).
type Stats struct {
	Files, InBytes, OutBytes, PEChecked, PEBad, Failed int64
	FailedPaths                                        []string
}

// SystemLeft counts files under \Windows\System32 and \Windows\Boot still WOF-compressed.
func (s Stats) SystemLeft() int { return len(s.FailedPaths) }

// Uncompress needs libntfs-3g and ntfs-3g: Linux (builder container) only.
func Uncompress(device, helper, tmp string, dryRun bool, logf func(string, ...any), progress ...func(done, total int)) (Stats, error) {
	return Stats{}, errors.New("WOF decompression runs only on the Linux builder")
}
