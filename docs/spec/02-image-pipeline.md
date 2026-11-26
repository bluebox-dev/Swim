# 02 · Image Pipeline & Lifecycle

## 1. วงจรสถานะของ Image

```
 draft ──build──► building ──capture ok──► testing ──pass──► pending ──approve──► published ──(ถูกแทน)──► superseded ──retention──► archived ──► deleted
                     │                        │                          │
                     └──fail──► failed        └──fail──► test_failed     └──reject──► rejected

 published ──fleet rollback──► withdrawn   (ห้าม deploy, เก็บไฟล์ไว้วิเคราะห์ — ดู 07 §6)
```

| สถานะ | ความหมาย | ใช้ deploy ได้ไหม |
|---|---|---|
| `building` | กำลัง sysprep/capture | ❌ |
| `testing` | กำลังรัน test บน Test Runner VM | ❌ |
| `pending` | ผ่าน test รอ Approver | เฉพาะ ring `canary` |
| `published` | ใช้จริง — **1 ตัวต่อ channel** | ✅ |
| `superseded` | ถูกแทนแล้ว แต่ยังเป็น **N-1** สำหรับ rollback | ✅ (rollback เท่านั้น) |
| `archived` | เก็บไฟล์ไว้แต่ไม่ใช้ | ❌ |

กติกา:
- Partial unique index: `UNIQUE (channel_id) WHERE status = 'published'`
- **Builder กับ Approver ต้องเป็นคนละคน** (ตรวจที่ backend)
- ลบไฟล์ได้เฉพาะเมื่อไม่มี job ที่ยังไม่จบอ้างถึง และไม่ใช่ N-1 ของ channel ใด

## 2. Channel

| ฟิลด์ | ตัวอย่าง |
|---|---|
| `name` | `win11-x64-stable` |
| `os` / `release` | `windows11` / `24H2` |
| `arch` | `amd64` (`arm64` แยก channel) |
| `edition` | `Enterprise` / `Pro` (ต้องตรงกับ license ของเครื่อง — ดู §8) |
| `eol_date` | วันหมด support ของ Microsoft — เตือนที่ 180/90/30 วัน |

host ผูกกับ channel ได้ 1 channel (ตาม arch + นโยบายแผนก)

## 3. กติกา Golden Master (บังคับ — Test Runner ตรวจอัตโนมัติ)

### ห้ามมีใน image
- [ ] โดเมน join / cached credential / user profile อื่นนอกจาก Default
- [ ] **Agent ที่สร้าง ID ประจำเครื่องตอนติดตั้ง** — EDR/AV onboarding (Defender for Endpoint, CrowdStrike, SentinelOne ฯลฯ), SCCM client, Intune enrollment, monitoring agent → ถ้าจำเป็นต้องติด ให้ใช้ **โหมด golden image/VDI ของผู้ผลิต** เท่านั้น
- [ ] `HKLM\SOFTWARE\Microsoft\Windows\CurrentVersion\WindowsUpdate\SusClientId`
- [ ] `swim-agent` ที่ enroll แล้ว (WinPE จะฉีดตัวล่าสุดให้ — D-18)
- [ ] pagefile, hiberfil, ไฟล์ temp, Delivery Optimization cache
- [ ] license key แบบ retail ของโปรแกรมที่ผูกเครื่อง

### ต้องทำก่อน Sysprep
1. ทำงานใน **Audit Mode** (Ctrl+Shift+F3 ตอน OOBE)
2. ปิด Microsoft Store auto-update (Policy) ระหว่าง build — กัน Appx error ตอน sysprep
3. ติดตั้ง Windows Update ล่าสุด แล้ว `Dism /Online /Cleanup-Image /StartComponentCleanup /ResetBase`
4. ติดตั้ง VirtIO driver ได้ (ไม่มีผลเสียกับเครื่องจริง) แต่ **ห้ามใช้ `sysprep /mode:vm`**
5. ปิด BitLocker ใน VM ต้นแบบ
6. snapshot `pre-sysprep-<version>` **ก่อนทุกครั้ง**

## 4. Build Pipeline (ปุ่ม "Build Image")

```
1. preflight-capacity     ตรวจพื้นที่ (§7) — ไม่พอ = บล็อก
2. snapshot               virsh snapshot-create-as master pre-sysprep-<ver>
3. sysprep                ส่งคำสั่งผ่าน QEMU guest agent:
                          sysprep /generalize /oobe /shutdown /unattend:C:\swim\sysprep-unattend.xml
4. wait-shutoff           timeout 30 นาที → fail ถ้าไม่ปิด
5. capture                preflight พื้นที่: WIM (ขนาดรุ่นก่อน +10% หรือ 20 GB) + overlay 20 GB + เหลือ ≥ 5 GB
                          qemu-img create -b master.qcow2 capture-<id>.qcow2 (overlay — master ไม่ถูกเขียน)
                          reseal: ถ้า State.ini ไม่ใช่ IMAGE_STATE_GENERALIZE_RESEAL_TO_OOBE → ใส่สคริปต์ครั้งเดียว
                            (HKLM\SYSTEM\Setup CmdLine/SetupType=2 → sysprep /generalize /oobe /shutdown /quiet)
                            แล้วบูต overlay เป็น VM ชั่วคราว (ไม่มี network, NVRAM/TPM ของตัวเอง) รอจนปิดเอง ≤ 45 นาที
                            ไม่สำเร็จ → fail พร้อม setuperr.log (Golden Master ที่ไม่มี QEMU guest agent ก็ทำได้)
                          qemu-nbd -c /dev/nbdX capture-<id>.qcow2
                          คลายไฟล์ CompactOS/WOF (reparse 0x80000017) เป็นไฟล์ปกติ: ntfs-unwof (libntfs-3g)
                            + ตัวถอด XPRESS ใน pkg/wof, ตรวจ PE checksum ทุกไฟล์ก่อนเขียน
                            เหตุผล: WinPE ไม่ attach wof.sys กับ volume ใหม่ → รัน DISM/bcdboot จาก image ไม่ได้
                          ตรวจ offline (image_tests): S-01 generalized · S-02 ไฟล์บูต/DISM/bcdboot เป็นไฟล์ปกติ
                            · S-03 ไม่มีตัวตน SWIM agent · S-04 ไม่มีรหัสผ่าน plain-text ใน unattend
                            · S-05 ไม่มี WinSxS\pending.xml · S-06 pagefile/hiberfil (ไม่เข้า WIM) — S-01/S-02 ไม่ผ่าน = fail
                          wimcapture /dev/nbdXpN image.wim "<channel> <ver>" --compress=LZX --check
                          expanded_bytes = Total Bytes − Hard Link Bytes (WinSxS hard links ไม่นับซ้ำ)
                          images.wof_files = 0 (image เก่าที่เป็น NULL ถูก server ปฏิเสธตอนสั่งติดตั้ง)
                          ลบ overlay
                          (ถ้ามี base: --delta-from=<base>.wim — ดู §6)
6. chunk+hash             แบ่ง chunk 64 MiB, SHA256 ต่อ chunk + ทั้งไฟล์ → chunks.json
7. sign                   เซ็น chunks.json ด้วยกุญแจ release (08-security)
8. revert                 virsh snapshot-revert master pre-sysprep-<ver>
9. → testing
```

ทุกขั้นเขียน `image_build_events` และแสดง live log บนหน้า Builder

## 5. Automated Image Test (บน Server)

รันบน **Image Test Runner VM × 2** (ดิสก์ 128 GB และ 512 GB) โดยใช้ **ขั้นตอน deploy จริงทั้งหมด** ไม่ใช่แค่บูต image

```
reset VM → snapshot "old-windows + swim-agent(current) + BitLocker on (vTPM)"
→ สร้าง job ปกติ ผ่าน API (ring = test) → agent ทำ staging → WinPE → apply → OOBE → health
→ เก็บผล → reset VM
```

| Test | ผ่านเมื่อ |
|---|---|
| T-01 Deploy end-to-end | job = `completed` ภายใน 60 นาที |
| T-02 Disk profiles | ทั้ง 2 ขนาดดิสก์ได้ layout ตามกฎ |
| T-03 Boot supervisor | Windows ใหม่ commit สำเร็จ, `ImageState = IMAGE_STATE_COMPLETE` |
| T-04 Agent | agent ใหม่ re-enroll ได้ host_id เดิม, version = published agent |
| T-05 Domain join | (ถ้า channel ตั้ง domain) อยู่ใน domain + secure channel OK (`Test-ComputerSecureChannel`) |
| T-06 Duplicate ID | ไม่มี `SusClientId` เดิม, ไม่มีค่าจาก denylist ID ของ EDR/SCCM ที่ตั้งไว้ |
| T-07 BitLocker | เข้ารหัสตามนโยบาย + key escrow สำเร็จ |
| T-08 Rollback | สั่ง rollback → ได้ N-1 กลับมา (รันเฉพาะเมื่อมี N-1) |
| T-09 Size | ขนาดหลังแตก < `min_disk_gb` ของ channel − 20 GB headroom |

ผลทุก test เก็บใน `image_tests` + แนบ log เต็ม; ผ่านทั้งหมด → `pending`

> VM **ทดสอบ driver ของเครื่องจริงไม่ได้** → ต้องผ่าน wave `canary` บนเครื่องจริง ≥1 เครื่องต่อรุ่นก่อน `broad` (ดู [07](07-fleet-safety.md))

## 6. Delta image และ Rebase

- รุ่นรายเดือนสร้างเป็น delta จาก **base** ล่าสุด (`--delta-from`) — เครื่องลูกโหลดเฉพาะ delta ถ้ามี base อยู่บน S: แล้ว
- apply ด้วย `wimlib-imagex apply image.wim --ref=base.wim` ใน WinPE (ฝัง wimlib ใน `boot.wim`)
- **Rebase ทุก 3–6 เดือน** หรือเมื่อ delta > 40% ของ base → สร้าง full image ใหม่เป็น base
- ห้ามลบ base ที่ยังมี delta ที่ published/superseded อ้างอยู่

## 7. Capacity Planning (Server รู้ก่อนเสมอ)

### ฝั่ง Server — ตรวจก่อนกด Build

```
ประมาณขนาด image ใหม่   = used_bytes(Golden Master C:) × compression_ratio (เริ่ม 0.5, ปรับจากประวัติ)
พื้นที่ที่ต้องใช้           = image ใหม่ + snapshot overhead (ประวัติ, เริ่ม 15 GB) + test artifacts (5 GB)
คงเหลือหลัง build        = free − พื้นที่ที่ต้องใช้
```

| เงื่อนไข | ผล |
|---|---|
| ใช้ไป ≥ 80% | เตือนบน Dashboard + เสนอรายการ archived ที่ลบได้ |
| คาดว่าหลัง build ≥ 90% | **บล็อก Build** |
| พยากรณ์ | แสดง "เต็มในอีก ~N รุ่น" จากค่าเฉลี่ย 3 รุ่นล่าสุด |

นโยบาย retention (ตั้งได้ต่อ channel): `published` + `superseded (N-1)` + `pending` + ที่ปักหมุด → ที่เหลือ `archived` อัตโนมัติ; `archived` ลบได้หลัง `archive_days` (ค่าเริ่มต้น 90)

### ฝั่งเครื่องลูก — ตรวจก่อน Approve/Publish

```
S: ต้องจุ ≥ image ใหม่ (หรือ delta) + N-1 + boot.wim + drivers + 2 GB
```

ก่อน Approve แสดง:
> v1.6 (9.1 GB) — 1,240 เครื่องพร้อม · **12 เครื่อง S: เล็กเกินไป** (จะ rollback ออฟไลน์ไม่ได้ / ต้องขยาย S:) · [ดูรายชื่อ]

Agent ขยาย S: เองได้ถ้า C: มีที่ว่างพอ (ดู [03 §6](03-agent.md)) — ไม่เช่นนั้นเครื่องนั้นต้องโหลด N-1 ผ่านเน็ตตอน rollback

## 8. License & Activation

| ประเภท | หลัง deploy |
|---|---|
| OEM ฝัง BIOS (MSDM) | activate เอง **ถ้า edition ตรง** |
| KMS / AD-Based Activation | activate เมื่ออยู่ในเครือข่ายองค์กร |
| Windows Enterprise E3/E5 (Subscription) | activate เมื่อผู้ใช้ล็อกอินบัญชี Entra |
| OEM Home แต่ image เป็น Pro/Enterprise | ❌ — preflight ต้องเตือน (Agent อ่าน `SoftwareLicensingService.OA3xOriginalProductKeyDescription`) |

- โปรแกรมที่ต้อง activate ใหม่ → ผู้ใช้/ช่างจัดการเอง (D-09)
- Agent รายงานสถานะ activation หลังติดตั้ง (`LicenseStatus`) เป็น **WARN** ถ้ายังไม่ activate ใน 24 ชม.

## 9. WinPE (boot.wim) Pipeline

- Build บน **Windows Utility VM** ด้วย ADK + WinPE add-on **เวอร์ชันล่าสุด** (รองรับ boot chain ที่เซ็นด้วย Windows UEFI CA 2023)
- Optional components: `WinPE-WMI`, `WinPE-StorageWMI`, `WinPE-SecureStartup`, `WinPE-EnhancedStorage`, `WinPE-PowerShell` (+ `NetFx`) สำหรับ Phase 1
- ฉีด driver: **storage (Intel VMD/RST, AMD RAID)** ของทุกรุ่นในองค์กร + NIC พื้นฐาน
- ฝัง: `swim-pe.exe`, `wimlib-imagex.exe`, root CA ภายใน, public key สำหรับตรวจ manifest
- `boot.wim` มีเวอร์ชันของตัวเอง (`pe 1.x`) ผ่าน pipeline เดียวกับ image: build → test → publish
