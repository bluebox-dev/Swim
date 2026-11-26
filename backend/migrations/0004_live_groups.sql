-- 0004 · full inventory, site management, group (batch) commands

ALTER TABLE hosts ADD COLUMN inventory jsonb, ADD COLUMN inventory_at timestamptz;
ALTER TABLE sites ADD COLUMN description text, ADD COLUMN created_at timestamptz NOT NULL DEFAULT now();

-- One admin action fanned out to many machines (a site or a selection).
-- Each machine still gets its own command, signed for that machine only.
CREATE TABLE command_batches (
  id text PRIMARY KEY,
  type text NOT NULL,
  args jsonb NOT NULL DEFAULT '{}',
  reason text,
  target jsonb NOT NULL,            -- {"site_id": …} or {"host_ids": […]}
  total int NOT NULL,
  skipped jsonb NOT NULL DEFAULT '[]',
  created_by text NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now()
);
ALTER TABLE agent_commands ADD COLUMN batch_id text REFERENCES command_batches;
CREATE INDEX agent_commands_batch ON agent_commands(batch_id) WHERE batch_id IS NOT NULL;
