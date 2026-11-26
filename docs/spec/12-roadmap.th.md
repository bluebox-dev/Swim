# 12 · Roadmap & Test Matrix

[English](12-roadmap.md) · **ไทย**

## เฟสพัฒนา

| เฟส | ขอบเขต | เกณฑ์ผ่าน (Exit criteria) |
|---|---|---|
| **0 · Decisions & Lab** | เลือกเครื่องทดสอบ 3 รุ่นที่มากที่สุด, ตั้ง Ubuntu server + KVM + Golden Master 1 ตัว + Utility VM, ยืนยัน D-01…D-19, ขอ policy AD (บัญชี djoin + allowlist re-use), ตกลงเรื่อง BitLocker บน S: | เอกสารตัดสินใจลงนาม, lab พร้อม |
| **1 · Core PoC (สคริปต์)** | WinPE จาก ADK ล่าสุด (+VMD driver), capture WIM ด้วย wimlib, สคริปต์ staging/BCD/`worker.ps1` ตาม state machine ([05](05-deployment-flow.th.md)), boot supervisor, fallback N-1, ODJ v1, Engine A เทียบ | **20 รอบติดกันไม่พลาดทั้ง 3 รุ่น** + ผ่าน Chaos test C-01…C-08 ทุกข้อ |
| **2 · Agent + swim-pe (Go)** | `pkg/manifest`, `pkg/state`, `pkg/chunks`; `swim-svc` + `swim-agent` + `swim` CLI + `swim-pe`; preflight, staging, netcond, outbox, watchdog, local deploy ด้วยไฟล์ manifest (ยังไม่มี server — ใช้ manifest ที่เซ็นด้วย key ทดสอบ); **self-update + auto-rollback ทำงานกับ release server จำลอง** | ทำซ้ำผลเฟส 1 ด้วย binary + ผ่าน U-01…U-08 |
| **3 · Control Plane** | backend + DB + Admin UI ขั้นต่ำ: hosts, enroll, images (upload WIM เองได้), approve 2 คน, jobs, rollout + wave, concurrency, kill switch, breaker, agent releases, audit, RBAC + SSO, PKI (step-ca), manifest signer, log bundle | สั่งงานจากเว็บครบวงจร + pentest ภายในผ่าน + pilot 20–50 เครื่องจริง |
| **4 · Scale & Quality** | driver matcher + driver pack lifecycle, pre-stage, delta WIM + rebase, site cache, ETA/risk, capacity planning ทั้งสองฝั่ง, BitLocker escrow/cleanup, ODJ v2 (TPM seal), SWIM USB, monitoring + alert, backup/DR + ซ้อม restore | rollout ทั้ง site แรกสำเร็จ ≥ 98%, DR drill ผ่าน |
| **5 · Web Builder** | libvirt control, Guacamole ในหน้าเว็บ, ปุ่ม Build (snapshot → sysprep → capture → sign → revert), automated image test (T-01…T-09) บน Test Runner VM | build → test → pending อัตโนมัติโดยไม่ต้อง SSH |

> Agent self-update อยู่ในเฟส 2 (ไม่ใช่เฟสท้าย) เพราะต้องมี **ก่อน** agent ตัวแรกออกไปถึงเครื่องผู้ใช้ — ไม่อย่างนั้นจะไม่มีทางแพตช์ agent รุ่นแรก

## Chaos Tests (เฟส 1 บังคับ, เฟส 2+ อัตโนมัติบน VM)

| ID | ทำ | ผลที่ต้องได้ |
|---|---|---|
| C-01 | ถอดปลั๊ก/ปิด VM ระหว่าง download | resume ต่อจาก chunk เดิม |
| C-02 | ถอดปลั๊กระหว่าง shrink C: / สร้าง S: | Windows เดิมบูตได้, agent ทำต่อหรือรายงาน |
| C-03 | ถอดปลั๊กระหว่าง `handoff` (หลังตั้ง BCD) | บูตเข้า PE หรือกลับ Windows เดิม — ไม่ค้าง |
| C-04 | ลบ storage driver ออกจาก boot.wim | `aborted` → กลับ Windows เดิมเอง |
| C-05 | ถอดปลั๊กระหว่าง format / apply | บูตกลับ PE → ทำต่อจนสำเร็จ |
| C-06 | image เสีย (แก้ 1 byte) | ตรวจพบใน `pe_verify` → `aborted` |
| C-07 | image ที่ทำให้ BSOD ตอนบูตครั้งแรก | supervisor → fallback N-1 → `rolled_back` |
| C-08 | kill switch ระหว่าง `waiting` / `pe_verify` | `cancelled` / `aborted` |
| C-09 | ตัดเน็ตตลอด WinPE | สำเร็จ และส่ง summary ตามหลัง |
| C-10 | Server ล่มระหว่าง rollout | job หลัง PONR จบเอง, outbox ส่งครบเมื่อกลับมา |

## Agent Update Tests (เฟส 2+, ทุก release ใน CI)

| ID | ทำ | ผลที่ต้องได้ |
|---|---|---|
| U-01 | อัปเดต N-1 → N | commit ภายใน 10 นาที |
| U-02 | อัปเดต N-2 → N (ข้ามเวอร์ชัน) | สำเร็จ |
| U-03 | เวอร์ชันใหม่ crash ตอนสตาร์ต | กลับเวอร์ชันเดิม + `BlockedVersions` + รายงาน |
| U-04 | เวอร์ชันใหม่ self-test ล้ม (DB migration) | กลับเวอร์ชันเดิม, outbox ใช้ได้ |
| U-05 | release.json ลายเซ็นผิด / exe ไม่มี Authenticode | ปฏิเสธ ไม่สลับ |
| U-06 | downgrade โดยไม่มี directive | ปฏิเสธ |
| U-07 | อัปเดต svc แล้ว svc ใหม่สตาร์ตไม่ได้ | `SWIMRescue` กู้กลับภายใน 6 ชม. (หรือรีบูตครั้งถัดไป) |
| U-08 | มี job อยู่ในสถานะ `handoff` | ไม่อัปเดต (deferred) |

## Hardware Test Matrix (ขั้นต่ำ)

| มิติ | ค่าที่ต้องครอบคลุม |
|---|---|
| รุ่น | 3 รุ่นที่มากที่สุด + รุ่นใหม่ล่าสุดที่จัดซื้อ |
| Storage | NVMe ปกติ, Intel VMD เปิด, ดิสก์ 2 ลูก |
| Firmware | Secure Boot เปิด (มี mitigation CVE-2023-24932 ครบ) / ปิด |
| BitLocker | ปิด / เปิด TPM-only / เปิด TPM+PIN |
| พลังงาน | AC / แบตอย่างเดียว (ต้องถูกบล็อก) |
| เครือข่าย | LAN / Wi-Fi / VPN / Hotspot (metered) |
| ขนาดดิสก์ | 128 / 256 / 512 GB / 1 TB |
| ภาษา Windows | th-TH และ en-US (กันการ parse ข้อความ) |

## Definition of Done (ทุกฟีเจอร์)

- ตรงตามสเปค — ถ้าไม่ตรง แก้สเปคใน PR เดียวกัน
- มี unit test ของ state transition ที่เกี่ยวข้อง
- error code ใหม่ถูกเพิ่มใน `pkg/state/errors.go` + docs
- event/metric ที่ต้องใช้สำหรับ ETA/risk/breaker ถูกส่งครบ
- audit log ครบสำหรับ action ที่เปลี่ยนสถานะ
