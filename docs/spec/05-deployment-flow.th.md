# 05 · Deployment Flow & State Machine

[English](05-deployment-flow.md) · **ไทย**

> ไฟล์นี้คือ source of truth ของ `pkg/state` — ทั้ง agent, swim-pe และ server ต้อง implement ตามนี้

## 1. สถานะของ Job

```
queued ─► preflight ─► staging ─► staged ─► waiting ─► handoff ─► pe_verify ═══PONR═══► applying ─► first_boot ─► health_check ─► completed
            │            │                    │           │           │                    │              │              │
            ▼            ▼                    ▼           ▼           ▼                    ▼              ▼              ▼
         blocked      (retry)            cancelled    aborted     aborted            (retry ×2)     (supervisor)   (supervisor)
                                                                                         └──────────┬─────┴──────────────┘
                                                                                                    ▼
                                                                                         fallback_apply (N-1) ─► rolled_back
                                                                                                    │
                                                                                                    ▼
                                                                                                 failed  (ต้องใช้ช่าง)

server-derived:  suspected_failure  (เงียบเกิน ETA P90 × 1.5 ในสถานะที่ควรมีสัญญาณ)
```

| สถานะ | ใครทำ | สภาพเครื่อง | ย้อนกลับได้ |
|---|---|---|---|
| `queued` | server | ปกติ | ✅ |
| `preflight` | agent | ปกติ | ✅ |
| `blocked` | agent | ปกติ — ไม่ผ่าน preflight (BLOCK) | ✅ (จบ) |
| `staging` | agent | ปกติ ใช้งานได้ | ✅ |
| `staged` | agent | ไฟล์ครบและตรวจแล้วบน S: | ✅ |
| `waiting` | agent | รอ schedule / concurrency slot / ผู้ใช้เลื่อน | ✅ |
| `handoff` | agent | กำลังแก้ BCD + suspend BitLocker + รีบูต | ✅ |
| `pe_verify` | swim-pe | อยู่ใน WinPE, ยังไม่แตะ C: | ✅ (รีบูตกลับ Windows เดิม) |
| **PONR** | swim-pe | **format C:** | ❌ |
| `applying` | swim-pe | apply image + drivers + ODJ + agent + bcdboot | ทำซ้ำได้ |
| `first_boot` | Windows Setup | specialize / OOBE | supervisor คุม |
| `health_check` | agent ใหม่ | Windows ใหม่ทำงาน รอ commit | supervisor คุม |
| `fallback_apply` | swim-pe | กำลัง apply image N-1 จาก S: | ทำซ้ำได้ |
| `completed` | agent | สำเร็จ | — |
| `cancelled` | agent/server | ยกเลิกก่อน `handoff` (คนสั่ง, kill switch, manifest หมดอายุ) → Windows เดิม | — |
| `aborted` | agent/pe | ล้มก่อน PONR → อยู่ใน **Windows เดิม** | — |
| `rolled_back` | agent | ล้มหลัง PONR → ได้ **image N-1** ใช้งานได้ | — |
| `failed` | pe/server | ใช้งานไม่ได้ ต้องใช้ช่าง | — |

## 2. `state.json` (บน `S:\swim\jobs\<job_id>\`)

```json
{
  "schema_version": 3,
  "job_id": "job_9821a4",
  "host_id": "h_01HFX...",
  "phase": "applying",
  "phase_attempt": 1,
  "boot_attempts": 0,
  "engine": "winpe",
  "target": {
    "disk_unique_id": "nvme.8086&...&SN12345",
    "partition_guids": {
      "esp":   "{c12a7328-...}",
      "os":    "{9f3a...}",
      "stage": "{51c0...}",
      "winre": "{de94...}",
      "data":  "{a1b2...}"
    }
  },
  "image":    { "id": "img_160", "path": "images/img_160/image.wim", "sha256": "…" },
  "fallback": { "id": "img_150", "path": "images/img_150/image.wim", "sha256": "…" },
  "using_fallback": false,
  "ponr_passed_at": null,
  "commit": { "required": true, "committed_at": null },
  "timings": { "pe_verify": 41.2 },
  "updated_at": "2026-11-23T14:02:11Z"
}
```

- เขียนแบบ atomic (write temp → fsync → rename) **ทุกครั้งก่อน**เริ่มขั้นใหม่
- **ระบุพาร์ทิชันด้วย GPT partition GUID เท่านั้น** ห้ามใช้เลขดิสก์/ตัวอักษรไดรฟ์ (ใน WinPE ไม่ตรงกับใน Windows)

## 3. ขั้นตอนฝั่ง Windows เดิม (agent)

### 3.1 staging
1. ตรวจลายเซ็น + วันหมดอายุของ manifest
2. โหลด image (หรือ delta), `boot.wim`, `boot.sdi`, driver pack, agent MSI ล่าสุด (D-18) → verify ทุก chunk
3. ถ้า N-1 ยังไม่มีบน S: และพื้นที่พอ → โหลด N-1 ด้วย (ลำดับความสำคัญต่ำ)
4. → `staged`

### 3.2 waiting
- รอ `schedule.not_before`, maintenance window, concurrency slot (ขอจาก server — [07](07-fleet-safety.th.md)), การแจ้งผู้ใช้/เลื่อน ([03 §9](03-agent.th.md))
- ก่อนออกจาก waiting: **ขอ go-token จาก server** (ยืนยัน kill switch ปิด + slot ยังว่าง) — ถ้าออฟไลน์และเป็น job local ที่มี standing manifest ให้ใช้วันหมดอายุของ manifest แทน

### 3.3 handoff (ทำตามลำดับนี้เท่านั้น)

```bat
:: 1) ตรวจ hash ของไฟล์บน S: อีกรอบ (quick: ตรวจ chunks.json sig + ขนาด + sample 5 chunk)
:: 2) BitLocker: suspend ให้กลับเข้า Windows เดิมได้ถ้า PE ล้มก่อน PONR
manage-bde -protectors -disable C: -RebootCount 2

:: 3) BCD — ใช้ GUID คงที่ของ SWIM (idempotent, ไม่ต้อง parse ข้อความที่แปลภาษา)
bcdedit /create {ramdiskoptions} /d "SWIM Ramdisk"            & rem ถ้ามีแล้วข้าม
bcdedit /set {ramdiskoptions} ramdisksdidevice partition=S:
bcdedit /set {ramdiskoptions} ramdisksdipath \swim\winpe\<pe_ver>\boot.sdi
bcdedit /create {5a1f0c3e-0000-4000-8000-5717aa000001} /d "SWIM WinPE" /application osloader
set PE={5a1f0c3e-0000-4000-8000-5717aa000001}
bcdedit /set %PE% device   ramdisk=[S:]\swim\winpe\<pe_ver>\boot.wim,{ramdiskoptions}
bcdedit /set %PE% osdevice ramdisk=[S:]\swim\winpe\<pe_ver>\boot.wim,{ramdiskoptions}
bcdedit /set %PE% path \windows\system32\boot\winload.efi
bcdedit /set %PE% systemroot \windows
bcdedit /set %PE% winpe yes
bcdedit /set %PE% detecthal yes

:: 4) บูตเข้า PE "ครั้งเดียว" — ถ้า PE บูตไม่ขึ้น เครื่องกลับ Windows เดิมเอง
bcdedit /bootsequence %PE%

:: 5) เขียน state=handoff, flush outbox, แล้วรีบูต
shutdown /r /t 0
```

> S: ต้อง mount เป็นตัวอักษรชั่วคราวระหว่างตั้ง BCD — BCD เก็บเป็น partition identity ไม่ใช่ตัวอักษร

## 4. ขั้นตอนใน WinPE (swim-pe)

```
startnet.cmd → wpeinit → swim-pe.exe
```

### 4.1 pe_verify (ยังย้อนกลับได้)
1. หา S: จาก GPT GUID → อ่าน `state.json` + manifest + ตรวจลายเซ็น (public key ฝังใน boot.wim)
2. หาพาร์ทิชันเป้าหมายทั้งหมดจาก GUID — ไม่เจอ = `aborted` (มักเป็นเพราะไม่มี storage driver)
3. ตรวจ SHA256 ของ image **เต็มไฟล์** (หรือทุก chunk)
4. ตรวจไฟ AC อีกครั้ง
5. (ถ้ามีเน็ต) ถาม server ว่า kill switch ปิดอยู่ไหม — ถ้าเปิด = `aborted`
6. ล้มข้อใด → เขียน log + `aborted` → `wpeutil reboot` (bootsequence หมดแล้ว → กลับ Windows เดิม)

### 4.2 PONR
```
1. bcdedit /default {SWIM-PE}          ← ไฟดับหลังจากนี้ = บูตกลับมา PE แล้วทำต่อ
2. state.phase = "applying", ponr_passed_at = now   (เขียนก่อน format)
3. format C: (เลือกจาก GUID)  →  format fs=ntfs quick label=OS
```
- โหมด `repartition` (ระบุใน manifest): ลบ/สร้างเฉพาะพาร์ทิชันที่อยู่ **ก่อน** S: ตาม Disk Profile rule — S:, ESP, WinRE ไม่แตะ

### 4.3 applying (ทำซ้ำได้ทั้งหมด — ถ้า retry ให้ format ใหม่ก่อน)
```
1. apply:    wimlib-imagex apply image.wim 1 W:\ [--ref=base.wim]   (หรือ dism /Apply-Image)
2. drivers:  dism /Image:W:\ /Add-Driver /Driver:S:\swim\drivers\<pack>\ /Recurse
3. unattend: เขียน W:\Windows\Panther\unattend.xml (hostname, locale, timezone, OOBE skip, LAPS policy)
4. ODJ:      djoin /requestodj /loadfile <blob> /windowspath W:\Windows   → ลบ blob ทันที
5. agent:    ใส่ agent เวอร์ชัน published ล่าสุด (D-18) แบบ offline — ไม่ต้องรัน MSI ใน WinPE:
             a. copy swim-agent.exe → W:\Program Files\SWIM\
             b. reg load HKLM\SWIMSYS W:\Windows\System32\config\SYSTEM → สร้าง ControlSet001\Services\SWIMAgent
                (Type=0x10, Start=2, ErrorControl=1, ImagePath="…\swim-agent.exe" run, ObjectName=LocalSystem) → reg unload
             c. เขียน W:\ProgramData\SWIM\bootstrap.json = {server, cert_sha256, reimage_ticket} (ACL: SYSTEM+Administrators)
             d. ห้ามคัด state.json เดิม — DPAPI ของ Windows ใหม่เปิดไม่ได้อยู่แล้ว และ clone guard จะทิ้งให้
             → Windows ใหม่บูต: service สตาร์ต → enroll ด้วย ticket → host_id เดิม → ลบ bootstrap.json (H-01)
6. boot:     W:\Windows\System32\bcdboot.exe W:\Windows /s <ESP> /f UEFI   ← ใช้ bcdboot ของ image ใหม่
7. หา {new-windows}: bcdboot ตั้ง entry ใหม่เป็น default ให้อยู่แล้ว → อ่าน `bcdedit /store <ESP>\EFI\Microsoft\Boot\BCD /v /enum {default}`
   แล้วดึง GUID ด้วย regex `\{[0-9a-fA-F-]{36}\}` (ไม่ parse ข้อความ) → บันทึกลง state.json
8. ตั้งการบูตแบบ "ทดลอง" (ใช้ /store ของ ESP ทุกคำสั่งใน WinPE):
   bcdedit /default {SWIM-PE}  +  bcdedit /bootsequence {new-windows}
9. state.phase = "first_boot", boot_attempts = 1 → reboot
```
> `bcdboot` ค่าเริ่มต้นจะ migrate entry เดิม (ไม่ใส่ `/c`) → entry `{SWIM-PE}` และ `{ramdiskoptions}` ยังอยู่
ล้มขั้นใด → retry สูงสุด **2 ครั้ง** → ยังล้ม → `fallback_apply` (§6)

## 5. Boot Supervisor (หลัง apply)

```
                   default = {SWIM-PE}   bootsequence = {new-windows}
                                   │
               ┌──── Windows ใหม่บูต ─────┐
               │                          │
     agent ใหม่ผ่าน health check      BSOD / ค้าง / Setup รีบูตกลางคัน
               │                          │
     bcdedit /default {current}            ▼
     ลบ entry SWIM-PE                  ตกกลับ WinPE (เพราะ default = PE)
     state = completed ✅                  │
                                  swim-pe อ่าน ImageState จาก registry offline:
                                  HKLM\SOFTWARE\Microsoft\Windows\CurrentVersion\Setup\State\ImageState
                                           │
                       ┌───────────────────┼───────────────────────────────┐
           IMAGE_STATE_SPECIALIZE_*  /     IMAGE_STATE_COMPLETE แต่         boot_attempts > 3
           UNDEPLOYABLE (setup ยังเดิน)     ยังไม่ commit (health ไม่ผ่าน)   หรือ ImageState ไม่เปลี่ยน 2 รอบติด
                       │                   │                                │
           boot_attempts++                 boot_attempts++                  ▼
           bootsequence → Windows ใหม่      ถ้า ≤ 3 → บูตลองใหม่              fallback_apply (N-1)
```

Windows Setup รีบูตเองระหว่าง specialize → OOBE จึงต้องอ่าน `ImageState` ก่อนตัดสินว่าล้ม

### Health check (agent ใหม่ ภายใน 20 นาทีหลัง OOBE จบ)

| ID | ตรวจ | บังคับ |
|---|---|---|
| H-01 | agent re-enroll สำเร็จ (host_id เดิม) | ✅ (ถ้ามีเน็ต; ไม่มีเน็ต → ผ่านแบบมีเงื่อนไข) |
| H-02 | ไม่มี BugCheck event ตั้งแต่บูต | ✅ |
| H-03 | Domain: `Test-ComputerSecureChannel` = True (ถ้า job มี domain) | ✅ |
| H-04 | ไม่มีอุปกรณ์ class `DiskDrive`, `SCSIAdapter`, `Net` ที่ ProblemCode ≠ 0 | ✅ |
| H-05 | ออกเน็ตได้ (ถ้าเป็นเครื่องสาย LAN ตาม inventory) | WARN |
| H-06 | activation (`LicenseStatus`) | WARN (ตรวจซ้ำใน 24 ชม.) |
| H-07 | BitLocker เริ่มเข้ารหัส + key escrow สำเร็จ (ถ้า policy บังคับ) | WARN → ERROR ถ้าไม่สำเร็จใน 2 ชม. |

ผ่านข้อบังคับทั้งหมด → **commit**: `bcdedit /default {current}`, `bcdedit /delete {SWIM-PE}`, เก็บ `{ramdiskoptions}` ไว้ใช้ครั้งต่อไปได้, state = `completed`

## 6. Fallback / Rollback

### fallback_apply (อัตโนมัติ)
- เงื่อนไข: apply ล้ม 3 ครั้ง หรือ boot supervisor ตัดสินว่าล้ม
- ทำ: format C: → apply **N-1** จาก S: → ขั้นตอน §4.3 เหมือนเดิมด้วย manifest ส่วน `fallback` → supervisor เหมือนเดิม
- ผ่าน → `rolled_back` (ใช้งานได้ + log เต็มส่งขึ้น server เป็น **FATAL** ของ image ใหม่บนรุ่นนี้)
- N-1 ก็ล้ม หรือไม่มี N-1 บน S: → `failed`: ค้างอยู่ใน WinPE แสดงหน้าจอ "ติดต่อ IT + job_id + error code + QR ของ job" และส่งรายงานถ้ามีเน็ต

### rollback (สั่งโดยคน — job ใหม่ประเภท `rollback`)
- deploy image N-1 แบบปกติทุกขั้น (มี preflight, แจ้งผู้ใช้) — ใช้ไฟล์บน S: ไม่ต้องโหลด
- ใช้สำหรับ fleet rollback ([07](07-fleet-safety.th.md))

## 7. Engine A — `setup.exe /auto clean` (PoC / สำรอง)

```bat
setup.exe /auto clean /quiet /eula accept /InstallFrom S:\swim\images\<id>\image.wim ^
  /Unattend:S:\swim\jobs\<job>\unattend.xml /InstallDrivers S:\swim\drivers\<pack> ^
  /DynamicUpdate disable /Compat IgnoreWarning /PostOOBE S:\swim\agent\install-agent.cmd
```
- ต้องใช้ `setup.exe` ที่มาจาก **ISO ของ Windows รุ่นเดียวกับ image** (เก็บเป็น artifact แยกของ channel)
- Setup จัดการ BCD/rollback ก่อนถึงขั้น commit ของมันเอง — map ผลลัพธ์: setup ล้มก่อน commit = `aborted`; หลัง commit แล้วล้ม = `failed`
- ข้อจำกัด: ช้ากว่า, แบ่งพาร์ทิชันไม่ได้, ไม่มี boot supervisor ของเรา, ต้องทดสอบว่า Setup ยอมลงเวอร์ชันที่เก่ากว่า/คนละ edition หรือไม่

## 8. หลังติดตั้ง (Post-deploy)

| เรื่อง | วิธี |
|---|---|
| Domain join | ODJ blob สร้างบน Utility VM: `djoin /provision /domain <d> /machine <name> /machineou <ou> /reuse /savefile <f>` ใช้ **บัญชี join เฉพาะ** ที่อยู่ใน policy *"Domain controller: Allow computer account re-use during domain join"* (จำเป็นตั้งแต่การ hardening KB5020276) |
| ความลับของ ODJ blob | ดู [08 §5](08-security.th.md) — v1: อายุ 24 ชม., ใช้ครั้งเดียว, ลบทันทีหลังใช้, ถ้า job ยกเลิก/หมดอายุ server reset รหัสบัญชีเครื่อง; v2: seal ด้วย TPM |
| Local admin | **Windows LAPS** (policy ผ่าน GPO/Intune); unattend ไม่มีรหัสผ่าน, built-in Administrator ปิด |
| BitLocker | เปิดตาม policy (TPM protector), escrow: AD (`manage-bde -protectors -adbackup`) หรือ Entra (`BackupToAAD-BitLockerKeyProtector`) — agent รายงานผล escrow; recovery object เก่าใต้ computer object ใน AD ให้ job ทำความสะอาดลบที่เก่ากว่า 30 วัน |
| Activation | ตาม [02 §8](02-image-pipeline.th.md) |
| แอป | ผ่าน Intune / winget / SCCM ขององค์กร (นอก scope SWIM v1) |

## 9. ETA และความเสี่ยงก่อนเริ่ม

### ETA

```
ETA = download_remaining_bytes / measured_throughput(host, 7 วัน, หลัง throttle)
    + P50(apply_seconds | model, image_size_bucket)
    + P50(first_boot_seconds | model, channel)
    + 120s (reboot ×2)
แสดง P50 และ P90 (ใช้ P90 ของแต่ละช่วงแทน P50)
ไม่มีประวัติ → ใช้ค่าเริ่มต้น apply = image_expanded_GB × 25s, first_boot = 600s และติดป้าย "ยังไม่มีข้อมูล"
```

### Risk score

```
success_rate = (สำเร็จ + 1) / (ทั้งหมด + 2)      # คีย์: (vendor, model, bios_version, image_id); ถ้าไม่มี ถอยไป (model, channel) → (model)
risk = HIGH   ถ้ามี BLOCK, หรือ success_rate < 0.80, หรือ รุ่นนี้ยังไม่เคยผ่าน image นี้ใน wave canary
       MEDIUM ถ้ามี WARN ≥ 2 หรือ 0.80 ≤ success_rate < 0.95
       LOW    อื่น ๆ
HIGH → ปุ่ม deploy ใช้ไม่ได้ ยกเว้น ring canary หรือผู้มีสิทธิ์ override (บันทึก audit)
```

ตัวอย่างที่แสดงก่อนยืนยัน:

```
BKK-FIN-042 · Dell Latitude 5440 · BIOS 1.18 · win11-x64-stable v1.6
Preflight   ✅ AC  ✅ UEFI/GPT  ✅ SecureBoot  ✅ 512 GB  ⚠ BitLocker (จะ suspend)  ✅ Driver matched
เวลาคาด     โหลดแล้ว 100% · ติดตั้ง ~14 นาที · บูตครั้งแรก ~9 นาที → ~25 นาที (P90 33 นาที)
ประวัติ     รุ่นนี้ + image นี้ สำเร็จ 47/48 (ล้ม 1: ไฟดับ → rolled_back)
ความเสี่ยง   LOW
```

## 10. Timeout ต่อขั้น (watchdog)

| ขั้น | Timeout | เกินแล้ว |
|---|---|---|
| preflight | 10 นาที | `blocked` (error: timeout) |
| staging | ไม่จำกัด (มี progress) — ไม่มี progress 2 ชม. | retry แหล่งอื่น |
| handoff → pe_verify | ETA P90 × 1.5 | server: `suspected_failure` |
| pe_verify | 20 นาที | `aborted` |
| applying | max(apply P90 × 3, 45 นาที) | retry → fallback |
| first_boot → health_check | 60 นาที / boot attempt | supervisor ตัดสิน |
