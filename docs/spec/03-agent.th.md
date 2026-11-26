# 03 · SWIM Agent (Windows)

[English](03-agent.md) · **ไทย**

## 1. โครงสร้างภายใน

```
swim-svc.exe  (Windows Service "SWIMSupervisor", LocalSystem, เล็กมาก เปลี่ยนน้อย)
  └── สตาร์ตและเฝ้า swim-agent.exe (เวอร์ชันตาม registry "ActiveVersion"), ทำ self-update (04-agent-updates)

swim-agent.exe (process ลูก, LocalSystem)
├── control    heartbeat 30s + long-poll คำสั่ง (timeout 55s)       ← ไม่มีวันถูกงานยาวบล็อก
├── worker     งานยาว (staging, download, shrink) ทีละ 1 job      ← เขียน event ลง outbox
├── outbox     SQLite (C:\ProgramData\SWIM\outbox.db) มี seq      ← ส่งขึ้น server แบบ at-least-once
├── watchdog   timeout ต่อขั้น = max(expected × 3, ขั้นต่ำ)       ← ค้าง = cancel + รายงาน
├── netcond    ตรวจ metered / VPN / เครือข่ายองค์กร / site       ← กำหนดว่าโหลดได้ไหม
├── telemetry  inventory (SMBIOS, disk, BitLocker, battery, license)
├── localipc   named pipe \\.\pipe\swim (ACL: Administrators + SYSTEM เท่านั้น)
└── notifier   แจ้งผู้ใช้ที่ล็อกอิน (ผ่าน swim-notify.exe ใน session ผู้ใช้)
```

- ทุก HTTP call: timeout ชัดเจน + retry exponential backoff (1s → 5 นาที) + jitter ±20%
- ระหว่างขั้นสำคัญเรียก `SetThreadExecutionState(ES_CONTINUOUS|ES_SYSTEM_REQUIRED)` กันเครื่อง sleep
- ไฟล์และโฟลเดอร์: `C:\Program Files\SWIM\versions\<ver>\` · ข้อมูล `C:\ProgramData\SWIM\` (ACL SYSTEM+Admins)

## 2. การติดตั้งครั้งแรกบนเครื่องที่มีอยู่ (Bootstrap)

| ช่องทาง | วิธี | หมายเหตุ |
|---|---|---|
| **หน้า "เพิ่มเครื่อง" → .msi** (หลัก, v0.2) | Admin เลือก site/อายุ/จำนวน → ได้ไฟล์ `swim-agent-<ver>-<site>.msi` ที่ฝัง server + pin + token → ดับเบิลคลิก → UAC → เสร็จ | server สร้าง MSI ด้วย `wixl` ทุกครั้ง (~1 วินาที), ProductCode คงที่ต่อเวอร์ชัน |
| หน้า "เพิ่มเครื่อง" → คำสั่ง PowerShell (สำรอง) | วางคำสั่งใน Terminal (Admin) | คำสั่งมี server URL + enrollment token + **pin SHA-256 ของใบรับรอง server** |
| **GPO** (แนะนำสำหรับ AD) | Startup Script รันคำสั่งเดียวกัน (หรือ `swim-agent.msi` เมื่อมี) | token แบบไม่จำกัดจำนวน อายุสั้น แล้วยกเลิกเมื่อเสร็จ |
| Intune / SCCM | PowerShell script (SYSTEM) / Win32 app | |
| Unattend (เครื่องใหม่) | `FirstLogonCommands` รันคำสั่งเดียวกัน | ทดสอบแล้วด้วย `scripts/create-test-client.sh` |

Flow ของคำสั่งติดตั้ง (`agent/install/install.ps1`, เสิร์ฟที่ `/dl/agent/install.ps1`):
1. PowerShell ตั้ง `ServerCertificateValidationCallback` ให้ยอมเฉพาะใบรับรองที่ SHA-256 ตรง pin → ดึง `install.ps1`
2. อ่าน `/dl/agent/latest.json` → ดาวน์โหลด exe → ตรวจ SHA-256 → `C:\Program Files\SWIM\swim-agent.exe`
3. ตั้ง ACL `C:\ProgramData\SWIM` ให้เฉพาะ SYSTEM + Administrators
4. `swim-agent enroll` (agent pin ใบรับรองเองอีกชั้น) → host token เก็บแบบ DPAPI (machine scope)
5. `swim-agent install` → Windows Service `SWIMAgent` (Automatic, restart เมื่อ crash) → heartbeat ทุก 60 วินาที (lab)

- ไฟล์ใน `/dl/agent/` เป็น public ได้เพราะไม่มีความลับ — ความลับอยู่ใน token ที่ admin ออกเท่านั้น
- **ก่อน production:** exe/MSI ต้อง **Authenticode-signed** (กัน SmartScreen/Defender) และ `latest.json` ต้องเซ็นด้วย release key (Ed25519, [04](04-agent-updates.th.md)) แทนการพึ่ง TLS pin อย่างเดียว
- Enrollment token: ผูก **site** + มีวันหมดอายุ + จำกัดจำนวนครั้ง, สร้างจาก Admin UI (RBAC: `admin`)

### 2.1 Agent ต้องอยู่ต่อเนื่องเสมอ (ไม่มีรอยต่อหลังล้างเครื่อง)

| เหตุการณ์ | Agent กลับมาอย่างไร | host_id |
|---|---|---|
| ติดตั้งครั้งแรก | MSI (ดับเบิลคลิก/GPO/Intune) → เขียน `HKLM\SOFTWARE\SWIM\Bootstrap` (Server, Pin, Token) → service enroll เอง → ลบ Token | ใหม่ |
| **SWIM ล้างเครื่อง** | swim-pe ใส่ exe + service + `bootstrap.json` (reimage ticket) ลง Windows ใหม่ ([05 §4.3](05-deployment-flow.th.md)) | **เดิม** |
| ช่างลง USB เอง | ติดตั้ง MSI → enroll ด้วย token → server จับคู่ smbios_uuid + serial | **เดิม** (ป้าย `manual_install`) |
| ติดตั้งตอนไม่มีเน็ต | service รอและ enroll เองเมื่อเน็ตกลับมา (ก่อน token หมดอายุ) | — |

**ข้อห้าม — ไม่ส่งต่อ "ตัวตน" ของ agent ข้ามการติดตั้ง Windows:** ถ้าคัด state ไปด้วย (หรือ agent ที่ enroll แล้วติดไปกับ Golden Master)
ทุกเครื่องที่ได้ image จะเป็น host เดียวกัน ระบบจึงมี 3 ชั้นกัน:
1. Capture ตัด `\ProgramData\SWIM` ออกจาก WIM เสมอ (builder exclusion list)
2. Clone guard: state ผูก SMBIOS UUID — UUID ไม่ตรง หรือ DPAPI เปิด token ไม่ได้ → ย้าย state ทิ้ง (`state.json.discarded-*`) แล้ว enroll ใหม่
3. Reimage ticket ใช้ได้ครั้งเดียว ผูก host และ SMBIOS UUID, อายุ ≤ 72 ชม.

## 3. ตัวตนของเครื่อง (Identity)

### การลงทะเบียน (Enroll)

```
1. agent สร้าง keypair:
     - ECDSA P-256 ใน TPM ผ่าน "Microsoft Platform Crypto Provider" (ถ้ามี TPM)
     - fallback: software key เก็บด้วย DPAPI (machine scope)
   + X25519 keypair สำหรับ "เปิดซอง" ข้อมูลลับใน manifest (DPAPI)
2. POST /v1/agent/enroll { token, csr, x25519_pub, smbios_uuid, serial, vendor, model, ... }
3. server จับคู่ host:
     - มี host เดิมที่ smbios_uuid + serial ตรง → ใช้ host_id เดิม (re-enroll)
     - ไม่มี → สร้าง host ใหม่
4. server ออก client cert (mTLS) อายุ 90 วัน → agent ต่ออายุเองเมื่อเหลือ 1/3
```

- `smbios_uuid` ที่เป็นค่าขยะ (`FFFFFFFF-…`, `00000000-…`, `03000200-0400-0500-0006-000700080009`) → ใช้ `serial + vendor + model` แทน และติดธง `weak_identity`

### หลัง Reimage

- WinPE เขียน **reimage ticket** (ใช้ครั้งเดียว, อายุ 24 ชม., ผูก job_id + host_id) ลง `C:\ProgramData\SWIM\reimage.ticket` ของ Windows ใหม่
- agent ใหม่ enroll ด้วย ticket → ได้ host_id เดิมโดยไม่ต้องมี enroll token

## 4. คำสั่งจาก server (Allowlist — D-14, D-20)

### 4.1 ที่ใช้งานได้แล้ว (agent 0.3)

| คำสั่ง | ระดับ | ผล | เงื่อนไข |
|---|---|---|---|
| `ping` | safe | ตอบเวอร์ชัน + clock skew | — |
| `collect_logs` | safe | ข้อมูลระบบ + error ใน System log 24 ชม. + agent.log 64 KB | — |
| `refresh_inventory` | safe | ส่ง inventory ใหม่ → server อัปเดต host | — |
| `restart_agent` | change | agent ออกแล้ว SCM recovery สตาร์ตใหม่ (~10 วินาที) | ถูกบล็อกเมื่อ kill switch ทำงาน |
| `reboot` | change | `shutdown /r /t N /c <ข้อความ>` (N = 60 วินาที–24 ชม.) | ถูกบล็อกเมื่อ kill switch ทำงาน |
| `run_script` | change | รัน PowerShell จาก **คลังสคริปต์ที่อนุมัติแล้ว** เป็น SYSTEM, timeout ≤ 1 ชม., ส่ง output + exit code กลับ | สคริปต์ต้องอนุมัติ 2 คน, ถูกบล็อกเมื่อ kill switch ทำงาน |
| `uninstall_agent` | destructive | `msiexec /x` (ถ้ามาจาก MSI) + ลบ service, ไฟล์, ข้อมูล, registry → server ยกเลิก token ทันที | ต้องระบุเหตุผล + พิมพ์ชื่อเครื่องยืนยันบนเว็บ |

นอกจากนี้ admin "ลบออกจากระบบ" (retire) ได้โดยไม่ผ่าน agent — ใช้กับเครื่องเสีย/หาย: ยกเลิก token และคำสั่งที่ค้าง

### 4.2 ชั้นความปลอดภัยของช่องทางคำสั่ง

1. **Allowlist ทั้งสองฝั่ง** (`pkg/manifest.CommandTypes`) — server ไม่สร้าง และ agent ไม่รับ type อื่น; **ไม่มี `run_command` / shell อิสระ**
2. **ลายเซ็น Ed25519** ทุกคำสั่ง (envelope เดียวกับ manifest, [06](06-manifest.th.md)) ผูก `command_id` + `host_id` + **SMBIOS UUID** + `not_before/not_after` (1 ชม.)
3. Agent ตรวจเวลาเทียบ **นาฬิกาของ server** (จาก header `Date`) — เครื่องที่นาฬิกาเพี้ยนยังใช้ได้ แต่คำสั่งเก่าใช้ไม่ได้
4. **At-most-once** — agent บันทึก id ก่อนรัน (`commands-seen.json`), ส่งซ้ำไม่ทำซ้ำ
5. **สคริปต์ 2 คน** — เนื้อหาล็อกด้วย SHA-256 ตั้งแต่สร้าง (แก้ไม่ได้), ผู้เขียนอนุมัติเองไม่ได้ (DB CHECK), agent ตรวจ hash อีกรอบก่อนรัน
6. **Kill switch** บล็อกคำสั่งระดับ change/destructive; safe ยังใช้ได้เพื่อสืบปัญหา
7. **Audit** — การสั่ง, การยกเลิก, ผลลัพธ์ (actor `host:<id>`) ลง audit log แบบ hash chain; agent log ทุกคำสั่งในเครื่องด้วย
8. ช่องทางเป็น **agent ดึงเอง** (long-poll 25 วินาที) — เครื่องลูกไม่มีพอร์ตเปิด

**ข้อจำกัดที่รู้อยู่:** กุญแจเซ็นคำสั่งอยู่บน server — ผู้ที่ยึด server ได้ทั้งเครื่องจะเซ็นคำสั่งได้ ระยะต่อไป: แยกการเซ็นสคริปต์ไปที่ผู้อนุมัติ (กุญแจของคน/HSM) เพื่อให้ server อย่างเดียวสั่งรันโค้ดไม่ได้

### 4.3 ที่จะเพิ่มกับระบบ deploy (ยังไม่มี)

| คำสั่ง | ผล |
|---|---|
| `stage` | โหลด image/winpe/drivers เก็บบน S: ล่วงหน้า |
| `deploy` / `rollback` | ทำ job ตาม manifest (แจ้งผู้ใช้ + นับถอยหลัง) |
| `cancel` | ยกเลิก job ที่ยังไม่ถึง PONR |
| `resize_staging` | ปรับขนาด S: |
| `update_agent` | อัปเดต agent ทันที (security fix) |

### คำสั่ง local (CLI `swim`)

```
swim status                      # สถานะ agent, job, S:, version
swim stage                       # โหลด image ที่ published ล่วงหน้า
swim deploy [--at 22:00]         # ใช้เฉพาะ manifest ที่ server เซ็นและยังไม่หมดอายุ
swim rollback
swim cancel
swim logs --export D:\swim-logs.zip
```

- ต้องเป็น **Local Administrator** (ตรวจที่ ACL ของ named pipe + ตรวจ token อีกชั้น)
- deploy แบบ local ต้องมี **manifest ที่เซ็นแล้ว** ใน cache: agent ขอ "standing manifest" ของ channel ปัจจุบันล่วงหน้าไว้ (อายุ 7 วัน, ต่ออายุทุก heartbeat ชั่วโมงละครั้ง) → ช่างสั่งได้แม้ออฟไลน์ แต่ใช้ image อื่นนอกจากที่อนุมัติไม่ได้
- ทุกคำสั่ง local บันทึก `actor = local:<DOMAIN\user>` ลง outbox → ขึ้น audit log ของ server

## 5. Preflight

| ID | ตรวจ | ระดับ |
|---|---|---|
| P-01 | เสียบไฟ AC (ถ้ามีแบต) และแบต ≥ 30% | **BLOCK** |
| P-02 | UEFI + GPT | **BLOCK** |
| P-03 | arch ตรง channel | **BLOCK** |
| P-04 | ดิสก์ OS ≥ `min_disk_gb` | **BLOCK** |
| P-05 | มี storage driver ของรุ่นนี้ใน `boot.wim` ที่ published (server ตอบจาก driver matrix) | **BLOCK** |
| P-06 | S: มีอยู่และจุพอ หรือ shrink C: ได้ (`Get-PartitionSupportedSize`) | **BLOCK** |
| P-07 | Secure Boot state + mitigation ตรงกับที่ WinPE รองรับ | **BLOCK** ถ้าไม่ตรง |
| P-08 | Kill switch ปิดอยู่ + manifest ยังไม่หมดอายุ + ลายเซ็นถูก | **BLOCK** |
| P-09 | BitLocker เปิด → จะ suspend ให้ | WARN |
| P-10 | ไม่มี driver pack ที่ตรงรุ่นแบบเต็ม | WARN |
| P-11 | edition ของ license (OEM) ไม่ตรง image | WARN |
| P-12 | มีผู้ใช้ล็อกอิน | INFO → ใช้ flow แจ้งเตือน |
| P-13 | มีดิสก์มากกว่า 1 ลูก / มี USB storage เสียบ | INFO (บันทึก GUID ดิสก์เป้าหมาย) |

ผล preflight ส่งขึ้น server เสมอ → ใช้คำนวณ risk/ETA ([05 §9](05-deployment-flow.th.md))

## 6. Staging Partition (S:) — D-07

- **ถาวร**, label `SWIM-STAGE`, GPT type = Basic Data, attribute `GPT_ATTRIBUTE_NO_DRIVE_LETTER` + hidden (ไม่ให้ผู้ใช้เห็น) — agent mount ชั่วคราวเมื่อต้องใช้ (`mountvol`)
- ขนาด: `max(24 GB, 2 × image_size + boot.wim + drivers + 2 GB)` ปัดขึ้นเป็น 4 GB
- สร้างครั้งแรก: shrink C: แล้วสร้าง S: **ต่อท้าย C:** (ก่อน WinRE ถ้ามี)
  - ถ้า shrink ไม่พอ: ปิด hibernation, ลบ VSS snapshot (`vssadmin delete shadows /for=C: /quiet`), ลอง `Get-PartitionSupportedSize` ใหม่
- **BitLocker บน S:**: ต้องไม่ถูกเข้ารหัสอัตโนมัติ — agent ตรวจ `manage-bde -status` หลังสร้าง; ถ้ามี policy บังคับ fixed drive → รายงาน BLOCK พร้อมคำแนะนำให้ยกเว้น (label/GPO)
- ข้อมูลใน S: **ไม่มีความลับ plain text** — ข้อมูลลับใน manifest ถูกเข้ารหัสด้วยกุญแจของเครื่อง (D-16)

```
S:\swim\
├── images\<image_id>\{image.wim | chunks\, chunks.json, chunks.sig}
├── winpe\<pe_ver>\{boot.wim, boot.sdi}
├── drivers\<pack_id>\
├── agent\<agent_ver>\swim-agent.msi          # ให้ WinPE ฉีด (D-18)
├── jobs\<job_id>\{manifest.json, manifest.sig, state.json}
└── logs\<job_id>\                            # เก็บ 5 job ล่าสุด
```

## 7. Download Engine

- ไฟล์ใหญ่แบ่งเป็น **chunk 64 MiB** ตาม `chunks.json` (เซ็นแล้ว) → โหลด/verify/resume ทีละ chunk
- ลำดับแหล่ง: **peer ใน subnet** (Phase 4) → **site cache** → **server**; chunk ที่ hash ไม่ตรง → ทิ้งและเปลี่ยนแหล่ง
- จำกัดแบนด์วิดท์ด้วย token bucket: `max_download_mbps` จาก manifest; ลดลงอีก 50% เมื่อผู้ใช้กำลังใช้งาน (idle < 5 นาที)
- จำนวน connection พร้อมกัน: 2 ต่อเครื่อง
- Delta: ถ้า base อยู่บน S: แล้ว โหลดเฉพาะ delta

## 8. เงื่อนไขเครือข่าย (netcond)

| สภาพ | ตรวจด้วย | โหลดได้ไหม |
|---|---|---|
| Metered (hotspot, 4G/5G) | `INetworkCostManager` → `NLM_CONNECTION_COST_*` ≠ unrestricted | ❌ (ยกเว้น admin ตั้ง override) |
| VPN | interface type / subnet ตาม config | ตามนโยบาย: ค่าเริ่มต้น **ไม่** โหลด image, อนุญาต heartbeat/agent update |
| เครือข่ายองค์กร | ตรวจ DNS suffix + เข้าถึง `https://<server>/v1/ping` ได้ | ✅ |
| Site | subnet → site mapping จาก server | เลือก site cache + นับ concurrency ต่อ site |

## 9. การแจ้งผู้ใช้

- ก่อน deploy/rollback/reboot: แสดง toast + หน้าต่างนับถอยหลัง (ค่าเริ่มต้น 15 นาที) ข้อความ "ข้อมูลบนไดรฟ์ C: จะถูกลบทั้งหมด"
- ผู้ใช้ **เลื่อนได้** ตาม `max_deferrals` (ค่าเริ่มต้น 2 ครั้ง ครั้งละ ≤ 4 ชม.) ถ้า job ไม่ใช่ `force`
- บันทึกว่าแจ้งแล้ว/ผู้ใช้เลื่อนกี่ครั้ง ลง audit (D-05)

## 10. Log ในเครื่อง

- `C:\ProgramData\SWIM\logs\agent-YYYYMMDD.jsonl` หมุนทุกวัน เก็บ 14 วัน
- log ของ job อยู่บน `S:\swim\logs\<job_id>\` เก็บ 5 job ล่าสุด
- ระดับ severity และสิ่งที่ส่งขึ้น server ดู [09 §1](09-operations.th.md)
