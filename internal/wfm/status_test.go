package wfm

import (
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/PithomLabs/a1/internal/hr"
)

// statusChain builds programme/team/template/roster plus one employee
// hired in January, so August roster dates fall inside effective
// employment unless a test changes status.
func statusChain(t *testing.T, pool *pgxpool.Pool, tag, week string) (emp, rosterID string) {
	t.Helper()
	emp = mkEmployer(t, pool, uniq(t, tag))
	c, err := CreateClient(ctx(t), pool, emp, "CLI", "Client")
	if err != nil {
		t.Fatalf("client: %v", err)
	}
	p, err := CreateProgramme(ctx(t), pool, emp, ProgrammeInput{Client: c.Code, Code: "PRG", Name: "Prog"})
	if err != nil {
		t.Fatalf("programme: %v", err)
	}
	tm, err := CreateTeam(ctx(t), pool, emp, TeamInput{Programme: p.Code, Code: "T1", Name: "Team"})
	if err != nil {
		t.Fatalf("team: %v", err)
	}
	e, err := hr.CreateEmployee(ctx(t), pool, emp, hr.EmployeeInput{
		EmployeeNo: "E001", LegalName: "E001", Address: "a",
		Gender: "F", DateOfBirth: "1990-01-01", Citizenship: "SG",
		Status: hr.StatusActive, StatusEffectiveDate: "2026-01-01",
	}, "test")
	if err != nil {
		t.Fatalf("employee: %v", err)
	}
	if _, err := AddMembership(ctx(t), pool, emp, tm.Code, e.EmployeeNo, "agent", "2026-01-01", "test"); err != nil {
		t.Fatalf("membership: %v", err)
	}
	tpl, err := CreateTemplate(ctx(t), pool, emp, TemplateInput{
		Code: "DAY", Name: "Day", StartLocal: "09:00", EndLocal: "18:00",
		Segments: []SegmentInput{{Kind: "work", StartOffsetMin: 0, DurationMin: 540, Paid: true}},
	})
	if err != nil {
		t.Fatalf("template: %v", err)
	}
	_ = tpl
	r, err := CreateRoster(ctx(t), pool, emp, RosterInput{Programme: p.Code, WeekStarting: week})
	if err != nil {
		t.Fatalf("roster: %v", err)
	}
	return emp, r.ID
}

func assign(t *testing.T, pool *pgxpool.Pool, emp, rosterID, date string) error {
	t.Helper()
	_, err := Assign(ctx(t), pool, emp, rosterID,
		AssignmentInput{Employee: "E001", Date: date, Template: "DAY"})
	return err
}

func setStatus(t *testing.T, pool *pgxpool.Pool, emp, status, eff string) {
	t.Helper()
	var id string
	if err := pool.QueryRow(ctx(t),
		`SELECT id FROM employee WHERE employer_id = $1 AND employee_no = 'E001'`,
		emp).Scan(&id); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if _, err := hr.SetStatus(ctx(t), pool, emp, id, status, eff, "test", "test-op"); err != nil {
		t.Fatalf("set %s: %v", status, err)
	}
}

func TestAssignActiveEmployee(t *testing.T) {
	pool := testPool(t)
	emp, roster := statusChain(t, pool, "SA", "2026-08-03")
	if err := assign(t, pool, emp, roster, "2026-08-03"); err != nil {
		t.Fatalf("assign: %v", err)
	}
	if _, err := Publish(ctx(t), pool, emp, roster, "test-op"); err != nil {
		t.Fatalf("publish: %v", err)
	}
}

func TestAssignSuspendedEmployee(t *testing.T) {
	pool := testPool(t)
	emp, roster := statusChain(t, pool, "SS", "2026-08-03")
	setStatus(t, pool, emp, hr.StatusSuspended, "2026-08-01")
	if err := assign(t, pool, emp, roster, "2026-08-03"); err == nil {
		t.Fatalf("suspended employee must not be scheduled")
	} else if !strings.Contains(err.Error(), "suspended") {
		t.Fatalf("err=%v want suspended named", err)
	}
}

func TestAssignResignedEmployee(t *testing.T) {
	pool := testPool(t)
	emp, roster := statusChain(t, pool, "SR", "2026-08-03")
	setStatus(t, pool, emp, hr.StatusResigned, "2026-08-05")
	if err := assign(t, pool, emp, roster, "2026-08-03"); err != nil {
		t.Fatalf("pre-resignation assign: %v", err)
	}
	if err := assign(t, pool, emp, roster, "2026-08-05"); err == nil {
		t.Fatalf("resignation-day assign must fail")
	} else if !strings.Contains(err.Error(), "resigned") {
		t.Fatalf("err=%v want resigned named", err)
	}
	if err := assign(t, pool, emp, roster, "2026-08-06"); err == nil {
		t.Fatalf("post-resignation assign must fail")
	}
}

func TestAssignTerminatedEmployee(t *testing.T) {
	pool := testPool(t)
	emp, roster := statusChain(t, pool, "ST", "2026-08-03")
	setStatus(t, pool, emp, hr.StatusTerminated, "2026-08-03")
	if err := assign(t, pool, emp, roster, "2026-08-03"); err == nil {
		t.Fatalf("termination-day assign must fail")
	} else if !strings.Contains(err.Error(), "terminated") {
		t.Fatalf("err=%v want terminated named", err)
	}
}

func TestAssignTerminatedAfterPeriod(t *testing.T) {
	pool := testPool(t)
	emp, roster := statusChain(t, pool, "SP", "2026-08-03")
	setStatus(t, pool, emp, hr.StatusTerminated, "2026-08-10")
	for _, d := range []string{"2026-08-03", "2026-08-09"} {
		if err := assign(t, pool, emp, roster, d); err != nil {
			t.Fatalf("assign %s: %v", d, err)
		}
	}
	if _, err := Publish(ctx(t), pool, emp, roster, "test-op"); err != nil {
		t.Fatalf("publish: %v", err)
	}
}

func TestAssignTerminatedDuringRoster(t *testing.T) {
	pool := testPool(t)
	emp, roster := statusChain(t, pool, "SD", "2026-08-03")
	if err := assign(t, pool, emp, roster, "2026-08-03"); err != nil {
		t.Fatalf("assign 08-03: %v", err)
	}
	if err := assign(t, pool, emp, roster, "2026-08-04"); err != nil {
		t.Fatalf("assign 08-04: %v", err)
	}
	setStatus(t, pool, emp, hr.StatusTerminated, "2026-08-05")
	if err := assign(t, pool, emp, roster, "2026-08-06"); err == nil {
		t.Fatalf("post-termination assign must fail")
	}
	// The pre-termination shifts stay valid and publishable.
	if _, err := Publish(ctx(t), pool, emp, roster, "test-op"); err != nil {
		t.Fatalf("publish: %v", err)
	}
}

func TestAssignHonorsCanonicalRestDay(t *testing.T) {
	pool := testPool(t)
	emp, roster := statusChain(t, pool, "SR2", "2026-08-03")
	var id string
	if err := pool.QueryRow(ctx(t),
		`SELECT id FROM employee WHERE employer_id = $1 AND employee_no = 'E001'`,
		emp).Scan(&id); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if _, err := hr.SetTerms(ctx(t), pool, emp, id, hr.TermsInput{
		EffectiveFrom: "2026-01-01", JobTitle: "Agent", JobDuties: "support",
		EmploymentCategory: "workman", PartIVEligible: true, EligibilityBasis: "test",
		MonthlyBasic: "2600", WorkingArrangement: "shift", NormalHoursWeekly: 44,
		WorkingDays: "Mon-Fri", DailyHours: 8.8, RestDay: "Sunday",
		StartDate: "2026-01-01", ContractType: "permanent",
		PlaceOfWork: "office", SalaryPeriod: "monthly",
	}, "test-op"); err != nil {
		t.Fatalf("terms: %v", err)
	}
	// 2026-08-09 is a Sunday: a shift assignment must refuse, a rest-day
	// assignment must pass.
	if err := assign(t, pool, emp, roster, "2026-08-09"); err == nil {
		t.Fatalf("Sunday shift must refuse for a Sunday rest day")
	} else if !strings.Contains(err.Error(), "rests on Sundays") {
		t.Fatalf("err=%v", err)
	}
	if _, err := Assign(ctx(t), pool, emp, roster,
		AssignmentInput{Employee: "E001", Date: "2026-08-09", RestDay: true}); err != nil {
		t.Fatalf("rest-day assign: %v", err)
	}
}

func TestPublishAfterStatusChange(t *testing.T) {
	pool := testPool(t)
	emp, roster := statusChain(t, pool, "SC", "2026-08-03")
	if err := assign(t, pool, emp, roster, "2026-08-03"); err != nil {
		t.Fatalf("assign 08-03: %v", err)
	}
	if err := assign(t, pool, emp, roster, "2026-08-04"); err != nil {
		t.Fatalf("assign 08-04: %v", err)
	}
	setStatus(t, pool, emp, hr.StatusSuspended, "2026-08-02")
	if _, err := Publish(ctx(t), pool, emp, roster, "test-op"); err == nil {
		t.Fatalf("publish must revalidate status")
	} else if !strings.Contains(err.Error(), "suspended") {
		t.Fatalf("err=%v want suspended named with the offending date", err)
	}
	var status string
	if err := pool.QueryRow(ctx(t),
		`SELECT status FROM roster WHERE employer_id = $1 AND id = $2::uuid`,
		emp, roster).Scan(&status); err != nil || status != "draft" {
		t.Fatalf("status=%q err=%v want draft", status, err)
	}
}
