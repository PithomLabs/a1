package payroll

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/PithomLabs/a1/internal/hr"
	"github.com/PithomLabs/a1/internal/timesheet"
)

// snapSetup builds an employer, an August period and a draft run.
func snapSetup(t *testing.T, pool *pgxpool.Pool) (emp, period, run string) {
	t.Helper()
	return snapSetupSpan(t, pool, "2026-08-01", "2026-08-31")
}

// snapSetupSpan builds an employer, a period over [start, end] and a
// draft run. Status fixtures use a span around tomorrow so
// in-period status changes stay after today's seeded status row.
func snapSetupSpan(t *testing.T, pool *pgxpool.Pool, start, end string) (emp, period, run string) {
	t.Helper()
	code := fmt.Sprintf("snap_%d", time.Now().UnixNano())
	if err := pool.QueryRow(ctx(t),
		`INSERT INTO employer (code, name, uen, address) VALUES ($1, $2, $3, 'addr') RETURNING id`,
		code, code, "UEN-"+code).Scan(&emp); err != nil {
		t.Fatalf("employer: %v", err)
	}
	if err := pool.QueryRow(ctx(t),
		`INSERT INTO pay_period (employer_id, code, starts_on, ends_on, pay_date, salary_deadline, ot_deadline)
		 VALUES ($1, 'PP', $2::date, $3::date, ($3::date + 7)::date, ($3::date + 7)::date, ($3::date + 14)::date)
		 RETURNING id`, emp, start, end).Scan(&period); err != nil {
		t.Fatalf("period: %v", err)
	}
	if _, err := pool.Exec(ctx(t),
		`INSERT INTO pay_code (code, name, kind, default_treatment) VALUES ('REG', 'Regular', 'earning', 'OW')
		 ON CONFLICT (code) DO NOTHING`); err != nil {
		t.Fatalf("pay_code: %v", err)
	}
	r, err := CreateRun(ctx(t), pool, emp, period, "regular", "", "test-op")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	return emp, period, r.ID
}

// mkSnapEmployee builds a complete approvable employee: January terms,
// one allowance, an approved sheet with one REG line, and an active
// SHG instruction. It returns the employee id and terms id.
func mkSnapEmployee(t *testing.T, pool *pgxpool.Pool, emp, period string) (empID, termsID string) {
	t.Helper()
	no := fmt.Sprintf("SN%06d", time.Now().UnixNano()%1000000)
	e, err := hr.CreateEmployee(ctx(t), pool, emp, hr.EmployeeInput{
		EmployeeNo: no, LegalName: no, Address: "a",
		Gender: "F", DateOfBirth: "1990-01-01", Citizenship: "SG",
	}, "test")
	if err != nil {
		t.Fatalf("employee: %v", err)
	}
	tm, err := hr.SetTerms(ctx(t), pool, emp, e.ID, hr.TermsInput{
		EffectiveFrom: "2026-01-01", JobTitle: "Agent", JobDuties: "support",
		EmploymentCategory: "workman", PartIVEligible: true, EligibilityBasis: "test",
		MonthlyBasic: "2600", WorkingArrangement: "shift", NormalHoursWeekly: 44,
		WorkingDays: "Mon-Fri", DailyHours: 8.8, RestDay: "Sunday",
		StartDate: "2026-01-01", ContractType: "permanent",
		PlaceOfWork: "office", SalaryPeriod: "monthly",
	}, "test-op")
	if err != nil {
		t.Fatalf("terms: %v", err)
	}
	if _, err := hr.AddCompensation(ctx(t), pool, emp, tm.ID, hr.CompensationInput{
		Kind: "allowance", Code: "SHIFT", AmountMonthly: "100.00",
		EffectiveFrom: "2026-01-01", Reason: "shift allowance",
	}, "test-op"); err != nil {
		t.Fatalf("comp: %v", err)
	}
	// SHG predates the approval: evidence recorded after approval is
	// a genuine post-approval conflict, not fixture order.
	amt := "fixed"
	if _, err := hr.SetSHG(ctx(t), pool, emp, e.ID, hr.SHGInput{
		Fund: "CDAC", Status: "active", AmountOrBasis: &amt,
		SourceDeclaration: "declared", EffectiveFrom: "2026-01-01",
	}, "test-op"); err != nil {
		t.Fatalf("shg: %v", err)
	}
	var sheet string
	if err := pool.QueryRow(ctx(t),
		`INSERT INTO timesheet (employer_id, employee_id, pay_period_id, status)
		 VALUES ($1, $2, $3, 'draft') RETURNING id`,
		emp, e.ID, period).Scan(&sheet); err != nil {
		t.Fatalf("sheet: %v", err)
	}
	if _, err := pool.Exec(ctx(t),
		`INSERT INTO timesheet_line (id, employer_id, timesheet_id, pay_code, hours, hours_us, pay_period_id)
		 VALUES (gen_random_uuid(), $1, $2, 'REG', 8, 28800000000, $3)`, emp, sheet, period); err != nil {
		t.Fatalf("line: %v", err)
	}
	if _, err := timesheet.Submit(ctx(t), pool, emp, sheet, "test"); err != nil {
		t.Fatalf("submit: %v", err)
	}
	if _, err := timesheet.Approve(ctx(t), pool, emp, sheet, "test-op"); err != nil {
		t.Fatalf("approve: %v", err)
	}
	return e.ID, tm.ID
}

// implementPacks marks the four base packs IMPLEMENTABLE as admin.
func implementPacks(t *testing.T, admin *pgxpool.Pool, emp string) {
	t.Helper()
	for _, p := range BasePacks {
		if _, err := admin.Exec(ctx(t),
			`INSERT INTO rule_pack_activation (employer_id, pack, version, status, activated_at)
			 VALUES ($1, $2, 'v0', 'IMPLEMENTABLE', now())
			 ON CONFLICT (employer_id, pack) DO UPDATE SET status = 'IMPLEMENTABLE'`, emp, p); err != nil {
			t.Fatalf("pack %s: %v", p, err)
		}
	}
}

func TestCaptureHappyPath(t *testing.T) {
	pool, admin := testPoolAndAdmin(t)
	emp, _, run := snapSetup(t, pool)
	e1, _ := mkSnapEmployee(t, pool, emp, mustPeriod(t, pool, emp))
	e2, _ := mkSnapEmployee(t, pool, emp, mustPeriod(t, pool, emp))
	implementPacks(t, admin, emp)
	snap, err := CaptureSnapshot(ctx(t), pool, emp, run, "test-op")
	if err != nil {
		t.Fatalf("capture: %v", err)
	}
	if snap.Version != 1 || snap.Status != "valid" || snap.SchemaVersion != SnapshotSchemaVersion {
		t.Fatalf("snapshot=%+v", snap)
	}
	if len(snap.ContentHash) != 64 {
		t.Fatalf("hash=%q want 64 hex chars", snap.ContentHash)
	}
	detail, err := GetSnapshot(ctx(t), pool, snap.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(detail.Employees) != 2 {
		t.Fatalf("employees=%d want 2", len(detail.Employees))
	}
	byID := map[string]SnapshotEmployee{}
	for _, se := range detail.Employees {
		byID[se.EmployeeID] = se
	}
	for _, id := range []string{e1, e2} {
		se, ok := byID[id]
		if !ok {
			t.Fatalf("employee %s missing from snapshot", id)
		}
		if se.Decision != "included" {
			t.Fatalf("employee %s decision=%s blockers=%v", id, se.Decision, se.Blockers)
		}
		if se.ApprovedSheetID == nil {
			t.Fatalf("employee %s has no approved sheet pin", id)
		}
		if len(se.TermsIDs) != 1 {
			t.Fatalf("employee %s terms=%v", id, se.TermsIDs)
		}
		if se.BasisSeq <= 0 {
			t.Fatalf("employee %s basis_seq=%d", id, se.BasisSeq)
		}
		if len(se.CompSegments) != 1 || se.CompSegments[0].Code != "allowance:SHIFT" {
			t.Fatalf("employee %s segments=%+v", id, se.CompSegments)
		}
		if len(se.SHGTips) != 1 || se.SHGTips[0].Status != "active" {
			t.Fatalf("employee %s shg=%+v", id, se.SHGTips)
		}
	}
	if len(detail.Packs) != len(BasePacks) {
		t.Fatalf("packs=%+v want the required set", detail.Packs)
	}
	for _, p := range detail.Packs {
		if p.Status != "IMPLEMENTABLE" {
			t.Fatalf("pack=%+v", p)
		}
	}
	// Re-read persisted state directly: the header hash matches.
	var hash string
	if err := pool.QueryRow(ctx(t),
		`SELECT content_hash FROM payroll_snapshot WHERE id = $1`, snap.ID).Scan(&hash); err != nil || hash != snap.ContentHash {
		t.Fatalf("persisted hash=%q err=%v", hash, err)
	}
}

// mkDraftEmployee builds terms plus a draft sheet with one REG
// line, but no compensation and no SHG: variant tests add inputs,
// then approve, so nothing postdates the approval.
func mkDraftEmployee(t *testing.T, pool *pgxpool.Pool, emp, period string) (empID, termsID, sheet string) {
	t.Helper()
	no := fmt.Sprintf("SN%06d", time.Now().UnixNano()%1000000)
	e, err := hr.CreateEmployee(ctx(t), pool, emp, hr.EmployeeInput{
		EmployeeNo: no, LegalName: no, Address: "a",
		Gender: "F", DateOfBirth: "1990-01-01", Citizenship: "SG",
	}, "test")
	if err != nil {
		t.Fatalf("employee: %v", err)
	}
	tm, err := hr.SetTerms(ctx(t), pool, emp, e.ID, hr.TermsInput{
		EffectiveFrom: "2026-01-01", JobTitle: "Agent", JobDuties: "support",
		EmploymentCategory: "workman", PartIVEligible: true, EligibilityBasis: "test",
		MonthlyBasic: "2600", WorkingArrangement: "shift", NormalHoursWeekly: 44,
		WorkingDays: "Mon-Fri", DailyHours: 8.8, RestDay: "Sunday",
		StartDate: "2026-01-01", ContractType: "permanent",
		PlaceOfWork: "office", SalaryPeriod: "monthly",
	}, "test-op")
	if err != nil {
		t.Fatalf("terms: %v", err)
	}
	if err := pool.QueryRow(ctx(t),
		`INSERT INTO timesheet (employer_id, employee_id, pay_period_id, status)
		 VALUES ($1, $2, $3, 'draft') RETURNING id`,
		emp, e.ID, period).Scan(&sheet); err != nil {
		t.Fatalf("sheet: %v", err)
	}
	if _, err := pool.Exec(ctx(t),
		`INSERT INTO timesheet_line (id, employer_id, timesheet_id, pay_code, hours, hours_us, pay_period_id)
		 VALUES (gen_random_uuid(), $1, $2, 'REG', 8, 28800000000, $3)`, emp, sheet, period); err != nil {
		t.Fatalf("line: %v", err)
	}
	return e.ID, tm.ID, sheet
}

// approveEmp submits and approves one draft sheet.
func approveEmp(t *testing.T, pool *pgxpool.Pool, emp, sheet string) {
	t.Helper()
	if _, err := timesheet.Submit(ctx(t), pool, emp, sheet, "test"); err != nil {
		t.Fatalf("submit: %v", err)
	}
	if _, err := timesheet.Approve(ctx(t), pool, emp, sheet, "test-op"); err != nil {
		t.Fatalf("approve: %v", err)
	}
}

// mkBareEmployee builds terms plus an approved sheet with one REG
// line, but no compensation and no SHG.
func mkBareEmployee(t *testing.T, pool *pgxpool.Pool, emp, period string) (empID, termsID string) {
	t.Helper()
	empID, termsID, sheet := mkDraftEmployee(t, pool, emp, period)
	approveEmp(t, pool, emp, sheet)
	return empID, termsID
}

func TestCaptureInputVariants(t *testing.T) {
	pool, admin := testPoolAndAdmin(t)
	emp, _, run := snapSetup(t, pool)
	period := mustPeriod(t, pool, emp)
	implementPacks(t, admin, emp)
	addComp := func(termsID string) {
		t.Helper()
		if _, err := hr.AddCompensation(ctx(t), pool, emp, termsID, hr.CompensationInput{
			Kind: "allowance", Code: "SHIFT", AmountMonthly: "100.00",
			EffectiveFrom: "2026-01-01", Reason: "shift",
		}, "test-op"); err != nil {
			t.Fatalf("comp: %v", err)
		}
	}
	addSHG := func(empID, status string, triple bool) {
		t.Helper()
		in := hr.SHGInput{Fund: "CDAC", Status: status,
			SourceDeclaration: "declared", EffectiveFrom: "2026-01-01"}
		if triple {
			at, by, basis := "2026-08-02T10:00:00Z", "verifier-1", "form on file"
			in.VerifiedAt, in.VerifiedBy, in.VerificationBasis = &at, &by, &basis
		}
		_ = empID
		var no string
		if err := pool.QueryRow(ctx(t), `SELECT employee_no FROM employee WHERE id = $1`, empID).Scan(&no); err != nil {
			t.Fatalf("no: %v", err)
		}
		if _, err := hr.SetSHG(ctx(t), pool, emp, no, in, "test-op"); err != nil {
			t.Fatalf("shg: %v", err)
		}
	}
	// Inputs land before approvals, so no post-approval conflict
	// masks the variant under test.
	// A: SHG but no compensation lines — includable, empty segments.
	aID, _, aSheet := mkDraftEmployee(t, pool, emp, period)
	addSHG(aID, "active", false)
	approveEmp(t, pool, emp, aSheet)
	// B: compensation but no SHG — incomplete with the SHG code.
	_, bTerms, bSheet := mkDraftEmployee(t, pool, emp, period)
	addComp(bTerms)
	approveEmp(t, pool, emp, bSheet)
	// C: verified-none SHG with its evidence triple — covered.
	cID, cTerms, cSheet := mkDraftEmployee(t, pool, emp, period)
	addComp(cTerms)
	addSHG(cID, "verified_none", true)
	approveEmp(t, pool, emp, cSheet)
	snap, err := CaptureSnapshot(ctx(t), pool, emp, run, "test-op")
	if err != nil {
		t.Fatalf("capture: %v", err)
	}
	detail, err := GetSnapshot(ctx(t), pool, snap.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	byID := map[string]SnapshotEmployee{}
	for _, se := range detail.Employees {
		byID[se.EmployeeID] = se
	}
	if got := byID[aID]; got.Decision != "included" || len(got.CompSegments) != 0 {
		t.Fatalf("no-comp: decision=%s segments=%+v", got.Decision, got.CompSegments)
	}
	gotB := byID[mustID(t, pool, emp, bTerms)]
	if gotB.Decision != "incomplete" || len(gotB.Blockers) != 1 ||
		!strings.HasPrefix(gotB.Blockers[0], "SHG_INSTRUCTION_MISSING") {
		t.Fatalf("no-shg: decision=%s blockers=%v", gotB.Decision, gotB.Blockers)
	}
	if got := byID[cID]; got.Decision != "included" {
		t.Fatalf("verified-none: decision=%s blockers=%v", got.Decision, got.Blockers)
	} else if len(got.SHGTips) != 1 || !got.SHGTips[0].Tripled {
		t.Fatalf("verified-none: tips=%+v want the evidenced tip", got.SHGTips)
	}
}

// mustID resolves the employee owning one terms row.
func mustID(t *testing.T, pool *pgxpool.Pool, emp, termsID string) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(ctx(t),
		`SELECT employee_id FROM employment_terms WHERE employer_id = $1 AND id = $2`,
		emp, termsID).Scan(&id); err != nil {
		t.Fatalf("terms owner: %v", err)
	}
	return id
}

func TestCaptureEmptyPopulation(t *testing.T) {
	pool, admin := testPoolAndAdmin(t)
	emp, _, run := snapSetup(t, pool)
	implementPacks(t, admin, emp)
	// No employees at all: capture still pins the required packs and
	// hashes the empty set. Refusing a vacuous validation is P1's job.
	snap, err := CaptureSnapshot(ctx(t), pool, emp, run, "test-op")
	if err != nil {
		t.Fatalf("capture: %v", err)
	}
	detail, err := GetSnapshot(ctx(t), pool, snap.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(detail.Employees) != 0 {
		t.Fatalf("employees=%d want none", len(detail.Employees))
	}
	if len(detail.Packs) != len(BasePacks) {
		t.Fatalf("packs=%+v want the base set pinned", detail.Packs)
	}
	if len(snap.ContentHash) != 64 {
		t.Fatalf("hash=%q", snap.ContentHash)
	}
}

func mustPeriod(t *testing.T, pool *pgxpool.Pool, emp string) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(ctx(t),
		`SELECT id FROM pay_period WHERE employer_id = $1 AND code = 'PP'`, emp).Scan(&id); err != nil {
		t.Fatalf("period: %v", err)
	}
	return id
}

func TestCaptureHashDeterminism(t *testing.T) {
	pool, admin := testPoolAndAdmin(t)
	emp, _, run := snapSetup(t, pool)
	period := mustPeriod(t, pool, emp)
	mkSnapEmployee(t, pool, emp, period)
	mkSnapEmployee(t, pool, emp, period)
	implementPacks(t, admin, emp)
	first, err := CaptureSnapshot(ctx(t), pool, emp, run, "test-op")
	if err != nil {
		t.Fatalf("capture: %v", err)
	}
	// Invalidate and recapture with no source change: identical
	// canonical inputs must produce the identical content hash, under
	// a new version.
	if err := InvalidateSnapshot(ctx(t), pool, first.ID, "revalidation drill", "test-op"); err != nil {
		t.Fatalf("invalidate: %v", err)
	}
	second, err := CaptureSnapshot(ctx(t), pool, emp, run, "test-op")
	if err != nil {
		t.Fatalf("recapture: %v", err)
	}
	if second.Version != 2 || second.Status != "valid" {
		t.Fatalf("recapture=%+v", second)
	}
	if second.ContentHash != first.ContentHash {
		t.Fatalf("hash changed without input change:\n%s\n%s", first.ContentHash, second.ContentHash)
	}
	// The original snapshot is preserved, marked invalidated with its reason.
	orig, err := GetSnapshot(ctx(t), pool, first.ID)
	if err != nil {
		t.Fatalf("get original: %v", err)
	}
	if orig.Status != "invalidated" || orig.InvalidatedReason == nil ||
		*orig.InvalidatedReason != "revalidation drill" {
		t.Fatalf("original=%+v", orig.Snapshot)
	}
	if orig.ContentHash != first.ContentHash || len(orig.Employees) != 2 {
		t.Fatalf("original mutated: %+v", orig.Snapshot)
	}
}

func TestCaptureDifferentInputsDifferentHashes(t *testing.T) {
	pool, admin := testPoolAndAdmin(t)
	emp, _, run1 := snapSetup(t, pool)
	period := mustPeriod(t, pool, emp)
	mkSnapEmployee(t, pool, emp, period)
	implementPacks(t, admin, emp)
	r2, err := CreateRun(ctx(t), pool, emp, period, "regular", "", "test-op")
	if err != nil {
		t.Fatalf("run2: %v", err)
	}
	s1, err := CaptureSnapshot(ctx(t), pool, emp, run1, "test-op")
	if err != nil {
		t.Fatalf("capture1: %v", err)
	}
	// Same employer and period, one more employee: the hash must differ.
	mkSnapEmployee(t, pool, emp, period)
	s2, err := CaptureSnapshot(ctx(t), pool, emp, r2.ID, "test-op")
	if err != nil {
		t.Fatalf("capture2: %v", err)
	}
	if s1.ContentHash == s2.ContentHash {
		t.Fatal("different inputs share a content hash")
	}
}

func TestCaptureRecordsBlockers(t *testing.T) {
	pool, _ := testPoolAndAdmin(t)
	emp, _, run := snapSetup(t, pool)
	// Employee with terms but no timesheet.
	no := fmt.Sprintf("SN%06d", time.Now().UnixNano()%1000000)
	e, err := hr.CreateEmployee(ctx(t), pool, emp, hr.EmployeeInput{
		EmployeeNo: no, LegalName: no, Address: "a",
		Gender: "F", DateOfBirth: "1990-01-01", Citizenship: "SG",
	}, "test")
	if err != nil {
		t.Fatalf("employee: %v", err)
	}
	if _, err := hr.SetTerms(ctx(t), pool, emp, e.ID, hr.TermsInput{
		EffectiveFrom: "2026-01-01", JobTitle: "Agent", JobDuties: "support",
		EmploymentCategory: "workman", PartIVEligible: true, EligibilityBasis: "test",
		MonthlyBasic: "2600", WorkingArrangement: "shift", NormalHoursWeekly: 44,
		WorkingDays: "Mon-Fri", DailyHours: 8.8, RestDay: "Sunday",
		StartDate: "2026-01-01", ContractType: "permanent",
		PlaceOfWork: "office", SalaryPeriod: "monthly",
	}, "test-op"); err != nil {
		t.Fatalf("terms: %v", err)
	}
	// Two packs noted BLOCKED through the app-safe default, two left
	// without activation rows at all: both pin faithfully.
	for _, p := range []string{"SG-CPF-2026", "SG-SDL"} {
		if _, err := pool.Exec(ctx(t), `SELECT ensure_pack_blocked($1::uuid, $2)`, emp, p); err != nil {
			t.Fatalf("ensure %s: %v", p, err)
		}
	}
	snap, err := CaptureSnapshot(ctx(t), pool, emp, run, "test-op")
	if err != nil {
		t.Fatalf("capture records blockers instead of failing: %v", err)
	}
	detail, err := GetSnapshot(ctx(t), pool, snap.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(detail.Employees) != 1 {
		t.Fatalf("employees=%d want the terms-only employee", len(detail.Employees))
	}
	se := detail.Employees[0]
	if se.Decision != "missing-timesheet" {
		t.Fatalf("decision=%s blockers=%v", se.Decision, se.Blockers)
	}
	// BLOCKED and missing activation rows are recorded as pack pins,
	// not gaps.
	statuses := map[string]string{}
	for _, p := range detail.Packs {
		statuses[p.Pack] = p.Status
	}
	if statuses["SG-CPF-2026"] != "BLOCKED" || statuses["SG-SDL"] != "BLOCKED" {
		t.Fatalf("packs=%+v want BLOCKED pins", detail.Packs)
	}
	if statuses["SG-SHG"] != "not activated" || statuses["SG-MOM-KET"] != "not activated" {
		t.Fatalf("packs=%+v want not-activated pins", detail.Packs)
	}
}

func TestCaptureTerminatedEmployeeRetained(t *testing.T) {
	pool, admin := testPoolAndAdmin(t)
	emp, _, run := snapSetup(t, pool)
	period := mustPeriod(t, pool, emp)
	eID, _ := mkSnapEmployee(t, pool, emp, period)
	implementPacks(t, admin, emp)
	// Terminate after the pay period (and after today, which seeds
	// the initial status row): the historical population keeps the
	// employee.
	if _, err := hr.SetStatus(ctx(t), pool, emp, eID, "terminated", "2026-12-01", "resigned", "test-op"); err != nil {
		t.Fatalf("terminate: %v", err)
	}
	snap, err := CaptureSnapshot(ctx(t), pool, emp, run, "test-op")
	if err != nil {
		t.Fatalf("capture: %v", err)
	}
	detail, err := GetSnapshot(ctx(t), pool, snap.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(detail.Employees) != 1 || detail.Employees[0].EmployeeID != eID {
		t.Fatalf("employees=%+v want the terminated employee retained", detail.Employees)
	}
}

func TestCaptureExclusionRecorded(t *testing.T) {
	pool, admin := testPoolAndAdmin(t)
	emp, _, run := snapSetup(t, pool)
	period := mustPeriod(t, pool, emp)
	eID, _ := mkSnapEmployee(t, pool, emp, period)
	implementPacks(t, admin, emp)
	if _, err := AddExclusion(ctx(t), pool, emp, run, eID, "contractor payroll", "test-op"); err != nil {
		t.Fatalf("exclude: %v", err)
	}
	snap, err := CaptureSnapshot(ctx(t), pool, emp, run, "test-op")
	if err != nil {
		t.Fatalf("capture: %v", err)
	}
	detail, err := GetSnapshot(ctx(t), pool, snap.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(detail.Employees) != 1 || detail.Employees[0].Decision != "excluded" {
		t.Fatalf("employees=%+v want one excluded row", detail.Employees)
	}
	// A second capture attempt while a valid snapshot stands is refused.
	if _, err := CaptureSnapshot(ctx(t), pool, emp, run, "test-op"); err == nil {
		t.Fatal("second capture over a valid snapshot accepted")
	} else if !strings.Contains(err.Error(), "already") && !strings.Contains(err.Error(), "conflict") {
		t.Fatalf("err=%v want already-captured refusal", err)
	}
}

func TestCaptureSupersededCompExcluded(t *testing.T) {
	pool, admin := testPoolAndAdmin(t)
	emp, _, run := snapSetup(t, pool)
	period := mustPeriod(t, pool, emp)
	eID, termsID := mkSnapEmployee(t, pool, emp, period)
	implementPacks(t, admin, emp)
	comps, err := hr.ListCompensation(ctx(t), pool, emp, termsID)
	if err != nil || len(comps) != 1 {
		t.Fatalf("comps=%+v err=%v", comps, err)
	}
	if _, err := hr.CorrectCompensation(ctx(t), pool, emp, comps[0].ID, hr.CorrectionInput{
		AmountMonthly: "120.00", EffectiveFrom: "2026-01-01", Reason: "raise",
	}, "test-op"); err != nil {
		t.Fatalf("correct: %v", err)
	}
	snap, err := CaptureSnapshot(ctx(t), pool, emp, run, "test-op")
	if err != nil {
		t.Fatalf("capture: %v", err)
	}
	detail, err := GetSnapshot(ctx(t), pool, snap.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	_ = eID
	segs := detail.Employees[0].CompSegments
	if len(segs) != 1 || segs[0].Amount != "120.00" {
		t.Fatalf("segments=%+v want the successor tip only", segs)
	}
	if len(segs[0].Lineage) != 2 {
		t.Fatalf("lineage=%v want original+successor", segs[0].Lineage)
	}
}

func TestDriftDetection(t *testing.T) {
	pool, admin := testPoolAndAdmin(t)
	emp, _, run := snapSetup(t, pool)
	period := mustPeriod(t, pool, emp)
	eID, termsID := mkSnapEmployee(t, pool, emp, period)
	implementPacks(t, admin, emp)
	snap, err := CaptureSnapshot(ctx(t), pool, emp, run, "test-op")
	if err != nil {
		t.Fatalf("capture: %v", err)
	}
	clean, err := DetectDrift(ctx(t), pool, emp, snap.ID)
	if err != nil {
		t.Fatalf("drift: %v", err)
	}
	if clean.Drifted {
		t.Fatalf("reasons=%v want no drift immediately after capture", clean.Reasons)
	}
	// A post-snapshot compensation correction is drift, not a rewrite.
	comps, err := hr.ListCompensation(ctx(t), pool, emp, termsID)
	if err != nil {
		t.Fatalf("comps: %v", err)
	}
	if _, err := hr.CorrectCompensation(ctx(t), pool, emp, comps[0].ID, hr.CorrectionInput{
		AmountMonthly: "150.00", EffectiveFrom: "2026-01-01", Reason: "late raise",
	}, "test-op"); err != nil {
		t.Fatalf("correct: %v", err)
	}
	drifted, err := DetectDrift(ctx(t), pool, emp, snap.ID)
	if err != nil {
		t.Fatalf("drift: %v", err)
	}
	if !drifted.Drifted {
		t.Fatal("compensation change undetected")
	}
	joined := strings.Join(drifted.Reasons, "\n")
	if !strings.Contains(joined, "compensation") {
		t.Fatalf("reasons=%v want the compensation section named", drifted.Reasons)
	}
	// The snapshot itself is untouched.
	detail, err := GetSnapshot(ctx(t), pool, snap.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if detail.ContentHash != snap.ContentHash || detail.Employees[0].CompSegments[0].Amount != "100.00" {
		t.Fatalf("snapshot rewritten: %+v", detail.Employees[0].CompSegments)
	}
	_ = eID
}

func TestSnapshotDirectMutationRejected(t *testing.T) {
	pool, admin := testPoolAndAdmin(t)
	emp, _, run := snapSetup(t, pool)
	period := mustPeriod(t, pool, emp)
	mkSnapEmployee(t, pool, emp, period)
	implementPacks(t, admin, emp)
	snap, err := CaptureSnapshot(ctx(t), pool, emp, run, "test-op")
	if err != nil {
		t.Fatalf("capture: %v", err)
	}
	if _, err := pool.Exec(ctx(t),
		`UPDATE payroll_snapshot SET content_hash = 'x' WHERE id = $1`, snap.ID); err == nil {
		t.Fatal("snapshot UPDATE as app accepted")
	}
	if _, err := pool.Exec(ctx(t),
		`DELETE FROM payroll_snapshot_employee WHERE snapshot_id = $1`, snap.ID); err == nil {
		t.Fatal("snapshot child DELETE as app accepted")
	}
	if _, err := pool.Exec(ctx(t),
		`UPDATE payroll_snapshot_pack SET version = 'v9' WHERE snapshot_id = $1`, snap.ID); err == nil {
		t.Fatal("pack pin UPDATE as app accepted")
	}
}
