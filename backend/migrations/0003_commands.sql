-- 0003 · command channel (spec 03 §4 allowlist), script library (2-person), host retirement

ALTER TABLE hosts ADD COLUMN retired_at timestamptz, ADD COLUMN retired_reason text;

-- PowerShell scripts an admin may push to machines. Body is immutable once
-- created (sha256 pins it); a second admin must approve before it can run.
CREATE TABLE scripts (
  id text PRIMARY KEY,
  name text NOT NULL,
  description text,
  body text NOT NULL CHECK (length(body) <= 65536),
  sha256 text NOT NULL,
  timeout_s int NOT NULL DEFAULT 600 CHECK (timeout_s BETWEEN 10 AND 3600),
  status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','approved','retired')),
  created_by text NOT NULL REFERENCES users, approved_by text REFERENCES users,
  created_at timestamptz NOT NULL DEFAULT now(), approved_at timestamptz,
  CHECK (approved_by IS NULL OR approved_by <> created_by)
);

-- One row per command sent to one machine. `envelope` is the Ed25519-signed
-- payload the agent verifies (bound to host_id + SMBIOS UUID + expiry).
CREATE TABLE agent_commands (
  id text PRIMARY KEY,
  host_id text NOT NULL REFERENCES hosts,
  type text NOT NULL,
  args jsonb NOT NULL DEFAULT '{}',
  envelope jsonb NOT NULL,
  status text NOT NULL DEFAULT 'queued'
    CHECK (status IN ('queued','delivered','running','done','failed','expired','cancelled')),
  reason text,
  created_by text NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  expires_at timestamptz NOT NULL,
  delivered_at timestamptz, finished_at timestamptz,
  result jsonb
);
CREATE INDEX agent_commands_open ON agent_commands(host_id, created_at) WHERE status IN ('queued','delivered','running');
CREATE INDEX agent_commands_host ON agent_commands(host_id, created_at DESC);
