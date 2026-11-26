-- Web sign-in: username + password + authenticator app (TOTP), server-side sessions.
CREATE TABLE IF NOT EXISTS admin_users (
  id text PRIMARY KEY,
  username text NOT NULL UNIQUE,
  display_name text NOT NULL DEFAULT '',
  password_hash text,
  totp_secret bytea,                 -- AES-256-GCM with the server key file
  totp_confirmed_at timestamptz,
  totp_last_step bigint NOT NULL DEFAULT 0,  -- a code is never accepted twice
  recovery_hashes jsonb NOT NULL DEFAULT '[]',
  setup_token_hash bytea,
  setup_expires_at timestamptz,
  disabled_at timestamptz,
  failed_count int NOT NULL DEFAULT 0,
  locked_until timestamptz,
  last_login_at timestamptz,
  created_by text,
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS admin_sessions (
  id text PRIMARY KEY,
  user_id text NOT NULL REFERENCES admin_users ON DELETE CASCADE,
  token_hash bytea NOT NULL UNIQUE,
  created_at timestamptz NOT NULL DEFAULT now(),
  last_seen_at timestamptz NOT NULL DEFAULT now(),
  expires_at timestamptz NOT NULL,
  ip text, user_agent text,
  revoked_at timestamptz
);
CREATE TABLE IF NOT EXISTS auth_challenges (
  token_hash bytea PRIMARY KEY,
  user_id text NOT NULL REFERENCES admin_users ON DELETE CASCADE,
  next text NOT NULL CHECK (next IN ('totp','setup')),
  expires_at timestamptz NOT NULL
);
GRANT SELECT, INSERT, UPDATE, DELETE ON admin_users, admin_sessions, auth_challenges TO swim_app;
