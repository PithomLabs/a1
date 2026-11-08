// Package seed builds the deterministic Meridian demo dataset through
// the same services the CLI and API use. All values are fixed: same
// database state in, same rows out. Synthetic reference data only.
package seed

import (
	"context"
	"fmt"
	"strings"

	coredatabase "github.com/PithomLabs/a1/core/database"
	coremigrations "github.com/PithomLabs/a1/migrations/core"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/PithomLabs/a1/internal/apperr"
	"github.com/PithomLabs/a1/internal/attendance"
	"github.com/PithomLabs/a1/internal/hr"
	"github.com/PithomLabs/a1/internal/timesheet"
	"github.com/PithomLabs/a1/internal/wfm"
)

const (
	// EmployerCode is the reference BPO employer.
	EmployerCode = "MERIDIAN"
	// Operator attributes every seeded row.
	Operator = "seed"
	// Period is the demo pay period code.
	Period = "PP-2026-08"
	// Week is the demo roster week.
	Week = "2026-08-03"
)

// Demo loads the seed. It refuses when MERIDIAN already exists; reset
// is a separate controlled operation (Reset), never row deletion past
// the freeze triggers.
func Demo(ctx context.Context, pool *pgxpool.Pool) (string, error) {
	var exists bool
	err := pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM employer WHERE code = $1)`, EmployerCode).Scan(&exists)
	if err != nil {
		return "", apperr.FromDB(err, "check seed state")
	}
	if exists {
		return "", apperr.New(apperr.CodeConflict, "employer MERIDIAN already seeded; use --reset")
	}
	if err := globals(ctx, pool); err != nil {
		return "", err
	}
	var employerID string
	err = pool.QueryRow(ctx,
		`INSERT INTO employer (code, name, uen, address) VALUES ('MERIDIAN', 'Meridian CX Demo (synthetic)', 'SYNTH-DEMO-001', '1 Synthetic Row, Singapore') RETURNING id`,
	).Scan(&employerID)
	if err != nil {
		return "", apperr.FromDB(err, "seed employer")
	}
	if err := people(ctx, pool, employerID); err != nil {
		return "", err
	}
	if err := structure(ctx, pool, employerID); err != nil {
		return "", err
	}
	if err := roster(ctx, pool, employerID); err != nil {
		return "", err
	}
	if err := clocks(ctx, pool, employerID); err != nil {
		return "", err
	}
	if err := period(ctx, pool, employerID); err != nil {
		return "", err
	}
	if err := activations(ctx, pool, employerID); err != nil {
		return "", err
	}
	return "seeded MERIDIAN: 3 employees, 1 programme, 1 roster week, 1 pay period", nil
}

// Reset recycles the database named by appDSN — drop, recreate,
// provision, migrate — using the privileged adminDSN. It is the only
// supported way to reload the demo dataset: published rosters and
// decided timesheets cannot be row-deleted by any role, so a reset
// must dispose at the database level on a verified disposable target.
// The caller confirms the target name explicitly (confirm must equal
// the DSN's database), both DSNs must address the same server, and
// RecycleDatabase additionally refuses protected and non-disposable
// names. rolePassword provisions the runtime roles' passwords on
// password-authenticated servers; empty leaves them untouched.
func Reset(ctx context.Context, appDSN, adminDSN, confirm, rolePassword string) error {
	name := coredatabase.DatabaseName(appDSN)
	if name == "" {
		return apperr.New(apperr.CodeValidation, "reset needs a --dsn naming the demo database")
	}
	if confirm == "" || confirm != name {
		return apperr.New(apperr.CodeValidation,
			"reset target %q does not match the database %q: retype --target to confirm", confirm, name)
	}
	if adminDSN == "" {
		return apperr.New(apperr.CodeValidation, "reset needs a privileged --admin-dsn")
	}
	if !coredatabase.SameServer(appDSN, adminDSN) {
		return apperr.New(apperr.CodeValidation, "admin DSN must address the same server as --dsn")
	}
	if err := coredatabase.RecycleDatabase(ctx, adminDSN, name, rolePassword); err != nil {
		return apperr.New(apperr.CodeInvalidState, "recycle demo database: %v", err)
	}
	migDSN := coredatabase.RoleDSN(adminDSN, name, "migration_admin")
	if rolePassword != "" {
		migDSN = coredatabase.SetPassword(migDSN, rolePassword)
	}
	if _, err := coredatabase.MigrateUp(ctx, migDSN, coremigrations.FS); err != nil {
		return fmt.Errorf("migrate recycled database: %w", err)
	}
	return nil
}

// globals inserts catalogue rows shared across employers.
func globals(ctx context.Context, pool *pgxpool.Pool) error {
	for _, s := range [][3]string{
		{"EN-SUPPORT", "English support", "language"},
		{"MOBILE-APP", "Mobile app product", "product"},
		{"DEESCALATION", "De-escalation process", "process"},
	} {
		if _, err := pool.Exec(ctx,
			`INSERT INTO skill (code, name, kind) VALUES ($1, $2, $3) ON CONFLICT (code) DO NOTHING`,
			s[0], s[1], s[2]); err != nil {
			return apperr.FromDB(err, "seed skills")
		}
	}
	for _, p := range [][4]string{
		{"REG", "Regular", "earning", "OW"},
		{"OT15", "Overtime 1.5x", "earning", "OW"},
		{"OT20", "Overtime 2.0x", "earning", "OW"},
		{"LEAVE", "Paid leave", "leave", "OW"},
		{"SICK", "Paid sick leave", "leave", "OW"},
		{"PH", "Public holiday", "earning", "OW"},
	} {
		if _, err := pool.Exec(ctx,
			`INSERT INTO pay_code (code, name, kind, default_treatment) VALUES ($1, $2, $3, $4)
			 ON CONFLICT (code) DO NOTHING`, p[0], p[1], p[2], p[3]); err != nil {
			return apperr.FromDB(err, "seed pay codes")
		}
	}
	for _, l := range [][2]string{{"ANNUAL", "Annual leave"}, {"SICK", "Sick leave"}} {
		if _, err := pool.Exec(ctx,
			`INSERT INTO leave_type (code, name) VALUES ($1, $2) ON CONFLICT (code) DO NOTHING`,
			l[0], l[1]); err != nil {
			return apperr.FromDB(err, "seed leave types")
		}
	}
	for _, h := range [][2]string{
		{"2026-08-09", "National Day"},
		{"2026-08-10", "National Day observed"},
	} {
		if _, err := pool.Exec(ctx,
			`INSERT INTO holiday_calendar (date, name) VALUES ($1::date, $2) ON CONFLICT (date) DO NOTHING`,
			h[0], h[1]); err != nil {
			return apperr.FromDB(err, "seed holidays")
		}
	}
	return nil
}

// people creates three synthetic employees with terms and SHG instructions.
func people(ctx context.Context, pool *pgxpool.Pool, employerID string) error {
	for _, no := range []string{"DEMO-001", "DEMO-002", "DEMO-003"} {
		e, err := hr.CreateEmployee(ctx, pool, employerID, hr.EmployeeInput{
			EmployeeNo: no, LegalName: "Synthetic " + no, Address: "1 Synthetic Row",
			Gender: "X", DateOfBirth: "1990-01-01", Citizenship: "SG",
			Status: hr.StatusActive, StatusEffectiveDate: "2026-01-01",
		}, Operator)
		if err != nil {
			return err
		}
		if _, err := hr.SetTerms(ctx, pool, employerID, e.ID, hr.TermsInput{
			EffectiveFrom: "2026-01-01", JobTitle: "Support Agent", JobDuties: "Tier 1 support",
			EmploymentCategory: "non_workman", PartIVEligible: true, EligibilityBasis: "synthetic demo data",
			MonthlyBasic: "2400", WorkingArrangement: "shift", NormalHoursWeekly: 44,
			WorkingDays: "Mon-Fri", DailyHours: 8.8, RestDay: "Sunday",
			StartDate: "2026-01-01", ContractType: "permanent",
			PlaceOfWork: "Singapore", SalaryPeriod: "monthly",
		}, "seed"); err != nil {
			return err
		}
		if _, err := pool.Exec(ctx,
			`INSERT INTO shg_instruction (employer_id, employee_id, fund, status, amount_or_basis, source_declaration, effective_from)
			 VALUES ($1, $2, 'CDAC', 'active', 'fixed', 'synthetic demo declaration', '2026-01-01')`,
			employerID, e.ID); err != nil {
			return apperr.FromDB(err, "seed SHG instruction")
		}
		if err := wfm.AssignSkills(ctx, pool, employerID, no, "2026-01-01", []wfm.SkillGrant{
			{Skill: "EN-SUPPORT", Level: 3},
		}, Operator); err != nil {
			return err
		}
	}
	return nil
}

// structure creates one client, programme, team and memberships.
func structure(ctx context.Context, pool *pgxpool.Pool, employerID string) error {
	c, err := wfm.CreateClient(ctx, pool, employerID, "ACME", "Acme Demo Client")
	if err != nil {
		return err
	}
	p, err := wfm.CreateProgramme(ctx, pool, employerID, wfm.ProgrammeInput{
		Client: c.Code, Code: "ACME-CARE", Name: "Acme Care", Timezone: "Asia/Singapore",
	})
	if err != nil {
		return err
	}
	if err := wfm.RequireSkills(ctx, pool, employerID, p.Code, []wfm.SkillGrant{{Skill: "EN-SUPPORT", Level: 2}}, Operator); err != nil {
		return err
	}
	t, err := wfm.CreateTeam(ctx, pool, employerID, wfm.TeamInput{Programme: p.Code, Code: "CARE-A", Name: "Care Team A"})
	if err != nil {
		return err
	}
	for _, no := range []string{"DEMO-001", "DEMO-002", "DEMO-003"} {
		if _, err := wfm.AddMembership(ctx, pool, employerID, t.Code, no, "agent", "2026-01-01", Operator); err != nil {
			return err
		}
	}
	return nil
}

// roster creates day and night templates and one published roster week:
// DEMO-001 on days, DEMO-002 on days, DEMO-003 on an overnight shift,
// Sunday rest for all.
func roster(ctx context.Context, pool *pgxpool.Pool, employerID string) error {
	day, err := wfm.CreateTemplate(ctx, pool, employerID, wfm.TemplateInput{
		Code: "DAY", Name: "Day shift", StartLocal: "09:00", EndLocal: "18:00",
		Segments: []wfm.SegmentInput{
			{Kind: "work", StartOffsetMin: 0, DurationMin: 180, Paid: true},
			{Kind: "break_unpaid", StartOffsetMin: 180, DurationMin: 60, Paid: false},
			{Kind: "work", StartOffsetMin: 240, DurationMin: 300, Paid: true},
		},
	})
	if err != nil {
		return err
	}
	night, err := wfm.CreateTemplate(ctx, pool, employerID, wfm.TemplateInput{
		Code: "NIGHT", Name: "Night shift", StartLocal: "22:00", EndLocal: "06:00", CrossMidnight: true,
		Segments: []wfm.SegmentInput{
			{Kind: "work", StartOffsetMin: 0, DurationMin: 240, Paid: true},
			{Kind: "break_unpaid", StartOffsetMin: 240, DurationMin: 60, Paid: false},
			{Kind: "work", StartOffsetMin: 300, DurationMin: 180, Paid: true},
		},
	})
	if err != nil {
		return err
	}
	r, err := wfm.CreateRoster(ctx, pool, employerID, wfm.RosterInput{Programme: "ACME-CARE", WeekStarting: Week})
	if err != nil {
		return err
	}
	days := []string{"2026-08-03", "2026-08-04", "2026-08-05", "2026-08-06", "2026-08-07"}
	for _, d := range days {
		if _, err := wfm.Assign(ctx, pool, employerID, r.ID, wfm.AssignmentInput{Employee: "DEMO-001", Date: d, Template: day.Code}); err != nil {
			return err
		}
		if _, err := wfm.Assign(ctx, pool, employerID, r.ID, wfm.AssignmentInput{Employee: "DEMO-002", Date: d, Template: day.Code}); err != nil {
			return err
		}
		if _, err := wfm.Assign(ctx, pool, employerID, r.ID, wfm.AssignmentInput{Employee: "DEMO-003", Date: d, Template: night.Code}); err != nil {
			return err
		}
	}
	for _, no := range []string{"DEMO-001", "DEMO-002", "DEMO-003"} {
		if _, err := wfm.Assign(ctx, pool, employerID, r.ID, wfm.AssignmentInput{Employee: no, Date: "2026-08-09", RestDay: true}); err != nil {
			return err
		}
	}
	_ = night
	_, err = wfm.Publish(ctx, pool, employerID, r.ID, Operator)
	return err
}

// clocks imports one clean week for DEMO-001, a missing punch for
// DEMO-002, and overnight spans for DEMO-003, then replays the file to
// prove idempotent duplicate delivery.
func clocks(ctx context.Context, pool *pgxpool.Pool, employerID string) error {
	var b strings.Builder
	b.WriteString("source,source_event_id,employee_no,occurred_at,timezone,kind\n")
	n := 0
	emit := func(no, ts, kind string) {
		n++
		fmt.Fprintf(&b, "demo-clock,seed-%04d,%s,%s,Asia/Singapore,%s\n", n, no, ts, kind)
	}
	for _, d := range []string{"2026-08-03", "2026-08-04", "2026-08-05", "2026-08-06", "2026-08-07"} {
		emit("DEMO-001", d+"T09:00:00+08:00", "in")
		emit("DEMO-001", d+"T12:00:00+08:00", "break_start")
		emit("DEMO-001", d+"T13:00:00+08:00", "break_end")
		emit("DEMO-001", d+"T18:00:00+08:00", "out")
		emit("DEMO-002", d+"T09:05:00+08:00", "in")
		emit("DEMO-002", d+"T18:02:00+08:00", "out")
		emit("DEMO-003", d+"T22:00:00+08:00", "in")
	}
	// DEMO-002 misses the Friday punch-out: the exception case.
	csv1 := b.String()
	lines := strings.Split(strings.TrimSpace(csv1), "\n")
	kept := []string{lines[0]}
	for _, l := range lines[1:] {
		if strings.Contains(l, "DEMO-002,2026-08-07T18:02:00") {
			continue
		}
		kept = append(kept, l)
	}
	// DEMO-003 night breaks and punch-outs land after midnight.
	// The NIGHT template breaks 02:00-03:00 (offset 240/60 from 22:00).
	for _, d := range []string{"2026-08-04", "2026-08-05", "2026-08-06", "2026-08-07", "2026-08-08"} {
		n++
		kept = append(kept, fmt.Sprintf("demo-clock,seed-%04d,DEMO-003,%sT02:00:00+08:00,Asia/Singapore,break_start", n, d))
		n++
		kept = append(kept, fmt.Sprintf("demo-clock,seed-%04d,DEMO-003,%sT03:00:00+08:00,Asia/Singapore,break_end", n, d))
		n++
		kept = append(kept, fmt.Sprintf("demo-clock,seed-%04d,DEMO-003,%sT06:00:00+08:00,Asia/Singapore,out", n, d))
	}
	csv1 = strings.Join(kept, "\n") + "\n"
	if _, err := attendance.Import(ctx, pool, employerID, strings.NewReader(csv1), Operator, timesheet.RegenerateDraft, timesheet.ChainTerminal); err != nil {
		return err
	}
	rep, err := attendance.Import(ctx, pool, employerID, strings.NewReader(csv1), Operator, timesheet.RegenerateDraft, timesheet.ChainTerminal)
	if err != nil {
		return err
	}
	if rep.EventsStored != 0 || rep.Duplicates == 0 {
		return apperr.New(apperr.CodeInternal, "seed replay was not idempotent: %+v", rep)
	}
	return nil
}

// period creates the demo pay period.
func period(ctx context.Context, pool *pgxpool.Pool, employerID string) error {
	_, err := pool.Exec(ctx,
		`INSERT INTO pay_period (employer_id, code, starts_on, ends_on, pay_date, salary_deadline, ot_deadline)
		 VALUES ($1, $2, '2026-08-01', '2026-08-31', '2026-09-07', '2026-09-07', '2026-09-14')`,
		employerID, Period)
	return apperr.FromDB(err, "seed pay period")
}

// activations records every pack as BLOCKED: readiness must report the
// block explicitly, never assume a pack.
func activations(ctx context.Context, pool *pgxpool.Pool, employerID string) error {
	for _, p := range []string{"SG-MOM-OT", "SG-MOM-SHIFT", "SG-CPF-2026", "SG-SDL", "SG-SHG", "SG-MOM-LEAVE", "SG-MOM-KET", "SG-MOM-PH", "SG-MOM-PAYDEAD"} {
		if _, err := pool.Exec(ctx,
			`INSERT INTO rule_pack_activation (employer_id, pack, version, status) VALUES ($1, $2, 'v0', 'BLOCKED')
			 ON CONFLICT (employer_id, pack) DO UPDATE SET status = 'BLOCKED', version = 'v0'`,
			employerID, p); err != nil {
			return apperr.FromDB(err, "seed activations")
		}
	}
	return nil
}
