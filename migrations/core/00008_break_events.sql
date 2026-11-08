-- WorkforceOps Lab baseline, part 8: break evidence on clock events.
--
-- Amendment A (plan_prompt7_review): in/out punches alone cannot establish
-- actual break times, and scheduled segments must never pose as observed
-- breaks. Clock events gain break_start/break_end kinds so the import
-- stream carries break evidence explicitly; pairing nests breaks inside
-- in/out spans. Paid/unpaid classification of an actual break maps it to
-- the roster's scheduled segments, with an explicit exception when no
-- roster or segment match exists. Missing break evidence is an exception,
-- never a silent inference.

-- +goose Up

ALTER TABLE clock_event DROP CONSTRAINT clock_event_kind_check;
ALTER TABLE clock_event ADD CONSTRAINT clock_event_kind_check
    CHECK (kind IN ('in', 'out', 'break_start', 'break_end'));

-- +goose Down

-- Fails if break_start/break_end rows exist; delete them first.
ALTER TABLE clock_event DROP CONSTRAINT clock_event_kind_check;
ALTER TABLE clock_event ADD CONSTRAINT clock_event_kind_check
    CHECK (kind IN ('in', 'out'));
