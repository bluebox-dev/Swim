# คู่มือติดตั้ง SWIM

จาก server เปล่าจนถึงสั่งติดตั้ง Windows ให้เครื่องลูกเครื่องแรก

## 1. สิ่งที่ต้องเตรียม

| รายการ | ขั้นต่ำ | หมายเหตุ |
|---|---|---|
| ระบบปฏิบัติการ | Ubuntu 24.04 LTS | ผู้ใช้ที่มีสิทธิ์ sudo (ไม่ใช่ root) |
| CPU | รองรับ virtualization (VT-x/AMD-V) | ถ้าเป็น VM ต้องเปิด nested virtualization ไม่อย่างนั้น Builder ใช้ไม่ได้ |
| RAM | 16 GB | เครื่องต้นแบบใช้ 8 GB ระหว่างเปิด |
| ดิสก์ | 100 GB ว่าง | image หนึ่งตัวประมาณ 12 GB, เครื่องต้นแบบ 30 ถึง 80 GB |
| เครือข่าย | IP คงที่ | เครื่องลูกต้องเข้าถึงพอร์ต 443 ของ server ได้ |
| ISO ของ Windows 11 | 1 ไฟล์ | ใช้สร้างเครื่องต้นแบบและ WinPE |

พอร์ตที่เปิดสู่ภายนอกมีแค่ 22, 80 และ 443 (สคริปต์ตั้ง ufw ให้)

## 2. ติดตั้ง server

```bash
sudo mkdir -p /opt/swim && sudo chown "$USER" /opt/swim
git clone https://github.com/bluebox-dev/Swim.git /opt/swim
cd /opt/swim
./install.sh
```

`install.sh` ทำ 5 ขั้นและรันซ้ำได้โดยไม่ทับของเดิม:

1. ติดตั้งแพ็กเกจ: Docker, KVM/libvirt, OVMF, swtpm, wimlib และเครื่องมือดิสก์
2. `builder-infra/scripts/setup-host.sh`: สร้าง `/srv/swim`, ไฟล์ `builder-infra/.env` ที่มีรหัสสุ่มชุดใหม่,
   ใบรับรอง TLS แบบ self-signed และกฎ firewall
3. `docker compose up -d --build`: build backend (รัน `go vet` และ `go test` ในขั้น build) แล้วสตาร์ตทุกบริการ
   ฐานข้อมูลถูกสร้างและ migrate เองตอน backend สตาร์ต
4. `builder-infra/scripts/build-agent.sh`: build agent ของ Windows และวางไว้ให้เครื่องลูกดาวน์โหลด
5. `builder-infra/scripts/build-winpe.sh`: build SWIM WinPE ถ้ามี ISO อยู่ใน `/srv/swim/vms/iso/` แล้ว

ออกจากระบบแล้วเข้าใหม่หนึ่งครั้ง เพื่อให้ผู้ใช้ได้สิทธิ์กลุ่ม `docker` และ `libvirt`

## 3. เข้าใช้งานครั้งแรก

1. เปิด `https://<IP ของ server>/` (เบราว์เซอร์จะเตือนเรื่องใบรับรอง เพราะเป็น self-signed)
2. ตอนยังไม่มีบัญชี ให้เข้าด้วย admin token ดูได้จาก

   ```bash
   sudo grep ^SWIM_ADMIN_TOKENS /opt/swim/builder-infra/.env
   ```

3. สร้างบัญชีผู้ดูแลคนแรกที่ ตั้งค่า → ผู้ดูแลระบบ → เพิ่มผู้ดูแล ระบบจะให้ลิงก์ตั้งค่าใช้ครั้งเดียว
   เปิดลิงก์เพื่อตั้งรหัสผ่านและสแกน QR ด้วย Google Authenticator แล้วเก็บรหัสสำรอง 10 ชุดไว้
4. จากนั้นเข้าระบบด้วยชื่อผู้ใช้ รหัสผ่าน และรหัส 6 หลักจากแอป

สร้างบัญชีจาก command line ได้เช่นกัน:

```bash
sudo docker exec swim-backend-1 swim-server user add <username>
```

## 4. นำ ISO ของ Windows ขึ้น server

หน้าเว็บไม่มีการอัปโหลด ISO โดยตั้งใจ ให้คัดลอกผ่าน ssh:

```bash
scp Windows11.iso <user>@<server>:/srv/swim/vms/iso/
```

แล้ว build WinPE (ถ้าตอนติดตั้งยังไม่มี ISO):

```bash
cd /opt/swim/builder-infra
./scripts/build-winpe.sh /srv/swim/vms/iso/Windows11.iso
```

## 5. สร้าง image แรก

มี 2 ทาง

**ทาง A นำเข้าจาก ISO โดยตรง** หน้า Images → เลือก ISO → เลือกรุ่นของ Windows → นำเข้า → อนุมัติ
ได้ Windows สะอาดตามแผ่น เหมาะกับการเริ่มต้นเร็ว

**ทาง B สร้างจากเครื่องต้นแบบ (Golden Master)** เมื่อต้องการลงโปรแกรมและตั้งค่าไว้ใน image

```bash
cd /opt/swim/builder-infra
ISO=/srv/swim/vms/iso/Windows11.iso DISK_GB=80 ./scripts/create-golden-master.sh gm-win11
```

1. หน้า Images → เปิดเครื่องต้นแบบ → เปิดหน้าจอ (Console) ในเบราว์เซอร์ แล้วติดตั้ง Windows โปรแกรม และอัปเดต
2. ปิดเครื่องต้นแบบ (Shut down)
3. กด Capture เลือกหัวข้อ image และตั้งชื่อเวอร์ชัน ระบบทำ sysprep ในสำเนาชั่วคราว ตรวจ image และ capture เอง
   เครื่องต้นแบบไม่ถูกแก้
4. เมื่อสถานะเป็น "รออนุมัติ" กดอนุมัติ image จึงพร้อมติดตั้ง

ห้ามลบเครื่องต้นแบบด้วย `virsh undefine --remove-all-storage` เพราะจะลบ ISO ใน `/srv/swim/vms/iso/` ไปด้วย

## 6. เพิ่มเครื่องลูก

หน้า เพิ่มเครื่อง ให้ 2 แบบ:

- **ไฟล์ MSI** ดาวน์โหลดแล้วดับเบิลคลิกบนเครื่องลูก
- **คำสั่ง PowerShell** วางใน PowerShell ที่เปิดแบบ Administrator

ทั้งสองแบบมี token อายุสั้นและลายนิ้วมือใบรับรองของ server ฝังอยู่ agent จึงคุยกับ server ตัวนี้เท่านั้น
เครื่องจะขึ้นในหน้า เครื่อง ภายในไม่กี่วินาที

สำหรับหลายเครื่องในโดเมน ดู [deploy/windows-trust-policy.md](deploy/windows-trust-policy.md)

## 7. สั่งติดตั้ง Windows

1. หน้าเครื่อง → แท็บ Windows → ล้างเครื่องและติดตั้ง
2. เลือก image, รูปแบบดิสก์ และตั้งชื่อกับรหัสผ่านของผู้ดูแลเครื่อง (local administrator)
3. ยืนยัน เครื่องจะดาวน์โหลด image ตรวจ SHA-256 บูตเข้า WinPE ลง Windows แล้วกลับมาออนไลน์เอง

สั่งหลายเครื่องพร้อมกันได้จากหน้า เครื่อง (เลือกหลายแถว) หรือตั้งเวลาในหน้า ตารางงาน

## 8. ตรวจว่าติดตั้งถูกต้อง

```bash
cd /opt/swim/builder-infra
sudo docker compose ps                     # ทุกบริการต้อง Up
curl -sk https://127.0.0.1/v1/health       # {"status":"ok"}
sudo bash ./scripts/smoke-test.sh          # ทดสอบ end-to-end (สร้างข้อมูลทดสอบ ใช้กับ lab เท่านั้น)
```

ขั้นต่อไป: [OPERATIONS.md](OPERATIONS.md)
