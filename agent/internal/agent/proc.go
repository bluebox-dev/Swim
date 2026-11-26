package agent

import (
	"context"
	"os/exec"
	"time"
)

// command is exec.CommandContext with a WaitDelay: when ctx times out the
// process is killed AND Wait gives up after 5 s even if a grandchild (e.g.
// DismHost.exe started by Get-WindowsDriver) still holds stdout open. Without
// it a timed-out script could block the agent's command channel forever.
func command(ctx context.Context, name string, args ...string) *exec.Cmd {
	c := exec.CommandContext(ctx, name, args...)
	c.WaitDelay = 5 * time.Second
	return c
}
