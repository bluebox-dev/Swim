# 04 · Agent Self-Update (Continuous Patching) — D-17

**English** · [ไทย](04-agent-updates.th.md)

> Goal: **every** agent receives patches continuously with nobody touching the machine, a broken update **rolls itself back**, and a release can **stop itself** before it spreads across the organisation.

## 1. SLOs

| Indicator | Target |
|---|---|
| Online machines on the latest version after `broad` starts | ≥ 95% within 7 days |
| Security fix (`priority=critical`) | ≥ 95% within 48 hours |
| A failed update recovers by itself (auto-rollback) | 100% of cases where the process can still start |
| Machines that "fall out of control" because of an update | 0 |

## 2. Updatable components

| Piece | Updated by | Frequency |
|---|---|---|
| `swim-agent.exe` + `swim.exe` + `swim-notify.exe` (agent bundle) | `swim-svc` | often (weekly patches / on demand) |
| `swim-svc.exe` (supervisor) | `swim-agent` | rarely |
| Configuration (not a binary) | `swim-agent` (hot reload) | any time |
| `boot.wim` / `swim-pe` | the image pipeline ([02 §9](02-image-pipeline.md)) | per WinPE cycle |

**Iron rule: never update `swim-svc` and the agent bundle in the same round** — one known-good piece must always be there to rescue the other.

## 3. Release artifact

```
agent-bundle-1.5.0-amd64.zip
├── swim-agent.exe, swim.exe, swim-notify.exe   # Authenticode-signed
└── release.json                                  # Ed25519-signed (release key)
    { "component":"agent", "version":"1.5.0", "arch":"amd64",
      "min_svc_version":"1.1.0", "state_schema":3, "outbox_schema":5,
      "files":[{"name":"swim-agent.exe","sha256":"..."}], "priority":"normal",
      "released_at":"2026-11-23T10:00:00Z" }
```

**Three checks** before switching: (1) the SHA256 of the zip from the server, (2) the Ed25519 signature of `release.json`, (3) the Authenticode signature of every `.exe`, which must be SWIM's publisher.
Where the organisation uses WDAC / AppLocker → allow by **publisher (signer)**, not by hash, so the rule does not change with every version.

## 4. On-disk layout (slots)

```
C:\Program Files\SWIM\versions\
├── agent-1.4.2\      ← PreviousVersion (kept for rollback)
├── agent-1.5.0\      ← ActiveVersion
├── svc-1.1.0\        ← the service ImagePath points here
└── svc-1.0.3\        ← previous svc (used by the rescue task)

HKLM\SOFTWARE\SWIM\Update
  ActiveVersion    = 1.5.0
  PreviousVersion  = 1.4.2
  PendingVersion   = (empty | 1.5.1)
  TrialStartedAt   = 2026-11-23T10:05:00Z
  TrialCrashCount  = 0
  BlockedVersions  = 1.5.1;...        # versions that failed on this machine; not retried until the server says so
```

At most 2 versions per piece are kept (active + previous); older ones are deleted after commit.

## 5. Agent bundle update flow

```
heartbeat ──► the server answers agent_target {version, url, sha256, release_sig, not_before}
   │
   ├─ 1. check that updating is allowed now (§6); if not → defer, report update_deferred
   ├─ 2. download to versions\agent-1.5.0.tmp\ (resumable, same netcond as images but **allowed on VPN / metered** because it is ~15 MB)
   ├─ 3. the three checks (§3) → rename to agent-1.5.0\
   ├─ 4. set PendingVersion=1.5.0 and ask svc over the pipe to switch
   │
swim-svc:
   ├─ 5. sends "drain" to the old agent → it stops taking work, flushes the outbox, closes the DB (60 s timeout)
   ├─ 6. starts agent-1.5.0 in TRIAL mode (TrialStartedAt=now)
   ├─ 7. the new agent runs its self-test (§7) → sends "healthy" to svc + a heartbeat with version=1.5.0 to the server
   │
   ├─ passes within 10 minutes ──► COMMIT: Active=1.5.0, Previous=1.4.2, delete older versions → report agent_updated
   └─ fails (≥ 3 crashes in 10 minutes / no "healthy" / server heartbeat fails)
          ──► ROLLBACK: start 1.4.2, add 1.5.0 to BlockedVersions
          ──► agent 1.4.2 reports agent_update_failed + the crash log of 1.5.0
```

## 6. When updating is forbidden

| Job state | Update allowed? |
|---|---|
| no job / `staging` / `staged` | ✅ (the download pauses and resumes after the switch) |
| `waiting` (for a time slot / user deferral) | ✅ |
| `handoff` → `health_check` (BCD changes, inside WinPE, first boot) | ❌ |
| The user set a maintenance window | always allowed (no reboot, no user impact) |

## 7. Self-test of the new agent (all must pass)

1. Read the configuration and verify its signature
2. Open the outbox DB and run its migrations
3. Read and write `state.json` (current schema)
4. An mTLS heartbeat reaches the server (when the network works — when offline this passes conditionally and a heartbeat must succeed within 24 hours of coming back online, otherwise rollback)
5. Open the named pipe
6. Mount S: and read `S:\swim\` (when S: exists)

### Migration rules (so rollback really works)

- Outbox / DB use **expand → contract**: version N may add columns or tables but must not remove or redefine anything N-1 uses — removal is allowed in N+1
- `state.json` writes a `schema_version`; the agent must always be able to read schema N-1
- Never write registry values or files in a format N-1 cannot read

## 8. Compatibility matrix

| Between | Rule |
|---|---|
| Server ↔ Agent | The server supports agents back to **N-2 minor** (server 1.7 supports agents 1.5–1.7). Older → HTTP `426 Upgrade Required` + update information (the agent can always call the update endpoint) |
| Agent ↔ swim-svc | `release.json.min_svc_version` — if svc is older, update svc first (do not switch the agent) |
| Agent ↔ swim-pe | `state_schema` must be in the `supported_state_schemas` of the published `boot.wim` — the server does not publish an agent that would break this |
| Agent ↔ Manifest | the agent declares `manifest_schemas: [1,2]` in its heartbeat → the server issues manifests in a schema the agent supports |

## 9. Updating swim-svc

```
the agent (committed and healthy) receives svc_target
 → download + three checks → versions\svc-1.2.0\
 → change the service ImagePath through SCM → set service recovery = restart 3 times
 → restart the service (the new svc starts the same ActiveVersion agent)
 → the new svc must report healthy within 10 minutes
```

**Last safety net — `SWIMRescue` (scheduled task, SYSTEM, at startup + every 6 hours)**
- Runs `svc-<previous>\swim-svc.exe rescue`
- If the `SWIMSupervisor` service is not running, or crash-loops more than 3 times an hour → points ImagePath back to the previous svc, restarts it, writes an event (`Application`, source `SWIM`)
- If both versions are broken → the last resort is **re-pushing the MSI by GPO / Intune** (the bootstrap channel — [03 §2](03-agent.md)) — the MSI must be idempotent and keep the identity (it does not delete `C:\ProgramData\SWIM\`)

## 10. Server-side rollout

### Release states

```
draft ─CI build+sign─► testing ─auto tests pass─► canary ─► pilot ─► broad ─► published(100%)
                                                     └────── paused ◄── breaker / admin
                                         revoked ◄── admin (orders every machine back to a given version)
```

### Machine selection (deterministic)

```
bucket(host) = uint(sha256(host_id || release_id)[0:2]) % 100
a host gets the release when: host.ring ≤ release.stage  and  bucket(host) < release.percent
```

| Stage | Group | % | Minimum soak |
|---|---|---|---|
| `testing` | Test Runner VMs + CI VMs | — | automated tests pass |
| `canary` | ring `canary` (~10–20 IT machines covering every main model) | 100% of the ring | 24 hours |
| `pilot` | ring `pilot` + 10% of broad | 10 | 48 hours |
| `broad` | everyone, stepping 25 → 50 → 100 | 25/50/100 | 24 hours per step |

`priority=critical` (security fix): canary 2 hours → broad 100% at once, but the **breaker stays active**.

### Health gate and circuit breaker (automatic)

Machines on the new version are compared with machines on the previous version over the same period:

| Metric | Rollout stops (`paused`) when |
|---|---|
| `agent_update_failed` rate | > 2% of machines that tried (minimum 3 machines) |
| Machines silent after the update (no heartbeat > 30 minutes although online before) | > 0.5% |
| Crash rate per machine per day | > 2× the previous version |
| Job failure rate (deploy) of machines on the new version | > previous version + 5 percentage points |

When the breaker trips: the rollout is `paused` and `release_manager` is notified — machines already updated stay there (no automatic revoke) until a person decides.

### Revoke and downgrade

- The agent **never downgrades by itself** (anti-rollback) unless it receives a signed **rollback directive** `{revoke: "1.5.0", target: "1.4.2"}`
- A `revoked` release is no longer distributed, and machines on it are switched to the target

## 11. Machines offline for a long time

| Offline for | What happens on return |
|---|---|
| < 90 days | a normal heartbeat → the latest agent_target at once (soak is skipped because the release already passed broad) |
| 90–180 days (client certificate expired) | calls `/v1/agent/renew` with a signature from the old key (proof of possession) → gets a new certificate |
| > 180 days | must re-enroll with a new token (technician or GPO) |
| Version older than N-2 | gets `426` → updates before anything else; several versions behind may jump straight to the latest unless the release says `requires_intermediate` |

## 12. Configuration updates (not binaries)

- Sent through the heartbeat: `{config_version, config, sig}` — e.g. heartbeat interval, bandwidth, netcond policy
- The agent verifies the signature + validates the schema → applies at once; if the agent crashes within 10 minutes of a new config → it returns to `last_good_config`

## 13. Release pipeline (CI)

```
git tag agent/v1.5.0
 → go test ./... + vet + race
 → build (GOOS=windows GOARCH=amd64/arm64, -trimpath, reproducible)
 → Authenticode sign (signtool + HSM / USB token of the code-signing certificate)
 → create release.json + Ed25519 sign (release key — 08-security)
 → upload → backend: agent_releases(status=testing)
 → Test Runner VM: update from the previous version + from N-2 + rollback test (force the self-test to fail → must return to the old version)
 → pass → canary (a person must press it — RBAC `release_manager`, not the person who tagged)
```

## 14. Required dashboards

- Share of agent / svc versions across the fleet and per site
- Current rollout state + health-gate metrics in real time
- Machines with a non-empty `BlockedVersions` / stuck on an old version > 14 days / offline > 30 days
