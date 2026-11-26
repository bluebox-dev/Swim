# 04 · Agent Self-Update (Continuous Patching) — D-17

[English](04-agent-updates.md) · **ไทย**

> เป้าหมาย: Agent **ทุกเครื่อง** ได้รับแพตช์ต่อเนื่องโดยไม่ต้องมีคนไปแตะ, อัปเดตพังต้อง **ย้อนกลับเอง**, และการปล่อยต้อง **หยุดเองได้** ก่อนลามทั้งองค์กร

## 1. SLO

| ตัวชี้วัด | เป้า |
|---|---|
| เครื่องที่ออนไลน์ได้เวอร์ชันล่าสุด หลังเริ่ม `broad` | ≥ 95% ภายใน 7 วัน |
| Security fix (`priority=critical`) | ≥ 95% ภายใน 48 ชม. |
| อัปเดตล้มเหลวแล้วกลับมาใช้งานได้เอง (auto-rollback) | 100% ของกรณีที่ process ยังสตาร์ตได้ |
| เครื่องที่ "หลุดการควบคุม" เพราะอัปเดต | 0 |

## 2. ส่วนประกอบที่อัปเดตได้

| ชิ้น | ใครอัปเดต | ความถี่ |
|---|---|---|
| `swim-agent.exe` + `swim.exe` + `swim-notify.exe` (agent bundle) | `swim-svc` | บ่อย (patch รายสัปดาห์/ตามต้องการ) |
| `swim-svc.exe` (supervisor) | `swim-agent` | นาน ๆ ครั้ง |
| Config (ไม่ใช่ binary) | `swim-agent` (hot reload) | ทุกเมื่อ |
| `boot.wim` / `swim-pe` | pipeline ของ image ([02 §9](02-image-pipeline.th.md)) | ตามรอบ WinPE |

**กฎเหล็ก: ห้ามอัปเดต `swim-svc` และ agent bundle ในรอบเดียวกัน** — ต้องมีตัวหนึ่งที่ "รู้ว่าดี" คอยกู้อีกตัวเสมอ

## 3. Release Artifact

```
agent-bundle-1.5.0-amd64.zip
├── swim-agent.exe, swim.exe, swim-notify.exe   # Authenticode-signed
└── release.json                                  # เซ็น Ed25519 (release key)
    { "component":"agent", "version":"1.5.0", "arch":"amd64",
      "min_svc_version":"1.1.0", "state_schema":3, "outbox_schema":5,
      "files":[{"name":"swim-agent.exe","sha256":"..."}], "priority":"normal",
      "released_at":"2026-11-23T10:00:00Z" }
```

ตรวจ **3 ชั้น** ก่อนสลับ: (1) SHA256 ของ zip จาก server (2) Ed25519 ของ `release.json` (3) Authenticode ของทุก `.exe` ต้องเป็น publisher ของ SWIM
ถ้าองค์กรใช้ WDAC/AppLocker → ตั้งกฎอนุญาตด้วย **publisher (signer)** ไม่ใช่ hash เพื่อไม่ต้องแก้กฎทุกเวอร์ชัน

## 4. โครงสร้างบนเครื่อง (Slots)

```
C:\Program Files\SWIM\versions\
├── agent-1.4.2\      ← PreviousVersion (เก็บไว้ rollback)
├── agent-1.5.0\      ← ActiveVersion
├── svc-1.1.0\        ← Service ImagePath ชี้มาที่นี่
└── svc-1.0.3\        ← svc เวอร์ชันก่อน (ให้ rescue task ใช้)

HKLM\SOFTWARE\SWIM\Update
  ActiveVersion    = 1.5.0
  PreviousVersion  = 1.4.2
  PendingVersion   = (ว่าง | 1.5.1)
  TrialStartedAt   = 2026-11-23T10:05:00Z
  TrialCrashCount  = 0
  BlockedVersions  = 1.5.1;...        # เวอร์ชันที่เคยล้มบนเครื่องนี้ ห้ามลองซ้ำจนกว่า server จะสั่ง
```

เก็บสูงสุด 2 เวอร์ชันต่อชิ้น (active + previous) ลบที่เก่ากว่าหลัง commit

## 5. Flow อัปเดต agent bundle

```
heartbeat ──► server ตอบ agent_target {version, url, sha256, release_sig, not_before}
   │
   ├─ 1. ตรวจว่าอยู่ในช่วงที่อัปเดตได้ (§6) ไม่ได้ → เลื่อน, รายงาน update_deferred
   ├─ 2. โหลดไป versions\agent-1.5.0.tmp\ (resume ได้, ใช้ netcond เดียวกับ image แต่ **อนุญาตบน VPN/metered** เพราะไฟล์ ~15 MB)
   ├─ 3. ตรวจ 3 ชั้น (§3) → rename เป็น agent-1.5.0\
   ├─ 4. ตั้ง PendingVersion=1.5.0 แล้วขอ svc สลับผ่าน pipe
   │
swim-svc:
   ├─ 5. ส่ง "drain" ให้ agent เก่า → agent เก่าหยุดรับงานใหม่, flush outbox, ปิด DB (timeout 60s)
   ├─ 6. สตาร์ต agent-1.5.0 ในโหมด TRIAL (TrialStartedAt=now)
   ├─ 7. agent ใหม่ทำ self-test (§7) → ส่ง "healthy" ให้ svc + heartbeat ที่มี version=1.5.0 ถึง server
   │
   ├─ ผ่านภายใน 10 นาที ──► COMMIT: Active=1.5.0, Previous=1.4.2, ลบเวอร์ชันเก่ากว่า → รายงาน agent_updated
   └─ ไม่ผ่าน (crash ≥3 ครั้งใน 10 นาที / ไม่ส่ง healthy / server heartbeat ไม่ผ่าน)
          ──► ROLLBACK: สตาร์ต 1.4.2, เพิ่ม 1.5.0 ใน BlockedVersions
          ──► agent 1.4.2 รายงาน agent_update_failed + crash log ของ 1.5.0
```

## 6. เมื่อไหร่ห้ามอัปเดต

| สถานะ job | อัปเดตได้ไหม |
|---|---|
| ไม่มี job / `staging` / `staged` | ✅ (download หยุดชั่วคราวแล้ว resume หลังสลับ) |
| `waiting` (รอเวลา/ผู้ใช้เลื่อน) | ✅ |
| `handoff` → `health_check` (ช่วงแก้ BCD, อยู่ใน WinPE, บูตครั้งแรก) | ❌ |
| ผู้ใช้ตั้ง maintenance window ไว้ | อัปเดตได้ตลอด (ไม่ต้องรีบูต ไม่กระทบผู้ใช้) |

## 7. Self-test ของ agent ใหม่ (ต้องผ่านทั้งหมด)

1. อ่าน config + ตรวจลายเซ็น config
2. เปิด outbox DB และรัน migration สำเร็จ
3. อ่าน/เขียน `state.json` (schema ปัจจุบัน) ได้
4. mTLS heartbeat ถึง server สำเร็จ (ถ้าเครือข่ายใช้ได้ — ถ้าออฟไลน์ ข้อนี้ผ่านแบบมีเงื่อนไข และต้อง heartbeat สำเร็จภายใน 24 ชม. หลังกลับมาออนไลน์ มิฉะนั้น rollback)
5. เปิด named pipe ได้
6. mount S: และอ่าน `S:\swim\` ได้ (ถ้ามี S:)

### กติกา Migration (ให้ rollback ได้จริง)

- Outbox/DB ใช้ **expand → contract**: เวอร์ชัน N เพิ่มคอลัมน์/ตารางได้ แต่ห้ามลบหรือเปลี่ยนความหมายของสิ่งที่ N-1 ใช้ — ลบได้ในเวอร์ชัน N+1
- `state.json`: เขียน `schema_version`; agent ต้องอ่าน schema N-1 ได้เสมอ
- ห้ามเขียน registry/ไฟล์ในรูปแบบที่ N-1 อ่านไม่ได้

## 8. Compatibility Matrix

| ระหว่าง | กติกา |
|---|---|
| Server ↔ Agent | Server รองรับ agent **N-2 minor** (เช่น server 1.7 รองรับ agent 1.5–1.7). เก่ากว่านั้น → HTTP `426 Upgrade Required` + ข้อมูลอัปเดต (agent ยังเรียก endpoint อัปเดตได้เสมอ) |
| Agent ↔ swim-svc | `release.json.min_svc_version` — ถ้า svc เก่ากว่า ต้องอัปเดต svc ก่อน (ไม่สลับ agent) |
| Agent ↔ swim-pe | `state_schema` ต้องอยู่ใน `supported_state_schemas` ของ `boot.wim` ที่ published — server ไม่ publish agent ที่ทำให้ไม่ตรงกัน |
| Agent ↔ Manifest | agent ประกาศ `manifest_schemas: [1,2]` ใน heartbeat → server ออก manifest ตาม schema ที่ agent รองรับ |

## 9. อัปเดต swim-svc

```
agent (ที่ commit แล้วและ healthy) ได้ svc_target
 → โหลด + ตรวจ 3 ชั้น → versions\svc-1.2.0\
 → เปลี่ยน Service ImagePath ผ่าน SCM → ตั้ง Service Recovery = restart 3 ครั้ง
 → restart service (svc ใหม่สตาร์ต agent ActiveVersion เดิม)
 → svc ใหม่ต้องรายงาน healthy ภายใน 10 นาที
```

**ตาข่ายกู้ชั้นสุดท้าย — `SWIMRescue` (Scheduled Task, SYSTEM, at startup + ทุก 6 ชม.)**
- รัน `svc-<previous>\swim-svc.exe rescue`
- ถ้า service `SWIMSupervisor` ไม่ทำงาน หรือ crash loop > 3 ครั้ง/ชม. → ชี้ ImagePath กลับไป svc เวอร์ชันก่อน, สตาร์ตใหม่, เขียน event log (`Application`, source `SWIM`)
- ถ้าทั้งสองเวอร์ชันพัง → ทางสุดท้ายคือ **GPO/Intune re-push MSI** (ช่องทางเดียวกับ bootstrap — [03 §2](03-agent.th.md)) — MSI ต้อง idempotent และเก็บ identity เดิมไว้ (ไม่ลบ `C:\ProgramData\SWIM\`)

## 10. Rollout ฝั่ง Server

### สถานะ release

```
draft ─CI build+sign─► testing ─auto test ผ่าน─► canary ─► pilot ─► broad ─► published(100%)
                                                     └────── paused ◄── breaker / admin
                                         revoked ◄── admin (สั่งให้ทุกเครื่องกลับเวอร์ชันที่กำหนด)
```

### การเลือกเครื่อง (deterministic)

```
bucket(host) = uint(sha256(host_id || release_id)[0:2]) % 100
host ได้ release เมื่อ: host.ring ≤ release.stage  และ  bucket(host) < release.percent
```

| Stage | กลุ่ม | % | Soak ขั้นต่ำ |
|---|---|---|---|
| `testing` | Test Runner VM + VM CI | — | ผ่าน test อัตโนมัติ |
| `canary` | ring `canary` (เครื่อง IT ~10–20 เครื่อง ครอบคลุมทุกรุ่นหลัก) | 100% ของ ring | 24 ชม. |
| `pilot` | ring `pilot` + 10% ของ broad | 10 | 48 ชม. |
| `broad` | ทั้งหมด ไล่ 25 → 50 → 100 | 25/50/100 | 24 ชม. ต่อขั้น |

`priority=critical` (security fix): canary 2 ชม. → broad 100% ทันที แต่ **breaker ยังทำงาน**

### Health gate และ Circuit breaker (อัตโนมัติ)

เทียบเครื่องที่อยู่บนเวอร์ชันใหม่กับเครื่องบนเวอร์ชันเดิม ในช่วงเวลาเดียวกัน:

| Metric | หยุด rollout (`paused`) เมื่อ |
|---|---|
| `agent_update_failed` rate | > 2% ของเครื่องที่ลองอัปเดต (ขั้นต่ำ 3 เครื่อง) |
| เครื่องเงียบหลังอัปเดต (ไม่มี heartbeat > 30 นาที ขณะที่ก่อนอัปเดตออนไลน์อยู่) | > 0.5% |
| crash rate ต่อเครื่องต่อวัน | > 2× ของเวอร์ชันเดิม |
| job failure rate (deploy) ของเครื่องบนเวอร์ชันใหม่ | > เวอร์ชันเดิม + 5 จุด % |

เมื่อ breaker ทำงาน: rollout `paused` + แจ้ง `release_manager` — เครื่องที่อัปเดตไปแล้วยังอยู่ต่อ (ไม่ revoke อัตโนมัติ) จนกว่าคนจะตัดสินใจ

### Revoke และ Downgrade

- Agent **ไม่ยอม downgrade เอง** (anti-rollback) ยกเว้นได้รับ **rollback directive** ที่เซ็นแล้ว `{revoke: "1.5.0", target: "1.4.2"}`
- `revoked` ห้ามแจกต่อ และเครื่องที่อยู่บนเวอร์ชันนั้นจะถูกสลับไป target

## 11. เครื่องที่ออฟไลน์นาน

| ออฟไลน์ | ผลเมื่อกลับมา |
|---|---|
| < 90 วัน | heartbeat ปกติ → ได้ agent_target ล่าสุดทันที (ข้าม soak เพราะ release ผ่าน broad แล้ว) |
| 90–180 วัน (client cert หมดอายุ) | เรียก `/v1/agent/renew` ด้วยลายเซ็นจากกุญแจเดิม (proof-of-possession) → ได้ cert ใหม่ |
| > 180 วัน | ต้อง re-enroll ด้วย token ใหม่ (ช่างหรือ GPO) |
| เวอร์ชันเก่ากว่า N-2 | ได้ `426` → อัปเดตก่อนทำอย่างอื่น; ถ้าห่างหลายเวอร์ชัน อัปเดตตรงไปเวอร์ชันล่าสุดได้เลย (ไม่ต้องไล่ทีละขั้น) เว้นแต่ release ระบุ `requires_intermediate` |

## 12. Config Update (ไม่ใช่ binary)

- ส่งผ่าน heartbeat: `{config_version, config, sig}` — เช่น heartbeat interval, bandwidth, netcond policy
- agent ตรวจลายเซ็น + validate schema → ใช้ทันที; ถ้า agent ล่มภายใน 10 นาทีหลังใช้ config ใหม่ → กลับ `last_good_config`

## 13. Release Pipeline (CI)

```
git tag agent/v1.5.0
 → go test ./... + vet + race
 → build (GOOS=windows GOARCH=amd64/arm64, -trimpath, reproducible)
 → Authenticode sign (signtool + HSM/USB token ของ code-signing cert)
 → สร้าง release.json + Ed25519 sign (release key — 08-security)
 → upload → backend: agent_releases(status=testing)
 → Test Runner VM: อัปเดตจากเวอร์ชันก่อนหน้า + จาก N-2 + rollback test (บังคับให้ self-test ล้ม → ต้องกลับเวอร์ชันเดิม)
 → ผ่าน → canary (ต้องมีคนกด — RBAC `release_manager`, ห้ามคนเดียวกับผู้ tag)
```

## 14. Dashboard ที่ต้องมี

- กราฟสัดส่วนเวอร์ชัน agent/svc ทั้ง fleet และแยก site
- สถานะ rollout ปัจจุบัน + metric ของ health gate แบบ real-time
- รายชื่อเครื่องที่ `BlockedVersions` ไม่ว่าง / ค้างเวอร์ชันเก่า > 14 วัน / ออฟไลน์ > 30 วัน
