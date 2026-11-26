-- A removed machine that is enrolled again with an enrollment token becomes a NEW host
-- (clean history). The old row keeps its hardware id here instead of in smbios_uuid,
-- so the identity index no longer matches it. history_since hides older commands and
-- jobs for a host whose id had to be kept (its agent already knows that id).
ALTER TABLE hosts
  ADD COLUMN IF NOT EXISTS removed_smbios_uuid uuid,
  ADD COLUMN IF NOT EXISTS history_since timestamptz;
