# คู่มือดูแลระบบ SWIM

## ที่อยู่ของสิ่งต่าง ๆ บน server

| Path | เนื้อหา |
|---|---|
| `/opt/swim` | โค้ด (repository นี้) |
| `/opt/swim/builder-infra/.env` | รหัสลับของระบบ (สิทธิ์ 0600 ไม่อยู่ใน Git) |
| `/srv/swim/storage/` | image, WinPE, ไฟล์ agent, log ของงาน |
| `/srv/swim/keys/` | กุญแจเซ็นคำสั่งและกุญแจเข้ารหัสตารางงาน |
| `/srv/swim/certs/` | ใบรับรอง TLS |
| `/srv/swim/vms/` | ดิสก์ของเครื่องต้นแบบ, `iso/` สำหรับ ISO |
| `/srv/swim/audit-archive/` | ไฟล์ประวัติการใช้งานที่เก็บถาวร |
| Docker volume `swim_pgdata` | ฐานข้อมูล PostgreSQL |

## อัปเดตระบบ

ก่อนทุกครั้ง ตรวจว่าไม่มีงานติดตั้งหรือ capture ค้างอยู่ (หน้าแรกของ console) การรีสตาร์ต backend ระหว่างติดตั้งทำให้งานสะดุด
และห้ามรีสตาร์ต container `builder` ระหว่าง capture

```bash
cd /opt/swim && git pull
cd builder-infra

# backend เปลี่ยน
sudo docker compose build backend && sudo docker compose up -d --no-deps backend

# nginx.conf เปลี่ยน
sudo docker exec swim-nginx-1 nginx -t && sudo docker exec swim-nginx-1 nginx -s reload

# หน้าเว็บเปลี่ยน: ไม่ต้องทำอะไร nginx อ่านไฟล์จาก ../frontend โดยตรง
# (เปลี่ยนเลข ?v= ใน frontend/index.html ทุกครั้งที่แก้ app.js หรือ app.css)
```

Migration ของฐานข้อมูลรันเองตอน backend สตาร์ต

## ออก agent รุ่นใหม่

1. เพิ่มเลขรุ่นใน `agent/internal/agent/state.go` (`const Version`)
2. `./builder-infra/scripts/build-agent.sh` รัน vet และ test แล้วเผยแพร่ไฟล์กับ `latest.json`
3. เครื่องลูกที่ออนไลน์อัปเดตตัวเองภายในไม่กี่นาที (ตรวจ SHA-256 ก่อนสลับ และย้อนกลับเองถ้ารุ่นใหม่สตาร์ตไม่ได้)
   เครื่องที่มีงานติดตั้งค้างอยู่จะรอจนงานจบ

ถ้าแก้ `pe/` ให้รัน `./builder-infra/scripts/build-winpe.sh` ด้วย

## สำรองข้อมูล

สิ่งที่ต้องสำรองเพื่อกู้ระบบได้ครบ:

```bash
# ฐานข้อมูล
sudo docker exec swim-postgres-1 pg_dump -U swim_owner -Fc swim > swim-$(date +%F).dump

# รหัสลับและกุญแจ (เก็บในที่ปลอดภัย แยกจาก Git)
sudo tar czf swim-secrets-$(date +%F).tgz /opt/swim/builder-infra/.env /srv/swim/keys /srv/swim/certs
```

ถ้ากุญแจใน `/srv/swim/keys/` หาย agent ที่ติดตั้งไปแล้วจะไม่เชื่อคำสั่งจาก server ตัวใหม่ ต้องติดตั้ง agent ใหม่ทุกเครื่อง
ถ้าใบรับรอง TLS เปลี่ยน ลายนิ้วมือที่ agent ตรึงไว้จะไม่ตรง ต้องติดตั้ง agent ใหม่เช่นกัน
image ใน `/srv/swim/storage/images/` สร้างใหม่ได้จากเครื่องต้นแบบ จะสำรองหรือไม่ก็ได้

## ความปลอดภัย

- **เข้าระบบ** ชื่อผู้ใช้ รหัสผ่าน และรหัสจากแอปยืนยันตัวตน (TOTP) ผิด 5 ครั้งบัญชีถูกพัก 15 นาที ผิด 20 ครั้งจาก IP เดียวถูกพัก 15 นาที
- **Admin token ใน `.env`** ใช้เข้าระบบได้โดยไม่ผ่าน 2FA เก็บไว้ใช้ยามฉุกเฉินและสำหรับสคริปต์บน server เท่านั้น
- **ลืมรหัสหรือมือถือหาย** ผู้ดูแลคนอื่นกด "รีเซ็ตการยืนยันตัวตน" หรือรันบน server:
  `sudo docker exec swim-backend-1 swim-server user reset-2fa <username>` จะได้ลิงก์ตั้งค่าใหม่
- **คำสั่งถึงเครื่องลูก** เซ็นด้วย Ed25519 ทุกคำสั่ง agent ตรวจลายเซ็น อายุคำสั่ง และกันการส่งซ้ำ
- **เครื่องมือบนเครื่องลูก** สคริปต์ของเครื่องมือสำเร็จรูปเขียนและตรวจค่าบน server (`backend/internal/api/tools.go`)
  API ไม่รับข้อความสคริปต์จากภายนอก และปิดการบันทึกสคริปต์ใหม่ผ่าน API
- **ประวัติการใช้งาน** แก้หรือลบตรง ๆ ไม่ได้ เก็บ 90 วันแล้วระบบลบเอง (`SWIM_AUDIT_KEEP_DAYS` ขั้นต่ำ 30)
  การเข้าระบบไม่สำเร็จบันทึก IP ชื่อที่กรอก และเบราว์เซอร์ ไม่บันทึกรหัสผ่านที่กรอก
- **nginx** บังคับ TLS 1.2 ขึ้นไป ส่ง security header (CSP, HSTS, กัน iframe) และจำกัดความถี่ของการเข้าระบบ

สิ่งที่ควรทำเพิ่มบน server จริง (สคริปต์ติดตั้งไม่ได้ทำให้ เพราะอาจทำให้เข้า server ไม่ได้):

```bash
# ปิดการเข้า SSH ด้วยรหัสผ่าน (ต้องมี key ใน ~/.ssh/authorized_keys ก่อน)
printf 'PasswordAuthentication no\nKbdInteractiveAuthentication no\n' | sudo tee /etc/ssh/sshd_config.d/00-swim-hardening.conf
sudo sshd -t && sudo systemctl reload ssh
```

ใช้ใบรับรอง TLS จาก CA ของหน่วยงานแทน self-signed เมื่อขึ้นใช้งานจริง

## แก้ปัญหา

| อาการ | ดูที่ไหน |
|---|---|
| หน้าเว็บเข้าไม่ได้ | `sudo docker compose ps`, `sudo docker compose logs nginx backend` |
| เครื่องลูกไม่ขึ้นในระบบ | บนเครื่องลูก `C:\ProgramData\SWIM\logs\agent.log` และ `"C:\Program Files\SWIM\swim-agent.exe" status` |
| งาน capture ล้มเหลว | ปุ่ม log ของงานในหน้า Images หรือ `/srv/swim/storage/logs/tasks/<task_id>.log` |
| งานติดตั้งล้มเหลว | หน้าเครื่อง → แท็บ Windows → รายละเอียดของงาน (มี log การบูตแนบ) |
| พื้นที่ server ใกล้เต็ม | หน้า พื้นที่ server |

## ปัญหาที่รู้อยู่

**เครื่องเสมือนบน KubeVirt บูตกลับเข้า Windows เดิมแทน WinPE** พบบน VM ทดสอบเมื่อ 26 พ.ย. 2026:
ตอนที่ VM เริ่มบูต WinPE แพลตฟอร์มสร้าง VM ขึ้นใหม่ทั้งตัว (TPM ถูกล้าง นาฬิกาและค่า Secure Boot ถูกรีเซ็ต)
เครื่องจึงกลับเข้า Windows เดิมโดยไม่มีอะไรถูกแก้ สาเหตุอยู่ฝั่งแพลตฟอร์มและยังไม่ได้ระบุ
agent ตั้งแต่ 0.5.23 ลองบูตเข้า WinPE ซ้ำเองสูงสุด 3 ครั้งก่อนยกเลิกงาน ยังไม่ได้ทดสอบกับเครื่อง PC จริง
