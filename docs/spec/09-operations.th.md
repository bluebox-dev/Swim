# 09 · Operations — Log, Monitoring, Backup/DR, ช่องทางสำรอง, Lifecycle

[English](09-operations.md) · **ไทย**

## 1. Log & Telemetry (D-11)

### Severity

| ระดับ | ความหมาย | ตัวอย่าง |
|---|---|---|
| `FATAL` | job จบแบบ `failed` หรือ `rolled_back` | apply ล้ม 3 ครั้ง, Windows ใหม่บูตไม่ขึ้น |
| `ERROR` | job สำเร็จแต่มีส่วนที่ต้องแก้ | domain join ล้ม, BitLocker escrow ล้ม, อุปกรณ์สำคัญมี error |
| `WARN` | ทำงานได้แต่ควรรู้ | ไม่ activate ใน 24 ชม., ไม่มี driver pack ตรงรุ่น |
| `INFO` | ปกติ | เปลี่ยนสถานะ, เวลาแต่ละขั้น |
| `DEBUG` | รายละเอียด | ผลคำสั่งเต็ม — เก็บในเครื่องเท่านั้น |

### อะไรส่งขึ้น Server

| ข้อมูล | เมื่อไหร่ | ขนาด |
|---|---|---|
| **Job summary** (สถานะสุดท้าย, เวลาแต่ละขั้น, error code, preflight, model/BIOS) | **ทุก job** | ~1 KB |
| Event เปลี่ยนสถานะ | ทุกครั้ง (ผ่าน outbox) | เล็ก |
| **Log bundle เต็ม** (`swim-pe.log`, `state.json` history, `dism.log`, `Panther\setupact.log`/`setuperr.log`, `bcdedit /enum all /v`, `Get-Disk/Get-Partition`, event log System/Application 24 ชม.) | อัตโนมัติเมื่อ `FATAL`/`ERROR` | 1–50 MB (zip) |
| Log bundle ตามคำขอ | คำสั่ง `collect_logs` | |

- WinPE เขียน log ลง `S:\swim\logs\<job_id>\` เสมอ → ถ้า WinPE มีเน็ตส่ง summary ทันที, ไม่มีก็ให้ agent ใหม่ส่งหลังบูต
- เครื่องที่ส่งอะไรไม่ได้เลย → server ตั้ง `suspected_failure` ([05 §10](05-deployment-flow.th.md)); ช่างเก็บ log จาก S: ได้ด้วย SWIM USB หรือ `swim logs --export` หลังซ่อม
- Retention บน server: summary 2 ปี, log bundle 180 วัน

### Error code

รูปแบบ `SWIM-<phase>-<nnn>` เช่น `SWIM-PE-012` (ไม่พบพาร์ทิชัน OS จาก GUID) — รายการเต็มเก็บใน `pkg/state/errors.go` + หน้า docs ที่ลิงก์จาก Dashboard พร้อมวิธีแก้

## 2. Monitoring & Alerting

- backend เปิด `/metrics` (Prometheus) — ทางเลือกติดตั้ง Prometheus + Grafana ใน compose
- แจ้งเตือนผ่าน webhook (Teams / Line / Slack / อีเมล)

| Alert | เงื่อนไข |
|---|---|
| Circuit breaker / kill switch ทำงาน | ทันที |
| `suspected_failure` ใหม่ | ทันที (สรุปทุก 15 นาที ถ้าหลายเครื่อง) |
| พื้นที่ storage server | ≥ 80% (warn), ≥ 90% (critical) |
| ใบรับรอง / กุญแจใกล้หมดอายุ | ≤ 30 วัน |
| Backup ล้ม / ไม่มี backup ใหม่ > 26 ชม. | ทันที |
| Agent version ค้าง | > 5% ของ fleet ยังอยู่เวอร์ชันที่ต่ำกว่า N-1 เกิน 14 วัน |
| เครื่องเงียบ | > 30 วัน (รายงานรายสัปดาห์) |
| Channel ใกล้หมด support | 180 / 90 / 30 วัน |
| Utility VM / Test Runner VM ไม่ตอบ | > 10 นาที |

## 3. Backup & Disaster Recovery

| ข้อมูล | วิธี | RPO | ที่เก็บสำเนา |
|---|---|---|---|
| PostgreSQL | pgBackRest: full รายสัปดาห์ + diff รายวัน + WAL archive ต่อเนื่อง | 15 นาที | NAS + offsite (เข้ารหัส) |
| Images / WinPE / Driver packs (published + N-1) | rsync รายวัน + ตรวจ SHA256 | 24 ชม. | NAS |
| Golden Master qcow2 + snapshot ล่าสุด | สำรองหลังทุก build | ต่อ build | NAS |
| Config, compose, nginx, secrets | git (ไม่รวม secrets) + backup เข้ารหัสของ secrets | ต่อการเปลี่ยน | NAS + offsite |
| กุญแจ ([08 §3](08-security.th.md)) | ตาม key ceremony — ไม่อยู่ใน backup ปกติ | — | ตู้เซฟ |

- **RTO เป้าหมาย 4 ชม.** — มี runbook สร้าง server ใหม่จาก backup (Ubuntu + compose + restore DB + rsync storage)
- ซ้อม restore ทุกไตรมาส บน VM แยก แล้วบันทึกผล

### ถ้า Server ล่ม เครื่องลูกเป็นอย่างไร

| สถานการณ์ | ผล |
|---|---|
| job ที่ผ่าน PONR แล้ว | ทำต่อจนจบได้ (WinPE ไม่ต้องพึ่ง server) — ส่งผลตามหลัง |
| job ที่ `staged` และรอ go-token | รอจนกว่าจะได้ หรือ manifest หมดอายุ → `cancelled` |
| local deploy ด้วย standing manifest | ทำได้จนกว่า manifest หมดอายุ (7 วัน) — ไม่มี domain join |
| agent update | หยุดชั่วคราว ไม่มีผลกับการทำงาน |
| outbox | สะสมในเครื่อง ส่งเมื่อ server กลับมา (ไม่มีข้อมูลหาย) |

## 4. ช่องทางสำรอง (D-12)

### 4.1 ช่างลง Windows เองด้วย USB (หลัก)

```
1. ลง Windows จาก USB ตามปกติ (edition ตรงกับ license เครื่อง)
2. ติดตั้ง agent:  msiexec /i swim-agent.msi ENROLL_TOKEN=<token ของ site> SERVER_URL=https://swim.corp.local /qn
3. agent enroll → server จับคู่ host เดิมด้วย smbios_uuid + serial → ประวัติต่อเนื่อง
4. agent สร้าง S: ใหม่ (ถ้าลงแบบล้างดิสก์ทั้งลูก S: หายไปแล้ว) → stage image ปัจจุบัน
5. (แนะนำ) สั่ง deploy image มาตรฐานทับอีกรอบ เพื่อให้เครื่องตรงมาตรฐาน
```
- Dashboard แสดงเครื่องที่ re-enroll แบบ "ลงเอง" ด้วยป้าย `manual_install` จนกว่าจะ deploy image มาตรฐานสำเร็จ

### 4.2 SWIM USB (ทางเลือก, Phase 4)

- USB ที่มี `boot.wim` (published) + `swim-pe` ในโหมด `usb` + root CA/keyset
- บูตแล้ว: ต่อเน็ต (LAN) → ช่างใส่ enrollment token หรือสแกน QR → ดึง manifest + image จาก server → ล้าง **ทั้งดิสก์** ตาม Disk Profile (สร้าง ESP/MSR/OS/S:/WinRE ใหม่) → เหมือน flow ปกติตั้งแต่ apply
- ใช้เก็บ log จาก S: ของเครื่องที่พังได้ (`swim-pe collect-logs → USB`)

## 5. Lifecycle อื่น ๆ

### Windows version lifecycle
- `channels.eol_date` จากตาราง support ของ Microsoft — เตือน 180/90/30 วัน
- 90 วันก่อนหมด: บล็อกการสร้าง rollout ใหม่ไปยัง channel นั้น (ยกเว้น rollback) และแสดงจำนวนเครื่องที่ยังอยู่

### Driver packs
- ต่อ `(vendor, match_key, os_build, arch)`; อัปเดตเมื่อผู้ผลิตออกใหม่หรือมีปัญหา
- driver pack ใหม่ต้องผ่าน canary บนรุ่นนั้นก่อนใช้กับ broad (ใช้กลไก rollout เดียวกัน ผูกกับ image)
- match key: Dell = `SystemSKU`, Lenovo = 4 ตัวแรกของ `Win32_ComputerSystemProduct.Name`, HP = `Win32_BaseBoard.Product`, อื่น ๆ = `Manufacturer + Model`

### WinPE
- rebuild ทุกครั้งที่: ADK ใหม่, มีรุ่นเครื่องใหม่ที่ต้องเพิ่ม storage/NIC driver, Secure Boot/boot chain มีการเปลี่ยนแปลง
- ผ่าน test pipeline + canary เหมือน image

## 6. Runbooks ที่ต้องเขียน (docs/runbooks/)

| Runbook | เมื่อไหร่ |
|---|---|
| RB-01 เครื่องค้าง `suspected_failure` | ทุกครั้งที่แจ้งเตือน |
| RB-02 ลง USB + re-enroll | เครื่อง `failed` |
| RB-03 Circuit breaker ทำงาน — วิเคราะห์และตัดสินใจ resume/rollback | breaker |
| RB-04 Fleet rollback | image มีปัญหาหลังปล่อย |
| RB-05 Agent update ล้มเหลวเป็นวงกว้าง | breaker ของ agent |
| RB-06 Restore server จาก backup | DR |
| RB-07 หมุนกุญแจ / กุญแจรั่ว | ตามรอบ / incident |
| RB-08 เพิ่มรุ่นฮาร์ดแวร์ใหม่ (driver, WinPE, canary) | ซื้อเครื่องรุ่นใหม่ |
| RB-09 Build image รายเดือน | ทุกเดือน |
