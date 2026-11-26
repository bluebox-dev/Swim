# 06 · Job Manifest

## 1. Envelope (ลายเซ็นแบบ detached บน bytes จริง — ไม่ต้อง canonicalize JSON)

```json
{
  "kid": "manifest-2026a",
  "alg": "Ed25519",
  "payload": "<base64url ของ JSON bytes>",
  "sig": "<base64url ของลายเซ็นบน payload bytes>"
}
```

ผู้ตรวจ (agent / swim-pe) ต้อง:
1. หา `kid` ใน keyset ที่เชื่อถือ ([08 §3](08-security.md)) → ไม่เจอ = ปฏิเสธ
2. ตรวจ `sig` บน **bytes ของ payload ตามที่ได้รับ** แล้วค่อย parse JSON
3. ตรวจ `host.smbios_uuid` ตรงกับเครื่อง, `not_before ≤ now ≤ not_after`
4. ตรวจ `schema` อยู่ในรายการที่รองรับ

## 2. Payload (schema 1)

```json
{
  "schema": 1,
  "kind": "deploy",                          // deploy | rollback | stage | standing
  "job_id": "job_9821a4",
  "issued_at": "2026-11-23T09:00:00Z",
  "not_before": "2026-11-23T09:00:00Z",
  "not_after":  "2026-11-24T09:00:00Z",      // ค่าเริ่มต้น 24 ชม. (standing = 7 วัน)
  "host": { "host_id": "h_01HFX...", "smbios_uuid": "4C4C4544-0042-...", "serial": "8XK2Q73" },
  "actor": "user:somchai@corp",               // หรือ "rollout:ro_77" / "local:CORP\\tech01"

  "engine": "winpe",                          // winpe | setup
  "image": {
    "id": "img_160", "channel": "win11-x64-stable", "version": "v1.6",
    "arch": "amd64", "edition": "Enterprise",
    "chunks_url": "https://swim.corp.local/storage/images/img_160/chunks.json",
    "chunks_sig": "<Ed25519 ของ chunks.json>",
    "sha256": "e3b0c442...", "size_bytes": 9771243008, "expanded_bytes": 21474836480,
    "delta_base_id": null
  },
  "fallback_image": { "id": "img_150", "version": "v1.5", "sha256": "…", "chunks_url": "…" },
  "winpe":  { "version": "pe-1.4.0", "url": "…/winpe/pe-1.4.0/boot.wim", "sha256": "…",
              "sdi_url": "…/boot.sdi", "sdi_sha256": "…" },
  "driver_pack": { "id": "dp_dell_5440_24h2", "match_key": "dell:0C6B", "url": "…", "sha256": "…" },
  "agent_package": { "version": "1.5.0", "url": "…/agent/1.5.0/swim-agent.msi", "sha256": "…" },

  "disk": {
    "mode": "refresh",                        // refresh | repartition
    "profile_id": "dp_std",
    "rules": [
      { "if_disk_gb_lt": 300, "layout": [{ "drive": "C", "size": "remaining" }] },
      { "else": true, "layout": [{ "drive": "C", "size_gb": 200 }, { "drive": "D", "label": "Data", "size": "remaining" }] }
    ],
    "min_disk_gb": 128,
    "confirm_data_loss_d": false              // repartition ที่ลบ D: ต้องเป็น true
  },

  "post_config": {
    "hostname": "BKK-FIN-042",
    "locale": "th-TH", "ui_language": "en-US", "timezone": "SE Asia Standard Time",
    "domain": { "name": "corp.local", "ou": "OU=Finance,OU=Workstations,DC=corp,DC=local" },
    // ไม่มีความลับใน manifest — ODJ blob มากับ go-token ตอน handoff (§3)
    "bitlocker": { "required": true, "escrow": "ad" },   // ad | entra | none
    "unattend_template": "std-v3"
  },

  "schedule": { "mode": "window", "window_id": "mw_bkk_night", "deadline": "2026-11-30T23:00:00Z" },
  "user_notice": { "countdown_min": 15, "max_deferrals": 2, "max_defer_hours": 4, "force": false },
  "constraints": { "max_download_mbps": 20, "allow_vpn_download": false, "allow_metered": false,
                   "require_go_token": true, "auto_reboot": true },
  "rollout": { "rollout_id": "ro_77", "wave": "pilot" }
}
```

### กติกาฟิลด์

| ฟิลด์ | กติกา |
|---|---|
| `kind=standing` | ไม่มี `job_id` เฉพาะ — agent สร้าง job local จากมัน; ถ้าออฟไลน์ตอน handoff (ไม่ได้ ODJ blob) → ติดตั้งแบบ workgroup แล้ว agent ใหม่ join ภายหลังเมื่อออนไลน์ + รายงาน WARN |
| `image.delta_base_id` | ถ้าไม่ null ต้องมี base บน S: หรือโหลด base ด้วย |
| `fallback_image` | null ได้ (channel ใหม่) → preflight WARN "rollback ออฟไลน์ไม่ได้" |
| `disk.mode=repartition` | ต้องมี `confirm_data_loss_d=true` ถ้า layout ใหม่ทับ D: เดิม |
| `constraints.require_go_token` | true = ต้องขอ go-token ก่อน handoff ([07 §4](07-fleet-safety.md)) |

## 3. Sealed Secrets

- ความลับไม่อยู่ใน manifest — มากับคำตอบของ go-token (`POST /v1/agent/jobs/{id}/go` → `{ go_token, secrets_sealed }`)
- อัลกอริทึม: X25519 + XChaCha20-Poly1305 (NaCl sealed box) ไปยัง `x25519_pub` ของเครื่องที่ลงทะเบียนไว้
- agent เปิดซองด้วย private key (DPAPI machine) **ตอน handoff เท่านั้น** แล้วส่งต่อให้ WinPE ตาม [08 §5](08-security.md)
- ห้าม log ค่าใน `secrets_sealed` หลังเปิดซอง

## 4. ไฟล์อื่นที่เซ็น

| ไฟล์ | เซ็นด้วย | ตรวจโดย |
|---|---|---|
| `chunks.json` ของ image/winpe/driver | manifest key | agent, swim-pe |
| `release.json` ของ agent bundle | release key | swim-svc, swim-agent |
| rollback directive ของ agent | release key | swim-svc |
| keyset (รายการ public key ที่เชื่อ) | root release key (offline) | ทุกตัว |
| agent config | manifest key | swim-agent |

```json
// chunks.json
{ "schema": 1, "object": "image", "id": "img_160", "total_sha256": "…", "size_bytes": 9771243008,
  "chunk_size": 67108864,
  "chunks": [ { "i": 0, "sha256": "…" }, { "i": 1, "sha256": "…" } ] }
```

## 5. การเปลี่ยน schema

- เพิ่มฟิลด์ที่ไม่บังคับได้โดยไม่เพิ่ม `schema` (ผู้อ่านต้อง ignore ฟิลด์ที่ไม่รู้จัก)
- เปลี่ยนความหมาย/ลบฟิลด์ → `schema` +1; server ออก manifest ตาม `manifest_schemas` ที่ agent ประกาศ ([04 §8](04-agent-updates.md))
