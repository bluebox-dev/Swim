-- Desired machine policy per host (agent applies it with a signed set_policy command and
-- reports the measured state in inventory.policy). {"install_lock": bool}
ALTER TABLE hosts ADD COLUMN IF NOT EXISTS policy jsonb NOT NULL DEFAULT '{}'::jsonb;
