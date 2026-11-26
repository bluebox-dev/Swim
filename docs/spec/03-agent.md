# 03 · SWIM Agent (Windows)

**English** · [ไทย](03-agent.th.md)

## 1. Internal structure

```
swim-svc.exe  (Windows service "SWIMSupervisor", LocalSystem, very small, rarely changes)
  └── starts and watches swim-agent.exe (version from the registry value "ActiveVersion"), performs self-update (04-agent-updates)

swim-agent.exe (child process, LocalSystem)
├── control    30 s heartbeat + command long-poll (55 s timeout)     ← never blocked by long work
├── worker     long work (staging, download, shrink), one job at a time ← writes events to the outbox
├── outbox     SQLite (C:\ProgramData\SWIM\outbox.db) with a sequence  ← delivered to the server at-least-once
├── watchdog   per-step timeout = max(expected × 3, a floor)          ← stuck = cancel + report
├── netcond    detects metered / VPN / corporate network / site       ← decides whether downloads are allowed
├── telemetry  inventory (SMBIOS, disk, BitLocker, battery, licence)
├── localipc   named pipe \\.\pipe\swim (ACL: Administrators + SYSTEM only)
└── notifier   notifies the signed-in user (through swim-notify.exe in the user's session)
```

- Every HTTP call has an explicit timeout + exponential backoff retry (1 s → 5 min) + ±20% jitter
- During critical steps it calls `SetThreadExecutionState(ES_CONTINUOUS|ES_SYSTEM_REQUIRED)` so the machine does not sleep
- Files and folders: `C:\Program Files\SWIM\versions\<ver>\` · data in `C:\ProgramData\SWIM\` (ACL SYSTEM + Admins)

## 2. First installation on an existing machine (bootstrap)

| Channel | How | Notes |
|---|---|---|
| **"Add machine" page → .msi** (primary, v0.2) | The admin picks site / lifetime / uses → gets `swim-agent-<ver>-<site>.msi` with server + pin + token embedded → double-click → UAC → done | The server builds the MSI with `wixl` every time (~1 second); the ProductCode is fixed per version |
| "Add machine" page → PowerShell command (fallback) | Paste the command into an elevated terminal | The command carries the server URL + enrollment token + **SHA-256 pin of the server certificate** |
| **GPO** (recommended with AD) | A startup script runs the same command (or `swim-agent.msi` when available) | An unlimited-use, short-lived token, revoked when done |
| Intune / SCCM | PowerShell script (SYSTEM) / Win32 app | |
| Unattend (new machines) | `FirstLogonCommands` runs the same command | Tested with `scripts/create-test-client.sh` |

Flow of the install command (`agent/install/install.ps1`, served at `/dl/agent/install.ps1`):
1. PowerShell sets `ServerCertificateValidationCallback` to accept only the certificate whose SHA-256 matches the pin → fetches `install.ps1`
2. Reads `/dl/agent/latest.json` → downloads the exe → verifies SHA-256 → `C:\Program Files\SWIM\swim-agent.exe`
3. Sets the ACL of `C:\ProgramData\SWIM` to SYSTEM + Administrators only
4. `swim-agent enroll` (the agent pins the certificate itself as a second layer) → the host token is stored with DPAPI (machine scope)
5. `swim-agent install` → Windows service `SWIMAgent` (Automatic, restarts on crash) → heartbeat every 60 seconds (lab)

- The files in `/dl/agent/` can be public because they contain no secrets — the only secret is the token an admin issues
- **Before production:** the exe/MSI must be **Authenticode-signed** (SmartScreen / Defender) and `latest.json` must be signed with the release key (Ed25519, [04](04-agent-updates.md)) instead of relying on the TLS pin alone
- Enrollment tokens are bound to a **site**, expire and have a use limit; they are created from the admin UI (RBAC: `admin`)

### 2.1 The agent must always persist (no gap after a reimage)

| Event | How the agent comes back | host_id |
|---|---|---|
| First installation | MSI (double-click / GPO / Intune) → writes `HKLM\SOFTWARE\SWIM\Bootstrap` (Server, Pin, Token) → the service enrolls by itself → deletes the token | new |
| **Reimaged by SWIM** | swim-pe places the exe + service + `bootstrap.json` (reimage ticket) into the new Windows ([05 §4.3](05-deployment-flow.md)) | **same** |
| Technician installs from USB | Install the MSI → enroll with a token → the server matches smbios_uuid + serial | **same** (labelled `manual_install`) |
| Installed without network | The service waits and enrolls when the network returns (before the token expires) | — |

**Hard rule — the agent's identity never travels across a Windows installation.** If state were copied (or an enrolled agent stayed in the Golden Master), every machine receiving the image would be the same host. Three layers prevent that:
1. Capture always excludes `\ProgramData\SWIM` from the WIM (builder exclusion list)
2. Clone guard: the state is bound to the SMBIOS UUID — a different UUID, or DPAPI failing to open the token → the state is moved aside (`state.json.discarded-*`) and the agent enrolls again
3. A reimage ticket is single-use, bound to the host and its SMBIOS UUID, valid ≤ 72 hours

## 3. Machine identity

### Enrollment

```
1. the agent creates a key pair:
     - ECDSA P-256 in the TPM through the "Microsoft Platform Crypto Provider" (when a TPM exists)
     - fallback: a software key protected with DPAPI (machine scope)
   + an X25519 key pair to "open the envelope" of secrets in a manifest (DPAPI)
2. POST /v1/agent/enroll { token, csr, x25519_pub, smbios_uuid, serial, vendor, model, ... }
3. the server matches the host:
     - an existing host with the same smbios_uuid + serial → same host_id (re-enroll)
     - none → a new host is created
4. the server issues a client certificate (mTLS) valid for 90 days → the agent renews it at one third remaining
```

- A junk `smbios_uuid` (`FFFFFFFF-…`, `00000000-…`, `03000200-0400-0500-0006-000700080009`) → `serial + vendor + model` is used instead and the host is flagged `weak_identity`

### After a reimage

- WinPE writes a **reimage ticket** (single-use, valid 24 hours, bound to job_id + host_id) to `C:\ProgramData\SWIM\reimage.ticket` of the new Windows
- The new agent enrolls with the ticket → it gets the same host_id without an enrollment token

## 4. Server commands (allowlist — D-14, D-20)

### 4.1 Available today (agent 0.3)

| Command | Level | Effect | Condition |
|---|---|---|---|
| `ping` | safe | returns version + clock skew | — |
| `collect_logs` | safe | system information + System log errors of the last 24 h + 64 KB of agent.log | — |
| `refresh_inventory` | safe | sends a fresh inventory → the server updates the host | — |
| `restart_agent` | change | the agent exits and SCM recovery restarts it (~10 seconds) | blocked while a kill switch is active |
| `reboot` | change | `shutdown /r /t N /c <message>` (N = 60 seconds – 24 hours) | blocked while a kill switch is active |
| `run_script` | change | runs PowerShell from the **approved script library** as SYSTEM, timeout ≤ 1 hour, returns output + exit code | two-person approval of the script, blocked while a kill switch is active |
| `uninstall_agent` | destructive | `msiexec /x` (when installed by MSI) + removes the service, files, data and registry → the server revokes the token at once | a reason + typing the machine name to confirm in the console |

An admin can also "remove from the system" (retire) without the agent — for broken or lost machines: it revokes the token and pending commands.

### 4.2 Safety layers of the command channel

1. **Allowlist on both sides** (`pkg/manifest.CommandTypes`) — the server does not create and the agent does not accept any other type; **there is no `run_command` / free shell**
2. **Ed25519 signature** on every command (same envelope as a manifest, [06](06-manifest.md)) binding `command_id` + `host_id` + **SMBIOS UUID** + `not_before/not_after` (1 hour)
3. The agent checks time against the **server's clock** (from the `Date` header) — a machine with a wrong clock still works, but an old command does not
4. **At-most-once** — the agent records the id before running (`commands-seen.json`); a resend is not executed again
5. **Two-person scripts** — the content is locked by SHA-256 at creation (immutable), the author cannot approve their own script (DB CHECK), and the agent re-checks the hash before running
6. The **kill switch** blocks change / destructive commands; safe ones still work for investigation
7. **Audit** — issuing, cancelling and results (actor `host:<id>`) go to the hash-chained audit log; the agent also logs every command locally
8. The channel is **agent pull** (25-second long-poll) — clients have no open port

**Known limitation:** the command-signing key lives on the server — whoever takes over the whole server can sign commands. Next step: move script signing to the approvers (personal keys / HSM) so the server alone cannot run code.

### 4.3 To be added with the deployment system (not yet)

| Command | Effect |
|---|---|
| `stage` | download image / WinPE / drivers to S: ahead of time |
| `deploy` / `rollback` | run a job from a manifest (notify the user + countdown) |
| `cancel` | cancel a job that has not reached the PONR |
| `resize_staging` | resize S: |
| `update_agent` | update the agent immediately (security fix) |

### Local commands (the `swim` CLI)

```
swim status                      # agent, job, S:, version
swim stage                       # pre-download the published image
swim deploy [--at 22:00]         # only with a manifest the server signed and that has not expired
swim rollback
swim cancel
swim logs --export D:\swim-logs.zip
```

- Requires a **local administrator** (checked by the named pipe ACL and again by the token)
- A local deploy needs a **signed manifest** in the cache: the agent requests a "standing manifest" of its current channel in advance (valid 7 days, refreshed once an hour on heartbeat) → a technician can run it offline, but only with the approved image
- Every local command records `actor = local:<DOMAIN\user>` in the outbox → it reaches the server's audit log

## 5. Preflight

| ID | Check | Level |
|---|---|---|
| P-01 | On AC power (when a battery exists) and battery ≥ 30% | **BLOCK** |
| P-02 | UEFI + GPT | **BLOCK** |
| P-03 | Architecture matches the channel | **BLOCK** |
| P-04 | OS disk ≥ `min_disk_gb` | **BLOCK** |
| P-05 | The published `boot.wim` has this model's storage driver (answered by the server from the driver matrix) | **BLOCK** |
| P-06 | S: exists and is large enough, or C: can be shrunk (`Get-PartitionSupportedSize`) | **BLOCK** |
| P-07 | Secure Boot state + mitigations match what WinPE supports | **BLOCK** on mismatch |
| P-08 | Kill switch off + manifest not expired + signature valid | **BLOCK** |
| P-09 | BitLocker is on → it will be suspended | WARN |
| P-10 | No full driver pack for this model | WARN |
| P-11 | Licence edition (OEM) does not match the image | WARN |
| P-12 | A user is signed in | INFO → notification flow |
| P-13 | More than one disk / USB storage attached | INFO (the target disk GUID is recorded) |

Preflight results are always sent to the server → used for risk and ETA ([05 §9](05-deployment-flow.md)).

## 6. Staging partition (S:) — D-07

- **Permanent**, label `SWIM-STAGE`, GPT type Basic Data, attribute `GPT_ATTRIBUTE_NO_DRIVE_LETTER` + hidden (users do not see it) — the agent mounts it temporarily when needed (`mountvol`)
- Size: `max(24 GB, 2 × image_size + boot.wim + drivers + 2 GB)` rounded up to 4 GB
- First creation: shrink C: and create S: **right after C:** (before WinRE when present)
  - If shrinking is not enough: turn off hibernation, delete VSS snapshots (`vssadmin delete shadows /for=C: /quiet`), try `Get-PartitionSupportedSize` again
- **BitLocker on S:** must not be auto-encrypted — the agent checks `manage-bde -status` after creation; if policy forces fixed-drive encryption → it reports BLOCK with advice to exclude it (label / GPO)
- S: holds **no plain-text secrets** — secrets in a manifest are encrypted to the machine's key (D-16)

```
S:\swim\
├── images\<image_id>\{image.wim | chunks\, chunks.json, chunks.sig}
├── winpe\<pe_ver>\{boot.wim, boot.sdi}
├── drivers\<pack_id>\
├── agent\<agent_ver>\swim-agent.msi          # injected by WinPE (D-18)
├── jobs\<job_id>\{manifest.json, manifest.sig, state.json}
└── logs\<job_id>\                            # last 5 jobs
```

## 7. Download engine

- Large files are split into **64 MiB chunks** per the signed `chunks.json` → download / verify / resume per chunk
- Source order: **peer in the subnet** (Phase 4) → **site cache** → **server**; a chunk with a wrong hash is dropped and the source is changed
- Bandwidth is capped by a token bucket: `max_download_mbps` from the manifest; reduced by another 50% while the user is active (idle < 5 minutes)
- Concurrent connections: 2 per machine
- Delta: when the base is already on S:, only the delta is downloaded

## 8. Network conditions (netcond)

| Condition | Detected by | Download allowed? |
|---|---|---|
| Metered (hotspot, 4G/5G) | `INetworkCostManager` → `NLM_CONNECTION_COST_*` ≠ unrestricted | ❌ (unless an admin sets an override) |
| VPN | interface type / subnet from configuration | by policy: default **no** image download, heartbeat and agent updates allowed |
| Corporate network | DNS suffix + `https://<server>/v1/ping` reachable | ✅ |
| Site | subnet → site mapping from the server | selects the site cache + counts concurrency per site |

## 9. User notification

- Before deploy / rollback / reboot: a toast + a countdown window (default 15 minutes) saying "All data on drive C: will be erased"
- The user **may defer** up to `max_deferrals` (default 2 times, ≤ 4 hours each) unless the job is `force`
- That the user was notified and how often they deferred is written to the audit log (D-05)

## 10. Local logs

- `C:\ProgramData\SWIM\logs\agent-YYYYMMDD.jsonl`, rotated daily, kept 14 days
- Job logs are on `S:\swim\logs\<job_id>\`, last 5 jobs
- Severity levels and what is sent to the server: see [09 §1](09-operations.md)
