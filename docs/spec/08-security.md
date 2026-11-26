# 08 · Security

**English** · [ไทย](08-security.th.md)

> SWIM is a system that **can wipe every machine in the organisation and run code as SYSTEM on it** → it must be treated as a Tier-0 asset, on a par with a domain controller.

## 1. Threat model

| Threat | Impact | Measures |
|---|---|---|
| Server takeover | wipe every machine / plant malware in an image | manifests and releases signed with separate keys (§3), two-person approval, images must pass tests, tamper-evident audit, kill switch |
| Admin account takeover | dangerous deploys / rollouts | SSO + MFA, RBAC, two-person approval for publish and global actions |
| MITM / DNS spoofing | a forged image is delivered | mTLS + pinned internal CA + signature check of chunks.json |
| Agent impersonation | steal the ODJ blob / inventory data | per-machine client certificate (TPM-backed when available), secrets sealed to the machine's key |
| A local administrator on a client | starts a deploy / edits files on S: | local deploys work only with a signed manifest; files on S: are verified by hash / signature every time |
| A disk removed to read S: | obtains the ODJ blob | short-lived, single-use blob + TPM sealing (v2) |
| Forged agent update | takeover of the whole fleet | Authenticode + an Ed25519 release key that is **kept off the server** |
| Supply chain (dependencies) | a backdoor in the binary | pinned go.sum, `govulncheck` in CI, reproducible builds, SBOM |

## 2. PKI and certificates

| Certificate / key | Kept in | Lifetime | Used for |
|---|---|---|---|
| **Root CA** | offline (USB / HSM in a safe) | 10 years | signs the intermediate |
| **Intermediate CA** | server — `step-ca` container (or the organisation's ADCS) | 2 years | issues the TLS server and mTLS client certificates |
| TLS server certificate | nginx | 90 days, auto-renewed (ACME from step-ca) | HTTPS |
| Agent client certificate | client (TPM / DPAPI) | 90 days, auto-renewed at 2/3 of its life | mTLS |
| Utility VM client certificate | Utility VM | 90 days | mTLS |

- The agent **pins** SWIM's intermediate / root (it does not trust public CAs for the SWIM API)
- Every certificate expiring within 30 days raises an alert on the dashboard (Operations)

## 3. Signing keys

| Key | Kept in | Signs | Rotation |
|---|---|---|---|
| **Root release key** (Ed25519) | offline, 2 copies (held by 2 people) | the keyset | only when necessary |
| **Release key** (Ed25519) | the CI's hardware token (YubiKey / HSM) — **not on the SWIM server** | `release.json`, rollback directives | yearly |
| **Manifest key** (Ed25519) | a separate `signer` container on the server, encrypted with a KEK from the host's TPM (or a YubiHSM) | manifests, chunks.json, config, go-tokens | every 6 months |
| **Code signing** (Authenticode) | hardware token | `.exe`, `.msi` | per certificate |

### Keyset and key rotation

```json
{ "schema": 1, "issued_at": "...", "keys": [
  { "kid": "manifest-2026a", "use": "manifest", "pub": "…", "not_after": "2027-06-01" },
  { "kid": "manifest-2026b", "use": "manifest", "pub": "…", "not_after": "2027-12-01" },
  { "kid": "release-2026",   "use": "release",  "pub": "…" } ],
  "sig": "<root release key>" }
```

- Clients trust keys per the latest keyset verified with the root release key (the root's public key is embedded in the binary)
- Rotation: add the new key to the keyset → wait until ≥ 95% of the fleet has the new keyset → start signing with the new key → remove the old key
- Key compromise: issue a new keyset without that key + global kill switch until the fleet has the new keyset

## 4. RBAC

| Role | May |
|---|---|
| `viewer` | see dashboards, hosts, jobs, summary logs |
| `technician` | run per-machine jobs (stage / deploy / rollback / cancel / collect_logs) in assigned sites only, see full logs |
| `image_builder` | use the Builder, build / test images, manage driver packs |
| `approver` | approve / reject images and WinPE (**never the person who built that release**) |
| `release_manager` | create and control rollouts, kill switch, fleet rollback, release agents |
| `admin` | users / roles, sites, enrollment tokens, policy, keyset ceremony |

- Admin login: **SSO (OIDC / SAML) or LDAP, with mandatory MFA**
- Two-person actions: publishing an image, clearing a global kill switch, fleet rollback of > 50 machines, releasing an agent to canary, key rotation

## 5. Secrets

### ODJ blob (contains the machine account password)

| Step | v1 (Phases 1–3) | v2 (Phase 4+) |
|---|---|---|
| Creation | by the Utility VM when handoff requests it (never in advance) | same |
| Transport | sealed box to the machine's `x25519_pub` | same |
| Passing to WinPE | the agent opens the envelope → writes `S:\swim\jobs\<id>\odj.bin` **right before the reboot** | the agent opens the envelope → **seals with the TPM** (not PCR-bound) → WinPE unseals with the same TPM |
| After use | WinPE overwrites it with zeros and deletes it | same |
| If the job fails / expires after 24 hours | the server tells the Utility VM to reset the machine account password (the old blob is useless) | same |

> v1 is a temporarily accepted exception to "no secrets on disk" — the exposure window is between handoff and apply (a few minutes) and the blob is good for a single machine account only.

### Other
- Local admin password: **not in the unattend file** — Windows LAPS is used
- Server-side secrets (DB password, Guacamole, SSO client secret): docker secrets / files with `chmod 600` + encrypted in backups
- The djoin account on the Utility VM: a dedicated account allowed to create / reset only in the client OU, listed in the policy "Allow computer account re-use during domain join"

## 6. Audit log

- Table `audit_log` is **append-only** (DB rights: INSERT only for the app user) + a hash chain (`prev_hash`) to detect tampering
- A copy is streamed to the organisation's syslog / SIEM in real time
- Records: who (user / local:user / system / rollout), what, on what, why, IP, result
- Kept for at least 1 year

## 7. Hardening

| Where | Measures |
|---|---|
| Server host | SSH keys only + MFA (or a bastion), UFW open for 443 only (+ SSH from the admin subnet), unattended-upgrades, unused services off |
| Guacamole | reachable through the backend's auth only, no VNC port outside the host, sessions recorded |
| nginx | TLS 1.2+ only, HSTS, rate limit on `/v1/agent/enroll`, request size limits |
| Agent | no listening port, named pipe ACL Admins + SYSTEM, restricted ACLs in Program Files / ProgramData |
| WinPE | no shell for the user (startnet runs swim-pe, then reboots or shows an error screen); Shift+F10 cannot be disabled in PE, but PE holds no lasting secrets |
| CI | branch protection, signed commits for release tags, `govulncheck`, SBOM per release |

## 8. Security testing

- Before production (end of Phase 3): an internal penetration test of the API and the agent (enrollment abuse, manifest tampering, go-token replay, privilege escalation through the named pipe)
- Every release: automated tests that "a manifest altered by 1 byte is rejected", "an unsigned keyset is rejected", "a downgrade without a directive is rejected"
