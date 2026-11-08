-- +goose Up
-- 00001: platform schema skeleton.
-- Database roles are provisioned separately (compose init script for local
-- development, documented bootstrap for Neon) because role management needs
-- superuser and must stay out of application migrations.

CREATE SCHEMA IF NOT EXISTS platform;
CREATE SCHEMA IF NOT EXISTS investigation;
CREATE SCHEMA IF NOT EXISTS evaluation_private;
CREATE SCHEMA IF NOT EXISTS shifthub;
CREATE SCHEMA IF NOT EXISTS payloop;

-- +goose Down
-- Down is supported for local development only. Never run Down against
-- shared or production-adjacent data.

DROP SCHEMA IF EXISTS payloop CASCADE;
DROP SCHEMA IF EXISTS shifthub CASCADE;
DROP SCHEMA IF EXISTS evaluation_private CASCADE;
DROP SCHEMA IF EXISTS investigation CASCADE;
DROP SCHEMA IF EXISTS platform CASCADE;
