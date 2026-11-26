# 11 · API (v1)

**English** · [ไทย](11-api.th.md)

- Base: `https://swim.corp.local/v1`
- Agent API: **mTLS** (the machine's client certificate) — except `enroll` / `renew`, which use a token / proof of possession
- Admin API: SSO session + CSRF token; every state-changing endpoint writes an audit entry
- Every agent request sends the headers `SWIM-Agent-Version` and `SWIM-Manifest-Schemas: 1,2`
- Error format: `{ "error": { "code": "SWIM-API-403", "message": "...", "retry_after_s": 60 } }`

> This chapter describes the target API. The endpoints implemented today are registered in
> `backend/internal/api/server.go`.

## 1. Agent API

| Method | Path | Purpose |
|---|---|---|
| POST | `/agent/enroll` | `{token \| reimage_ticket, csr, x25519_pub, inventory}` → `{host_id, cert, keyset, config}` |
| POST | `/agent/renew` | renew an expired certificate (≤ 180 days) with a signature from the old key |
| POST | `/agent/heartbeat` | `{state, job_status, versions, netcond, stage_free}` → `{halt, agent_target, svc_target, config?, keyset?, standing_manifest?, commands_pending: bool}` |
| GET | `/agent/commands?wait=55` | long-poll → `[{id, type, manifest?, args}]` |
| POST | `/agent/commands/{id}/ack` | acknowledge a command |
| PUT | `/agent/inventory` | full inventory update |
| POST | `/agent/events` | outbox batch `[{job_id, seq, source, status, step, severity, data, at}]` → idempotent |
| POST | `/agent/jobs/{id}/preflight` | submit preflight results → `{risk, eta, allow}` |
| POST | `/agent/jobs/{id}/slot` | request a concurrency slot (download / deploy) → `{granted, lease_until \| retry_after_s}` |
| POST | `/agent/jobs/{id}/go` | request a go-token before handoff → `{go_token, secrets_sealed?}` or `409 halted` |
| POST | `/agent/jobs/{id}/summary` | job summary (every job) |
| POST | `/agent/logs` | multipart upload of a log bundle |
| GET | `/agent/releases/{component}/{version}` | bundle information + download link |
| POST | `/agent/updates` | report an update result `{release, result: updated\|failed\|deferred, detail}` |
| GET | `/agent/peers?chunk=…` | (Phase 4) peers in the subnet that hold a chunk |

Large files: `GET /storage/...` through nginx — the backend authorises with `auth_request` and nginx serves the bytes (`Range` supported).

## 2. WinPE API (best effort, mTLS with a temporary per-job certificate)

| Method | Path | Purpose |
|---|---|---|
| GET | `/pe/jobs/{id}/halt` | check the kill switch before the PONR |
| POST | `/pe/jobs/{id}/events` | status during apply (with network) |

> WinPE's temporary certificate: requested by the agent together with the go-token (valid 6 hours, usable only for `/pe/jobs/{id}/*`), stored on S: — if it leaks it can only report events of that job.

## 3. Utility VM API

| Method | Path | Purpose |
|---|---|---|
| GET | `/utility/tasks?wait=55` | pull work (build_winpe, odj_provision, odj_reset, dism_service) |
| POST | `/utility/tasks/{id}/result` | submit a result (an ODJ blob is returned sealed to the backend's public key) |
| POST | `/utility/tasks/{id}/artifact` | upload a file (boot.wim etc.) |

## 4. Admin API (summary)

| Group | Endpoints |
|---|---|
| Hosts | `GET /hosts`, `GET /hosts/{id}`, `PATCH /hosts/{id}` (ring, channel, site), `POST /hosts/{id}/commands` |
| Jobs | `POST /jobs` (per machine), `GET /jobs?status=`, `GET /jobs/{id}` (+events, summary, logs), `POST /jobs/{id}/cancel` |
| Preview | `POST /jobs/preview` → latest preflight + ETA + risk (creates no job) |
| Images | `GET /channels`, `POST /channels`, `GET /images`, `POST /images/build`, `POST /images/{id}/approve`, `.../reject`, `.../pin`, `DELETE /images/{id}` |
| Capacity | `GET /capacity/server`, `GET /capacity/hosts?image_id=` |
| Builder | `GET /builder/vms`, `POST /builder/vms/{id}/start\|stop\|snapshot\|revert`, `GET /builder/vms/{id}/console` (Guacamole token) |
| WinPE / drivers | `GET/POST /winpe`, `POST /winpe/{id}/approve`, `GET/POST /driver-packs`, `POST /driver-packs/{id}/promote` |
| Disk profiles | CRUD `/disk-profiles` |
| Rollouts | `POST /rollouts`, `GET /rollouts/{id}`, `POST /rollouts/{id}/pause\|resume\|advance\|cancel`, `POST /rollouts/fleet-rollback` |
| Kill switch | `GET /kill-switches`, `PUT /kill-switches/{scope}/{id}`, `DELETE ...` (two people for global) |
| Agent releases | `GET /agent-releases`, `POST /agent-releases` (from CI), `POST /agent-releases/{id}/promote\|pause\|revoke` |
| Admin | `/sites`, `/users`, `/roles`, `/enrollment-tokens`, `/keysets` |
| Approvals | `GET /approvals`, `POST /approvals/{id}/approve` (for two-person actions) |
| Audit | `GET /audit?actor=&object=` |
| Ops | `GET /health`, `GET /metrics` |

## 5. Versioning

- A breaking change → `/v2`, with `/v1` supported alongside for at least 2 server minor versions
- Agents older than N-2 → `426` on every endpoint except `heartbeat`, `releases`, `updates`
