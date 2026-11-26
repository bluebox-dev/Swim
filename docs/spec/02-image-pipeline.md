# 02 · Image Pipeline & Lifecycle

**English** · [ไทย](02-image-pipeline.th.md)

## 1. Image state cycle

```
 draft ──build──► building ──capture ok──► testing ──pass──► pending ──approve──► published ──(replaced)──► superseded ──retention──► archived ──► deleted
                     │                        │                          │
                     └──fail──► failed        └──fail──► test_failed     └──reject──► rejected

 published ──fleet rollback──► withdrawn   (must not be deployed; files kept for analysis — see 07 §6)
```

| State | Meaning | Deployable? |
|---|---|---|
| `building` | sysprep / capture in progress | ❌ |
| `testing` | tests running on the Test Runner VM | ❌ |
| `pending` | tests passed, waiting for an approver | ring `canary` only |
| `published` | in production — **one per channel** | ✅ |
| `superseded` | replaced, but still **N-1** for rollback | ✅ (rollback only) |
| `archived` | files kept but unused | ❌ |

Rules:
- Partial unique index: `UNIQUE (channel_id) WHERE status = 'published'`
- **Builder and approver must be different people** (checked by the backend)
- Files may be deleted only when no unfinished job refers to them and they are not N-1 of any channel

## 2. Channels

| Field | Example |
|---|---|
| `name` | `win11-x64-stable` |
| `os` / `release` | `windows11` / `24H2` |
| `arch` | `amd64` (`arm64` is a separate channel) |
| `edition` | `Enterprise` / `Pro` (must match the machine's licence — see §8) |
| `eol_date` | Microsoft's end-of-support date — warnings at 180 / 90 / 30 days |

A host belongs to one channel (by architecture and department policy).

## 3. Golden Master rules (mandatory — checked automatically by the Test Runner)

### Must not be in the image
- [ ] Domain membership, cached credentials, or any user profile other than Default
- [ ] **Agents that generate a per-machine ID at install time** — EDR/AV onboarding (Defender for Endpoint, CrowdStrike, SentinelOne …), SCCM client, Intune enrollment, monitoring agents → if they must be present, use the vendor's **golden image / VDI mode** only
- [ ] `HKLM\SOFTWARE\Microsoft\Windows\CurrentVersion\WindowsUpdate\SusClientId`
- [ ] An enrolled `swim-agent` (WinPE injects the latest one — D-18)
- [ ] pagefile, hiberfil, temp files, Delivery Optimization cache
- [ ] Retail licence keys of machine-bound software

### Before sysprep
1. Work in **Audit Mode** (Ctrl+Shift+F3 at OOBE)
2. Turn off Microsoft Store auto-update (policy) during the build — prevents Appx errors in sysprep
3. Install the latest Windows Updates, then `Dism /Online /Cleanup-Image /StartComponentCleanup /ResetBase`
4. VirtIO drivers may be installed (harmless on real hardware), but **never use `sysprep /mode:vm`**
5. Turn BitLocker off in the reference VM
6. Take a `pre-sysprep-<version>` snapshot **every time**

## 4. Build pipeline (the "Build image" button)

```
1. preflight-capacity     check free space (§7) — not enough = blocked
2. snapshot               virsh snapshot-create-as master pre-sysprep-<ver>
3. sysprep                sent through the QEMU guest agent:
                          sysprep /generalize /oobe /shutdown /unattend:C:\swim\sysprep-unattend.xml
4. wait-shutoff           30 min timeout → fail if it does not power off
5. capture                space preflight: WIM (previous size +10% or 20 GB) + 20 GB overlay + ≥ 5 GB left
                          qemu-img create -b master.qcow2 capture-<id>.qcow2 (overlay — the master is never written)
                          reseal: if State.ini is not IMAGE_STATE_GENERALIZE_RESEAL_TO_OOBE → plant a one-shot script
                            (HKLM\SYSTEM\Setup CmdLine/SetupType=2 → sysprep /generalize /oobe /shutdown /quiet)
                            and boot the overlay as a temporary VM (no network, its own NVRAM/TPM), wait ≤ 45 min for power-off
                            failure → fail with setuperr.log (works even for a Golden Master without the QEMU guest agent)
                          qemu-nbd -c /dev/nbdX capture-<id>.qcow2
                          expand CompactOS/WOF files (reparse 0x80000017) into normal files: ntfs-unwof (libntfs-3g)
                            + the XPRESS decoder in pkg/wof, PE checksum verified for every file before writing
                            reason: WinPE does not attach wof.sys to a new volume → DISM/bcdboot cannot run from the image
                          offline checks (image_tests): S-01 generalized · S-02 boot/DISM/bcdboot files are normal files
                            · S-03 no SWIM agent identity · S-04 no plain-text password in unattend
                            · S-05 no WinSxS\pending.xml · S-06 pagefile/hiberfil (not captured) — S-01/S-02 failing = fail
                          wimcapture /dev/nbdXpN image.wim "<channel> <ver>" --compress=LZX --check
                          expanded_bytes = Total Bytes − Hard Link Bytes (WinSxS hard links are not counted twice)
                          images.wof_files = 0 (older images where it is NULL are refused by the server at install time)
                          delete the overlay
                          (with a base: --delta-from=<base>.wim — see §6)
6. chunk+hash             split into 64 MiB chunks, SHA256 per chunk + whole file → chunks.json
7. sign                   sign chunks.json with the release key (08-security)
8. revert                 virsh snapshot-revert master pre-sysprep-<ver>
9. → testing
```

Every step writes `image_build_events` and shows a live log on the Builder page.

## 5. Automated image test (on the server)

Runs on **two Image Test Runner VMs** (128 GB and 512 GB disks) using the **entire real deployment path**, not just booting the image.

```
reset VM → snapshot "old-windows + swim-agent(current) + BitLocker on (vTPM)"
→ create a normal job through the API (ring = test) → agent stages → WinPE → apply → OOBE → health
→ collect results → reset VM
```

| Test | Passes when |
|---|---|
| T-01 Deploy end-to-end | job = `completed` within 60 minutes |
| T-02 Disk profiles | both disk sizes get the layout the rules require |
| T-03 Boot supervisor | the new Windows commits, `ImageState = IMAGE_STATE_COMPLETE` |
| T-04 Agent | the new agent re-enrolls with the same host_id, version = published agent |
| T-05 Domain join | (if the channel sets a domain) joined, secure channel OK (`Test-ComputerSecureChannel`) |
| T-06 Duplicate ID | no old `SusClientId`, no values from the configured EDR/SCCM ID denylist |
| T-07 BitLocker | encrypted per policy + key escrow succeeded |
| T-08 Rollback | a rollback command brings N-1 back (only when N-1 exists) |
| T-09 Size | expanded size < the channel's `min_disk_gb` − 20 GB headroom |

Every result is stored in `image_tests` with the full log attached; all passing → `pending`.

> A VM **cannot test real hardware drivers** → the image must pass a `canary` wave on at least one real machine per model before `broad` (see [07](07-fleet-safety.md)).

## 6. Delta images and rebase

- Monthly releases are built as a delta from the latest **base** (`--delta-from`) — clients download only the delta when the base is already on S:
- Applied with `wimlib-imagex apply image.wim --ref=base.wim` in WinPE (wimlib is embedded in `boot.wim`)
- **Rebase every 3–6 months**, or when the delta exceeds 40% of the base → build a new full image as the base
- Never delete a base that a published or superseded delta still refers to

## 7. Capacity planning (the server always knows first)

### Server side — checked before a build

```
estimated new image size  = used_bytes(Golden Master C:) × compression_ratio (starts at 0.5, tuned from history)
space needed              = new image + snapshot overhead (history, starts at 15 GB) + test artifacts (5 GB)
left after build          = free − space needed
```

| Condition | Result |
|---|---|
| ≥ 80% used | Dashboard warning + a list of archived images that can be deleted |
| predicted ≥ 90% after the build | **build blocked** |
| Forecast | shows "full in about N releases" from the average of the last 3 |

Retention policy (per channel): `published` + `superseded (N-1)` + `pending` + pinned → everything else is `archived` automatically; `archived` can be deleted after `archive_days` (default 90).

### Client side — checked before approve / publish

```
S: must hold ≥ new image (or delta) + N-1 + boot.wim + drivers + 2 GB
```

Before approval the console shows:
> v1.6 (9.1 GB) — 1,240 machines ready · **12 machines with S: too small** (cannot roll back offline / S: must grow) · [show list]

The agent can grow S: by itself when C: has enough free space (see [03 §6](03-agent.md)) — otherwise that machine must download N-1 over the network at rollback time.

## 8. Licensing & activation

| Type | After deployment |
|---|---|
| OEM key in firmware (MSDM) | activates by itself **if the edition matches** |
| KMS / AD-Based Activation | activates when on the corporate network |
| Windows Enterprise E3/E5 (subscription) | activates when the user signs in with an Entra account |
| OEM Home but a Pro/Enterprise image | ❌ — preflight must warn (the agent reads `SoftwareLicensingService.OA3xOriginalProductKeyDescription`) |

- Software that needs re-activation → handled by the user or a technician (D-09)
- The agent reports the activation state after installation (`LicenseStatus`) as **WARN** if not activated within 24 hours

## 9. WinPE (boot.wim) pipeline

- Built on the **Windows Utility VM** with the **latest** ADK + WinPE add-on (supports a boot chain signed by Windows UEFI CA 2023)
- Optional components: `WinPE-WMI`, `WinPE-StorageWMI`, `WinPE-SecureStartup`, `WinPE-EnhancedStorage`, `WinPE-PowerShell` (+ `NetFx`) for Phase 1
- Injected drivers: **storage (Intel VMD/RST, AMD RAID)** for every model in the organisation + basic NICs
- Embedded: `swim-pe.exe`, `wimlib-imagex.exe`, the internal root CA, the public key that verifies manifests
- `boot.wim` has its own version (`pe 1.x`) and goes through the same pipeline as an image: build → test → publish
