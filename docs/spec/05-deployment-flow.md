# 05 · Deployment Flow & State Machine

**English** · [ไทย](05-deployment-flow.th.md)

> This file is the source of truth for `pkg/state` — the agent, swim-pe and the server must all implement it.

## 1. Job states

```
queued ─► preflight ─► staging ─► staged ─► waiting ─► handoff ─► pe_verify ═══PONR═══► applying ─► first_boot ─► health_check ─► completed
            │            │                    │           │           │                    │              │              │
            ▼            ▼                    ▼           ▼           ▼                    ▼              ▼              ▼
         blocked      (retry)            cancelled    aborted     aborted            (retry ×2)     (supervisor)   (supervisor)
                                                                                         └──────────┬─────┴──────────────┘
                                                                                                    ▼
                                                                                         fallback_apply (N-1) ─► rolled_back
                                                                                                    │
                                                                                                    ▼
                                                                                                 failed  (needs a technician)

server-derived:  suspected_failure  (silent for longer than ETA P90 × 1.5 in a state that should produce signals)
```

| State | Who | Machine condition | Reversible |
|---|---|---|---|
| `queued` | server | normal | ✅ |
| `preflight` | agent | normal | ✅ |
| `blocked` | agent | normal — preflight failed (BLOCK) | ✅ (ends) |
| `staging` | agent | normal, usable | ✅ |
| `staged` | agent | files complete and verified on S: | ✅ |
| `waiting` | agent | waiting for schedule / concurrency slot / user deferral | ✅ |
| `handoff` | agent | editing BCD + suspending BitLocker + rebooting | ✅ |
| `pe_verify` | swim-pe | in WinPE, C: untouched | ✅ (reboots to the old Windows) |
| **PONR** | swim-pe | **format C:** | ❌ |
| `applying` | swim-pe | apply image + drivers + ODJ + agent + bcdboot | repeatable |
| `first_boot` | Windows Setup | specialize / OOBE | under the supervisor |
| `health_check` | new agent | new Windows running, awaiting commit | under the supervisor |
| `fallback_apply` | swim-pe | applying image N-1 from S: | repeatable |
| `completed` | agent | success | — |
| `cancelled` | agent / server | cancelled before `handoff` (by a person, kill switch, expired manifest) → old Windows | — |
| `aborted` | agent / pe | failed before the PONR → still on the **old Windows** | — |
| `rolled_back` | agent | failed after the PONR → running **image N-1** | — |
| `failed` | pe / server | unusable, needs a technician | — |

## 2. `state.json` (in `S:\swim\jobs\<job_id>\`)

```json
{
  "schema_version": 3,
  "job_id": "job_9821a4",
  "host_id": "h_01HFX...",
  "phase": "applying",
  "phase_attempt": 1,
  "boot_attempts": 0,
  "engine": "winpe",
  "target": {
    "disk_unique_id": "nvme.8086&...&SN12345",
    "partition_guids": {
      "esp":   "{c12a7328-...}",
      "os":    "{9f3a...}",
      "stage": "{51c0...}",
      "winre": "{de94...}",
      "data":  "{a1b2...}"
    }
  },
  "image":    { "id": "img_160", "path": "images/img_160/image.wim", "sha256": "…" },
  "fallback": { "id": "img_150", "path": "images/img_150/image.wim", "sha256": "…" },
  "using_fallback": false,
  "ponr_passed_at": null,
  "commit": { "required": true, "committed_at": null },
  "timings": { "pe_verify": 41.2 },
  "updated_at": "2026-11-23T14:02:11Z"
}
```

- Written atomically (write temp → fsync → rename) **every time before** a new step begins
- **Partitions are identified by GPT partition GUID only**; disk numbers and drive letters are forbidden (they differ between WinPE and Windows)

## 3. Steps on the old Windows (agent)

### 3.1 staging
1. Verify the manifest's signature and expiry
2. Download the image (or delta), `boot.wim`, `boot.sdi`, the driver pack and the latest agent MSI (D-18) → verify every chunk
3. If N-1 is not on S: yet and there is room → download N-1 too (low priority)
4. → `staged`

### 3.2 waiting
- Waits for `schedule.not_before`, the maintenance window, a concurrency slot (requested from the server — [07](07-fleet-safety.md)), user notification and deferrals ([03 §9](03-agent.md))
- Before leaving waiting: **request a go-token from the server** (confirms the kill switch is off and the slot is still free) — when offline on a local job with a standing manifest, the manifest's expiry is used instead

### 3.3 handoff (in exactly this order)

```bat
:: 1) re-check the hashes of the files on S: (quick: chunks.json signature + sizes + 5 sample chunks)
:: 2) BitLocker: suspend so the old Windows still boots if PE fails before the PONR
manage-bde -protectors -disable C: -RebootCount 2

:: 3) BCD — fixed SWIM GUIDs (idempotent, no parsing of localised text)
bcdedit /create {ramdiskoptions} /d "SWIM Ramdisk"            & rem skip if it exists
bcdedit /set {ramdiskoptions} ramdisksdidevice partition=S:
bcdedit /set {ramdiskoptions} ramdisksdipath \swim\winpe\<pe_ver>\boot.sdi
bcdedit /create {5a1f0c3e-0000-4000-8000-5717aa000001} /d "SWIM WinPE" /application osloader
set PE={5a1f0c3e-0000-4000-8000-5717aa000001}
bcdedit /set %PE% device   ramdisk=[S:]\swim\winpe\<pe_ver>\boot.wim,{ramdiskoptions}
bcdedit /set %PE% osdevice ramdisk=[S:]\swim\winpe\<pe_ver>\boot.wim,{ramdiskoptions}
bcdedit /set %PE% path \windows\system32\boot\winload.efi
bcdedit /set %PE% systemroot \windows
bcdedit /set %PE% winpe yes
bcdedit /set %PE% detecthal yes

:: 4) boot into PE "once" — if PE does not come up, the machine returns to the old Windows by itself
bcdedit /bootsequence %PE%

:: 5) write state=handoff, flush the outbox, reboot
shutdown /r /t 0
```

> S: must be mounted on a temporary letter while BCD is configured — BCD stores the partition identity, not the letter.

## 4. Steps in WinPE (swim-pe)

```
startnet.cmd → wpeinit → swim-pe.exe
```

### 4.1 pe_verify (still reversible)
1. Find S: by GPT GUID → read `state.json` + the manifest + verify the signature (public key embedded in boot.wim)
2. Find every target partition by GUID — not found = `aborted` (usually a missing storage driver)
3. Verify the SHA256 of the **whole** image (or of every chunk)
4. Check AC power again
5. (With network) ask the server whether the kill switch is off — if on = `aborted`
6. Any failure → write the log + `aborted` → `wpeutil reboot` (the bootsequence is used up → old Windows)

### 4.2 PONR
```
1. bcdedit /default {SWIM-PE}          ← a power cut after this = boots back into PE and continues
2. state.phase = "applying", ponr_passed_at = now   (written before formatting)
3. format C: (selected by GUID)  →  format fs=ntfs quick label=OS
```
- `repartition` mode (set in the manifest): deletes / creates only partitions located **before** S: per the disk profile rule — S:, the ESP and WinRE are untouched

### 4.3 applying (entirely repeatable — a retry formats again first)
```
1. apply:    wimlib-imagex apply image.wim 1 W:\ [--ref=base.wim]   (or dism /Apply-Image)
2. drivers:  dism /Image:W:\ /Add-Driver /Driver:S:\swim\drivers\<pack>\ /Recurse
3. unattend: write W:\Windows\Panther\unattend.xml (hostname, locale, timezone, OOBE skip, LAPS policy)
4. ODJ:      djoin /requestodj /loadfile <blob> /windowspath W:\Windows   → delete the blob at once
5. agent:    place the latest published agent (D-18) offline — no MSI run inside WinPE:
             a. copy swim-agent.exe → W:\Program Files\SWIM\
             b. reg load HKLM\SWIMSYS W:\Windows\System32\config\SYSTEM → create ControlSet001\Services\SWIMAgent
                (Type=0x10, Start=2, ErrorControl=1, ImagePath="…\swim-agent.exe" run, ObjectName=LocalSystem) → reg unload
             c. write W:\ProgramData\SWIM\bootstrap.json = {server, cert_sha256, reimage_ticket} (ACL: SYSTEM+Administrators)
             d. never copy the old state.json — the new Windows' DPAPI cannot open it anyway, and the clone guard would discard it
             → the new Windows boots: the service starts → enrolls with the ticket → same host_id → deletes bootstrap.json (H-01)
6. boot:     W:\Windows\System32\bcdboot.exe W:\Windows /s <ESP> /f UEFI   ← uses the new image's own bcdboot
7. find {new-windows}: bcdboot already makes the new entry the default → read `bcdedit /store <ESP>\EFI\Microsoft\Boot\BCD /v /enum {default}`
   and extract the GUID with the regex `\{[0-9a-fA-F-]{36}\}` (no text parsing) → store it in state.json
8. set up the "trial" boot (every command in WinPE uses /store of the ESP):
   bcdedit /default {SWIM-PE}  +  bcdedit /bootsequence {new-windows}
9. state.phase = "first_boot", boot_attempts = 1 → reboot
```
> `bcdboot` migrates existing entries by default (no `/c`) → the `{SWIM-PE}` and `{ramdiskoptions}` entries remain.

Any failing step → retried up to **2 times** → still failing → `fallback_apply` (§6).

## 5. Boot supervisor (after apply)

```
                   default = {SWIM-PE}   bootsequence = {new-windows}
                                   │
               ┌──── new Windows boots ────┐
               │                           │
     new agent passes the health check   BSOD / hang / Setup reboots half-way
               │                           │
     bcdedit /default {current}             ▼
     delete the SWIM-PE entry           falls back to WinPE (because default = PE)
     state = completed ✅                   │
                                  swim-pe reads ImageState from the offline registry:
                                  HKLM\SOFTWARE\Microsoft\Windows\CurrentVersion\Setup\State\ImageState
                                           │
                       ┌───────────────────┼───────────────────────────────┐
           IMAGE_STATE_SPECIALIZE_*  /     IMAGE_STATE_COMPLETE but          boot_attempts > 3
           UNDEPLOYABLE (setup running)    not committed (health failed)     or ImageState unchanged twice in a row
                       │                   │                                │
           boot_attempts++                 boot_attempts++                  ▼
           bootsequence → new Windows      if ≤ 3 → try booting again       fallback_apply (N-1)
```

Windows Setup reboots by itself between specialize and OOBE, so `ImageState` must be read before deciding that the boot failed.

### Health check (new agent, within 20 minutes after OOBE ends)

| ID | Check | Mandatory |
|---|---|---|
| H-01 | the agent re-enrolled (same host_id) | ✅ (with network; without → passes conditionally) |
| H-02 | no BugCheck event since boot | ✅ |
| H-03 | Domain: `Test-ComputerSecureChannel` = True (when the job has a domain) | ✅ |
| H-04 | no device of class `DiskDrive`, `SCSIAdapter`, `Net` with ProblemCode ≠ 0 | ✅ |
| H-05 | network reachable (for wired machines per inventory) | WARN |
| H-06 | activation (`LicenseStatus`) | WARN (re-checked within 24 hours) |
| H-07 | BitLocker started encrypting + key escrow succeeded (when policy requires) | WARN → ERROR if not done in 2 hours |

All mandatory checks pass → **commit**: `bcdedit /default {current}`, `bcdedit /delete {SWIM-PE}`, `{ramdiskoptions}` is kept for next time, state = `completed`.

## 6. Fallback / rollback

### fallback_apply (automatic)
- Condition: apply failed 3 times, or the boot supervisor decided the boot failed
- Action: format C: → apply **N-1** from S: → the same steps as §4.3 with the manifest's `fallback` section → the same supervisor
- Success → `rolled_back` (usable + the full log goes to the server as a **FATAL** of the new image on this model)
- N-1 fails too, or there is no N-1 on S: → `failed`: stays in WinPE showing "Contact IT + job_id + error code + a QR code of the job" and reports when a network is available

### rollback (ordered by a person — a new job of type `rollback`)
- Deploys image N-1 through every normal step (preflight, user notification) — uses the files on S:, no download
- Used for fleet rollback ([07](07-fleet-safety.md))

## 7. Engine A — `setup.exe /auto clean` (PoC / fallback)

```bat
setup.exe /auto clean /quiet /eula accept /InstallFrom S:\swim\images\<id>\image.wim ^
  /Unattend:S:\swim\jobs\<job>\unattend.xml /InstallDrivers S:\swim\drivers\<pack> ^
  /DynamicUpdate disable /Compat IgnoreWarning /PostOOBE S:\swim\agent\install-agent.cmd
```
- Requires the `setup.exe` from an **ISO of the same Windows release as the image** (stored as a separate artifact of the channel)
- Setup manages BCD and rollback itself until its own commit point — result mapping: setup fails before commit = `aborted`; after commit = `failed`
- Limitations: slower, cannot repartition, lacks our boot supervisor, and it must be tested whether Setup accepts an older version / a different edition

## 8. Post-deploy

| Topic | How |
|---|---|
| Domain join | The ODJ blob is created on the Utility VM: `djoin /provision /domain <d> /machine <name> /machineou <ou> /reuse /savefile <f>` with a **dedicated join account** listed in the policy *"Domain controller: Allow computer account re-use during domain join"* (required since the KB5020276 hardening) |
| Secrecy of the ODJ blob | See [08 §5](08-security.md) — v1: valid 24 hours, single-use, deleted right after use; if the job is cancelled or expires the server resets the machine account password; v2: sealed with the TPM |
| Local admin | **Windows LAPS** (policy through GPO / Intune); the unattend file has no password, the built-in Administrator is disabled |
| BitLocker | Enabled by policy (TPM protector), escrow to AD (`manage-bde -protectors -adbackup`) or Entra (`BackupToAAD-BitLockerKeyProtector`) — the agent reports the escrow result; old recovery objects under the AD computer object older than 30 days are removed by a clean-up job |
| Activation | Per [02 §8](02-image-pipeline.md) |
| Applications | Through the organisation's Intune / winget / SCCM (out of scope for SWIM v1) |

## 9. ETA and risk before starting

### ETA

```
ETA = download_remaining_bytes / measured_throughput(host, 7 days, after throttling)
    + P50(apply_seconds | model, image_size_bucket)
    + P50(first_boot_seconds | model, channel)
    + 120s (reboot ×2)
P50 and P90 are both shown (P90 of each part instead of P50)
no history → defaults apply = image_expanded_GB × 25s, first_boot = 600s, labelled "no data yet"
```

### Risk score

```
success_rate = (successes + 1) / (total + 2)      # key: (vendor, model, bios_version, image_id); falls back to (model, channel) → (model)
risk = HIGH   on any BLOCK, or success_rate < 0.80, or this model has not passed this image in a canary wave yet
       MEDIUM with ≥ 2 WARN or 0.80 ≤ success_rate < 0.95
       LOW    otherwise
HIGH → the deploy button is disabled except for ring canary or an authorised override (audited)
```

Example shown before confirmation:

```
BKK-FIN-042 · Dell Latitude 5440 · BIOS 1.18 · win11-x64-stable v1.6
Preflight   ✅ AC  ✅ UEFI/GPT  ✅ SecureBoot  ✅ 512 GB  ⚠ BitLocker (will be suspended)  ✅ Driver matched
Estimate    downloaded 100% · install ~14 min · first boot ~9 min → ~25 min (P90 33 min)
History     this model + this image: 47/48 succeeded (1 failed: power cut → rolled_back)
Risk        LOW
```

## 10. Per-step timeouts (watchdog)

| Step | Timeout | When exceeded |
|---|---|---|
| preflight | 10 minutes | `blocked` (error: timeout) |
| staging | unlimited while progressing — no progress for 2 hours | retry another source |
| handoff → pe_verify | ETA P90 × 1.5 | server: `suspected_failure` |
| pe_verify | 20 minutes | `aborted` |
| applying | max(apply P90 × 3, 45 minutes) | retry → fallback |
| first_boot → health_check | 60 minutes per boot attempt | the supervisor decides |
