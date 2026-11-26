# 11 · API (v1)

- Base: `https://swim.corp.local/v1`
- Agent API: **mTLS** (client cert ของเครื่อง) — ยกเว้น `enroll` / `renew` ที่ใช้ token / proof-of-possession
- Admin API: session จาก SSO + CSRF token; ทุก endpoint ที่เปลี่ยนสถานะเขียน audit
- ทุก request ของ agent ส่ง header `SWIM-Agent-Version`, `SWIM-Manifest-Schemas: 1,2`
- Error format: `{ "error": { "code": "SWIM-API-403", "message": "...", "retry_after_s": 60 } }`

## 1. Agent API

| Method | Path | ใช้ทำ |
|---|---|---|
| POST | `/agent/enroll` | `{token \| reimage_ticket, csr, x25519_pub, inventory}` → `{host_id, cert, keyset, config}` |
| POST | `/agent/renew` | ต่อ cert ที่หมดอายุ (≤ 180 วัน) ด้วยลายเซ็นจากกุญแจเดิม |
| POST | `/agent/heartbeat` | `{state, job_status, versions, netcond, stage_free}` → `{halt, agent_target, svc_target, config?, keyset?, standing_manifest?, commands_pending: bool}` |
| GET | `/agent/commands?wait=55` | long-poll → `[{id, type, manifest?, args}]` |
| POST | `/agent/commands/{id}/ack` | ยืนยันรับคำสั่ง |
| PUT | `/agent/inventory` | อัปเดต inventory เต็ม |
| POST | `/agent/events` | batch จาก outbox `[{job_id, seq, source, status, step, severity, data, at}]` → idempotent |
| POST | `/agent/jobs/{id}/preflight` | ส่งผล preflight → `{risk, eta, allow}` |
| POST | `/agent/jobs/{id}/slot` | ขอ concurrency slot (download/deploy) → `{granted, lease_until \| retry_after_s}` |
| POST | `/agent/jobs/{id}/go` | ขอ go-token ก่อน handoff → `{go_token, secrets_sealed?}` หรือ `409 halted` |
| POST | `/agent/jobs/{id}/summary` | job summary (ทุก job) |
| POST | `/agent/logs` | multipart upload log bundle |
| GET | `/agent/releases/{component}/{version}` | ข้อมูล bundle + ลิงก์ดาวน์โหลด |
| POST | `/agent/updates` | รายงานผลอัปเดต `{release, result: updated\|failed\|deferred, detail}` |
| GET | `/agent/peers?chunk=…` | (Phase 4) รายชื่อ peer ใน subnet ที่มี chunk |

ไฟล์ใหญ่: `GET /storage/...` ผ่าน nginx — backend ตรวจสิทธิ์ด้วย `auth_request` แล้ว nginx เสิร์ฟ (รองรับ `Range`)

## 2. WinPE API (best-effort, mTLS ด้วย cert ชั่วคราวผูก job)

| Method | Path | ใช้ทำ |
|---|---|---|
| GET | `/pe/jobs/{id}/halt` | ตรวจ kill switch ก่อน PONR |
| POST | `/pe/jobs/{id}/events` | สถานะระหว่าง apply (ถ้ามีเน็ต) |

> cert ชั่วคราวของ WinPE: agent ขอพร้อม go-token (อายุ 6 ชม., ใช้ได้เฉพาะ `/pe/jobs/{id}/*`), เก็บบน S: — ถ้ารั่วทำได้แค่รายงาน event ของ job นั้น

## 3. Utility VM API

| Method | Path | ใช้ทำ |
|---|---|---|
| GET | `/utility/tasks?wait=55` | ดึงงาน (build_winpe, odj_provision, odj_reset, dism_service) |
| POST | `/utility/tasks/{id}/result` | ส่งผล (ODJ blob ส่งกลับแบบ sealed ไปยัง public key ของ backend) |
| POST | `/utility/tasks/{id}/artifact` | อัปโหลดไฟล์ (boot.wim ฯลฯ) |

## 4. Admin API (ย่อ)

| กลุ่ม | Endpoints |
|---|---|
| Hosts | `GET /hosts`, `GET /hosts/{id}`, `PATCH /hosts/{id}` (ring, channel, site), `POST /hosts/{id}/commands` |
| Jobs | `POST /jobs` (รายเครื่อง), `GET /jobs?status=`, `GET /jobs/{id}` (+events, summary, logs), `POST /jobs/{id}/cancel` |
| Preview | `POST /jobs/preview` → preflight ล่าสุด + ETA + risk (ไม่สร้าง job) |
| Images | `GET /channels`, `POST /channels`, `GET /images`, `POST /images/build`, `POST /images/{id}/approve`, `.../reject`, `.../pin`, `DELETE /images/{id}` |
| Capacity | `GET /capacity/server`, `GET /capacity/hosts?image_id=` |
| Builder | `GET /builder/vms`, `POST /builder/vms/{id}/start\|stop\|snapshot\|revert`, `GET /builder/vms/{id}/console` (Guacamole token) |
| WinPE / Drivers | `GET/POST /winpe`, `POST /winpe/{id}/approve`, `GET/POST /driver-packs`, `POST /driver-packs/{id}/promote` |
| Disk profiles | CRUD `/disk-profiles` |
| Rollouts | `POST /rollouts`, `GET /rollouts/{id}`, `POST /rollouts/{id}/pause\|resume\|advance\|cancel`, `POST /rollouts/fleet-rollback` |
| Kill switch | `GET /kill-switches`, `PUT /kill-switches/{scope}/{id}`, `DELETE ...` (ต้อง 2 คนสำหรับ global) |
| Agent releases | `GET /agent-releases`, `POST /agent-releases` (จาก CI), `POST /agent-releases/{id}/promote\|pause\|revoke` |
| Admin | `/sites`, `/users`, `/roles`, `/enrollment-tokens`, `/keysets` |
| Approvals | `GET /approvals`, `POST /approvals/{id}/approve` (สำหรับ action ที่ต้อง 2 คน) |
| Audit | `GET /audit?actor=&object=` |
| Ops | `GET /health`, `GET /metrics` |

## 5. Versioning

- breaking change → `/v2` และรองรับ `/v1` คู่กันอย่างน้อย 2 minor ของ server
- agent ที่เก่ากว่า N-2 → `426` ทุก endpoint ยกเว้น `heartbeat`, `releases`, `updates`
