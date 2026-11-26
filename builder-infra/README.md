# builder-infra — SWIM server stack

Ubuntu 24.04 host: KVM/libvirt + OVMF + swtpm + wimlib on the host, and the
control plane in Docker Compose (postgres, backend, builder, waker, nginx, guacd, guacamole).
First installation from a fresh server: `../install.sh` and `../docs/INSTALL.md`.

## ติดตั้ง / อัปเดต

```bash
# จากเครื่องนักพัฒนา (root ของ repo)
rsync -az --delete --exclude .env --exclude docs/review ./ ubuntu@<server>:/opt/swim/

# บน server
cd /opt/swim/builder-infra
./scripts/setup-host.sh                 # ครั้งแรก: /srv/swim, .env (รหัสผ่าน+admin token), cert, ufw
sudo docker compose up -d --build       # build backend (go vet + go test ใน Dockerfile) แล้วสตาร์ต
sudo bash ./scripts/smoke-test.sh       # ทดสอบ end-to-end 49 ข้อ (สร้างข้อมูลทดสอบ — ใช้กับ lab เท่านั้น)
```

- Admin token อยู่ใน `builder-infra/.env` (`SWIM_ADMIN_TOKENS=admin1:…,admin2:…`) — ใช้ล็อกอินหน้า Dashboard ที่ `https://<server>/`
- Migration รันอัตโนมัติตอน backend สตาร์ต ด้วย role `swim_owner`; แอปใช้ `swim_app` ซึ่งแก้/ลบ `audit_log` ไม่ได้

## ที่อยู่ข้อมูล (`/srv/swim`)

| Path | ใช้ทำ |
|---|---|
| `storage/{images,winpe,drivers,agent,logs}` | ไฟล์ที่เสิร์ฟให้เครื่องลูก (nginx `/storage/` แบบ internal) |
| `keys/manifest-ed25519.pem` | กุญแจเซ็น manifest (สร้างอัตโนมัติ, 0600) |
| `certs/` | TLS self-signed (แทนจนกว่าจะมี step-ca) |
| `vms/` | ดิสก์ qcow2 ของ VM, `vms/iso/` วาง ISO ของ Windows |

## Builder ผ่านเว็บ (Golden Master → WIM)

หน้า `https://<server>/` ส่วน **Builder**:

1. **Console** — เปิดหน้าจอ VM ในเบราว์เซอร์ (Guacamole) ติดตั้ง Windows/โปรแกรม/อัปเดต แล้ว **ปิด VM** (Shut down)
2. **Capture** (เลือก channel และเวอร์ชัน) → ระบบทำทุกขั้นเอง ไม่ต้องรัน sysprep เอง:
   preflight พื้นที่ → reseal (sysprep ใน VM ชั่วคราว) → คลายไฟล์ CompactOS → ตรวจ S-01…S-06 → capture
   → WIM ที่ `storage/images/<channel>/<version>.wim` สถานะ `pending` — **Golden Master ไม่ถูกแก้เลย** ใช้ต่อได้ทันที
3. Admin กด **อนุมัติ** → `published` (ดูผลแต่ละขั้นและผลตรวจได้ที่ `GET /v1/images/{id}`)

การทำงานเบื้องหลัง:
- Console: backend ออกลิงก์ Guacamole แบบ JSON auth (AES-128-CBC + HMAC-SHA256, อายุ 5 นาที) — ไม่มี user/DB ของ Guacamole
- Capture: container `builder` (privileged) ดึงงานจาก `server_tasks`:
  - overlay `capture-<image>.qcow2` บน disk ของ Golden Master (master เป็น backing file อ่านอย่างเดียว; ระหว่าง capture
    API จะไม่ยอมให้ start/revert VM นั้น)
  - ถ้า `Windows/Setup/State/State.ini` ไม่ใช่ `IMAGE_STATE_GENERALIZE_RESEAL_TO_OOBE`: เขียน `HKLM\SYSTEM\Setup`
    CmdLine (SetupType=2) + `PreventDeviceEncryption` แบบ offline แล้วบูต overlay เป็น VM ชั่วคราว (ไม่มี network,
    `on_reboot=destroy`) ให้ sysprep `/generalize /oobe` ราว 2–3 นาที — sysprep ไม่ผ่าน → งาน fail พร้อม setuperr.log
  - ไฟล์ CompactOS (WOF) ใน `\Windows` → ไฟล์ปกติ (`ntfs-unwof` + `pkg/wof`, ตรวจ PE checksum ก่อนเขียน)
    เพราะ WinPE รัน DISM/bcdboot จาก image ที่ยังบีบอัดอยู่ไม่ได้
  - ตรวจ offline S-01 generalized · S-02 ไฟล์บูต/DISM ปกติ · S-03 ไม่มีตัวตน agent · S-04 ไม่มีรหัสผ่านใน unattend
    · S-05 ไม่มี pending.xml · S-06 pagefile — S-01/S-02 ไม่ผ่าน = fail
  - `wimlib-imagex capture` (LZX, ตัด pagefile/hiberfil/swapfile) แล้วลบ overlay
- ห้ามบูต Golden Master ขณะมี overlay/linked clone ที่ใช้ disk ของมันอยู่ (clone จะเสีย)
- log ของแต่ละงานอยู่ที่ `storage/logs/tasks/<task_id>.log` (ดูได้จากปุ่ม log บนเว็บ)
- พอร์ตภายใน: backend `127.0.0.1:8090`, guacamole `:8080` (ufw บล็อกจากภายนอก), guacd `127.0.0.1:4822`

## Agent (เครื่องลูก)

```bash
./scripts/build-agent.sh          # vet + test + build exe → /srv/swim/storage/agent + latest.json + install.ps1
```

- หน้าเว็บ **เพิ่มเครื่อง** สร้างคำสั่งติดตั้ง (token อายุสั้น + pin ใบรับรอง) → วางใน PowerShell (Admin) บน Windows
- ไฟล์สาธารณะ: `https://<server>/dl/agent/{latest.json,install.ps1,swim-agent-<ver>-windows-amd64.exe}`
- บนเครื่องลูก: `C:\Program Files\SWIM\swim-agent.exe status`, log ที่ `C:\ProgramData\SWIM\logs\agent.log`, service `SWIMAgent`
- ทดสอบครบวงจร: `./scripts/create-test-client.sh swim-test01` (Windows 11 ติดตั้งเองแล้วรันคำสั่งติดตั้ง agent ตอน logon แรก)

## Golden Master VM

```bash
# วาง ISO ที่ /srv/swim/vms/iso/ ก่อน แล้ว
# ถ้าจะสร้างใหม่: ห้ามใช้ --remove-all-storage — มันลบ "ทุกไฟล์ที่ต่อกับ VM" รวมถึง ISO ใน vms/iso/ ด้วย
sudo virsh undefine gm-win11 --nvram --tpm && sudo rm /srv/swim/vms/gm-win11.qcow2
ISO=/srv/swim/vms/iso/Windows11.iso DISK_GB=80 ./scripts/create-golden-master.sh gm-win11
```

VNC ฟังเฉพาะ 127.0.0.1 — เข้าดูชั่วคราวด้วย `ssh -L 5900:127.0.0.1:5900 ubuntu@<server>` แล้วเปิด VNC client ที่ `localhost:5900`

## สิ่งที่ยังเป็น DEV (ต้องเปลี่ยนก่อน production)

- Admin token ใน `.env` ยังเข้าระบบได้โดยไม่ผ่าน 2FA (ไว้ใช้ฉุกเฉิน) — บัญชีผู้ดูแลใช้รหัสผ่าน + TOTP แล้ว
- เครื่องลูกใช้ host token (`hosts.dev_token_hash`, migration 0002) → เปลี่ยนเป็น mTLS cert จาก step-ca
- TLS self-signed → cert จาก PKI ภายใน
