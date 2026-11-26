# 07 · Fleet Safety — Waves, Concurrency, Kill Switch, Circuit Breaker, Fleet Rollback

**English** · [ไทย](07-fleet-safety.th.md)

## 1. Image rollouts

Releasing an image to many machines always goes through a **rollout** (a single-machine order is a rollout of size 1).

```
Rollout ro_77: win11-x64-stable v1.6 → target: site BKK, OU Finance (412 machines)
 ├─ wave canary : 3 machines per model (ring=canary)  gate: 100% completed, 24 h soak
 ├─ wave pilot  : 10%                                  gate: success ≥ 95%, 24 h soak
 ├─ wave 50     : 50%                                  gate: success ≥ 97%
 └─ wave 100    : 100%
```

| Rule | Default |
|---|---|
| A machine `model` with no machine that passed the canary wave on this image | **not released in the next wave** (stays `held: untested_model`) |
| Advancing a wave | automatic once the gate passes (can be set to manual) |
| A `pending` image | usable in the canary wave only; later waves need `published` |

## 2. Concurrency limits

| Level | Default | Counts |
|---|---|---|
| System-wide | 50 | jobs in `handoff` → `health_check` |
| Per site | 10 | the same |
| Per site — download | 20 | jobs in `staging` downloading from the server / WAN (site cache and peers are not counted) |
| Per rollout | configurable | |

- The agent requests a slot when leaving `waiting` (go-token, §4) — no slot = keep waiting (5–15 min backoff + jitter)
- A slot has a 2-hour lease renewed by heartbeats and events; a silent machine loses its lease at `suspected_failure`

## 3. Maintenance windows

- Configured per site / OU, e.g. `Mon–Fri 20:00–06:00, Sat–Sun all day`
- A deploy may start (handoff) only inside the window; if ETA P90 runs past the end of the window → it waits for the next one
- Staging (download) is always allowed, subject to the bandwidth policy

## 4. Kill switch

### Levels

| Level | Effect |
|---|---|
| **Global** | stops every rollout + every job that has not reached the PONR + stops distributing agent updates |
| Channel | stops only images of that channel |
| Site | stops only machines in the site |
| Rollout | stops one rollout (= pause) |

### Mechanisms (work even when some machines are offline)

1. **Heartbeat** — every heartbeat answers `halt: {global, channels[], sites[]}` → the agent cancels jobs that have not reached `handoff` at once (state `cancelled`)
2. **Go-token** — before `handoff` the agent must call `POST /v1/agent/jobs/{id}/go` → the server checks the kill switch + slot + window → issues a signed token valid 15 minutes
3. **WinPE check** — in `pe_verify`, with network, the server is asked again; kill switch on → `aborted`
4. **Manifest expiry** — the server stops issuing and renewing manifests the moment the kill switch is on → an offline machine holding a manifest can continue no later than `not_after`
5. Jobs that are **past the PONR** cannot be stopped; they run to the end (completed / rolled_back / failed) — the dashboard shows this number clearly when the button is pressed

### UX

- A red button on every page (RBAC: `release_manager`, `admin`) — a reason must be typed; clearing a Global switch takes two people (one presses, one approves)
- After pressing, the console shows: "Stopped N jobs · M jobs past the PONR are continuing · K offline machines hold a manifest (expires within X hours)"

## 5. Circuit breaker (automatic stop)

Evaluated every minute per rollout and per (rollout, model):

| Condition | Action |
|---|---|
| The canary wave has ≥ 1 `failed` or `rolled_back` | pause the rollout |
| Other waves: (`failed` + `rolled_back`) ≥ 2 in the first 10 jobs, or > 5% (when n ≥ 20) | pause the rollout |
| One model: ≥ 2 consecutive failures | hold only that model (`held: model_breaker`) |
| `suspected_failure` > 3% of running jobs | pause the rollout + alert (may be a network or server problem) |
| System-wide: > 10 failed jobs in 30 minutes across several rollouts | **automatic Global kill switch** |

When the breaker trips → `release_manager` is notified (email / Line / Teams webhook) with a summary of the most frequent error codes and links to the logs.

## 6. Fleet rollback

Scenario: image v1.6 went out to 200 machines and a problem shows up in use.

```
Release manager → "Rollback v1.6 → v1.5" (a scope is required: all / site / model / list)
  → creates a rollout of type rollback (image = N-1; the canary wave may be skipped because v1.5 already passed)
  → normal concurrency + maintenance windows + user notification
  → machines with v1.5 on S: → no network needed (~25 minutes per machine)
  → machines without it (small S: / deleted) → download from the site cache / server
  → channel: v1.5 becomes published again, v1.6 = withdrawn (files kept for analysis, deployment forbidden)
  → the channel's new N-1 = the release before v1.5 (if still present) — warn if there is none
```

- Dashboard before pressing: "200 machines · 188 have v1.5 on S: · 12 must download (108 GB) · ETA by window: 2 nights"
- User data on C: is lost once more (D-05) — the user notice must say so clearly

## 7. Required screens

| Screen | Content |
|---|---|
| Rollout | waves, gates, per-machine state, breaker, pause / resume / advance buttons |
| Fleet health | running jobs by state, `suspected_failure`, slots in use per site |
| Kill switch | current state at every level + history (who / when / why) |
| Model matrix | model × image table: passed / failed / untested |
