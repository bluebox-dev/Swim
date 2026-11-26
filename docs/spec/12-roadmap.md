# 12 · Roadmap & Test Matrix

**English** · [ไทย](12-roadmap.th.md)

## Development phases

| Phase | Scope | Exit criteria |
|---|---|---|
| **0 · Decisions & lab** | Pick the 3 most common test models, set up the Ubuntu server + KVM + one Golden Master + the Utility VM, confirm D-01…D-19, request the AD policies (djoin account + re-use allowlist), agree on BitLocker for S: | decision document signed, lab ready |
| **1 · Core PoC (scripts)** | WinPE from the latest ADK (+VMD driver), WIM capture with wimlib, staging / BCD / `worker.ps1` scripts following the state machine ([05](05-deployment-flow.md)), boot supervisor, N-1 fallback, ODJ v1, Engine A for comparison | **20 consecutive flawless runs on all 3 models** + every chaos test C-01…C-08 passes |
| **2 · Agent + swim-pe (Go)** | `pkg/manifest`, `pkg/state`, `pkg/chunks`; `swim-svc` + `swim-agent` + `swim` CLI + `swim-pe`; preflight, staging, netcond, outbox, watchdog, local deploy from a manifest file (no server yet — manifests signed with a test key); **self-update + auto-rollback against a mock release server** | Phase 1 results reproduced with binaries + U-01…U-08 pass |
| **3 · Control plane** | backend + DB + minimal admin UI: hosts, enrollment, images (manual WIM upload allowed), two-person approval, jobs, rollouts + waves, concurrency, kill switch, breaker, agent releases, audit, RBAC + SSO, PKI (step-ca), manifest signer, log bundles | end-to-end operation from the web + internal pentest passed + pilot on 20–50 real machines |
| **4 · Scale & quality** | driver matcher + driver pack lifecycle, pre-staging, delta WIM + rebase, site cache, ETA / risk, capacity planning on both sides, BitLocker escrow / clean-up, ODJ v2 (TPM seal), SWIM USB, monitoring + alerts, backup / DR + restore rehearsal | first whole-site rollout ≥ 98% successful, DR drill passed |
| **5 · Web Builder** | libvirt control, Guacamole in the console, the Build button (snapshot → sysprep → capture → sign → revert), automated image tests (T-01…T-09) on the Test Runner VM | build → test → pending automatically, without SSH |

> Agent self-update is in Phase 2 (not a late phase) because it must exist **before** the first agent reaches a user's machine — otherwise there would be no way to patch the first agent.

## Chaos tests (mandatory in Phase 1, automated on VMs from Phase 2)

| ID | Action | Required result |
|---|---|---|
| C-01 | Pull the plug / power off the VM during download | resumes from the same chunk |
| C-02 | Pull the plug while shrinking C: / creating S: | the old Windows boots, the agent continues or reports |
| C-03 | Pull the plug during `handoff` (after BCD is set) | boots into PE or back to the old Windows — never stuck |
| C-04 | Remove the storage driver from boot.wim | `aborted` → returns to the old Windows by itself |
| C-05 | Pull the plug during format / apply | boots back into PE → continues to success |
| C-06 | Corrupted image (1 byte changed) | detected in `pe_verify` → `aborted` |
| C-07 | An image that blue-screens on first boot | supervisor → fallback N-1 → `rolled_back` |
| C-08 | Kill switch during `waiting` / `pe_verify` | `cancelled` / `aborted` |
| C-09 | No network for the whole WinPE phase | success, summary sent afterwards |
| C-10 | Server down during a rollout | jobs past the PONR finish by themselves, the outbox is fully delivered on return |

## Agent update tests (from Phase 2, every release in CI)

| ID | Action | Required result |
|---|---|---|
| U-01 | Update N-1 → N | committed within 10 minutes |
| U-02 | Update N-2 → N (skipping a version) | success |
| U-03 | The new version crashes at start | back to the old version + `BlockedVersions` + report |
| U-04 | The new version fails its self-test (DB migration) | back to the old version, the outbox still works |
| U-05 | Wrong `release.json` signature / exe without Authenticode | refused, no switch |
| U-06 | Downgrade without a directive | refused |
| U-07 | svc updated and the new svc does not start | `SWIMRescue` recovers within 6 hours (or at the next reboot) |
| U-08 | A job is in state `handoff` | no update (deferred) |

## Hardware test matrix (minimum)

| Dimension | Values to cover |
|---|---|
| Model | the 3 most common models + the newest model purchased |
| Storage | plain NVMe, Intel VMD enabled, two disks |
| Firmware | Secure Boot on (with the complete CVE-2023-24932 mitigations) / off |
| BitLocker | off / TPM-only / TPM+PIN |
| Power | AC / battery only (must be blocked) |
| Network | LAN / Wi-Fi / VPN / hotspot (metered) |
| Disk size | 128 / 256 / 512 GB / 1 TB |
| Windows language | th-TH and en-US (guards against parsing text) |

## Definition of done (every feature)

- Matches the specification — if not, the specification is changed in the same PR
- Unit tests exist for the state transitions involved
- New error codes are added to `pkg/state/errors.go` + the docs
- The events and metrics needed for ETA / risk / breaker are all emitted
- The audit log is complete for state-changing actions
