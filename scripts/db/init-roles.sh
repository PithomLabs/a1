#!/bin/sh
# Local-dev role bootstrap for docker compose (first container init only).
# NOT for shared, staging or Neon databases — see docs/workforceops/database-design.md.
# Schemas are created here so grants can apply; migration 00001 is idempotent
# and converges to the same state.
set -eu

echo "WARNING: creating local-dev roles with a development password (localhost only)."

psql -v ON_ERROR_STOP=1 -v devpw="$A1_DEV_ROLE_PASSWORD" -v db="$POSTGRES_DB" \
  --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" <<'EOSQL'
REVOKE CREATE ON SCHEMA public FROM PUBLIC;

CREATE SCHEMA IF NOT EXISTS platform;
CREATE SCHEMA IF NOT EXISTS investigation;
CREATE SCHEMA IF NOT EXISTS evaluation_private;
CREATE SCHEMA IF NOT EXISTS shifthub;
CREATE SCHEMA IF NOT EXISTS payloop;

DO $do$
DECLARE
  r text;
BEGIN
  FOREACH r IN ARRAY ARRAY[
    'migration_admin', 'shifthub_app', 'payloop_app',
    'sync_worker', 'investigator_readonly', 'evaluator'
  ] LOOP
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = r) THEN
      EXECUTE format('CREATE ROLE %I LOGIN PASSWORD %L', r, :'devpw');
    END IF;
  END LOOP;
END $do$;

GRANT CONNECT ON DATABASE :"db" TO migration_admin, shifthub_app, payloop_app,
  sync_worker, investigator_readonly, evaluator;

-- migration_admin owns nothing yet but can manage everything once objects exist.
GRANT ALL PRIVILEGES ON SCHEMA platform, investigation, evaluation_private, shifthub, payloop TO migration_admin;

-- Runtime roles: schema-scoped least privilege (refined by later migrations).
GRANT USAGE ON SCHEMA shifthub TO shifthub_app, sync_worker, investigator_readonly;
GRANT USAGE ON SCHEMA payloop TO payloop_app, sync_worker, investigator_readonly;
GRANT USAGE ON SCHEMA platform TO sync_worker, investigator_readonly;
GRANT USAGE ON SCHEMA investigation TO investigator_readonly, evaluator;
GRANT USAGE ON SCHEMA evaluation_private TO evaluator;

-- Objects created by the compose superuser during local migrations.
ALTER DEFAULT PRIVILEGES IN SCHEMA shifthub
  GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO shifthub_app, sync_worker;
ALTER DEFAULT PRIVILEGES IN SCHEMA shifthub
  GRANT SELECT ON TABLES TO investigator_readonly;
ALTER DEFAULT PRIVILEGES IN SCHEMA payloop
  GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO payloop_app, sync_worker;
ALTER DEFAULT PRIVILEGES IN SCHEMA payloop
  GRANT SELECT ON TABLES TO investigator_readonly;
ALTER DEFAULT PRIVILEGES IN SCHEMA platform
  GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO sync_worker;
ALTER DEFAULT PRIVILEGES IN SCHEMA platform
  GRANT SELECT ON TABLES TO investigator_readonly;
ALTER DEFAULT PRIVILEGES IN SCHEMA investigation
  GRANT SELECT, INSERT ON TABLES TO investigator_readonly;
ALTER DEFAULT PRIVILEGES IN SCHEMA investigation
  GRANT SELECT ON TABLES TO evaluator;
ALTER DEFAULT PRIVILEGES IN SCHEMA evaluation_private
  GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO evaluator;
EOSQL
