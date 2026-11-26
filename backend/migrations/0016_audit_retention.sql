-- Activity log retention: rows older than the retention period are deleted automatically.
--
-- The application role still cannot UPDATE or DELETE audit_log (0001). The only way a row
-- leaves the table is this function, which runs with the owner's rights and refuses to
-- delete anything younger than 30 days — so a stolen application login cannot erase its
-- own recent tracks. It takes the same advisory lock as audit.Write and never removes the
-- newest row, which anchors the hash chain of everything written afterwards.
CREATE OR REPLACE FUNCTION audit_prune(keep interval) RETURNS bigint
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE n bigint;
BEGIN
  IF keep < interval '30 days' THEN
    RAISE EXCEPTION 'audit_prune: the retention period must be at least 30 days';
  END IF;
  PERFORM pg_advisory_xact_lock(727002);
  DELETE FROM audit_log WHERE at < now() - keep AND id < (SELECT max(id) FROM audit_log);
  GET DIAGNOSTICS n = ROW_COUNT;
  RETURN n;
END $$;
REVOKE ALL ON FUNCTION audit_prune(interval) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION audit_prune(interval) TO swim_app;
