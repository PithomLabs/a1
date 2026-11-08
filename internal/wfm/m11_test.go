package wfm

import (
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/PithomLabs/a1/internal/apperr"
	"github.com/PithomLabs/a1/internal/hr"
)

func TestPublishGate(t *testing.T) {
	pool := testPool(t)
	emp := mkEmployer(t, pool, uniq(t, "E"))
	c, _ := CreateClient(ctx(t), pool, emp, "CLI", "Client")
	p, _ := CreateProgramme(ctx(t), pool, emp, ProgrammeInput{Client: c.Code, Code: "PRG", Name: "Prog"})
	tm, _ := CreateTeam(ctx(t), pool, emp, TeamInput{Programme: p.Code, Code: "T1", Name: "Team"})
	mkEmployee(t, pool, emp, "E001")
	if _, err := AddMembership(ctx(t), pool, emp, tm.Code, "E001", "agent", "2026-01-01", "test"); err != nil {
		t.Fatalf("membership: %v", err)
	}
	r, _ := CreateRoster(ctx(t), pool, emp, RosterInput{Programme: p.Code, WeekStarting: "2026-08-03"})
	tpl, _ := CreateTemplate(ctx(t), pool, emp, TemplateInput{Code: "DAY", Name: "Day", StartLocal: "09:00", EndLocal: "18:00"})
	// Empty roster cannot publish.
	if _, err := Publish(ctx(t), pool, emp, r.ID, "test-op"); err == nil {
		t.Fatal("empty roster published")
	}
	// Outside the roster week cannot assign.
	if _, err := Assign(ctx(t), pool, emp, r.ID, AssignmentInput{Employee: "E001", Date: "2026-08-12", Template: tpl.Code}); err == nil {
		t.Fatal("out-of-week assignment accepted")
	}
	// Contractual rest day cannot take a shift.
	eid := mkEmployee(t, pool, emp, "E002")
	if _, err := AddMembership(ctx(t), pool, emp, tm.Code, "E002", "agent", "2026-01-01", "test"); err != nil {
		t.Fatalf("membership2: %v", err)
	}
	if _, err := hr.SetTerms(ctx(t), pool, emp, eid, hr.TermsInput{
		EffectiveFrom: "2026-01-01", JobTitle: "Agent", JobDuties: "support",
		EmploymentCategory: "workman", PartIVEligible: true, EligibilityBasis: "test",
		MonthlyBasic: "2600", WorkingArrangement: "shift", NormalHoursWeekly: 44,
		WorkingDays: "Mon-Sat", DailyHours: 8, RestDay: "Sunday",
		StartDate: "2026-01-01", ContractType: "permanent",
		PlaceOfWork: "office", SalaryPeriod: "monthly",
	}, "test-op"); err != nil {
		t.Fatalf("terms: %v", err)
	}
	// 2026-08-09 is a Sunday.
	if _, err := Assign(ctx(t), pool, emp, r.ID, AssignmentInput{Employee: "E002", Date: "2026-08-09", Template: tpl.Code}); err == nil {
		t.Fatal("shift on contractual rest day accepted")
	}
	if _, err := Assign(ctx(t), pool, emp, r.ID, AssignmentInput{Employee: "E002", Date: "2026-08-09", RestDay: true}); err != nil {
		t.Fatalf("rest-day assignment: %v", err)
	}
	// Publish revalidates: suspend the assignees through the governed
	// transition, publish must fail. (Membership rows are insert-only
	// since C8, so direct-SQL expiry is pinned as rejected in the
	// integrity suite instead of simulated here.)
	if _, err := Assign(ctx(t), pool, emp, r.ID, AssignmentInput{Employee: "E001", Date: "2026-08-03", Template: tpl.Code}); err != nil {
		t.Fatalf("assign: %v", err)
	}
	var e001 string
	if err := pool.QueryRow(ctx(t),
		`SELECT id FROM employee WHERE employer_id = $1 AND employee_no = 'E001'`,
		emp).Scan(&e001); err != nil {
		t.Fatalf("resolve E001: %v", err)
	}
	for _, id := range []string{e001, eid} {
		if _, err := hr.SetStatus(ctx(t), pool, emp, id, hr.StatusSuspended, "2026-08-01", "stale crew", "test-op"); err != nil {
			t.Fatalf("suspend: %v", err)
		}
	}
	if _, err := Publish(ctx(t), pool, emp, r.ID, "test-op"); err == nil {
		t.Fatal("publish with suspended assignees succeeded")
	}
}

func TestReviseRoundTrip(t *testing.T) {
	pool := testPool(t)
	emp := mkEmployer(t, pool, uniq(t, "E"))
	c, _ := CreateClient(ctx(t), pool, emp, "CLI", "Client")
	p, _ := CreateProgramme(ctx(t), pool, emp, ProgrammeInput{Client: c.Code, Code: "PRG", Name: "Prog"})
	tm, _ := CreateTeam(ctx(t), pool, emp, TeamInput{Programme: p.Code, Code: "T1", Name: "Team"})
	mkEmployee(t, pool, emp, "E001")
	if _, err := AddMembership(ctx(t), pool, emp, tm.Code, "E001", "agent", "2026-01-01", "test"); err != nil {
		t.Fatalf("membership: %v", err)
	}
	r, _ := CreateRoster(ctx(t), pool, emp, RosterInput{Programme: p.Code, WeekStarting: "2026-08-03"})
	tpl, _ := CreateTemplate(ctx(t), pool, emp, TemplateInput{Code: "DAY", Name: "Day", StartLocal: "09:00", EndLocal: "18:00"})
	if _, err := Assign(ctx(t), pool, emp, r.ID, AssignmentInput{Employee: "E001", Date: "2026-08-03", Template: tpl.Code}); err != nil {
		t.Fatalf("assign: %v", err)
	}
	if _, err := Publish(ctx(t), pool, emp, r.ID, "test-op"); err != nil {
		t.Fatalf("publish: %v", err)
	}
	rev, err := Revise(ctx(t), pool, emp, r.ID, "swap Friday cover", "test-op")
	if err != nil {
		t.Fatalf("revise: %v", err)
	}
	if rev.Status != "draft" || rev.CurrentVersion != 2 {
		t.Fatalf("revised = %+v", rev)
	}
	d, err := Show(ctx(t), pool, emp, r.ID)
	if err != nil {
		t.Fatalf("show: %v", err)
	}
	if len(d.Assignments) != 1 {
		t.Fatalf("assignments = %d, want 1 carried forward", len(d.Assignments))
	}
	// The new draft accepts edits and republishes.
	if _, err := Assign(ctx(t), pool, emp, r.ID, AssignmentInput{Employee: "E001", Date: "2026-08-04", RestDay: true}); err != nil {
		t.Fatalf("assign v2: %v", err)
	}
	if _, err := Publish(ctx(t), pool, emp, r.ID, "test-op"); err != nil {
		t.Fatalf("republish: %v", err)
	}
	// Published history is immutable: the v1 rows still exist untouched.
	var v1 int
	if err := pool.QueryRow(ctx(t),
		`SELECT count(*) FROM roster_assignment a JOIN roster_revision rev ON rev.id = a.revision_id
		 WHERE a.employer_id = $1 AND rev.roster_id = $2 AND rev.version = 1`, emp, r.ID).Scan(&v1); err != nil || v1 != 1 {
		t.Fatalf("v1 rows = %d, err = %v", v1, err)
	}
}

func TestConcurrentPublishExactlyOnce(t *testing.T) {
	pool := testPool(t)
	emp := mkEmployer(t, pool, uniq(t, "E"))
	c, _ := CreateClient(ctx(t), pool, emp, "CLI", "Client")
	p, _ := CreateProgramme(ctx(t), pool, emp, ProgrammeInput{Client: c.Code, Code: "PRG", Name: "Prog"})
	tm, _ := CreateTeam(ctx(t), pool, emp, TeamInput{Programme: p.Code, Code: "T1", Name: "Team"})
	mkEmployee(t, pool, emp, "E001")
	if _, err := AddMembership(ctx(t), pool, emp, tm.Code, "E001", "agent", "2026-01-01", "test"); err != nil {
		t.Fatalf("membership: %v", err)
	}
	r, _ := CreateRoster(ctx(t), pool, emp, RosterInput{Programme: p.Code, WeekStarting: "2026-08-03"})
	tpl, _ := CreateTemplate(ctx(t), pool, emp, TemplateInput{Code: "DAY", Name: "Day", StartLocal: "09:00", EndLocal: "18:00"})
	if _, err := Assign(ctx(t), pool, emp, r.ID, AssignmentInput{Employee: "E001", Date: "2026-08-03", Template: tpl.Code}); err != nil {
		t.Fatalf("assign: %v", err)
	}
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			_, errs[n] = Publish(ctx(t), pool, emp, r.ID, "test-op")
		}(i)
	}
	wg.Wait()
	wins := 0
	for _, err := range errs {
		if err == nil {
			wins++
			continue
		}
		var ae *apperr.Error
		if !errors.As(err, &ae) || ae.Code != apperr.CodeInvalidState {
			t.Fatalf("loser error = %v, want invalid_state", err)
		}
	}
	if wins != 1 {
		t.Fatalf("wins = %d, want exactly one publisher", wins)
	}
	var status string
	var version int
	if err := pool.QueryRow(ctx(t),
		`SELECT status, current_version FROM roster WHERE id = $1`, r.ID).Scan(&status, &version); err != nil {
		t.Fatalf("roster: %v", err)
	}
	if status != "published" || version != 1 {
		t.Fatalf("status=%s version=%d", status, version)
	}
}

func TestConcurrentPublishReviseCoherent(t *testing.T) {
	pool := testPool(t)
	emp := mkEmployer(t, pool, uniq(t, "E"))
	c, _ := CreateClient(ctx(t), pool, emp, "CLI", "Client")
	p, _ := CreateProgramme(ctx(t), pool, emp, ProgrammeInput{Client: c.Code, Code: "PRG", Name: "Prog"})
	tm, _ := CreateTeam(ctx(t), pool, emp, TeamInput{Programme: p.Code, Code: "T1", Name: "Team"})
	mkEmployee(t, pool, emp, "E001")
	if _, err := AddMembership(ctx(t), pool, emp, tm.Code, "E001", "agent", "2026-01-01", "test"); err != nil {
		t.Fatalf("membership: %v", err)
	}
	r, _ := CreateRoster(ctx(t), pool, emp, RosterInput{Programme: p.Code, WeekStarting: "2026-08-03"})
	tpl, _ := CreateTemplate(ctx(t), pool, emp, TemplateInput{Code: "DAY", Name: "Day", StartLocal: "09:00", EndLocal: "18:00"})
	if _, err := Assign(ctx(t), pool, emp, r.ID, AssignmentInput{Employee: "E001", Date: "2026-08-03", Template: tpl.Code}); err != nil {
		t.Fatalf("assign: %v", err)
	}
	// Publish and revise race on a draft: either the publish wins and
	// the revise opens v2, or the revise loses and the publish wins.
	// Every outcome is machine-readable and the roster stays coherent.
	var wg sync.WaitGroup
	var pubErr, revErr error
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, pubErr = Publish(ctx(t), pool, emp, r.ID, "test-op")
	}()
	go func() {
		defer wg.Done()
		_, revErr = Revise(ctx(t), pool, emp, r.ID, "race", "test-op")
	}()
	wg.Wait()
	for _, err := range []error{pubErr, revErr} {
		if err == nil {
			continue
		}
		var ae *apperr.Error
		if !errors.As(err, &ae) || ae.Code != apperr.CodeInvalidState {
			t.Fatalf("race error = %v, want nil or invalid_state", err)
		}
	}
	var status string
	var version int
	if err := pool.QueryRow(ctx(t),
		`SELECT status, current_version FROM roster WHERE id = $1`, r.ID).Scan(&status, &version); err != nil {
		t.Fatalf("roster: %v", err)
	}
	switch {
	case status == "published" && version == 1:
	case status == "draft" && version == 2:
	default:
		t.Fatalf("incoherent roster: status=%s version=%d (publish=%v revise=%v)",
			status, version, pubErr, revErr)
	}
	var revs int
	if err := pool.QueryRow(ctx(t),
		`SELECT count(*) FROM roster_revision WHERE employer_id = $1 AND roster_id = $2`, emp, r.ID).Scan(&revs); err != nil || revs != version {
		t.Fatalf("revisions=%d version=%d err=%v", revs, version, err)
	}
}

// Two concurrent revises of one published roster admit exactly one
// winner: the row lock serializes the readers and the revision UNIQUE
// admits a single v2.
func TestConcurrentReviseSingleWinner(t *testing.T) {
	pool := testPool(t)
	emp := mkEmployer(t, pool, uniq(t, "E"))
	c, _ := CreateClient(ctx(t), pool, emp, "CLI", "Client")
	p, _ := CreateProgramme(ctx(t), pool, emp, ProgrammeInput{Client: c.Code, Code: "PRG", Name: "Prog"})
	tm, _ := CreateTeam(ctx(t), pool, emp, TeamInput{Programme: p.Code, Code: "T1", Name: "Team"})
	mkEmployee(t, pool, emp, "E001")
	if _, err := AddMembership(ctx(t), pool, emp, tm.Code, "E001", "agent", "2026-01-01", "test"); err != nil {
		t.Fatalf("membership: %v", err)
	}
	r, _ := CreateRoster(ctx(t), pool, emp, RosterInput{Programme: p.Code, WeekStarting: "2026-08-03"})
	tpl, _ := CreateTemplate(ctx(t), pool, emp, TemplateInput{Code: "DAY", Name: "Day", StartLocal: "09:00", EndLocal: "18:00"})
	if _, err := Assign(ctx(t), pool, emp, r.ID, AssignmentInput{Employee: "E001", Date: "2026-08-03", Template: tpl.Code}); err != nil {
		t.Fatalf("assign: %v", err)
	}
	if _, err := Publish(ctx(t), pool, emp, r.ID, "test-op"); err != nil {
		t.Fatalf("publish: %v", err)
	}
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			_, errs[n] = Revise(ctx(t), pool, emp, r.ID, "race", "test-op")
		}(i)
	}
	wg.Wait()
	wins := 0
	for _, err := range errs {
		if err == nil {
			wins++
			continue
		}
		var ae *apperr.Error
		if !errors.As(err, &ae) || (ae.Code != apperr.CodeInvalidState && ae.Code != apperr.CodeConflict) {
			t.Fatalf("loser error = %v, want invalid_state or conflict", err)
		}
	}
	if wins != 1 {
		t.Fatalf("wins = %d, want exactly one revision", wins)
	}
	var status string
	var version int
	if err := pool.QueryRow(ctx(t),
		`SELECT status, current_version FROM roster WHERE id = $1`, r.ID).Scan(&status, &version); err != nil {
		t.Fatalf("roster: %v", err)
	}
	if status != "draft" || version != 2 {
		t.Fatalf("status=%s version=%d, want draft/v2", status, version)
	}
}

func TestPublishMetadataInAudit(t *testing.T) {
	pool := testPool(t)
	emp := mkEmployer(t, pool, uniq(t, "E"))
	c, _ := CreateClient(ctx(t), pool, emp, "CLI", "Client")
	p, _ := CreateProgramme(ctx(t), pool, emp, ProgrammeInput{Client: c.Code, Code: "PRG", Name: "Prog"})
	tm, _ := CreateTeam(ctx(t), pool, emp, TeamInput{Programme: p.Code, Code: "T1", Name: "Team"})
	mkEmployee(t, pool, emp, "E001")
	if _, err := AddMembership(ctx(t), pool, emp, tm.Code, "E001", "agent", "2026-01-01", "test"); err != nil {
		t.Fatalf("membership: %v", err)
	}
	r, _ := CreateRoster(ctx(t), pool, emp, RosterInput{Programme: p.Code, WeekStarting: "2026-08-03"})
	tpl, _ := CreateTemplate(ctx(t), pool, emp, TemplateInput{Code: "DAY", Name: "Day", StartLocal: "09:00", EndLocal: "18:00"})
	if _, err := Assign(ctx(t), pool, emp, r.ID, AssignmentInput{Employee: "E001", Date: "2026-08-03", Template: tpl.Code}); err != nil {
		t.Fatalf("assign: %v", err)
	}
	if _, err := Publish(ctx(t), pool, emp, r.ID, "test-op"); err != nil {
		t.Fatalf("publish: %v", err)
	}
	// The revision row stays insert-only: its published_* columns are
	// never backfilled, so no UPDATE privilege or trigger exemption is
	// needed to record a publication.
	var filled int
	if err := pool.QueryRow(ctx(t),
		`SELECT count(*) FROM roster_revision
		 WHERE employer_id = $1 AND roster_id = $2 AND (published_at IS NOT NULL OR published_by IS NOT NULL)`,
		emp, r.ID).Scan(&filled); err != nil || filled != 0 {
		t.Fatalf("filled=%d err=%v want the revision row untouched", filled, err)
	}
	// The publication facts live in the audit event instead.
	var after string
	if err := pool.QueryRow(ctx(t),
		`SELECT after_state::text FROM audit_event
		 WHERE employer_id = $1 AND action = 'roster.publish' AND entity_id = $2`,
		emp, r.ID).Scan(&after); err != nil {
		t.Fatalf("audit: %v", err)
	}
	for _, want := range []string{"published", "revision", "checks"} {
		if !strings.Contains(after, want) {
			t.Fatalf("after_state=%s missing %q", after, want)
		}
	}
}
