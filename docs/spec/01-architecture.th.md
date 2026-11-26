# 01 · Architecture

[English](01-architecture.md) · **ไทย**

## 1. ภาพรวม

```
                         ┌──────────────────────── SWIM Server (Ubuntu 24.04, bare metal) ────────────────────────┐
                         │                                                                                        │
 Admin (Browser) ──HTTPS─┼─► nginx ──► frontend (React)                                                           │
                         │     │                                                                                  │
                         │     ├────► backend (Go) ──► PostgreSQL                                                 │
                         │     │        │   │                                                                     │
                         │     │        │   └──► libvirt (KVM) ──┬─ VM: Golden Master (ต่อ channel)              │
                         │     │        │                        ├─ VM: Image Test Runner (ทดสอบ deploy จริง)      │
                         │     │        │                        └─ VM: Windows Utility (ADK/DISM/djoin, join AD) │
                         │     │        └──► job runner ── capture (wimlib/qemu-nbd) ── sign ── storage            │
                         │     ├────► guacd ──VNC──► QEMU console ของ VM                                           │
                         │     └────► /storage (X-Accel-Redirect, HTTP Range)                                      │
                         └────────────────────────────────────────────────────────────────────────────────────────┘
                                   ▲  HTTPS + mTLS (Agent ดึงเอง ไม่มีพอร์ตเปิดที่เครื่องลูก)
             ┌─────────────────────┼───────────────────────────┐
             │ (ทางเลือก) Site Cache │ nginx proxy_cache ต่อสาขา   │
             └─────────────────────┼───────────────────────────┘
                                   │
   ┌───────────────────────────────┴───────────── เครื่องลูก (Windows 10/11) ─────────────────────────────┐
   │  swim-svc (supervisor/updater) ──► swim-agent (core)  ──► S:\swim\  (images, winpe, drivers, state) │
   │                                                     └──► BCD ──► WinPE (swim-pe) ──► Windows ใหม่    │
   └────────────────────────────────────────────────────────────────────────────────────────────────────┘
```

## 2. ส่วนประกอบ

| ส่วน | ภาษา/เครื่องมือ | หน้าที่ |
|---|---|---|
| `backend` | Go, PostgreSQL | API, คิวงาน (`FOR UPDATE SKIP LOCKED`), เซ็น manifest, rollout controller, circuit breaker |
| `frontend` | React + TypeScript | Dashboard, Builder (ฝัง Guacamole), Release Manager, Host Inventory, Rollout |
| `builder` | Go + shell | สั่ง libvirt, snapshot, capture ด้วย wimlib, ทดสอบ image, คำนวณ hash/chunk |
| `utility-runner` | PowerShell บน Windows Utility VM | Build WinPE, DISM servicing, `djoin /provision /reuse` — รับงานจาก backend ผ่าน API ขาออก (pull) |
| `swim-svc` | Go | Windows service ตัวเล็ก: เฝ้า/สตาร์ต `swim-agent`, ทำ self-update + auto-rollback (ดู [04](04-agent-updates.th.md)) |
| `swim-agent` | Go | Telemetry, preflight, staging, BCD, รับคำสั่ง, รายงานผล, local CLI |
| `swim-pe` | Go (ฝังใน `boot.wim`) | Worker ใน WinPE: verify → PONR → apply → drivers → ODJ → inject agent → bcdboot → boot supervisor |
| `swim` (CLI) | Go | คำสั่งฝั่งเครื่อง (local) — คุยกับ agent ผ่าน named pipe ที่จำกัดสิทธิ์ Local Admin |

> หมายเหตุ: Phase 1 ใช้ `worker.ps1` แทน `swim-pe` ได้ แต่ state machine ต้องตรงกับ [05](05-deployment-flow.th.md)

## 3. Server — สเปกและการติดตั้ง

### ฮาร์ดแวร์ขั้นต่ำ

| รายการ | ขั้นต่ำ | แนะนำ |
|---|---|---|
| CPU | 8 cores, VT-x/AMD-V | 16 cores |
| RAM | 32 GB | 64 GB (Golden Master 8 GB + Test Runner 8 GB × 2 + Utility 4 GB + Docker) |
| Storage (OS) | 256 GB SSD | |
| Storage (data) | 1 TB SSD | 2 TB NVMe, ZFS หรือ LVM-thin สำหรับ snapshot |
| NIC | 1 GbE | 10 GbE ถ้าเครื่องลูก > 500 |

### Software บน host

- Ubuntu 24.04 LTS, `qemu-kvm`, `libvirt-daemon-system`, `ovmf` (Secure Boot vars), `swtpm`
- `wimtools` (wimlib), `qemu-utils` (`qemu-nbd`), `ntfs-3g`
- Docker Engine + Compose plugin
- **ห้ามรัน Ubuntu เป็น VM ซ้อน** (nested virtualization) ใน production

### Docker Compose services

```
nginx      : 443 → frontend, /api → backend, /storage → X-Accel-Redirect, /guac → guacd
backend    : Go API + job runner (privileged access to libvirt socket ผ่าน bind mount)
frontend   : static build เสิร์ฟผ่าน nginx
postgres   : 16.x, volume แยก, WAL archive (ดู 09-operations)
guacamole  : guacamole/guacd + guacamole web (ใช้ auth ของ backend ผ่าน JSON auth extension)
```

### VM บน Server

| VM | จำนวน | เฟิร์มแวร์ | ดิสก์ | หมายเหตุ |
|---|---|---|---|---|
| Golden Master | 1 ต่อ channel | OVMF + Secure Boot + swtpm | qcow2 80–100 GB | snapshot `clean-base`, `pre-sysprep-<ver>` |
| Image Test Runner | 2 (ขนาดดิสก์ 128/512 GB) | OVMF + SB + swtpm | qcow2 | snapshot `old-windows+agent+bitlocker` |
| Windows Utility | 1 | OVMF | 120 GB | join AD, มี ADK + WinPE add-on เวอร์ชันล่าสุด, ใช้ gMSA หรือบัญชีที่สิทธิ์จำกัดสำหรับ djoin |

## 4. การไหลของเครือข่าย

| จาก → ถึง | โปรโตคอล | หมายเหตุ |
|---|---|---|
| Agent → Server | HTTPS 443 + mTLS | long-poll, heartbeat, event, download |
| Agent → Site Cache | HTTPS 443 | ไฟล์เท่านั้น (ตรวจ chunk hash) |
| Agent → Peer (ทางเลือก Phase 4) | HTTPS บน subnet เดียวกัน | เสิร์ฟ chunk ที่ตรวจแล้วเท่านั้น |
| Utility VM → Server | HTTPS (pull งาน) | ไม่มีพอร์ตขาเข้า |
| Utility VM → AD DC | LDAP/Kerberos | สำหรับ djoin |
| Admin → Server | HTTPS + SSO (OIDC/LDAP) | |
| WinPE → Server | HTTPS (best-effort) | ไม่บังคับ — WinPE ทำงานออฟไลน์ได้ |

## 5. โครง Monorepo

```text
swim-platform/
├── docs/spec/                 # เอกสารชุดนี้
├── pkg/                       # Go shared (import ได้ทั้ง backend/agent/pe)
│   ├── manifest/              # struct + validate + sign/verify (Ed25519) + seal/open (X25519)
│   ├── state/                 # state.json ของเครื่อง + transitions (source of truth ของ state machine)
│   ├── chunks/                # chunk manifest, hash, resume
│   ├── proto/                 # API DTO + version negotiation
│   └── logx/                  # structured log + severity
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
│   ├── winpe/build-winpe.ps1  # รันบน Utility VM
│   └── winpe/startnet.cmd
├── builder-infra/
│   ├── docker-compose.yml
│   ├── nginx/
│   ├── libvirt/               # XML ของ VM templates
│   └── scripts/{capture-wim.sh,test-image.sh}
└── utility-runner/            # PowerShell agent บน Windows Utility VM
```

## 6. หลักการเวอร์ชัน

- ทุกชิ้นใช้ SemVer: `agent 1.4.2`, `pe 1.4.0`, `manifest schema 1`, `api v1`
- Server รองรับ Agent ย้อนหลัง **N-2 minor** (ดู [04](04-agent-updates.th.md))
- `state.json` มี `schema_version` — `swim-pe` ต้องอ่าน schema ที่ agent เขียนได้ (compat matrix ใน [04 §8](04-agent-updates.th.md))
