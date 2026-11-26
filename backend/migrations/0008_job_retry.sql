-- Retry of a job that failed after the point of no return (machine waits in WinPE):
-- the admin's plan (image + refreshed WinPE) that WinPE fetches from /v1/pe/jobs/{id}/next.
ALTER TABLE deployment_jobs ADD COLUMN IF NOT EXISTS retry jsonb;
