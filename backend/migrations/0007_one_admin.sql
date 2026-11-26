-- The two-person rule is now an app setting (SWIM_TWO_PERSON), not a database rule:
-- a single-admin install must be able to approve its own images and scripts.
DO $$
DECLARE c record;
BEGIN
  FOR c IN SELECT conrelid::regclass AS tbl, conname FROM pg_constraint
           WHERE contype = 'c' AND conrelid IN ('images'::regclass, 'scripts'::regclass)
             AND pg_get_constraintdef(oid) LIKE '%approved_by <> %'
  LOOP
    EXECUTE format('ALTER TABLE %s DROP CONSTRAINT %I', c.tbl, c.conname);
  END LOOP;
END $$;
