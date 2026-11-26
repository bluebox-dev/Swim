# 10 · Data Model (PostgreSQL 16)

**English** · [ไทย](10-data-model.th.md)

> IDs are prefixed ULIDs (`h_`, `img_`, `job_` …) stored as `text`; times are `timestamptz` (UTC)

```sql
-- ───────── organisation / users ─────────
CREATE TABLE sites (
  id text PRIMARY KEY, name text NOT NULL UNIQUE,
  subnets cidr[] NOT NULL DEFAULT '{}',
  cache_url text,                                   -- site cache (if any)
  max_concurrent_deploy int NOT NULL DEFAULT 10,
  max_concurrent_download int NOT NULL DEFAULT 20,
  maintenance_window jsonb                          -- {"tz":"Asia/Bangkok","rules":[...]}
);

CREATE TABLE users (
  id text PRIMARY KEY, subject text NOT NULL UNIQUE, -- from SSO
  display_name text, email text, disabled bool NOT NULL DEFAULT false
);
CREATE TABLE user_roles (
  user_id text NOT NULL REFERENCES users, role text NOT NULL,
  site_id text REFERENCES sites                     -- null = every site
);
-- PRIMARY KEY forces NOT NULL, so a unique index is used instead to allow site_id = null (every site)
CREATE UNIQUE INDEX user_roles_uniq ON user_roles(user_id, role, COALESCE(site_id, '*'));

-- ───────── Image ─────────
CREATE TABLE channels (
  id text PRIMARY KEY, name text NOT NULL UNIQUE,
  os text NOT NULL, release text NOT NULL, arch text NOT NULL, edition text NOT NULL,
  min_disk_gb int NOT NULL DEFAULT 128,
  eol_date date, retention jsonb NOT NULL DEFAULT '{"archive_days":90}'
);

CREATE TABLE images (
  id text PRIMARY KEY, channel_id text NOT NULL REFERENCES channels,
  version text NOT NULL,
  status text NOT NULL CHECK (status IN ('building','failed','testing','test_failed','pending','rejected',
                                         'published','superseded','withdrawn','archived','deleted')),
  file_path text, sha256 text, size_bytes bigint, expanded_bytes bigint,
  chunk_size int, chunks_sig text,
  delta_base_id text REFERENCES images,
  setup_media_path text,                            -- engine A
  built_by text REFERENCES users, approved_by text REFERENCES users,
  pinned bool NOT NULL DEFAULT false,
  created_at timestamptz NOT NULL DEFAULT now(), published_at timestamptz,
  UNIQUE (channel_id, version),
  CHECK (approved_by IS NULL OR approved_by <> built_by)
);
CREATE UNIQUE INDEX one_published_per_channel ON images(channel_id) WHERE status = 'published';

CREATE TABLE image_build_events (id bigserial PRIMARY KEY, image_id text REFERENCES images,
  step text, status text, message text, at timestamptz NOT NULL DEFAULT now());
CREATE TABLE image_tests (id bigserial PRIMARY KEY, image_id text REFERENCES images,
  test_id text NOT NULL, passed bool, duration_s int, log_path text, at timestamptz DEFAULT now());

CREATE TABLE winpe_releases (
  id text PRIMARY KEY, version text NOT NULL UNIQUE, status text NOT NULL,
  file_path text, sha256 text, sdi_sha256 text, adk_version text,
  supported_state_schemas int[] NOT NULL, storage_driver_keys text[] NOT NULL DEFAULT '{}',
  created_at timestamptz DEFAULT now()
);

CREATE TABLE driver_packs (
  id text PRIMARY KEY, vendor text NOT NULL, match_key text NOT NULL,
  os_build text NOT NULL, arch text NOT NULL, model_name text,
  zip_path text, sha256 text, status text NOT NULL DEFAULT 'pending'
    CHECK (status IN ('pending','canary','published','archived'))
);
CREATE UNIQUE INDEX one_published_driver_pack
  ON driver_packs(vendor, match_key, os_build, arch) WHERE status = 'published';

CREATE TABLE disk_profiles (
  id text PRIMARY KEY, name text NOT NULL UNIQUE,
  rules jsonb NOT NULL, is_default bool NOT NULL DEFAULT false
);

-- ───────── machines ─────────
CREATE TABLE hosts (
  id text PRIMARY KEY,
  smbios_uuid uuid, serial text, vendor text, model text, match_key text, bios_version text,
  weak_identity bool NOT NULL DEFAULT false,
  hostname text, site_id text REFERENCES sites, channel_id text REFERENCES channels,
  ring text NOT NULL DEFAULT 'broad' CHECK (ring IN ('test','canary','pilot','broad')),
  arch text, firmware text, secure_boot bool, tpm_version text,
  os_disk jsonb,                                    -- {unique_id, size_gb, bus, partitions:[{guid,type,size}]}
  stage_size_bytes bigint, cached_images text[] NOT NULL DEFAULT '{}',
  bitlocker jsonb, battery jsonb, license jsonb,
  agent_version text, svc_version text, blocked_agent_versions text[] DEFAULT '{}',
  x25519_pub bytea, cert_serial text, cert_not_after timestamptz,
  install_source text DEFAULT 'swim',               -- swim | manual_install
  last_seen timestamptz, created_at timestamptz DEFAULT now()
);
CREATE UNIQUE INDEX hosts_identity ON hosts(smbios_uuid, serial) WHERE NOT weak_identity;

CREATE TABLE enrollment_tokens (
  id text PRIMARY KEY, token_hash bytea NOT NULL, site_id text REFERENCES sites,
  max_uses int, uses int NOT NULL DEFAULT 0, expires_at timestamptz NOT NULL,
  created_by text REFERENCES users
);
CREATE TABLE reimage_tickets (
  ticket_hash bytea PRIMARY KEY, host_id text REFERENCES hosts, job_id text,
  expires_at timestamptz NOT NULL, used_at timestamptz
);

-- ───────── Rollout / Job ─────────
CREATE TABLE rollouts (
  id text PRIMARY KEY, kind text NOT NULL CHECK (kind IN ('deploy','rollback')),
  image_id text NOT NULL REFERENCES images, target jsonb NOT NULL,   -- {sites:[],ous:[],models:[],hosts:[]}
  waves jsonb NOT NULL, current_wave int NOT NULL DEFAULT 0,
  status text NOT NULL CHECK (status IN ('draft','running','paused','completed','cancelled')),
  paused_reason text, max_concurrent int,
  created_by text REFERENCES users, approved_by text REFERENCES users,
  created_at timestamptz DEFAULT now()
);

CREATE TABLE deployment_jobs (
  id text PRIMARY KEY, host_id text NOT NULL REFERENCES hosts,
  rollout_id text REFERENCES rollouts, wave int,
  kind text NOT NULL CHECK (kind IN ('deploy','rollback','stage')),
  engine text NOT NULL DEFAULT 'winpe',
  image_id text NOT NULL REFERENCES images, fallback_image_id text REFERENCES images,
  winpe_id text REFERENCES winpe_releases, driver_pack_id text REFERENCES driver_packs,
  disk_profile_id text REFERENCES disk_profiles, disk_mode text NOT NULL DEFAULT 'refresh',
  target_hostname text, domain jsonb,
  status text NOT NULL,                              -- per 05 §1
  ponr_passed_at timestamptz, error_code text, severity text,
  eta_p50_s int, eta_p90_s int, risk text,
  actor text NOT NULL,                               -- user:… | rollout:… | local:…
  manifest_not_after timestamptz,
  created_at timestamptz DEFAULT now(), finished_at timestamptz
);
CREATE INDEX jobs_active ON deployment_jobs(status) WHERE finished_at IS NULL;
CREATE UNIQUE INDEX one_active_job_per_host ON deployment_jobs(host_id) WHERE finished_at IS NULL;

CREATE TABLE job_events (                            -- append-only, idempotent by (job_id, seq)
  job_id text REFERENCES deployment_jobs, seq int NOT NULL, source text NOT NULL, -- agent|pe|server
  status text, step text, severity text, data jsonb, at timestamptz NOT NULL,
  PRIMARY KEY (job_id, seq, source)
);
CREATE TABLE job_summaries (job_id text PRIMARY KEY REFERENCES deployment_jobs,
  timings jsonb, preflight jsonb, health jsonb, model text, bios_version text);
CREATE TABLE log_bundles (id text PRIMARY KEY, host_id text REFERENCES hosts, job_id text,
  reason text, path text, size_bytes bigint, uploaded_at timestamptz DEFAULT now());

CREATE TABLE concurrency_leases (
  job_id text PRIMARY KEY REFERENCES deployment_jobs, site_id text REFERENCES sites,
  kind text NOT NULL CHECK (kind IN ('deploy','download')), expires_at timestamptz NOT NULL
);

CREATE TABLE kill_switches (
  scope text NOT NULL CHECK (scope IN ('global','channel','site','rollout')),
  scope_id text NOT NULL DEFAULT '*', active bool NOT NULL,
  reason text, set_by text, set_at timestamptz, cleared_by text, cleared_at timestamptz,
  PRIMARY KEY (scope, scope_id)
);

-- ───────── Agent releases ─────────
CREATE TABLE agent_releases (
  id text PRIMARY KEY, component text NOT NULL CHECK (component IN ('agent','svc')),
  version text NOT NULL, arch text NOT NULL,
  status text NOT NULL CHECK (status IN ('draft','testing','canary','pilot','broad','published','paused','revoked')),
  percent int NOT NULL DEFAULT 0, priority text NOT NULL DEFAULT 'normal',
  min_svc_version text, state_schema int, release_json bytea, release_sig bytea,
  bundle_path text, sha256 text, rollback_target text,
  stage_started_at timestamptz, created_at timestamptz DEFAULT now(),
  UNIQUE (component, version, arch)
);
CREATE TABLE agent_update_events (id bigserial PRIMARY KEY, host_id text REFERENCES hosts,
  release_id text REFERENCES agent_releases, result text, detail jsonb, at timestamptz DEFAULT now());

-- ───────── server-side tasks (builder / utility VM) ─────────
CREATE TABLE server_tasks (
  id text PRIMARY KEY,
  kind text NOT NULL,          -- build_image | test_image | build_winpe | odj_provision | odj_reset | dism_service
  payload jsonb NOT NULL, status text NOT NULL DEFAULT 'queued',
  locked_by text, locked_until timestamptz, result jsonb,
  created_at timestamptz DEFAULT now(), finished_at timestamptz
);

-- ───────── Audit ─────────
CREATE TABLE audit_log (
  id bigserial PRIMARY KEY, at timestamptz NOT NULL DEFAULT now(),
  actor text NOT NULL, action text NOT NULL, object_type text, object_id text,
  reason text, ip inet, result text, data jsonb,
  prev_hash bytea, hash bytea NOT NULL
);
REVOKE UPDATE, DELETE, TRUNCATE ON audit_log FROM swim_app;   -- swim_app is created at initdb and owns no table
```

> The version that actually runs is `backend/migrations/0001_init.sql` (with the full GRANTs for swim_app) — tested on PostgreSQL 16. Later migrations in the same folder extend it.

## Job queue

- The backend worker takes jobs with `SELECT … FROM deployment_jobs WHERE … FOR UPDATE SKIP LOCKED`
- Builder / utility tasks use the `server_tasks` table the same way (the Utility VM pulls through `/v1/utility/tasks`)
