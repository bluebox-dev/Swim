//go:build windows

package agent

import (
	"context"
	"fmt"
	"time"
)

// fixClock sets the Windows clock from the server's when they differ by more than two
// minutes. Some machines (virtual ones whose hardware clock is kept in UTC) come back from a
// reboot hours off; a wrong clock breaks logs, schedules and certificate checks.
func fixClock(c *Client, lg interface{ Printf(string, ...any) }) {
	skew := c.Skew()
	if skew < 2*time.Minute && skew > -2*time.Minute {
		return
	}
	target := time.Now().Add(skew).UTC()
	_, err := ps(context.Background(), 30*time.Second, fmt.Sprintf(
		`Set-Date -Date ([datetime]::Parse('%s').ToLocalTime()) | Out-Null; w32tm /resync /nowait 2>$null | Out-Null; 'ok'`,
		target.Format(time.RFC3339)))
	if err != nil {
		lg.Printf("clock is %s off the server, could not correct it: %v", skew, err)
		return
	}
	lg.Printf("clock was %s off the server — corrected", skew)
}
