# 01 · Architecture

**English** · [ไทย](01-architecture.th.md)

## 1. Overview

```
                         ┌──────────────────────── SWIM Server (Ubuntu 24.04, bare metal) ────────────────────────┐
                         │                                                                                        │
 Admin (browser) ──HTTPS─┼─► nginx ──► frontend (React)                                                           │
                         │     │                                                                                  │
                         │     ├────► backend (Go) ──► PostgreSQL                                                 │
                         │     │        │   │                                                                     │
                         │     │        │   └──► libvirt (KVM) ──┬─ VM: Golden Master (one per channel)          │
                         │     │        │                        ├─ VM: Image Test Runner (real deploy tests)     │
                         │     │        │                        └─ VM: Windows Utility (ADK/DISM/djoin, AD-joined)│
                         │     │        └──► job runner ── capture (wimlib/qemu-nbd) ── sign ── storage            │
                         │     ├────► guacd ──VNC──► QEMU console of the VMs                                       │
                         │     └────► /storage (X-Accel-Redirect, HTTP Range)                                      │
                         └────────────────────────────────────────────────────────────────────────────────────────┘
                                   ▲  HTTPS + mTLS (the agent pulls; no port is open on the client)
             ┌─────────────────────┼───────────────────────────┐
             │ (optional) Site Cache │ nginx proxy_cache per site │
             └─────────────────────┼───────────────────────────┘
                                   │
   ┌───────────────────────────────┴───────────── Client (Windows 10/11) ───────────────────────────────┐
   │  swim-svc (supervisor/updater) ──► swim-agent (core)  ──► S:\swim\  (images, winpe, drivers, state) │
   │                                                     └──► BCD ──► WinPE (swim-pe) ──► new Windows    │
   └────────────────────────────────────────────────────────────────────────────────────────────────────┘
```

## 2. Components

| Component | Language / tools | Role |
|---|---|---|
| `backend` | Go, PostgreSQL | API, job queue (`FOR UPDATE SKIP LOCKED`), manifest signing, rollout controller, circuit breaker |
| `frontend` | React + TypeScript | Dashboard, Builder (embedded Guacamole), Release Manager, Host Inventory, Rollout |
| `builder` | Go + shell | Drives libvirt, snapshots, captures with wimlib, tests images, computes hashes and chunks |
| `utility-runner` | PowerShell on the Windows Utility VM | Builds WinPE, DISM servicing, `djoin /provision /reuse` — pulls work from the backend over an outbound API |
| `swim-svc` | Go | A small Windows service: watches and starts `swim-agent`, performs self-update and auto-rollback (see [04](04-agent-updates.md)) |
| `swim-agent` | Go | Telemetry, preflight, staging, BCD, command handling, reporting, local CLI |
| `swim-pe` | Go (embedded in `boot.wim`) | Worker inside WinPE: verify → PONR → apply → drivers → ODJ → inject agent → bcdboot → boot supervisor |
| `swim` (CLI) | Go | Local commands on the machine — talks to the agent over a named pipe restricted to local administrators |

> Note: Phase 1 may use `worker.ps1` instead of `swim-pe`, but the state machine must match [05](05-deployment-flow.md).

## 3. Server — sizing and installation

### Minimum hardware

| Item | Minimum | Recommended |
|---|---|---|
| CPU | 8 cores, VT-x/AMD-V | 16 cores |
| RAM | 32 GB | 64 GB (Golden Master 8 GB + Test Runner 8 GB × 2 + Utility 4 GB + Docker) |
| Storage (OS) | 256 GB SSD | |
| Storage (data) | 1 TB SSD | 2 TB NVMe, ZFS or LVM-thin for snapshots |
| NIC | 1 GbE | 10 GbE above 500 clients |

### Host software

- Ubuntu 24.04 LTS, `qemu-kvm`, `libvirt-daemon-system`, `ovmf` (Secure Boot vars), `swtpm`
- `wimtools` (wimlib), `qemu-utils` (`qemu-nbd`), `ntfs-3g`
- Docker Engine + Compose plugin
- **Do not run Ubuntu as a nested VM** in production

### Docker Compose services

```
nginx      : 443 → frontend, /api → backend, /storage → X-Accel-Redirect, /guac → guacd
backend    : Go API + job runner (privileged access to the libvirt socket through a bind mount)
frontend   : static build served by nginx
postgres   : 16.x, separate volume, WAL archive (see 09-operations)
guacamole  : guacamole/guacd + guacamole web (uses the backend's auth through the JSON auth extension)
```

### VMs on the server

| VM | Count | Firmware | Disk | Notes |
|---|---|---|---|---|
| Golden Master | 1 per channel | OVMF + Secure Boot + swtpm | qcow2 80–100 GB | snapshots `clean-base`, `pre-sysprep-<ver>` |
| Image Test Runner | 2 (128 / 512 GB disks) | OVMF + SB + swtpm | qcow2 | snapshot `old-windows+agent+bitlocker` |
| Windows Utility | 1 | OVMF | 120 GB | AD-joined, latest ADK + WinPE add-on, uses a gMSA or a limited account for djoin |

## 4. Network flows

| From → to | Protocol | Notes |
|---|---|---|
| Agent → Server | HTTPS 443 + mTLS | long-poll, heartbeat, events, downloads |
| Agent → Site Cache | HTTPS 443 | files only (chunk hashes are verified) |
| Agent → Peer (optional, Phase 4) | HTTPS within the same subnet | serves verified chunks only |
| Utility VM → Server | HTTPS (pulls work) | no inbound port |
| Utility VM → AD DC | LDAP / Kerberos | for djoin |
| Admin → Server | HTTPS + SSO (OIDC / LDAP) | |
| WinPE → Server | HTTPS (best effort) | optional — WinPE works offline |

## 5. Monorepo layout

```text
swim-platform/
├── docs/spec/                 # these documents
├── pkg/                       # shared Go (imported by backend, agent and pe)
│   ├── manifest/              # structs + validation + sign/verify (Ed25519) + seal/open (X25519)
│   ├── state/                 # the machine's state.json + transitions (source of truth of the state machine)
│   ├── chunks/                # chunk manifest, hashing, resume
│   ├── proto/                 # API DTOs + version negotiation
│   └── logx/                  # structured logging + severity
├── backend/
│   ├── cmd/server/
│   ├── internal/{api,db,rollout,breaker,builder,signing,storage,odj,auth,audit}/
│   └── migrations/
├── frontend/
├── agent/
│   ├── cmd/{swim-svc,swim-agent,swim}/
│   └── internal/{control,worker,outbox,watchdog,preflight,staging,bootmgr,netcond,telemetry,update,localipc}/
├── pe/
│   ├── cmd/swim-pe/
│   ├── winpe/build-winpe.ps1  # runs on the Utility VM
│   └── winpe/startnet.cmd
├── builder-infra/
│   ├── docker-compose.yml
│   ├── nginx/
│   ├── libvirt/               # VM template XML
│   └── scripts/{capture-wim.sh,test-image.sh}
└── utility-runner/            # PowerShell agent on the Windows Utility VM
```

## 6. Versioning

- Everything uses SemVer: `agent 1.4.2`, `pe 1.4.0`, `manifest schema 1`, `api v1`
- The server supports agents back to **N-2 minor** versions (see [04](04-agent-updates.md))
- `state.json` carries a `schema_version` — `swim-pe` must read the schema the agent wrote (compatibility matrix in [04 §8](04-agent-updates.md))
