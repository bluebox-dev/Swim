# 07 · Fleet Safety — Wave, Concurrency, Kill Switch, Circuit Breaker, Fleet Rollback

## 1. Rollout ของ Image

การปล่อย image ให้หลายเครื่องทำผ่าน **Rollout** เสมอ (การสั่งรายเครื่องเป็น rollout ขนาด 1)

```
Rollout ro_77: win11-x64-stable v1.6 → เป้าหมาย: site BKK, OU Finance (412 เครื่อง)
 ├─ wave canary : 3 เครื่อง/รุ่น (ring=canary)      gate: 100% completed, soak 24 ชม.
 ├─ wave pilot  : 10%                                 gate: success ≥ 95%, soak 24 ชม.
 ├─ wave 50     : 50%                                 gate: success ≥ 97%
 └─ wave 100    : 100%
```

| กติกา | ค่าเริ่มต้น |
|---|---|
| รุ่นเครื่อง (`model`) ที่ยังไม่มีเครื่องผ่าน wave canary กับ image นี้ | **ไม่ถูกปล่อยใน wave ถัดไป** (ค้างเป็น `held: untested_model`) |
| เลื่อน wave | อัตโนมัติเมื่อผ่าน gate (ตั้งเป็น manual ได้) |
| image `pending` | ใช้ได้เฉพาะ wave canary; wave ถัดไปต้อง `published` |

## 2. จำกัดการทำพร้อมกัน (Concurrency)

| ระดับ | ค่าเริ่มต้น | นับอะไร |
|---|---|---|
| ทั้งระบบ | 50 | job ในสถานะ `handoff` → `health_check` |
| ต่อ site | 10 | เหมือนกัน |
| ต่อ site — download | 20 | job ใน `staging` ที่โหลดจาก server/WAN (ไม่นับ site cache/peer) |
| ต่อ rollout | ตั้งได้ | |

- agent ขอ slot ตอนออกจาก `waiting` (go-token §4) — ไม่ได้ slot = รอต่อ (backoff 5–15 นาที + jitter)
- slot มี lease 2 ชม. ต่ออายุโดย heartbeat/event; เครื่องเงียบ → lease หมดเมื่อ `suspected_failure`

## 3. Maintenance Window

- ตั้งต่อ site/OU: เช่น `จ–ศ 20:00–06:00, ส–อา ทั้งวัน`
- deploy เริ่ม (handoff) ได้เฉพาะในหน้าต่าง; ถ้า ETA P90 เกินท้ายหน้าต่าง → รอหน้าต่างถัดไป
- staging (download) ทำได้ตลอดตาม bandwidth policy

## 4. Kill Switch

### ระดับ

| ระดับ | ผล |
|---|---|
| **Global** | หยุดทุก rollout + ทุก job ที่ยังไม่ถึง PONR + หยุดแจก agent update |
| Channel | หยุดเฉพาะ image ของ channel นั้น |
| Site | หยุดเฉพาะเครื่องใน site |
| Rollout | หยุด rollout เดียว (= pause) |

### กลไก (ทำงานได้แม้เครื่องออฟไลน์บางส่วน)

1. **Heartbeat** — ทุก heartbeat ตอบ `halt: {global, channels[], sites[]}` → agent ยกเลิก job ที่ยังไม่ถึง `handoff` ทันที (สถานะ `cancelled`)
2. **Go-token** — ก่อน `handoff` agent ต้องขอ `POST /v1/agent/jobs/{id}/go` → server ตรวจ kill switch + slot + window → ออก token เซ็นแล้ว อายุ 15 นาที
3. **WinPE check** — ใน `pe_verify` ถ้ามีเน็ต ถาม server อีกครั้ง; kill switch เปิด → `aborted`
4. **Manifest หมดอายุ** — server หยุดออก/ต่ออายุ manifest ทันทีที่ kill switch เปิด → เครื่องที่ออฟไลน์และมี manifest อยู่จะทำต่อได้ไม่เกิน `not_after`
5. job ที่ **ผ่าน PONR แล้ว** — หยุดไม่ได้ ทำต่อจนจบ (completed / rolled_back / failed) — Dashboard แสดงจำนวนนี้ชัดเจนตอนกด

### UX

- ปุ่มแดงบนทุกหน้า (RBAC: `release_manager`, `admin`) — ต้องพิมพ์เหตุผล; ปลดต้องใช้ 2 คน (คนกด + คนอนุมัติ) สำหรับ Global
- หลังกด แสดง: "หยุดแล้ว N job · ผ่าน PONR แล้วกำลังทำต่อ M job · เครื่องออฟไลน์ที่มี manifest ค้าง K เครื่อง (หมดอายุภายใน X ชม.)"

## 5. Circuit Breaker (หยุดเองอัตโนมัติ)

ประเมินทุก 1 นาทีต่อ rollout และต่อ (rollout, model):

| เงื่อนไข | การกระทำ |
|---|---|
| wave canary มี `failed` หรือ `rolled_back` ≥ 1 | pause rollout |
| wave อื่น: (`failed` + `rolled_back`) ≥ 2 ใน 10 job แรก หรือ > 5% (เมื่อ n ≥ 20) | pause rollout |
| รุ่นใดรุ่นหนึ่ง: ล้ม ≥ 2 ครั้งติด | hold เฉพาะรุ่นนั้น (`held: model_breaker`) |
| `suspected_failure` > 3% ของ job ที่กำลังทำ | pause rollout + แจ้งเตือน (อาจเป็นปัญหาเครือข่าย/server) |
| ทั้งระบบ: ล้ม > 10 job ใน 30 นาที ข้ามหลาย rollout | **Global kill switch อัตโนมัติ** |

breaker ทำงาน → แจ้ง `release_manager` (อีเมล/Line/Teams webhook) + แนบสรุป error code ที่พบมากสุด + ลิงก์ log

## 6. Fleet Rollback

สถานการณ์: image v1.6 ลงไปแล้ว 200 เครื่อง และพบปัญหาหลังใช้งาน

```
Release Manager → "Rollback v1.6 → v1.5" (ต้องระบุขอบเขต: ทั้งหมด / site / model / รายชื่อ)
  → สร้าง Rollout ประเภท rollback (image = N-1, ข้าม wave canary ได้เพราะ v1.5 เคยผ่านแล้ว)
  → ใช้ concurrency + maintenance window + การแจ้งผู้ใช้ ตามปกติ
  → เครื่องที่มี v1.5 บน S: → ไม่ใช้เน็ต (~25 นาที/เครื่อง)
  → เครื่องที่ไม่มี (S: เล็ก / ลบไปแล้ว) → โหลดจาก site cache/server
  → channel: v1.5 กลับเป็น published, v1.6 = withdrawn (เก็บไฟล์ไว้วิเคราะห์ ห้าม deploy)
  → N-1 ของ channel ใหม่ = รุ่นก่อน v1.5 (ถ้ายังมี) — แจ้งเตือนถ้าไม่มี
```

- Dashboard ก่อนกด: "200 เครื่อง · 188 มี v1.5 บน S: · 12 ต้องโหลด (108 GB) · ETA ตาม window: 2 คืน"
- ข้อมูลบน C: ของผู้ใช้หายอีกรอบ (D-05) — ข้อความแจ้งผู้ใช้ต้องบอกชัด

## 7. หน้าจอที่ต้องมี

| หน้า | เนื้อหา |
|---|---|
| Rollout | wave, gate, สถานะต่อเครื่อง, breaker, ปุ่ม pause/resume/advance |
| Fleet health | job ที่กำลังทำ แยกสถานะ, `suspected_failure`, slot ที่ใช้ต่อ site |
| Kill switch | สถานะปัจจุบัน ทุกระดับ + ประวัติ (ใคร/เมื่อไหร่/เหตุผล) |
| Model matrix | ตาราง model × image: ผ่าน/ล้ม/ยังไม่ทดสอบ |
