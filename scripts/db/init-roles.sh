#!/bin/sh
# Local-dev role + extension bootstrap for docker compose (first container init only).
# NOT for shared, staging or Neon databases — see docs/workforceops/database-design.md.
# Two roles: migration_admin owns the schema and runs DDL; app runs the product.
# Extensions must be installed by a superuser before any migration runs.
#
# Required environment: POSTGRES_DB, POSTGRES_USER, A1_DEV_ROLE_PASSWORD.
# Connection details come from the standard PG* variables (PGHOST, PGPORT, ...).
# Re-running is safe: existing roles keep their passwords (reported, not reset).
set -eu

: "${POSTGRES_DB:?POSTGRES_DB is required}"
: "${POSTGRES_USER:?POSTGRES_USER is required}"
: "${A1_DEV_ROLE_PASSWORD:?A1_DEV_ROLE_PASSWORD is required}"

echo "WARNING: creating local-dev roles with a development password (localhost only)."

# :'var' interpolation is used only OUTSIDE dollar-quoted blocks: psql does
# not substitute inside dollar quotes, so the old DO-block CREATE ROLE with
# :'devpw' failed with a syntax error. Existence checks run as plain
# SELECT ... \gset and gate \if/\endif CREATE ROLE statements instead.
psql -v ON_ERROR_STOP=1 -v devpw="$A1_DEV_ROLE_PASSWORD" -v db="$POSTGRES_DB" \
  --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" <<'EOSQL'
REVOKE CREATE ON SCHEMA public FROM PUBLIC;

CREATE EXTENSION IF NOT EXISTS pgcrypto;
CREATE EXTENSION IF NOT EXISTS btree_gist;

SELECT NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'migration_admin') AS need_admin \gset
SELECT NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'app') AS need_app \gset
\if :need_admin
CREATE ROLE migration_admin LOGIN PASSWORD :'devpw';
\else
\echo 'role migration_admin exists, keeping its password'
\endif
\if :need_app
CREATE ROLE app LOGIN PASSWORD :'devpw';
\else
\echo 'role app exists, keeping its password'
\endif

GRANT CONNECT ON DATABASE :"db" TO migration_admin, app;

-- Neither runtime role needs temp tables; the SECURITY DEFINER
-- maintenance functions resolve names under a restricted search path
-- and must never see a pg_temp shadow. TEMP rides the default PUBLIC
-- grant, so revoke from PUBLIC as well as app.
REVOKE TEMPORARY ON DATABASE :"db" FROM app, PUBLIC;

-- migration_admin owns the single public schema; the app role cannot DDL.
ALTER SCHEMA public OWNER TO migration_admin;
GRANT USAGE ON SCHEMA public TO app;

-- Objects created later by migration_admin default to app read/write; the
-- protection migration then revokes UPDATE/DELETE on insert-only tables.
ALTER DEFAULT PRIVILEGES FOR ROLE migration_admin IN SCHEMA public
  GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO app;

-- Fail loudly on incomplete provisioning: re-check every intended
-- object and privilege. (Pure SQL here: no psql variables inside the
-- dollar-quoted block.)
DO $verify$
DECLARE
  defacl TEXT;
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'migration_admin' AND rolcanlogin) THEN
    RAISE EXCEPTION 'provisioning incomplete: role migration_admin missing or NOLOGIN';
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'app' AND rolcanlogin) THEN
    RAISE EXCEPTION 'provisioning incomplete: role app missing or NOLOGIN';
  END IF;
  IF NOT has_database_privilege('migration_admin', current_database(), 'CONNECT') THEN
    RAISE EXCEPTION 'provisioning incomplete: migration_admin lacks CONNECT on %', current_database();
  END IF;
  IF NOT has_database_privilege('app', current_database(), 'CONNECT') THEN
    RAISE EXCEPTION 'provisioning incomplete: app lacks CONNECT on %', current_database();
  END IF;
  IF has_database_privilege('app', current_database(), 'TEMP') THEN
    RAISE EXCEPTION 'provisioning incomplete: app keeps TEMPORARY on %', current_database();
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_namespace n JOIN pg_roles u ON u.oid = n.nspowner
                 WHERE n.nspname = 'public' AND u.rolname = 'migration_admin') THEN
    RAISE EXCEPTION 'provisioning incomplete: schema public not owned by migration_admin';
  END IF;
  IF NOT has_schema_privilege('app', 'public', 'USAGE') THEN
    RAISE EXCEPTION 'provisioning incomplete: app lacks USAGE on schema public';
  END IF;
  IF NOT EXISTS (SELECT 1 FROM (SELECT nspacl FROM pg_namespace WHERE nspname = 'public') s
                 WHERE s.nspacl IS NOT NULL
                   AND NOT EXISTS (SELECT 1 FROM aclexplode(s.nspacl)
                                       AS a(grantor, grantee, priv, grantable)
                                   WHERE a.grantee = 0 AND a.priv = 'CREATE')) THEN
    RAISE EXCEPTION 'provisioning incomplete: PUBLIC keeps CREATE on schema public';
  END IF;
  SELECT d.defaclacl::text INTO defacl FROM pg_default_acl d
   WHERE d.defaclrole = 'migration_admin'::regrole
     AND d.defaclnamespace = 'public'::regnamespace AND d.defaclobjtype = 'r';
  IF defacl IS NULL OR defacl NOT LIKE '%app=arwd%' THEN
    RAISE EXCEPTION 'provisioning incomplete: default app read/write privileges missing (%s)', defacl;
  END IF;
END
$verify$;
EOSQL
