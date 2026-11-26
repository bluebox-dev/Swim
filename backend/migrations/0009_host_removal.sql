-- Removing a machine uninstalls its agent when it is online. When it is offline the
-- token hash is kept as a tombstone: if that agent comes back, the machine reappears
-- in "pending review" instead of polling with 401 forever.
ALTER TABLE hosts
  ADD COLUMN IF NOT EXISTS removed_token_hash bytea,   -- tombstone of a removed machine's host token
  ADD COLUMN IF NOT EXISTS removing_at timestamptz,    -- removal asked; waiting for the agent to uninstall itself
  ADD COLUMN IF NOT EXISTS review_at timestamptz,      -- agent returned after removal; admin must accept or uninstall
  ADD COLUMN IF NOT EXISTS deleted_at timestamptz;     -- deleted by an admin but kept as a tombstone (hidden from lists)
CREATE INDEX IF NOT EXISTS hosts_removed_token ON hosts(removed_token_hash) WHERE removed_token_hash IS NOT NULL;
