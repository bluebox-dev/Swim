# ให้เครื่อง Windows ในองค์กรเชื่อถือ SWIM (Org trust policy)

**เป้าหมาย:** ผู้ใช้เปิดเว็บ SWIM และดาวน์โหลด/ดับเบิลคลิก `swim-agent-*.msi` ได้โดย **ไม่มีคำเตือน** จาก
เบราว์เซอร์ (ใบรับรองไม่น่าเชื่อถือ), Edge/Chrome (ไฟล์ไม่ค่อยมีคนโหลด) และ Windows SmartScreen ("Windows protected your PC")

ตั้ง 3 อย่างผ่าน GPO หรือ Intune — ทั้งหมดจำกัดเฉพาะ server SWIM ไม่ได้ปิด SmartScreen ทั้งเครื่อง

| # | ตั้งค่า | แก้คำเตือน |
|---|---|---|
| 1 | ใบรับรองของ SWIM → **Trusted Root Certification Authorities** | "Your connection isn't private" บนเว็บ SWIM |
| 2 | URL ของ SWIM → **Local intranet zone** | Windows SmartScreen ตอนเปิดไฟล์ (SmartScreen ตรวจเฉพาะไฟล์จาก Internet zone) |
| 3 | Edge `SmartScreenAllowListDomains` + Chrome `SafeBrowsingAllowlistDomains` | คำเตือนตอนดาวน์โหลดในเบราว์เซอร์ |

ค่าที่ใช้ใน lab ปัจจุบัน (ดูค่าจริงได้ที่หน้า **เพิ่มเครื่อง** บนเว็บ):

| ค่า | lab |
|---|---|
| URL | `https://swim.example.internal` (production ควรเป็นชื่อ เช่น `https://swim.corp.local`) |
| Host สำหรับ allowlist | `swim.example.internal` |
| ไฟล์ใบรับรอง | `https://swim.example.internal/dl/agent/swim-server.cer` |
| SHA-256 ของใบรับรอง | แสดงบนหน้าเพิ่มเครื่อง — **ตรวจให้ตรงก่อน import** |

---

## A. Group Policy (เครื่องที่ join AD)

สร้าง GPO ใหม่ เช่น `SWIM - Trust server` แล้วลิงก์กับ OU ของเครื่องลูก — ทุกค่าอยู่ใต้ **Computer Configuration**

### 1. ใบรับรอง
`Policies → Windows Settings → Security Settings → Public Key Policies → Trusted Root Certification Authorities`
→ คลิกขวา **Import…** → เลือก `swim-server.cer`

> ก่อน import: `certutil -hashfile swim-server.cer SHA256` ต้องตรงกับ SHA-256 บนหน้าเพิ่มเครื่อง
> เมื่อเปลี่ยนเป็นใบรับรองจาก CA ภายใน (step-ca / AD CS, [08](../spec/08-security.md)) ให้ import root ของ CA นั้นแทน และไม่ต้องทำข้อนี้ต่อ

### 2. Local intranet zone
`Policies → Administrative Templates → Windows Components → Internet Explorer → Internet Control Panel → Security Page`
→ **Site to Zone Assignment List** = Enabled → Show…

| Value name | Value |
|---|---|
| `https://swim.example.internal` | `1` |

(1 = Local intranet. GPO engine แปลงรายการนี้เป็น `HKLM\SOFTWARE\Policies\Microsoft\Windows\CurrentVersion\Internet Settings\ZoneMap\{Domains|Ranges}`
ซึ่ง Windows ใช้ทั้งเครื่องแม้ IE จะเลิกใช้แล้ว — Attachment Manager/SmartScreen อ่านจากที่นี่.
**ข้อควรรู้ (ทดสอบแล้ว):** การเขียนค่า `ZoneMapKey` ลง registry ตรง ๆ โดยไม่ผ่าน GPO จะ **ไม่มีผล** — สคริปต์ `Set-SwimTrust.ps1` จึงเขียนที่ `ZoneMap\Ranges` / `ZoneMap\Domains` โดยตรง)

### 3. Edge และ Chrome
ต้องมี ADMX ของ [Microsoft Edge](https://www.microsoft.com/edge/business/download) และ [Google Chrome](https://chromeenterprise.google/browser/download/) ใน Central Store

- `Administrative Templates → Microsoft Edge → SmartScreen settings →`
  **Configure the list of domains for which Microsoft Defender SmartScreen won't trigger warnings** = Enabled → `swim.example.internal`
- `Administrative Templates → Google → Google Chrome → Safe Browsing settings →`
  **Configure the list of domains on which Safe Browsing will not trigger warnings** = Enabled → `swim.example.internal`

### ตรวจผล
บนเครื่องลูก: `gpupdate /force` แล้ว
```powershell
[System.Security.Policy.Zone]::CreateFromUrl('https://swim.example.internal/').SecurityZone   # ต้องได้ Intranet
Invoke-WebRequest https://swim.example.internal/v1/health -UseBasicParsing                     # ต้องไม่ error เรื่องใบรับรอง
```
เปิด Edge ใหม่ → `edge://policy` ต้องเห็น `SmartScreenAllowListDomains`

---

## B. Intune (เครื่องที่ไม่ join AD)

1. **ใบรับรอง:** Devices → Configuration → Create → *Templates → Trusted certificate* → อัปโหลด `swim-server.cer` (Computer certificate store - Root)
2. **Edge / Chrome / Zone:** Create → *Settings catalog* แล้วค้นหา
   - `Configure the list of domains for which Microsoft Defender SmartScreen won't trigger warnings` (Microsoft Edge)
   - `Configure the list of domains on which Safe Browsing will not trigger warnings` (Google Chrome)
   - `Site to Zone Assignment List` (Administrative Templates → Windows Components → Internet Explorer → Internet Control Panel → Security Page) → `https://swim.example.internal` = `1`
   - ถ้า Intune ไม่แปลงรายการนี้ให้ (ตรวจด้วยคำสั่งตรวจผลด้านล่าง) ให้ใช้ PowerShell script ใน Intune รัน `Set-SwimTrust.ps1` แทน
3. Assign ให้กลุ่มเครื่องลูก

---

## C. เครื่องเดี่ยว (นอกโดเมน เช่น เครื่องทดสอบที่บ้าน)

ใช้ **คำสั่งบนหน้าเพิ่มเครื่อง → "ตั้งค่าความเชื่อถือเครื่องนี้"** ใน PowerShell (Run as administrator) — สคริปต์
[`Set-SwimTrust.ps1`](../../agent/install/Set-SwimTrust.ps1) ตั้งค่า 3 ข้อเดียวกับ GPO โดย:
- ดึงใบรับรองผ่านการเชื่อมต่อที่ยอมรับเฉพาะ SHA-256 ที่ระบุ (pin) — ไม่ตรงไม่ทำอะไรเลย
- ตรวจผลให้: zone ต้องเป็น `Intranet` และ Windows ต้องเชื่อถือ TLS ของ server

ถอนการตั้งค่า: รันคำสั่งเดิมโดยเติม `$SwimTrustRemove=$true;` ไว้หน้าสุด

> Edge จะแสดง "Your browser is managed by your organization" เพราะมีการตั้ง policy — เป็นเรื่องปกติ

---

## ความปลอดภัย

- ทุกค่าจำกัดเฉพาะ host ของ SWIM — ไม่ได้ปิด SmartScreen/Safe Browsing กับเว็บอื่น
- ข้อ 2 ทำให้ Windows ถือว่า SWIM เป็นเว็บภายใน (เหมือน intranet ทั่วไป) ซึ่งรวมถึงการส่ง credential แบบ Windows Integrated Auth ให้โดยอัตโนมัติ — SWIM ยังไม่ใช้ แต่ต้องดูแล server ให้ปลอดภัยเท่าระบบภายในอื่น
- ไฟล์ที่ดาวน์โหลดจาก SWIM ถูกตรวจซ้ำอยู่แล้ว: MSI ฝัง pin ของ server, agent ตรวจ SHA-256 ของตัวเองตอนติดตั้งผ่านคำสั่ง, ทุกคำสั่งจาก server เซ็น Ed25519
- ระยะยาวควรเซ็น MSI/exe ด้วย Code Signing ([03 §2](../spec/03-agent.md)) เพื่อให้ใช้กับเครื่องนอกองค์กรได้โดยไม่มีคำเตือน
