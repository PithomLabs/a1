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
// leaves the schema at version 12, fully intact.
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
	if got := headVersion(t, pos.MigDSN); got != 12 {
		t.Fatalf("head=%d want 12", got)
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
	// An SHG row trips the 00012 guard: the SHG block is 00012's
	// outermost dependent, so Down must refuse before touching it.
	if _, err := app.Exec(ctx,
		`SELECT add_shg_instruction($1::uuid, $2::uuid, 'CDAC', 'active', 'fixed', 'declared',
		  '2026-01-01'::date, NULL, NULL, NULL, NULL, 'op')`,
		emp, ee); err != nil {
		t.Fatalf("shg: %v", err)
	}
	if err := downOnce(t, pos.MigDSN); err == nil {
		t.Fatal("Down on a populated database succeeded, want the guard refusal")
	} else if !strings.Contains(err.Error(), "Down refused") {
		t.Fatalf("Down error = %v, want the guard refusal", err)
	}
	if got := headVersion(t, pos.MigDSN); got != 12 {
		t.Fatalf("head=%d want 12 (schema intact)", got)
	}
	var links bool
	if err := app.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM pg_tables WHERE schemaname = 'public' AND tablename = 'attendance_correction_group_predecessor')`,
	).Scan(&links); err != nil || !links {
		t.Fatalf("link table exists=%v err=%v want the schema untouched", links, err)
	}
	var lifecycle bool
	if err := app.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM pg_trigger WHERE tgname = 'shg_instruction_lifecycle')`,
	).Scan(&lifecycle); err != nil || !lifecycle {
		t.Fatalf("SHG lifecycle trigger exists=%v err=%v want the schema untouched", lifecycle, err)
	}
}

// Down on a freshly migrated empty database unwinds 00012 exactly:
// 00011 shapes return, 00012 objects vanish, and Up re-applies.
// Later sections extend both the checks and the re-apply below.
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
	if got := headVersion(t, pos.MigDSN); got != 11 {
		t.Fatalf("head=%d want 11 after Down", got)
	}
	app := openApp(t, pos.AppDSN)
	checks := []struct {
		name string
		sql  string
		want bool
	}{
		{"per-fund exclusion back", `SELECT EXISTS (SELECT 1 FROM pg_constraint WHERE conname='shg_instruction_no_overlap')`, true},
		{"old status check back", `SELECT EXISTS (SELECT 1 FROM pg_constraint WHERE conname='shg_instruction_status_check')`, true},
		{"add_shg gone", `SELECT EXISTS (SELECT 1 FROM pg_proc WHERE proname='add_shg_instruction')`, false},
		{"verify_shg gone", `SELECT EXISTS (SELECT 1 FROM pg_proc WHERE proname='verify_shg_instruction')`, false},
		{"correct_shg gone", `SELECT EXISTS (SELECT 1 FROM pg_proc WHERE proname='correct_shg_instruction')`, false},
		{"lifecycle trigger gone", `SELECT EXISTS (SELECT 1 FROM pg_trigger WHERE tgname='shg_instruction_lifecycle')`, false},
		{"basis trigger gone", `SELECT EXISTS (SELECT 1 FROM pg_trigger WHERE tgname='shg_instruction_basis_seq')`, false},
		{"normalize trigger gone", `SELECT EXISTS (SELECT 1 FROM pg_trigger WHERE tgname='shg_instruction_created_at_normalize')`, false},
		{"shg INSERT granted", `SELECT has_table_privilege('app', 'public.shg_instruction', 'INSERT')`, true},
		{"supersedes column gone", `SELECT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema='public' AND table_name='shg_instruction' AND column_name='supersedes_id')`, false},
		{"shg basis gone", `SELECT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema='public' AND table_name='shg_instruction' AND column_name='basis_seq')`, false},
		{"status check gone", `SELECT EXISTS (SELECT 1 FROM pg_constraint WHERE conname='shg_status_values')`, false},
		{"verified check gone", `SELECT EXISTS (SELECT 1 FROM pg_constraint WHERE conname='shg_verified_consistency')`, false},
		// RULES (P5) reversal checks.
		{"activate gone", `SELECT EXISTS (SELECT 1 FROM pg_proc WHERE proname='activate_rule_pack')`, false},
		{"ensure gone", `SELECT EXISTS (SELECT 1 FROM pg_proc WHERE proname='ensure_pack_blocked')`, false},
		{"history gone", `SELECT EXISTS (SELECT 1 FROM pg_tables WHERE schemaname='public' AND tablename='rule_pack_activation_history')`, false},
		{"registry gone", `SELECT EXISTS (SELECT 1 FROM pg_tables WHERE schemaname='public' AND tablename='rule_pack')`, false},
		{"activation INSERT granted", `SELECT has_table_privilege('app', 'public.rule_pack_activation', 'INSERT')`, true},
		{"param INSERT granted", `SELECT has_table_privilege('app', 'public.sdl_params', 'INSERT')`, true},
		// SNAPSHOT (P2) reversal checks.
		{"invalidate gone", `SELECT EXISTS (SELECT 1 FROM pg_proc WHERE proname='invalidate_payroll_snapshot')`, false},
		{"snapshot tables gone", `SELECT EXISTS (SELECT 1 FROM pg_tables WHERE schemaname='public' AND tablename IN ('payroll_snapshot','payroll_snapshot_employee','payroll_snapshot_pack'))`, false},
		{"population lock gone", `SELECT EXISTS (SELECT 1 FROM pg_proc WHERE proname='acquire_payroll_population_lock')`, false},
		{"pack lock gone", `SELECT EXISTS (SELECT 1 FROM pg_proc WHERE proname='acquire_rule_pack_lock')`, false},
		{"population triggers gone", `SELECT EXISTS (SELECT 1 FROM pg_trigger WHERE tgname IN ('employee_population_barrier','employment_terms_population_barrier'))`, false},
		{"transition body restored", `SELECT pg_get_functiondef(oid) NOT LIKE '%acquire_payroll_population_lock%' FROM pg_proc WHERE proname='transition_employee_status'`, true},
		{"transition keeps employee lock", `SELECT pg_get_functiondef(oid) LIKE '%acquire_payroll_input_lock%' FROM pg_proc WHERE proname='transition_employee_status'`, true},
		{"close body restored", `SELECT pg_get_functiondef(oid) NOT LIKE '%acquire_payroll_population_lock%' FROM pg_proc WHERE proname='close_employment_terms'`, true},
		// 00011 objects below are untouched by the 00012 unwind.
		{"compensation functions stay", `SELECT EXISTS (SELECT 1 FROM pg_proc WHERE proname='add_compensation')`, true},
		{"pay_basis_seq stays", `SELECT EXISTS (SELECT 1 FROM pg_class WHERE relname='pay_basis_seq')`, true},
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
		if len(applied) != 1 || applied[0].Version != 12 {
			return fmt.Errorf("re-apply applied %+v, want [12]", applied)
		}
		return nil
	}); err != nil {
		t.Fatalf("re-apply: %v", err)
	}
}
