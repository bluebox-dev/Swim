package builder

// Live progress of the running task, so the Builder page can show what is happening
// and roughly how long is left instead of a spinner that looks stuck.
//
// While a task runs, server_tasks.result = {"progress": {...}}:
//
//	stage      preflight | sysprep | decompress | test | capture | hash
//	percent    0–100 for the whole task
//	message    what is being done right now
//	elapsed_s  seconds since the task started
//	eta_s      estimated seconds left (null while unknown)
//	eta_at     estimated finish time, RFC 3339 (null while unknown)
//	started_at when the task started, RFC 3339
//	updated_at RFC 3339
//
// When the task ends, result is replaced by its final result (or {"error": …}).

import (
	"context"
	"encoding/json"
	"time"
)

type taskProgress struct {
	id, kind  string
	started   time.Time
	lastWrite time.Time
	lastStage string
	lastPct   float64
}

// Stage bands of a capture, sized by how long each stage takes on the lab server
// (reseal ~3 min, CompactOS rewrite ~20 min, LZX capture ~10 min, hash ~2 min).
var (
	stageStart = map[string]float64{"preflight": 0, "sysprep": 2, "decompress": 10, "test": 62, "capture": 64, "hash": 95}
	stageEnd   = map[string]float64{"preflight": 2, "sysprep": 10, "decompress": 62, "test": 64, "capture": 95, "hash": 100}
)

// stagePct maps a fraction (0–1) of one stage to the percentage of the whole task.
func stagePct(stage string, frac float64) float64 {
	frac = max(0, min(frac, 1))
	return stageStart[stage] + (stageEnd[stage]-stageStart[stage])*frac
}

// typical total duration, used for the estimate until enough of the task has run
const typicalCapture = 35 * time.Minute

func (w *Worker) progress(ctx context.Context, stage string, pct float64, msg string) {
	p := w.cur
	if p == nil {
		return
	}
	pct = max(pct, p.lastPct) // never goes backwards
	now := time.Now()
	if stage == p.lastStage && now.Sub(p.lastWrite) < 3*time.Second && pct-p.lastPct < 1 {
		return
	}
	elapsed := now.Sub(p.started)
	var eta any
	switch {
	case pct >= 100:
		eta = 0
	case pct >= 15: // enough history: extrapolate from this run
		eta = int(elapsed.Seconds() * (100 - pct) / pct)
	case p.kind == "capture_image":
		eta = int(max(typicalCapture-elapsed, time.Minute).Seconds())
	}
	var etaAt any // estimated finish as a clock time — what people actually want to read
	if s, ok := eta.(int); ok {
		etaAt = now.Add(time.Duration(s) * time.Second).UTC().Format(time.RFC3339)
	}
	b, _ := json.Marshal(map[string]any{"progress": map[string]any{
		"stage": stage, "percent": int(pct), "message": msg, "elapsed_s": int(elapsed.Seconds()), "eta_s": eta,
		"eta_at": etaAt, "started_at": p.started.UTC().Format(time.RFC3339), "updated_at": now.UTC().Format(time.RFC3339)}})
	if _, err := w.Pool.Exec(ctx, `UPDATE server_tasks SET result=$2::jsonb WHERE id=$1 AND status='running'`, p.id, string(b)); err == nil {
		p.lastWrite, p.lastStage, p.lastPct = now, stage, pct
	}
}
