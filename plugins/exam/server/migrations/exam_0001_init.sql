-- Exam mode: an optional module with its own tables. Nothing else in SWIM reads them, and
-- dropping the three tables removes the module's data without touching machines or installs.

-- named set-ups an admin saved to use again ("Midterm, web exam", "Programming lab")
CREATE TABLE IF NOT EXISTS exam_profiles (
  id          text PRIMARY KEY,
  name        text NOT NULL UNIQUE,
  config      jsonb NOT NULL,
  created_at  timestamptz NOT NULL DEFAULT now(),
  created_by  text NOT NULL DEFAULT ''
);

-- the exam set-up each machine should be in (desired state; the agent reports the measured one)
CREATE TABLE IF NOT EXISTS exam_hosts (
  host_id      text PRIMARY KEY REFERENCES hosts(id) ON DELETE CASCADE,
  enabled      boolean NOT NULL,
  config       jsonb NOT NULL,
  profile_name text NOT NULL DEFAULT '',
  updated_at   timestamptz NOT NULL DEFAULT now(),
  updated_by   text NOT NULL DEFAULT ''
);

-- what the watcher on a machine found, with the screenshot file kept beside the database
CREATE TABLE IF NOT EXISTS exam_events (
  id          text PRIMARY KEY,
  host_id     text NOT NULL REFERENCES hosts(id) ON DELETE CASCADE,
  at          timestamptz NOT NULL,              -- the machine's clock when it happened
  received_at timestamptz NOT NULL DEFAULT now(),
  kind        text NOT NULL,
  detail      jsonb NOT NULL DEFAULT '{}'::jsonb,
  shot_file   text,                              -- relative to the evidence directory
  shot_sha256 text,                              -- of the file as received: shows it was not changed later
  shot_bytes  integer
);
CREATE INDEX IF NOT EXISTS exam_events_host_at ON exam_events(host_id, at DESC);
CREATE INDEX IF NOT EXISTS exam_events_at ON exam_events(at DESC);

GRANT SELECT, INSERT, UPDATE, DELETE ON exam_profiles, exam_hosts, exam_events TO swim_app;
