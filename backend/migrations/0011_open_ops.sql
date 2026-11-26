-- Single-admin, "allow everything" operation: no script approval and no emergency stop.
-- In exchange every command keeps its full script text and result (audit log + agent_commands).
ALTER TABLE agent_commands ADD COLUMN IF NOT EXISTS cleared_at timestamptz;  -- hidden from the host's history (audit keeps it)
UPDATE scripts SET status='approved', approved_by=COALESCE(approved_by, created_by), approved_at=COALESCE(approved_at, now())
  WHERE status='pending';
UPDATE kill_switches SET active=false, cleared_by='system:feature-removed', cleared_at=now() WHERE active;
