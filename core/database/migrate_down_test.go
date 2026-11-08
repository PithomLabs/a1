package database

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	coremigrations "github.com/PithomLabs/a1/migrations/core"
	"github.com/PithomLabs/a1/tests/dbutil"
)

// Stage F: 00011 Down is guarded forward-only. It refuses populated
// databases before anything destructive and unwinds cleanly only on
// freshly migrated empty ones. The test-only Down invocation here is
// not a product rollback path: the application and Taskfile expose
// no Down operation.

func downOnce(t *testing.T, migDSN string) error {
	t.Helper()
	p, db, err := openProvider(migDSN, coremigrations.FS)
	if err != nil {
		t.Fatalf("open provider: %v", err)
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	_, err = p.Down(ctx)
	return err
}

func headVersion(t *testing.T, migDSN string) int64 {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	statuses, err := MigrationStatus(ctx, migDSN, coremigrations.FS)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	var head int64
	for _, st := range statuses {
		if st.State == "applied" && st.Version > head {
			head = st.Version
		}
	}
	return head
}

func openApp(t *testing.T, dsn string) *pgxpool.Pool {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	pool, err := Open(ctx, dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// Down on a populated database fails with the guard message and
// leaves the schema at version 11, fully intact.
func TestDownRefusesPopulatedDatabase(t *testing.T) {
	dsn := dbutil.DSN(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	pos, err := dbutil.SetupDB(ctx, dsn,
		fmt.Sprintf("m13_down_neg_%d", time.Now().UnixNano()),
		func(ctx context.Context, migDSN string) error {
			_, err := MigrateUp(ctx, migDSN, coremigrations.FS)
			return err
		})
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
	if got := headVersion(t, pos.MigDSN); got != 11 {
		t.Fatalf("head=%d want 11", got)
	}
	app := openApp(t, pos.AppDSN)
	var emp, ee, terms string
	if err := app.QueryRow(ctx,
		`INSERT INTO employer (code, name, uen, address) VALUES ('DN', 'Down Neg', 'UEN-DN', 'a')
		 RETURNING id::text`).Scan(&emp); err != nil {
		t.Fatalf("employer: %v", err)
	}
	if err := app.QueryRow(ctx,
		`INSERT INTO employee (employer_id, employee_no, legal_name, address, gender, date_of_birth, citizenship, status, status_effective_date)
		 VALUES ($1, 'E001', 'x', 'a', 'F', '1990-01-01', 'SG', 'active', CURRENT_DATE) RETURNING id::text`,
		emp).Scan(&ee); err != nil {
		t.Fatalf("employee: %v", err)
	}
	if err := app.QueryRow(ctx,
		`INSERT INTO employment_terms (employer_id, employee_id, effective_from, job_title, job_duties,
		  employment_category, part_iv_eligible, eligibility_basis, monthly_basic, working_arrangement,
		  normal_hours_weekly, working_days, daily_hours, rest_day, start_date, contract_type,
		  place_of_work, salary_period)
		 VALUES ($1, $2, '2026-01-01', 'Agent', 'Support', 'workman', true, 'manual-wages', 2600,
		  'shift', 44, 'Mon-Fri', 8.8, 'Sunday', '2026-01-01', 'permanent', 'Office', 'monthly')
		 RETURNING id::text`,
		emp, ee).Scan(&terms); err != nil {
		t.Fatalf("terms: %v", err)
	}
	if _, err := app.Exec(ctx,
		`SELECT add_compensation($1::uuid, $2::uuid, $3::uuid, 'allowance', 'SHIFT', 100,
		  '2026-01-01'::date, NULL, 'op', 'test')`,
		emp, ee, terms); err != nil {
		t.Fatalf("comp: %v", err)
	}
	if err := downOnce(t, pos.MigDSN); err == nil {
		t.Fatal("Down on a populated database succeeded, want the guard refusal")
	} else if !strings.Contains(err.Error(), "Down refused") {
		t.Fatalf("Down error = %v, want the guard refusal", err)
	}
	if got := headVersion(t, pos.MigDSN); got != 11 {
		t.Fatalf("head=%d want 11 (schema intact)", got)
	}
	var links bool
	if err := app.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM pg_tables WHERE schemaname = 'public' AND tablename = 'attendance_correction_group_predecessor')`,
	).Scan(&links); err != nil || !links {
		t.Fatalf("link table exists=%v err=%v want the schema untouched", links, err)
	}
}

// Down on a freshly migrated empty database unwinds 00011 exactly:
// 00010 shapes return, 00011 objects vanish, and Up re-applies.
func TestDownEmptyDatabaseRoundTrip(t *testing.T) {
	dsn := dbutil.DSN(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	pos, err := dbutil.SetupDB(ctx, dsn,
		fmt.Sprintf("m13_down_pos_%d", time.Now().UnixNano()),
		func(ctx context.Context, migDSN string) error {
			_, err := MigrateUp(ctx, migDSN, coremigrations.FS)
			return err
		})
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
	if err := downOnce(t, pos.MigDSN); err != nil {
		t.Fatalf("Down on empty database: %v", err)
	}
	if got := headVersion(t, pos.MigDSN); got != 10 {
		t.Fatalf("head=%d want 10 after Down", got)
	}
	app := openApp(t, pos.AppDSN)
	checks := []struct {
		name string
		sql  string
		want bool
	}{
		{"link table gone", `SELECT EXISTS (SELECT 1 FROM pg_tables WHERE schemaname='public' AND tablename='attendance_correction_group_predecessor')`, false},
		{"single-pred column back", `SELECT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema='public' AND table_name='attendance_correction_group' AND column_name='predecessor_interval_id')`, true},
		{"per-terms exclusion back", `SELECT EXISTS (SELECT 1 FROM pg_constraint WHERE conname='compensation_line_no_overlap')`, true},
		{"add_compensation gone", `SELECT EXISTS (SELECT 1 FROM pg_proc WHERE proname='add_compensation')`, false},
		{"correct_compensation gone", `SELECT EXISTS (SELECT 1 FROM pg_proc WHERE proname='correct_compensation')`, false},
		{"comp INSERT granted", `SELECT has_table_privilege('app', 'public.compensation_line', 'INSERT')`, true},
		{"comp employee_id gone", `SELECT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema='public' AND table_name='compensation_line' AND column_name='employee_id')`, false},
		{"amount type restored", `SELECT format_type(atttypid, atttypmod) = 'numeric(12,2)' FROM pg_attribute WHERE attrelid='public.compensation_line'::regclass AND attname='amount_monthly'`, true},
		{"basic type restored", `SELECT format_type(atttypid, atttypmod) = 'numeric(12,2)' FROM pg_attribute WHERE attrelid='public.employment_terms'::regclass AND attname='monthly_basic'`, true},
		{"pay_basis_seq gone", `SELECT EXISTS (SELECT 1 FROM pg_class WHERE relname='pay_basis_seq')`, false},
		{"lock helper gone", `SELECT EXISTS (SELECT 1 FROM pg_proc WHERE proname='acquire_payroll_input_lock')`, false},
		{"timesheet basis gone", `SELECT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema='public' AND table_name='timesheet' AND column_name='basis_seq')`, false},
		{"legacy EXECUTE restored", `SELECT has_function_privilege('app', 'public.forbid_if_leave_decided()', 'EXECUTE')`, true},
		{"disposition trigger restored", `SELECT pg_get_functiondef(oid) NOT LIKE '%already canonical%' FROM pg_proc WHERE proname='check_clock_event_disposition'`, true},
		{"close body restored", `SELECT pg_get_functiondef(oid) NOT LIKE '%outlive%' FROM pg_proc WHERE proname='close_employment_terms'`, true},
	}
	for _, c := range checks {
		var got bool
		if err := app.QueryRow(ctx, c.sql).Scan(&got); err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if got != c.want {
			t.Fatalf("%s = %v, want %v", c.name, got, c.want)
		}
	}
	// Up re-applies cleanly after the unwind.
	if err := dbutil.WithSetupLock(ctx, pos.AdminDSN, pos.Name, func() error {
		applied, err := MigrateUp(ctx, pos.MigDSN, coremigrations.FS)
		if err != nil {
			return err
		}
		if len(applied) != 1 || applied[0].Version != 11 {
			return fmt.Errorf("re-apply applied %+v, want [11]", applied)
		}
		return nil
	}); err != nil {
		t.Fatalf("re-apply: %v", err)
	}
}
