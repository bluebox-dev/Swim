-- Scheduled work (ตารางงาน): one row per schedule, one row per run of an occurrence.
CREATE TABLE IF NOT EXISTS schedules (
  id text PRIMARY KEY,
  kind text NOT NULL CHECK (kind IN ('deploy','reboot','wake','update_agent','shutdown')),
  title text NOT NULL,
  start_at timestamptz NOT NULL,
  repeat jsonb NOT NULL DEFAULT '{"freq":"none"}',
  targets jsonb NOT NULL,                 -- {"host_ids":[...]} or {"site_id":"..."}
  args jsonb NOT NULL DEFAULT '{}',       -- never holds a password
  secret bytea,                           -- deploy admin password, AES-GCM with the server's schedule key
  skips jsonb NOT NULL DEFAULT '[]',      -- occurrence keys (unix seconds) skipped one by one
  created_by text NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  cancelled_at timestamptz,
  cancelled_by text
);
CREATE TABLE IF NOT EXISTS schedule_runs (
  id text PRIMARY KEY,
  schedule_id text NOT NULL REFERENCES schedules,
  occurrence timestamptz NOT NULL,
  status text NOT NULL CHECK (status IN ('running','done','failed')),
  started_at timestamptz NOT NULL DEFAULT now(),
  finished_at timestamptz,
  result jsonb,
  UNIQUE (schedule_id, occurrence)
);
GRANT SELECT, INSERT, UPDATE, DELETE ON schedules, schedule_runs TO swim_app;
