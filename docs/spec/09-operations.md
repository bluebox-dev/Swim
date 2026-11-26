# 09 · Operations — Logs, Monitoring, Backup/DR, Fallback Paths, Lifecycle

**English** · [ไทย](09-operations.th.md)

## 1. Logs & telemetry (D-11)

### Severity

| Level | Meaning | Examples |
|---|---|---|
| `FATAL` | the job ended `failed` or `rolled_back` | apply failed 3 times, the new Windows does not boot |
| `ERROR` | the job succeeded but something needs fixing | domain join failed, BitLocker escrow failed, an important device has an error |
| `WARN` | works, but worth knowing | not activated within 24 hours, no matching driver pack |
| `INFO` | normal | state changes, step durations |
| `DEBUG` | detail | full command output — kept on the machine only |

### What is sent to the server

| Data | When | Size |
|---|---|---|
| **Job summary** (final state, step durations, error code, preflight, model / BIOS) | **every job** | ~1 KB |
| State-change events | every time (through the outbox) | small |
| **Full log bundle** (`swim-pe.log`, `state.json` history, `dism.log`, `Panther\setupact.log` / `setuperr.log`, `bcdedit /enum all /v`, `Get-Disk` / `Get-Partition`, System / Application event logs of 24 hours) | automatically on `FATAL` / `ERROR` | 1–50 MB (zip) |
| Log bundle on request | the `collect_logs` command | |

- WinPE always writes its log to `S:\swim\logs\<job_id>\` → with a network WinPE sends the summary at once; without one the new agent sends it after boot
- A machine that can send nothing → the server sets `suspected_failure` ([05 §10](05-deployment-flow.md)); a technician collects logs from S: with the SWIM USB or `swim logs --export` after repair
- Retention on the server: summaries 2 years, log bundles 180 days

### Error codes

Format `SWIM-<phase>-<nnn>`, e.g. `SWIM-PE-012` (OS partition not found by GUID) — the full list lives in `pkg/state/errors.go` plus a docs page linked from the dashboard with remedies.

## 2. Monitoring & alerting

- The backend exposes `/metrics` (Prometheus) — Prometheus + Grafana are an optional part of the compose stack
- Alerts go out by webhook (Teams / Line / Slack / email)

| Alert | Condition |
|---|---|
| Circuit breaker / kill switch tripped | immediately |
| New `suspected_failure` | immediately (summarised every 15 minutes when several machines) |
| Server storage | ≥ 80% (warning), ≥ 90% (critical) |
| Certificate / key close to expiry | ≤ 30 days |
| Backup failed / no new backup for > 26 hours | immediately |
| Agent version lag | > 5% of the fleet below N-1 for more than 14 days |
| Silent machines | > 30 days (weekly report) |
| Channel close to end of support | 180 / 90 / 30 days |
| Utility VM / Test Runner VM not responding | > 10 minutes |

## 3. Backup & disaster recovery

| Data | Method | RPO | Copies kept |
|---|---|---|---|
| PostgreSQL | pgBackRest: weekly full + daily differential + continuous WAL archive | 15 minutes | NAS + offsite (encrypted) |
| Images / WinPE / driver packs (published + N-1) | daily rsync + SHA256 check | 24 hours | NAS |
| Golden Master qcow2 + latest snapshot | backed up after every build | per build | NAS |
| Config, compose, nginx, secrets | git (without secrets) + an encrypted backup of the secrets | per change | NAS + offsite |
| Keys ([08 §3](08-security.md)) | per the key ceremony — not in normal backups | — | safe |

- **RTO target 4 hours** — a runbook rebuilds the server from backup (Ubuntu + compose + DB restore + storage rsync)
- A restore is rehearsed every quarter on a separate VM and the result is recorded

### What happens to clients when the server is down

| Situation | Result |
|---|---|
| Jobs past the PONR | run to the end (WinPE does not depend on the server) — results follow later |
| Jobs `staged` and waiting for a go-token | wait until it arrives, or the manifest expires → `cancelled` |
| Local deploy with a standing manifest | works until the manifest expires (7 days) — no domain join |
| Agent updates | paused, no effect on operation |
| Outbox | accumulates locally, sent when the server returns (no data loss) |

## 4. Fallback paths (D-12)

### 4.1 A technician installs Windows from USB (primary)

```
1. install Windows from USB as usual (edition matching the machine's licence)
2. install the agent:  msiexec /i swim-agent.msi ENROLL_TOKEN=<site token> SERVER_URL=https://swim.corp.local /qn
3. the agent enrolls → the server matches the existing host by smbios_uuid + serial → history continues
4. the agent creates S: again (a whole-disk install removed it) → stages the current image
5. (recommended) deploy the standard image on top once more so the machine matches the standard
```
- The dashboard labels machines that re-enrolled this way `manual_install` until a standard image deploy succeeds

### 4.2 SWIM USB (optional, Phase 4)

- A USB stick with the published `boot.wim` + `swim-pe` in `usb` mode + root CA / keyset
- After booting: connect the network (LAN) → the technician types an enrollment token or scans a QR code → manifest and image are fetched from the server → the **whole disk** is wiped per the disk profile (new ESP / MSR / OS / S: / WinRE) → then the normal flow from apply onwards
- Can also collect logs from S: of a broken machine (`swim-pe collect-logs → USB`)

## 5. Other lifecycles

### Windows version lifecycle
- `channels.eol_date` follows Microsoft's support table — warnings at 180 / 90 / 30 days
- 90 days before the end: new rollouts to that channel are blocked (except rollback) and the number of machines still on it is shown

### Driver packs
- Keyed by `(vendor, match_key, os_build, arch)`; updated when the vendor releases a new one or a problem appears
- A new driver pack must pass a canary on that model before broad use (same rollout mechanism, tied to an image)
- Match key: Dell = `SystemSKU`, Lenovo = first 4 characters of `Win32_ComputerSystemProduct.Name`, HP = `Win32_BaseBoard.Product`, others = `Manufacturer + Model`

### WinPE
- Rebuilt whenever: a new ADK appears, a new hardware model needs storage / NIC drivers, Secure Boot or the boot chain changes
- Goes through the test pipeline + canary like an image

## 6. Runbooks to write (docs/runbooks/)

| Runbook | When |
|---|---|
| RB-01 Machine stuck in `suspected_failure` | every alert |
| RB-02 USB install + re-enroll | a `failed` machine |
| RB-03 Circuit breaker tripped — analyse and decide resume / rollback | breaker |
| RB-04 Fleet rollback | an image causes problems after release |
| RB-05 Widespread agent update failure | agent breaker |
| RB-06 Restore the server from backup | DR |
| RB-07 Key rotation / key compromise | per schedule / incident |
| RB-08 Add a new hardware model (drivers, WinPE, canary) | new machines purchased |
| RB-09 Monthly image build | every month |
