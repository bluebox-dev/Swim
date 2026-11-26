# SWIM — Staged Windows Image Manager · Specification

[English](README.md) · **ไทย**

> สถานะ: **Draft v0.1** · อัปเดต: 2026-11-23
> เอกสารชุดนี้คือ "สัญญา" ที่ Server, Agent และ WinPE Worker ต้องยึดร่วมกัน
> ถ้าโค้ดขัดกับสเปค ให้แก้สเปคก่อนแล้วค่อยแก้โค้ด

## สารบัญ

| # | เอกสาร | เนื้อหา |
|---|---|---|
| 01 | [architecture.md](01-architecture.th.md) | ภาพรวมระบบ, Server บน Ubuntu, VM, Tech stack, โครง Monorepo |
| 02 | [image-pipeline.md](02-image-pipeline.th.md) | กติกา Golden Image, Build → Test → Approve → Publish, Channel, วงจรชีวิต/ความจุ, License |
| 03 | [agent.md](03-agent.th.md) | สถาปัตยกรรม Agent, การติดตั้งครั้งแรก, ตัวตนเครื่อง, คำสั่ง, Preflight, Staging, เงื่อนไขเครือข่าย |
| 04 | [agent-updates.md](04-agent-updates.th.md) | **การอัปเดต/แพตช์ Agent อย่างต่อเนื่อง** (Self-update, Ring, Auto-rollback) |
| 05 | [deployment-flow.md](05-deployment-flow.th.md) | State machine, BCD, WinPE, จุดย้อนกลับไม่ได้, Boot supervisor, Rollback, หลังติดตั้ง, ETA |
| 06 | [manifest.md](06-manifest.th.md) | รูปแบบ Job Manifest, การเซ็น, การเข้ารหัสข้อมูลลับ, วันหมดอายุ |
| 07 | [fleet-safety.md](07-fleet-safety.th.md) | Wave, จำกัดการทำพร้อมกัน, Kill switch, Circuit breaker, Fleet rollback |
| 08 | [security.md](08-security.th.md) | Threat model, PKI/ใบรับรอง, กุญแจเซ็น, RBAC, Audit, AD/BitLocker/LAPS |
| 09 | [operations.md](09-operations.th.md) | Log/Telemetry, Monitoring, Backup/DR, USB สำรอง, Re-enroll, Windows lifecycle |
| 10 | [data-model.md](10-data-model.th.md) | PostgreSQL schema |
| 11 | [api.md](11-api.th.md) | REST API (Agent API + Admin API) |
| 12 | [roadmap.md](12-roadmap.th.md) | เฟสพัฒนา, เกณฑ์ผ่าน, Test matrix |

## บันทึกการตัดสินใจ (Decision Log)

| ID | การตัดสินใจ | เหตุผลย่อ |
|---|---|---|
| D-01 | Server = **Ubuntu 24.04 LTS bare metal** + KVM/libvirt | ถูก, เหมาะกับ Docker/nginx/Guacamole |
| D-02 | มี **Windows Utility VM** บน Server สำหรับ ADK / DISM / djoin | งานเหล่านี้ทำบน Linux ไม่ได้ |
| D-03 | สิ่งที่ส่งไปเครื่องลูกคือ **WIM** (ไม่ใช่ ISO) + `boot.wim` + driver pack | WIM เป็นไฟล์-based ใส่ได้ทุกขนาดดิสก์ |
| D-04 | Deploy engine: **A = `setup.exe /auto clean`** (PoC/สำรอง), **B = WinPE + DISM** (Production) | A พิสูจน์ไว, B ควบคุมได้เต็มที่ |
| D-05 | **ข้อมูลผู้ใช้บน C: หายหมด** — ยอมรับ | นโยบายองค์กร; ต้องแจ้งผู้ใช้ + เก็บ audit |
| D-06 | **Rollback = ลงใหม่ด้วย image N-1** จาก cache บน S: (ไม่ใช่คืนสภาพเครื่องเดิม) | สอดคล้อง D-05, ไม่ใช้เน็ต |
| D-07 | **S: (Staging) เป็นพาร์ทิชันถาวร** ขนาดพอสำหรับ image ปัจจุบัน + N-1 | รองรับ D-06 และไม่ต้อง shrink C: ซ้ำ |
| D-08 | **ทดสอบ image อัตโนมัติบน VM ของ Server** ก่อน Approve + Canary บนเครื่องจริงรุ่นละ ≥1 | VM ทดสอบ driver จริงไม่ได้ |
| D-09 | License: Windows activate ตาม OEM/KMS/Subscription ของเครื่อง; แอปที่ต้อง activate ใหม่ ผู้ใช้จัดการเอง | Sysprep ล้าง activation |
| D-10 | วงจรชีวิต image **วัดตามความจุ** — Server ต้องพยากรณ์ก่อน Build ทั้งฝั่ง Server และฝั่ง S: ของเครื่องลูก | กันพื้นที่เต็มกลางทาง |
| D-11 | Log: **สรุปส่งทุกครั้ง**, Log เต็มส่งเฉพาะ FATAL/ERROR, เก็บในเครื่อง 5 งานล่าสุด | สถิติสำเร็จ/ETA ต้องมีข้อมูลงานที่สำเร็จด้วย |
| D-12 | ช่องทางสำรอง: **ช่างลง Windows เองด้วย USB** แล้ว Re-enroll Agent; (ทางเลือก) SWIM USB | ง่ายและช่างทำเป็นอยู่แล้ว |
| D-13 | ภาษา: **Go** ทั้ง backend / agent / pe-worker; DB **PostgreSQL**; UI **React** | แชร์ type ของ manifest/state ได้ |
| D-14 | Agent รับเฉพาะ **คำสั่งที่อยู่ใน allowlist** — ห้ามมี `run_command` | Agent รันเป็น SYSTEM ทุกเครื่อง |
| D-15 | Domain join ด้วย **Offline Domain Join (ODJ) + `/reuse`**, local admin ด้วย **Windows LAPS** | ไม่มีรหัสผ่านค้างบนดิสก์ |
| D-16 | Manifest **เซ็นด้วย Ed25519** + **มีวันหมดอายุ** + ข้อมูลลับ **เข้ารหัสด้วยกุญแจของเครื่อง** | Kill switch ทำงานแม้เครื่องออฟไลน์ |
| D-17 | **Agent ต้องอัปเดต/แพตช์ตัวเองได้ต่อเนื่อง** แบบ Ring + Health gate + Auto-rollback | Agent พังทั้งองค์กร = เสียการควบคุม |
| D-18 | Windows ใหม่ทุกเครื่องได้ **Agent เวอร์ชันที่ Published ล่าสุด** (WinPE ฉีดให้) ไม่ใช่เวอร์ชันที่ติดมากับ image | กันเครื่องใหม่ได้ Agent เก่า |
| D-19 | การ patch Windows (Windows Update) **ใช้ช่องทางเดิมขององค์กร** (WSUS / WUfB / Intune) — ไม่อยู่ใน scope ของ SWIM v1 | ลดขอบเขต; เปิดเป็นฟีเจอร์ภายหลังได้ |
| D-20 | **อนุญาต `run_script` เฉพาะจากคลังสคริปต์ที่อนุมัติ 2 คน + SHA-256** (แก้ D-14 เดิมที่ห้ามทั้งหมด) — ยังห้าม `run_command`/shell อิสระ; คำสั่งทุกตัวเซ็น Ed25519 ผูกเครื่อง | ผู้ใช้ต้องการให้ server จัดการเครื่องได้จริง แต่ต้องมีชั้นความปลอดภัย ([03 §4](03-agent.th.md)) |

## คำศัพท์

| คำ | ความหมาย |
|---|---|
| **Golden Master** | VM Windows ต้นแบบบน Server ที่ใช้ลงโปรแกรมก่อน Sysprep |
| **Image** | ไฟล์ `.wim` ที่ capture จาก Golden Master (1 release = 1 image ต่อ channel) |
| **Channel** | สายของ image เช่น `win11-x64-stable` — published ได้ทีละ 1 ต่อ channel |
| **Ring / Wave** | กลุ่มเครื่องที่ปล่อยทีละระลอก: `canary` → `pilot` → `broad` |
| **S: / Staging** | พาร์ทิชันถาวรบนเครื่องลูก เก็บ image, WinPE, driver, log, state |
| **PONR** | Point of No Return — ขั้น format C: หลังจากนี้ย้อนกลับเป็น Windows เดิมไม่ได้ |
| **N-1** | image รุ่นก่อนหน้าที่ยัง cache อยู่บน S: ใช้ rollback |
| **Manifest** | JSON ที่ Server เซ็นแล้ว บอก Agent/WinPE ว่าต้องทำอะไร |
| **Boot supervisor** | บทบาทของ WinPE หลัง apply ที่คอยตรวจว่า Windows ใหม่บูตสำเร็จหรือไม่ |
| **Outbox** | คิวเหตุการณ์ในเครื่อง (SQLite) ที่รอส่งขึ้น Server |

## หลักการออกแบบ 7 ข้อ

1. **ก่อน PONR ทุกอย่างย้อนกลับได้เอง** — หลัง PONR ทุกอย่างทำซ้ำได้เอง (idempotent) จนสำเร็จหรือตกไป N-1
2. **WinPE ไม่ต้องพึ่งเน็ต** — ทุกอย่างที่ต้องใช้อยู่บน S: แล้ว
3. **ไม่มีอะไรที่ไม่ได้เซ็น** — image, manifest, agent binary, WinPE ต้องตรวจลายเซ็น/hash ทุกครั้ง
4. **ไม่มีความลับบนดิสก์แบบ plain text** (ข้อยกเว้นชั่วคราวเดียว: ODJ blob ใน v1 — ดู [08 §5](08-security.th.md))
5. **ระบบหยุดตัวเองได้ก่อนคนรู้** — circuit breaker ทั้งการ deploy image และการอัปเดต Agent
6. **ทุกขั้นวัดเวลาและผลได้** — เพื่อทำ ETA และอัตราสำเร็จจากข้อมูลจริง
7. **Agent ต้องไม่ค้าง** — งานยาวแยกจากการสื่อสาร, ทุก I/O มี timeout
