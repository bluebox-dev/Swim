# ผลทดสอบ Agent lifecycle บน Windows จริง — 24 พ.ย. 2026

**สภาพแวดล้อม:** server `swim.example.internal` (Ubuntu 24.04, KVM ซ้อน), VM `swim-test01` = Windows 11 Pro build 26300,
UEFI Secure Boot + TPM 2.0 (swtpm), เครือข่าย NAT, agent 0.2.0 → 0.2.1

การทำงาน "แทน WinPE" ทำจาก Linux แบบ offline (qemu-nbd + ntfs-3g + hivexregedit) — ขั้นตอนเดียวกับที่ swim-pe จะทำด้วย `reg load` ([05 §4.3](../spec/05-deployment-flow.md) ข้อ 5)
และไม่มีการพิมพ์รหัสผ่านเข้า Windows เลย

| # | ทดสอบ | วิธี | ผล |
|---|---|---|---|
| 1 | ติดตั้งด้วยคำสั่ง PowerShell จากหน้า "เพิ่มเครื่อง" | Windows ติดตั้งแบบ unattended แล้วรันคำสั่งตอน logon แรก | ✅ enroll เอง, heartbeat ทุก 60 วินาที, host token เก็บแบบ DPAPI (ciphertext 440 ตัวอักษร) |
| 2 | ติดตั้งด้วย **MSI** ที่ server สร้าง (site LAB) | ลบ agent ออกทั้งหมด แล้วให้ `msiexec /i … /qn` รันเป็น SYSTEM ตอนบูต | ✅ msiexec exit 0, service RUNNING, enroll จาก registry, **host_id เดิม** (จับคู่ SMBIOS UUID), ป้าย `manual_install`, token ใช้ 1/5, **Token ถูกลบจาก registry หลังใช้** |
| 3 | **Reimage** — ฉีด agent แบบ swim-pe | ทำให้เหมือน Windows ใหม่ (ไม่มี agent/registry/state) → copy exe + สร้าง service ใน SYSTEM hive + `bootstrap.json` (reimage ticket) | ✅ 45 วินาทีหลังบูต enroll ด้วย ticket, **host_id เดิม**, hosts ยังเป็น 1 เครื่อง, ป้ายกลับเป็น `swim`, `bootstrap.json` ถูกลบ, audit `host.reenroll` โดย `reimage_ticket` |
| 4 | **Clone guard** | เปลี่ยน SMBIOS UUID ของ VM (เหมือนย้ายดิสก์/clone image ที่มี agent) | ✅ agent ทิ้ง state ทันทีที่สตาร์ต (`state.json.discarded-*`) และ **ไม่ส่ง heartbeat แทนเครื่องเดิม** |
| 4b | รีบูตปกติไม่ถูกมองว่าเป็น clone | คืน UUID เดิม + state → บูต 2 รอบ | ✅ heartbeat ต่อบน host เดิมใน 20–40 วินาที |
| 5 | Capture ตัด `\ProgramData\SWIM` | Builder capture VM ที่มี agent enroll แล้ว (WIM 8.47 GB, 7 นาที) | ✅ `/ProgramData/SWIM` ใน WIM = 0 รายการ, เหลือแค่ exe ใน Program Files (ไม่มีตัวตน), pagefile ถูกตัด |
| — | ทดสอบบน Linux agent (โค้ดเดียวกัน) | ticket ใช้ซ้ำ / ใช้กับเครื่องอื่น / pin ผิด | ✅ ปฏิเสธทั้งหมด (401 / pin mismatch) |

## ปัญหาที่พบระหว่างทดสอบและแก้แล้ว

1. **Clone guard ถูกข้ามตอนบูต** (พบในข้อ 2): PowerShell/WMI ยังไม่พร้อมช่วงบูต (> 60 วินาที) agent จึง log
   "clone guard skipped" → แก้ใน 0.2.1: อ่าน SMBIOS UUID จาก firmware โดยตรง (`GetSystemFirmwareTable('RSMB')`)
   และ **ไม่ข้ามการตรวจเด็ดขาด** — อ่านไม่ได้ให้รอแล้วลองใหม่ · ยืนยันแล้วในข้อ 4/4b
2. wixl ไม่รับ path แบบ absolute → copy exe เข้าโฟลเดอร์ build ก่อน (token ของ MSI ที่ build ล้มถูกยกเลิกอัตโนมัติ — ยืนยันแล้ว)
3. unattend ของ test client ไม่มี locale ใน oobeSystem → OOBE หยุดที่หน้าเลือกประเทศ (แก้ใน `create-test-client.sh` แล้ว)

## ข้อสังเกต (ยังไม่แก้)

- นาฬิกาใน VM ช้ากว่าจริง 7 ชม. (RTC ของ VM เป็น localtime) — log ของ agent จึงแสดงเวลาเพี้ยน ไม่กระทบการทำงาน
- MSI ยังไม่ได้เซ็น Authenticode → SmartScreen จะเตือนเมื่อดับเบิลคลิกไฟล์ที่ดาวน์โหลดมา (ทดสอบนี้รันผ่าน msiexec จึงไม่เห็น)
- การ reimage จริงทั้งกระบวนการ (staging → WinPE → apply) ยังไม่ได้สร้าง — ข้อ 3 ทดสอบเฉพาะ "ขั้นใส่ agent กลับ" ซึ่งเป็นส่วนที่ทำให้ไม่มีรอยต่อ

## ช่องทางคำสั่งจาก server (agent 0.3.0) — Windows จริง

| ทดสอบ | ผล |
|---|---|
| `ping` | ✅ 4 วินาที — นาฬิกาเครื่องเพี้ยน 25,199 วินาที (7 ชม.) แต่คำสั่งผ่าน เพราะ agent ตรวจเวลาด้วยนาฬิกา server |
| `collect_logs` | ✅ ข้อมูลระบบ, ดิสก์, IP, สถานะ service, error ใน System log 24 ชม. + agent.log |
| `refresh_inventory` | ✅ server อัปเดตข้อมูลเครื่อง |
| สคริปต์: รันก่อนอนุมัติ / ผู้เขียนอนุมัติเอง | ✅ ปฏิเสธทั้งคู่ |
| สคริปต์ที่อนุมัติแล้ว | ✅ รันเป็น `nt authority\system`, output ภาษาไทยถูกต้อง, exit code ผิดพลาด (7) รายงานตรง |
| Kill switch ทำงาน | ✅ `run_script` / `reboot` ถูกปฏิเสธ, `ping` ยังใช้ได้ |
| คำสั่งนอก allowlist / uninstall ไม่ใส่เหตุผล | ✅ ปฏิเสธ |
| `restart_agent` | ✅ หยุด → SCM สตาร์ตใหม่ใน 10 วินาที |
| `reboot` 60 วินาที | ✅ boot time เปลี่ยน 06:50:49 → 06:54:14, agent กลับมาเอง |
| `uninstall_agent` | ✅ server ยกเลิก token ทันที + ปฏิเสธคำสั่งใหม่; บนเครื่อง: Program Files\SWIM, ProgramData\SWIM, service, HKLM\SOFTWARE\SWIM, MSI product ถูกลบหมด, สคริปต์ลบตัวเอง |
| Unit test ความปลอดภัย (Linux, ทุก build) | ✅ ลายเซ็นปลอม, kid ไม่รู้จัก, host อื่น, UUID อื่น, หมดอายุ, นอก allowlist, hash สคริปต์ผิด, id ไม่ตรง, replay — ปฏิเสธทั้งหมด |

## Org trust policy (`Set-SwimTrust.ps1`) — Windows จริง (รันผ่าน SWIM run_script)

| ทดสอบ | ก่อน | หลังตั้งค่า | หลังถอน |
|---|---|---|---|
| Zone ของ `https://swim.example.internal` | Internet | **Intranet** | Internet |
| TLS ของ server (Windows chain) | NOT trusted | **trusted** | NOT trusted |
| Edge `SmartScreenAllowListDomains` | — | `swim.example.internal` | — |
| Chrome `SafeBrowsingAllowlistDomains` | — | `swim.example.internal` | — |

- รันซ้ำไม่สร้างค่าซ้ำ · pin ผิด → หยุดทันที ไม่เปลี่ยนอะไร
- **พบระหว่างทดสอบ:** เขียน `ZoneMapKey` (ค่าของ GPO "Site to Zone Assignment List") ลง registry ตรง ๆ ไม่มีผล — Windows อ่านจาก
  `Policies\...\ZoneMap\{Ranges|Domains}` ที่ GPO engine สร้าง; สคริปต์จึงเขียนที่นั่นโดยตรง (ใช้ได้ทั้ง IP และชื่อโดเมน, มีผลทุก user)
- เครื่องที่ถูกถอดออกแล้วติดตั้ง agent ใหม่ → กลับเข้าระบบ (host_id เดิม, ไม่ติดสถานะถอดออก)
- ยังไม่ได้ทดสอบ: หน้าต่างเตือนของ Edge/SmartScreen หายจริงบนหน้าจอ (ต้องคลิกดาวน์โหลดใน GUI) — ตั้งค่าที่ Windows ใช้ตัดสินถูกต้องครบแล้ว
