-- 0005 · reimage jobs (WinPE engine): progress, disk profile, WinPE reporting token, result log

ALTER TABLE deployment_jobs
  ADD COLUMN phase text,
  ADD COLUMN progress int,
  ADD COLUMN message text,
  ADD COLUMN disk jsonb,
  ADD COLUMN pe_token_hash bytea,
  ADD COLUMN boot_wim text,          -- per-job WinPE with this machine's storage/network drivers
  ADD COLUMN result jsonb,
  ADD COLUMN log text,
  ADD COLUMN updated_at timestamptz NOT NULL DEFAULT now();

ALTER TABLE job_events ALTER COLUMN at SET DEFAULT now();
