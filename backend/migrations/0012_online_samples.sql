-- How many machines were online over time (dashboard sparkline). One row every 5 minutes,
-- written by the backend; rows older than 30 days are pruned by the same loop.
CREATE TABLE IF NOT EXISTS online_samples (
  at timestamptz PRIMARY KEY DEFAULT now(),
  online int NOT NULL,
  total int NOT NULL
);
GRANT SELECT, INSERT, DELETE ON online_samples TO swim_app;
