# SWIM — Staged Windows Image Manager · Specification

**English** · [ไทย](README.th.md)

> Status: **Draft v0.1** · Updated: 2026-11-23
> These documents are the contract that the Server, the Agent and the WinPE worker share.
> If the code contradicts the specification, change the specification first and then the code.
>
> The specification describes the target design. Parts of it are not built yet; the current
> implementation is summarised in the [project README](../../README.md).

## Contents

| # | Document | Covers |
|---|---|---|
| 01 | [architecture.md](01-architecture.md) | System overview, the Ubuntu server, VMs, tech stack, monorepo layout |
| 02 | [image-pipeline.md](02-image-pipeline.md) | Golden image rules, Build → Test → Approve → Publish, channels, lifecycle and capacity, licensing |
| 03 | [agent.md](03-agent.md) | Agent architecture, first installation, machine identity, commands, preflight, staging, network conditions |
| 04 | [agent-updates.md](04-agent-updates.md) | **Continuous agent updates and patching** (self-update, rings, auto-rollback) |
| 05 | [deployment-flow.md](05-deployment-flow.md) | State machine, BCD, WinPE, point of no return, boot supervisor, rollback, post-install, ETA |
| 06 | [manifest.md](06-manifest.md) | Job manifest format, signing, secret encryption, expiry |
| 07 | [fleet-safety.md](07-fleet-safety.md) | Waves, concurrency limits, kill switch, circuit breaker, fleet rollback |
| 08 | [security.md](08-security.md) | Threat model, PKI and certificates, signing keys, RBAC, audit, AD / BitLocker / LAPS |
| 09 | [operations.md](09-operations.md) | Logs and telemetry, monitoring, backup and DR, fallback USB, re-enrollment, Windows lifecycle |
| 10 | [data-model.md](10-data-model.md) | PostgreSQL schema |
| 11 | [api.md](11-api.md) | REST API (agent API and admin API) |
| 12 | [roadmap.md](12-roadmap.md) | Development phases, exit criteria, test matrix |

## Decision log

| ID | Decision | Short rationale |
|---|---|---|
| D-01 | Server = **Ubuntu 24.04 LTS on bare metal** + KVM/libvirt | Inexpensive, a good fit for Docker, nginx and Guacamole |
| D-02 | A **Windows Utility VM** on the server for ADK / DISM / djoin | These jobs cannot be done on Linux |
| D-03 | What is sent to clients is a **WIM** (not an ISO) + `boot.wim` + a driver pack | WIM is file-based and fits any disk size |
| D-04 | Deploy engine: **A = `setup.exe /auto clean`** (PoC / fallback), **B = WinPE + DISM** (production) | A proves the concept quickly, B gives full control |
| D-05 | **User data on C: is lost** — accepted | Organisation policy; users must be told and it must be audited |
| D-06 | **Rollback = reinstall image N-1** from the cache on S: (not restoring the previous machine state) | Consistent with D-05, needs no network |
| D-07 | **S: (staging) is a permanent partition** large enough for the current image + N-1 | Supports D-06 and avoids shrinking C: repeatedly |
| D-08 | **Images are tested automatically on server VMs** before approval, plus a canary on at least one real machine per model | A VM cannot test real drivers |
| D-09 | Licensing: Windows activates by the machine's OEM / KMS / subscription; apps that need re-activation are the user's responsibility | Sysprep clears activation |
| D-10 | The image lifecycle is **governed by capacity** — the server must forecast before a build, for both the server and the clients' S: | Avoids running out of space half-way |
| D-11 | Logs: **a summary is always sent**, full logs only on FATAL/ERROR, the last 5 jobs are kept on the machine | Success-rate and ETA statistics need data from successful jobs too |
| D-12 | Fallback path: **a technician installs Windows from USB** and re-enrolls the agent; (optional) a SWIM USB | Simple, and technicians already know how |
| D-13 | Languages: **Go** for backend, agent and PE worker; **PostgreSQL**; **React** UI | Manifest and state types can be shared |
| D-14 | The agent accepts only **allowlisted commands** — no `run_command` | The agent runs as SYSTEM on every machine |
| D-15 | Domain join by **Offline Domain Join (ODJ) + `/reuse`**, local admin by **Windows LAPS** | No password is left on disk |
| D-16 | Manifests are **signed with Ed25519**, **expire**, and secrets are **encrypted to the machine's key** | The kill switch works even for offline machines |
| D-17 | **The agent must be able to update and patch itself continuously** with rings, health gates and auto-rollback | A broken agent fleet-wide means losing control |
| D-18 | Every new Windows gets the **latest published agent** (injected by WinPE), not the one baked into the image | Prevents new machines from getting an old agent |
| D-19 | Patching Windows (Windows Update) **stays on the organisation's existing channel** (WSUS / WUfB / Intune) — out of scope for SWIM v1 | Keeps the scope small; can become a feature later |
| D-20 | **`run_script` is allowed only from a script library approved by two people + SHA-256** (amends D-14, which forbade it entirely) — free-form `run_command` / shell stays forbidden; every command is Ed25519-signed and bound to one machine | Users need the server to actually manage machines, but with safety layers ([03 §4](03-agent.md)) |

## Glossary

| Term | Meaning |
|---|---|
| **Golden Master** | The reference Windows VM on the server where programs are installed before sysprep |
| **Image** | The `.wim` file captured from the Golden Master (1 release = 1 image per channel) |
| **Channel** | A line of images such as `win11-x64-stable` — one published image per channel at a time |
| **Ring / Wave** | Groups of machines released in stages: `canary` → `pilot` → `broad` |
| **S: / Staging** | A permanent partition on the client that holds images, WinPE, drivers, logs and state |
| **PONR** | Point of No Return — the step that formats C:. After it the old Windows cannot be restored |
| **N-1** | The previous image, still cached on S: and used for rollback |
| **Manifest** | Server-signed JSON that tells the agent / WinPE what to do |
| **Boot supervisor** | WinPE's role after apply: it checks whether the new Windows boots successfully |
| **Outbox** | A local event queue (SQLite) waiting to be sent to the server |

## Seven design principles

1. **Before the PONR everything rolls back by itself** — after the PONR everything can be repeated (idempotent) until it succeeds or falls back to N-1.
2. **WinPE does not depend on the network** — everything it needs is already on S:.
3. **Nothing is unsigned** — images, manifests, agent binaries and WinPE are verified by signature or hash every time.
4. **No plain-text secrets on disk** (the one temporary exception is the ODJ blob in v1 — see [08 §5](08-security.md)).
5. **The system can stop itself before a human notices** — circuit breakers for both image deployment and agent updates.
6. **Every step is timed and measured** — so ETA and success rates come from real data.
7. **The agent must never hang** — long work is separate from communication, and every I/O has a timeout.
