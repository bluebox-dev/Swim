package manifest

import (
	"encoding/json"
	"time"
)

// Command is the signed payload of one server→agent command. Both sides use
// CommandTypes as the allowlist: there is deliberately no "run arbitrary
// shell" type — free-form code only travels as an approved, hash-pinned script.
type Command struct {
	Schema    int             `json:"schema"`
	Kind      string          `json:"kind"` // always "command"
	CommandID string          `json:"command_id"`
	Type      string          `json:"type"`
	Args      json.RawMessage `json:"args"`
	Actor     string          `json:"actor"`
	Host      struct {
		HostID     string `json:"host_id"`
		SmbiosUUID string `json:"smbios_uuid"`
	} `json:"host"`
	IssuedAt  time.Time `json:"issued_at"`
	NotBefore time.Time `json:"not_before"`
	NotAfter  time.Time `json:"not_after"`
}

// Risk levels drive server policy: "safe" always allowed, "change" blocked by
// an active kill switch, "destructive" also needs a written reason.
var CommandTypes = map[string]string{
	"ping":              "safe",
	"collect_logs":      "safe",
	"refresh_inventory": "safe",
	"restart_agent":     "change",
	"reboot":            "change",
	"run_script":        "change",
	"update_agent":      "change",
	"uninstall_agent":   "destructive",
	"reimage":           "destructive",
	// removes one image cached on the SWIMSTAGE partition (re-downloadable)
	"delete_staged_image": "change",
	// deletes the whole SWIMSTAGE partition and gives its space back to C: (agent ≥ 0.5.13)
	"release_staging": "change",
	// one-click Remote Desktop: (re)creates the swim-remote account with a sealed one-time
	// password, turns RDP on for the server only, disables the account again after ttl (agent ≥ 0.5.15)
	"remote_session": "change",
	// install lock: only SWIM (SYSTEM) can install software (agent ≥ 0.5.18)
	"set_policy": "change",
	// keep an install image stored on the machine ahead of time (agent ≥ 0.5.19)
	"prestage_image": "change",
	// re-try the restart into SWIM WinPE of a job whose machine came back into Windows (agent ≥ 0.5.21)
	"rehandoff": "destructive",
	// Wake-on-LAN relay: the agent broadcasts magic packets on its own segment
	"wake": "safe",
}

// RemoteSessionArgs is the "remote_session" command. The account password travels
// sealed to the host's X25519 key, so it is in no log and no database in clear.
type RemoteSessionArgs struct {
	Sealed    string `json:"sealed"`     // base64(box.SealAnonymous(JSON(RemoteSecrets)))
	AllowFrom string `json:"allow_from"` // IP allowed to reach RDP, or "LocalSubnet"
	TTLS      int    `json:"ttl_s"`      // the account is disabled again after this long
}

// SetPolicyArgs is the "set_policy" command (desired machine policy).
type SetPolicyArgs struct {
	InstallLock bool     `json:"install_lock"`
	Keep        []string `json:"keep,omitempty"` // admin accounts that stay administrators (the reimage admin)
}

// PrestageArgs is the "prestage_image" command; an empty Image clears it.
type PrestageArgs struct {
	ImageID string  `json:"image_id,omitempty"`
	Image   FileRef `json:"image"`
}

// ReleaseStagingArgs: Force = the server checked that no install job is open, so job
// files left by an aborted install may be removed too.
type ReleaseStagingArgs struct {
	Force bool `json:"force,omitempty"`
}

// RehandoffArgs: boot the machine into SWIM WinPE again for an install whose files are
// still staged (the earlier restart came back into Windows).
type RehandoffArgs struct {
	JobID string `json:"job_id"`
}

type RemoteSecrets struct {
	User     string `json:"user"`
	Password string `json:"password"`
}

type WakeArgs struct {
	MACs []string `json:"macs"`
}

type DeleteStagedImageArgs struct {
	Version string `json:"version"`
}

type RebootArgs struct {
	DelayS  int    `json:"delay_s"`
	Message string `json:"message"`
}

type ScriptArgs struct {
	ScriptID string `json:"script_id"`
	Name     string `json:"name"`
	SHA256   string `json:"sha256"`
	Body     string `json:"body"`
	TimeoutS int    `json:"timeout_s"`
}

// UpdateArgs come from the server's published latest.json; the agent downloads
// <server>/dl/agent/<File> over its pinned connection and checks SHA256.
type UpdateArgs struct {
	Version string `json:"version"`
	File    string `json:"file"`
	SHA256  string `json:"sha256"`
	Size    int64  `json:"size_bytes"`
}

// DiskProfile decides what happens to the disk during a reimage.
//
//	refresh:     format only the current Windows partition (other partitions, e.g. D:, survive)
//	repartition: delete every partition except EFI, MSR and the SWIM staging partition,
//	             then create C: (SystemGB, 0 = all space) and optionally a Data partition
type DiskProfile struct {
	Mode      string `json:"mode"`
	SystemGB  int    `json:"system_gb"`
	DataLabel string `json:"data_label,omitempty"` // "" = no data partition
}

// ReimageArgs is the signed "reimage" command. Secrets (local admin password,
// reimage ticket, WinPE token) are sealed to the host's X25519 key (NaCl box).
type ReimageArgs struct {
	JobID    string      `json:"job_id"`
	Image    FileRef     `json:"image"`
	WinPE    FileRef     `json:"winpe"`            // base boot.wim (drivers are added per job on the server)
	PEExe    FileRef     `json:"pe_exe,omitempty"` // swim-pe.exe: agents ≥ 0.5.11 build WinPE from the machine's own WinRE
	BootSDI  FileRef     `json:"boot_sdi"`
	Agent    FileRef     `json:"agent"`
	Disk     DiskProfile `json:"disk"`
	Hostname string      `json:"hostname"`
	Admin    string      `json:"admin_user"`
	TimeZone string      `json:"time_zone"`
	Sealed   string      `json:"sealed"` // base64(box.SealAnonymous(JSON(ReimageSecrets)))
}

type FileRef struct {
	Version string `json:"version,omitempty"`
	Path    string `json:"path"` // relative to the server's storage, fetched via /v1/agent/files/
	SHA256  string `json:"sha256"`
	Size    int64  `json:"size"`
}

type ReimageSecrets struct {
	AdminPassword string `json:"admin_password"`
	Ticket        string `json:"reimage_ticket"`
	PEToken       string `json:"pe_token"`
}
