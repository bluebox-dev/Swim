//go:build !windows

package agent

import (
	"context"
	"encoding/json"
	"os"
	"runtime"
	"syscall"
	"time"
)

func CollectDetails(ctx context.Context) (json.RawMessage, error) {
	inv, err := CollectInventory()
	if err != nil {
		return nil, err
	}
	var st syscall.Statfs_t
	_ = syscall.Statfs("/", &st)
	host, _ := os.Hostname()
	return json.Marshal(map[string]any{
		"schema": 2, "collected_at": time.Now().UTC(), "smbios_uuid": inv.SmbiosUUID, "serial": inv.Serial,
		"vendor": inv.Vendor, "model": inv.Model, "hostname": host, "arch": runtime.GOARCH, "firmware": inv.Firmware,
		"os":    map[string]any{"caption": runtime.GOOS},
		"disks": []map[string]any{{"drive": "/", "size": int64(st.Blocks) * st.Bsize, "free": int64(st.Bavail) * st.Bsize}},
	})
}
