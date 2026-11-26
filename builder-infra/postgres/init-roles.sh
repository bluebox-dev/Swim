#!/bin/sh
# Runs once on an empty data dir. The app role gets table grants from migration 0001;
# it never owns objects, so it cannot alter or bypass the append-only audit log.
set -e
psql -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$POSTGRES_DB" \
  -v pw="$SWIM_APP_PASSWORD" <<'SQL'
CREATE ROLE swim_app LOGIN PASSWORD :'pw';
GRANT CONNECT ON DATABASE swim TO swim_app;
GRANT USAGE ON SCHEMA public TO swim_app;
SQL
