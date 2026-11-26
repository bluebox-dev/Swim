# 08 · Security

> SWIM = ระบบที่ **ล้างเครื่องและรันโค้ดระดับ SYSTEM ได้ทุกเครื่องในองค์กร** → ต้องถือเป็น Tier-0 asset เทียบเท่า Domain Controller

## 1. Threat Model

| ภัย | ผลกระทบ | มาตรการ |
|---|---|---|
| ยึด Server ได้ | ล้างทุกเครื่อง / ฝัง malware ใน image | manifest/release เซ็นด้วยกุญแจแยก (§3), approve 2 คน, image ต้องผ่าน test, audit แบบแก้ไม่ได้, kill switch |
| ยึดบัญชี admin | สั่ง deploy/rollout อันตราย | SSO + MFA, RBAC, approve 2 คนสำหรับ publish/global actions |
| MITM / DNS spoof | ส่ง image ปลอม | mTLS + pin CA ภายใน + ตรวจลายเซ็น chunks.json |
| ปลอมเป็น agent | ขโมย ODJ blob / ข้อมูล inventory | client cert ต่อเครื่อง (TPM-backed ถ้ามี), secrets seal ไปยังกุญแจของเครื่อง |
| ผู้ใช้ local admin บนเครื่องลูก | สั่ง deploy เอง / แก้ไฟล์บน S: | local deploy ใช้ได้เฉพาะ manifest ที่เซ็น; ไฟล์บน S: ตรวจ hash/ลายเซ็นทุกครั้ง |
| ถอดดิสก์ไปอ่าน S: | ได้ ODJ blob | blob อายุสั้น + ใช้ครั้งเดียว + TPM seal (v2) |
| Agent update ปลอม | ยึดทั้ง fleet | Authenticode + Ed25519 release key ที่ **อยู่นอก server** |
| Supply chain (dependency) | backdoor ใน binary | pin go.sum, `govulncheck` ใน CI, reproducible build, SBOM |

## 2. PKI และใบรับรอง

| ใบ/กุญแจ | ที่เก็บ | อายุ | ใช้ทำ |
|---|---|---|---|
| **Root CA** | offline (USB/HSM ในตู้เซฟ) | 10 ปี | เซ็น Intermediate |
| **Intermediate CA** | Server — `step-ca` container (หรือ ADCS ขององค์กร) | 2 ปี | ออก TLS server + mTLS client cert |
| TLS server cert | nginx | 90 วัน auto-renew (ACME จาก step-ca) | HTTPS |
| Agent client cert | เครื่องลูก (TPM/DPAPI) | 90 วัน auto-renew ที่ 2/3 อายุ | mTLS |
| Utility VM client cert | Utility VM | 90 วัน | mTLS |

- agent **pin** Intermediate/Root ของ SWIM (ไม่เชื่อ CA สาธารณะสำหรับ API ของ SWIM)
- แจ้งเตือนใบที่จะหมดอายุ ≤ 30 วัน ทุกใบใน Dashboard (Operations)

## 3. กุญแจเซ็น (Signing Keys)

| กุญแจ | ที่เก็บ | เซ็นอะไร | หมุนเวียน |
|---|---|---|---|
| **Root release key** (Ed25519) | offline 2 ชุด (2 คนถือ) | keyset | เมื่อจำเป็นเท่านั้น |
| **Release key** (Ed25519) | hardware token ของ CI (YubiKey/HSM) — **ไม่อยู่บน SWIM server** | `release.json`, rollback directive | ทุก 1 ปี |
| **Manifest key** (Ed25519) | `signer` service แยก container บน server, กุญแจเข้ารหัสด้วย KEK จาก TPM ของ host (หรือ YubiHSM) | manifest, chunks.json, config, go-token | ทุก 6 เดือน |
| **Code signing** (Authenticode) | hardware token | `.exe`, `.msi` | ตามใบ |

### Keyset และการหมุนกุญแจ

```json
{ "schema": 1, "issued_at": "...", "keys": [
  { "kid": "manifest-2026a", "use": "manifest", "pub": "…", "not_after": "2027-06-01" },
  { "kid": "manifest-2026b", "use": "manifest", "pub": "…", "not_after": "2027-12-01" },
  { "kid": "release-2026",   "use": "release",  "pub": "…" } ],
  "sig": "<root release key>" }
```

- เครื่องลูกเชื่อ key ตาม keyset ล่าสุดที่ตรวจด้วย root release key (pubkey ของ root ฝังใน binary)
- หมุน: เพิ่ม key ใหม่ใน keyset → รอ ≥ 95% ของ fleet ได้ keyset ใหม่ → เริ่มเซ็นด้วย key ใหม่ → ถอด key เก่า
- กุญแจรั่ว: ออก keyset ใหม่ที่ถอด key นั้น + global kill switch จนกว่า fleet จะได้ keyset ใหม่

## 4. RBAC

| Role | ทำได้ |
|---|---|
| `viewer` | ดู dashboard, host, job, log สรุป |
| `technician` | สั่ง job รายเครื่อง (stage/deploy/rollback/cancel/collect_logs) เฉพาะ site ที่ได้รับ, ดู log เต็ม |
| `image_builder` | ใช้ Builder, build/test image, จัดการ driver pack |
| `approver` | approve/reject image และ WinPE (**ห้ามเป็นคนเดียวกับผู้ build รุ่นนั้น**) |
| `release_manager` | สร้าง/คุม rollout, kill switch, fleet rollback, ปล่อย agent release |
| `admin` | user/role, site, enrollment token, policy, keyset ceremony |

- Admin login: **SSO (OIDC/SAML) หรือ LDAP + MFA บังคับ**
- การกระทำที่ต้อง 2 คน: publish image, global kill switch (ปลด), fleet rollback > 50 เครื่อง, ปล่อย agent release ไป canary, หมุนกุญแจ

## 5. ความลับ (Secrets)

### ODJ blob (มีรหัสผ่านบัญชีเครื่อง)

| ขั้น | v1 (Phase 1–3) | v2 (Phase 4+) |
|---|---|---|
| สร้าง | Utility VM ตอน handoff ขอ (ไม่สร้างล่วงหน้า) | เหมือนเดิม |
| ส่ง | sealed box ไปยัง `x25519_pub` ของเครื่อง | เหมือนเดิม |
| ส่งต่อให้ WinPE | agent เปิดซอง → เขียน `S:\swim\jobs\<id>\odj.bin` **ทันทีก่อนรีบูต** | agent เปิดซอง → **seal ด้วย TPM** (ไม่ผูก PCR) → WinPE unseal ด้วย TPM เดียวกัน |
| หลังใช้ | WinPE เขียนทับด้วยศูนย์แล้วลบ | เหมือนเดิม |
| ถ้า job ไม่สำเร็จ/หมดอายุ 24 ชม. | server สั่ง Utility VM reset รหัสบัญชีเครื่อง (blob เดิมใช้ไม่ได้) | เหมือนเดิม |

> v1 คือข้อยกเว้นของหลัก "ไม่มีความลับบนดิสก์" ที่ยอมรับชั่วคราว — ช่วงเสี่ยงคือระหว่าง handoff ถึง apply (ไม่กี่นาที) และ blob ใช้ได้แค่บัญชีเครื่องเดียว

### อื่น ๆ
- รหัสผ่าน local admin: **ไม่มีใน unattend** — ใช้ Windows LAPS
- ความลับฝั่ง server (DB password, guacamole, SSO client secret): docker secrets / ไฟล์ที่ `chmod 600` + เข้ารหัสใน backup
- บัญชี djoin บน Utility VM: บัญชีเฉพาะ, สิทธิ์สร้าง/รีเซ็ตเฉพาะ OU เครื่องลูก, อยู่ใน allowlist ของ policy "Allow computer account re-use during domain join"

## 6. Audit Log

- ตาราง `audit_log` แบบ **append-only** (สิทธิ์ DB: INSERT อย่างเดียวสำหรับ app user) + hash chain (`prev_hash`) เพื่อตรวจการแก้ไข
- ส่งสำเนาไป syslog/SIEM ขององค์กรแบบ real-time
- เก็บ: ใคร (user / local:user / system / rollout), ทำอะไร, กับอะไร, เหตุผล, IP, ผล
- เก็บอย่างน้อย 1 ปี

## 7. Hardening

| ที่ | มาตรการ |
|---|---|
| Server host | SSH key only + MFA (หรือ bastion), UFW เปิดแค่ 443 (+ SSH จาก admin subnet), unattended-upgrades, ปิด service ที่ไม่ใช้ |
| Guacamole | เข้าผ่าน auth ของ backend เท่านั้น, ไม่เปิดพอร์ต VNC ออกนอก host, บันทึก session |
| nginx | TLS 1.2+ เท่านั้น, HSTS, rate limit ที่ `/v1/agent/enroll`, ขนาด request จำกัด |
| Agent | ไม่มีพอร์ตเปิดรับ, named pipe ACL Admins+SYSTEM, ไฟล์ใน Program Files/ProgramData ACL จำกัด |
| WinPE | ไม่มี shell เปิดให้ผู้ใช้ (startnet รัน swim-pe แล้วรีบูต/แสดงหน้าจอ error เท่านั้น), ปิด Shift+F10 ไม่ได้ใน PE แต่ PE ไม่มีความลับถาวร |
| CI | branch protection, signed commits สำหรับ tag release, `govulncheck`, SBOM ต่อ release |

## 8. ทดสอบความปลอดภัย

- ก่อนขึ้น production (จบ Phase 3): pentest ภายในของ API + agent (enroll abuse, manifest tamper, replay go-token, privilege escalation ผ่าน named pipe)
- ทุก release: test อัตโนมัติ "manifest ที่ถูกแก้ 1 byte ต้องถูกปฏิเสธ", "keyset ที่ไม่ได้เซ็นต้องถูกปฏิเสธ", "downgrade โดยไม่มี directive ต้องถูกปฏิเสธ"
