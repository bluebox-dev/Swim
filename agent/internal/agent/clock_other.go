//go:build !windows

package agent

func fixClock(*Client, interface{ Printf(string, ...any) }) {}
