# 06 · Job Manifest

**English** · [ไทย](06-manifest.th.md)

## 1. Envelope (detached signature over the actual bytes — no JSON canonicalisation needed)

```json
{
  "kid": "manifest-2026a",
  "alg": "Ed25519",
  "payload": "<base64url of the JSON bytes>",
  "sig": "<base64url of the signature over the payload bytes>"
}
```

A verifier (agent / swim-pe) must:
1. Find `kid` in the trusted keyset ([08 §3](08-security.md)) → not found = reject
2. Verify `sig` over the **payload bytes as received**, and only then parse the JSON
3. Check that `host.smbios_uuid` matches the machine and `not_before ≤ now ≤ not_after`
4. Check that `schema` is in the supported list

## 2. Payload (schema 1)

```json
{
  "schema": 1,
  "kind": "deploy",                          // deploy | rollback | stage | standing
  "job_id": "job_9821a4",
  "issued_at": "2026-11-23T09:00:00Z",
  "not_before": "2026-11-23T09:00:00Z",
  "not_after":  "2026-11-24T09:00:00Z",      // default 24 hours (standing = 7 days)
  "host": { "host_id": "h_01HFX...", "smbios_uuid": "4C4C4544-0042-...", "serial": "8XK2Q73" },
  "actor": "user:somchai@corp",               // or "rollout:ro_77" / "local:CORP\\tech01"

  "engine": "winpe",                          // winpe | setup
  "image": {
    "id": "img_160", "channel": "win11-x64-stable", "version": "v1.6",
    "arch": "amd64", "edition": "Enterprise",
    "chunks_url": "https://swim.corp.local/storage/images/img_160/chunks.json",
    "chunks_sig": "<Ed25519 of chunks.json>",
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
    "confirm_data_loss_d": false              // a repartition that erases D: requires true
  },

  "post_config": {
    "hostname": "BKK-FIN-042",
    "locale": "th-TH", "ui_language": "en-US", "timezone": "SE Asia Standard Time",
    "domain": { "name": "corp.local", "ou": "OU=Finance,OU=Workstations,DC=corp,DC=local" },
    // no secrets in the manifest — the ODJ blob arrives with the go-token at handoff (§3)
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

### Field rules

| Field | Rule |
|---|---|
| `kind=standing` | No specific `job_id` — the agent creates a local job from it; when offline at handoff (no ODJ blob) → installs as a workgroup machine, the new agent joins the domain later when online and reports WARN |
| `image.delta_base_id` | When not null, the base must be on S: or be downloaded as well |
| `fallback_image` | May be null (new channel) → preflight WARN "offline rollback is not possible" |
| `disk.mode=repartition` | Requires `confirm_data_loss_d=true` when the new layout overwrites an existing D: |
| `constraints.require_go_token` | true = a go-token must be requested before handoff ([07 §4](07-fleet-safety.md)) |

## 3. Sealed secrets

- Secrets are not in the manifest — they come with the go-token answer (`POST /v1/agent/jobs/{id}/go` → `{ go_token, secrets_sealed }`)
- Algorithm: X25519 + XChaCha20-Poly1305 (NaCl sealed box) to the machine's enrolled `x25519_pub`
- The agent opens the envelope with its private key (DPAPI machine scope) **only at handoff** and passes it to WinPE per [08 §5](08-security.md)
- Values in `secrets_sealed` must never be logged after opening

## 4. Other signed files

| File | Signed with | Verified by |
|---|---|---|
| `chunks.json` of an image / WinPE / driver pack | manifest key | agent, swim-pe |
| `release.json` of an agent bundle | release key | swim-svc, swim-agent |
| agent rollback directive | release key | swim-svc |
| keyset (the list of trusted public keys) | root release key (offline) | everything |
| agent configuration | manifest key | swim-agent |

```json
// chunks.json
{ "schema": 1, "object": "image", "id": "img_160", "total_sha256": "…", "size_bytes": 9771243008,
  "chunk_size": 67108864,
  "chunks": [ { "i": 0, "sha256": "…" }, { "i": 1, "sha256": "…" } ] }
```

## 5. Schema changes

- Optional fields may be added without raising `schema` (readers must ignore unknown fields)
- Changing the meaning of a field, or removing one → `schema` +1; the server issues manifests per the `manifest_schemas` the agent declares ([04 §8](04-agent-updates.md))
