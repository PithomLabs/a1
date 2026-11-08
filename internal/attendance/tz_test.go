package attendance

import (
	"strings"
	"testing"

	"github.com/PithomLabs/a1/internal/hr"
	"github.com/PithomLabs/a1/internal/timesheet"
)

// TestEmploymentTimezoneAttribution pins Singapore break classification
// for rostered SGT evidence. classifyBreak must anchor shift windows in
// the employment timezone, never the host process timezone: run the
// suite with TZ=UTC to force a host-dependent implementation to fail.
func TestEmploymentTimezoneAttribution(t *testing.T) {
	pool := testPool(t)
	emp, no := chain(t, pool, "E", "2026-08-03")
	csv := `source,source_event_id,employee_no,occurred_at,timezone,kind
vendor-x,tz-1,` + no + `,2026-08-03T09:00:00+08:00,Asia/Singapore,in
vendor-x,tz-2,` + no + `,2026-08-03T12:00:00+08:00,Asia/Singapore,break_start
vendor-x,tz-3,` + no + `,2026-08-03T13:00:00+08:00,Asia/Singapore,break_end
vendor-x,tz-4,` + no + `,2026-08-03T18:00:00+08:00,Asia/Singapore,out
`
	rep, err := Import(ctx(t), pool, emp, strings.NewReader(csv), "test-op", timesheet.RegenerateDraft, timesheet.ChainTerminal)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if rep.IntervalsBuilt != 3 {
		t.Fatalf("built=%d want 3 (work/break/work)", rep.IntervalsBuilt)
	}
	rows, err := pool.Query(ctx(t),
		`SELECT kind, local_date::text, timezone FROM attendance_interval
		 WHERE employer_id = $1 AND is_current ORDER BY interval_start`, emp)
	if err != nil {
		t.Fatalf("intervals: %v", err)
	}
	defer rows.Close()
	type iv struct{ kind, date, tz string }
	var got []iv
	for rows.Next() {
		var v iv
		if err := rows.Scan(&v.kind, &v.date, &v.tz); err != nil {
			t.Fatalf("scan: %v", err)
		}
		got = append(got, v)
	}
	want := []iv{
		{"work", "2026-08-03", "Asia/Singapore"},
		{"break_unpaid", "2026-08-03", "Asia/Singapore"},
		{"work", "2026-08-03", "Asia/Singapore"},
	}
	if len(got) != len(want) {
		t.Fatalf("got=%+v want=%+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got=%+v want=%+v", got, want)
		}
	}
}

// TestSourceTimezoneNeverMovesEmploymentDate proves the Amendment B
// separation: the same absolute instants, expressed once with a +08:00
// offset and once as UTC, attribute to the same Singapore business
// date. The vendor timezone resolves the instant; it must not move the
// payroll date, and no offset may be applied twice.
func TestSourceTimezoneNeverMovesEmploymentDate(t *testing.T) {
	pool := testPool(t)
	empA, noA := chain(t, pool, "A", "2026-08-03")
	empB, noB := chain(t, pool, "B", "2026-08-03")
	csvA := `source,source_event_id,employee_no,occurred_at,timezone,kind
vendor-x,mx-1,` + noA + `,2026-08-04T00:30:00+08:00,Asia/Singapore,in
vendor-x,mx-2,` + noA + `,2026-08-04T08:30:00+08:00,Asia/Singapore,out
`
	csvB := `source,source_event_id,employee_no,occurred_at,timezone,kind
vendor-x,mx-1,` + noB + `,2026-08-03T16:30:00Z,UTC,in
vendor-x,mx-2,` + noB + `,2026-08-04T00:30:00Z,UTC,out
`
	if _, err := Import(ctx(t), pool, empA, strings.NewReader(csvA), "test-op", timesheet.RegenerateDraft, timesheet.ChainTerminal); err != nil {
		t.Fatalf("import A: %v", err)
	}
	if _, err := Import(ctx(t), pool, empB, strings.NewReader(csvB), "test-op", timesheet.RegenerateDraft, timesheet.ChainTerminal); err != nil {
		t.Fatalf("import B: %v", err)
	}
	one := func(emp string) iv {
		var v iv
		var start, end string
		if err := pool.QueryRow(ctx(t),
			`SELECT kind, local_date::text, timezone,
			   interval_start::text, interval_end::text
			 FROM attendance_interval WHERE employer_id = $1 AND is_current`,
			emp).Scan(&v.kind, &v.date, &v.tz, &start, &end); err != nil {
			t.Fatalf("interval %s: %v", emp, err)
		}
		v.start, v.end = start, end
		return v
	}
	gotA, gotB := one(empA), one(empB)
	if gotA != gotB {
		t.Fatalf("A=%+v B=%+v want identical attribution", gotA, gotB)
	}
	if gotA.date != "2026-08-04" || gotA.tz != "Asia/Singapore" {
		t.Fatalf("A=%+v want SGT 2026-08-04", gotA)
	}
}

type iv struct {
	kind, date, tz string
	start, end     string
}

// TestClientTimezoneIndependence proves the programme (client/service)
// timezone cannot move attendance attribution: the same evidence under
// a New York client programme attributes identically to Singapore.
func TestClientTimezoneIndependence(t *testing.T) {
	pool := testPool(t)
	empA, noA := chain(t, pool, "A", "2026-08-03")
	empB, noB := chain(t, pool, "B", "2026-08-03")
	if _, err := pool.Exec(ctx(t),
		`UPDATE programme SET timezone = 'America/New_York' WHERE employer_id = $1`, empB); err != nil {
		t.Fatalf("retz programme: %v", err)
	}
	mk := func(no, tag string) string {
		return `source,source_event_id,employee_no,occurred_at,timezone,kind
vendor-x,` + tag + `-1,` + no + `,2026-08-03T09:00:00+08:00,Asia/Singapore,in
vendor-x,` + tag + `-2,` + no + `,2026-08-03T18:00:00+08:00,Asia/Singapore,out
`
	}
	repA, err := Import(ctx(t), pool, empA, strings.NewReader(mk(noA, "ca")), "test-op", timesheet.RegenerateDraft, timesheet.ChainTerminal)
	if err != nil {
		t.Fatalf("import A: %v", err)
	}
	repB, err := Import(ctx(t), pool, empB, strings.NewReader(mk(noB, "cb")), "test-op", timesheet.RegenerateDraft, timesheet.ChainTerminal)
	if err != nil {
		t.Fatalf("import B: %v", err)
	}
	if repA.IntervalsBuilt != 1 || repB.IntervalsBuilt != 1 {
		t.Fatalf("built A=%d B=%d", repA.IntervalsBuilt, repB.IntervalsBuilt)
	}
	one := func(emp string) iv {
		var v iv
		if err := pool.QueryRow(ctx(t),
			`SELECT kind, local_date::text, timezone FROM attendance_interval
			 WHERE employer_id = $1 AND is_current`,
			emp).Scan(&v.kind, &v.date, &v.tz); err != nil {
			t.Fatalf("interval: %v", err)
		}
		return v
	}
	if one(empA) != one(empB) {
		t.Fatalf("client timezone moved attribution")
	}
}

// TestUnknownJurisdictionRefusesAttribution proves employment-timezone
// resolution fails loudly for jurisdictions without a pinned mapping
// instead of silently falling back to UTC.
func TestUnknownJurisdictionRefusesAttribution(t *testing.T) {
	pool := testPool(t)
	emp := mkEmployer(t, pool, uniq(t, "X"))
	if _, err := pool.Exec(ctx(t),
		`UPDATE employer SET jurisdiction = 'XX' WHERE id = $1`, emp); err != nil {
		t.Fatalf("jurisdiction: %v", err)
	}
	if _, err := hr.CreateEmployee(ctx(t), pool, emp, hr.EmployeeInput{
		EmployeeNo: "E001", LegalName: "E001", Address: "a",
		Gender: "F", DateOfBirth: "1990-01-01", Citizenship: "SG",
	}, "test"); err != nil {
		t.Fatalf("employee: %v", err)
	}
	csv := `source,source_event_id,employee_no,occurred_at,timezone,kind
vendor-x,ux-1,E001,2026-08-03T09:00:00+08:00,Asia/Singapore,in
vendor-x,ux-2,E001,2026-08-03T18:00:00+08:00,Asia/Singapore,out
`
	if _, err := Import(ctx(t), pool, emp, strings.NewReader(csv), "test-op", timesheet.RegenerateDraft, timesheet.ChainTerminal); err == nil {
		t.Fatal("expected validation error for unmapped jurisdiction")
	}
}
