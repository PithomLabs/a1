package attendance

import (
	"strings"
	"testing"

	"github.com/PithomLabs/a1/internal/timesheet"
)

// H2 (C-TZ-01/02): audit and diagnostic instants render in UTC so the
// same instant produces the same stable text on every host, while
// business dates stay employment-local alongside them.
func TestAuditInstantsRenderUTC(t *testing.T) {
	pool := testPool(t)
	emp, no := dayChain(t, pool, "UT")
	importCSV(t, pool, emp, spanCSV(no, "ut-1", "ut-2",
		"2026-08-03T09:00:00+08:00", "2026-08-03T18:00:00+08:00"))
	orig := liveWorkID(t, pool, emp)
	if _, err := Correct(ctx(t), pool, emp, CorrectionInput{
		IntervalIDs: []string{orig}, Start: "2026-08-03T22:00:00+08:00", End: "2026-08-04T06:00:00+08:00",
		Kind: "work", Reason: "night shift",
	}, "test-op", timesheet.RegenerateDraft, timesheet.CorrectionDrafts); err != nil {
		t.Fatalf("correct: %v", err)
	}
	var before, after string
	if err := pool.QueryRow(ctx(t),
		`SELECT before_state::text, after_state::text FROM audit_event
		 WHERE employer_id = $1 AND action = 'interval.correct'`, emp).Scan(&before, &after); err != nil {
		t.Fatalf("audit: %v", err)
	}
	// 22:00+08 is 14:00Z; the employment dates ride alongside.
	for _, want := range []string{"2026-08-03T14:00:00Z", "2026-08-03T01:00:00Z"} {
		if !strings.Contains(before, want) && !strings.Contains(after, want) {
			t.Fatalf("before=%s after=%s want UTC %s", before, after, want)
		}
	}
	if strings.Contains(before, "+08:00") || strings.Contains(after, "+08:00") {
		t.Fatalf("before=%s after=%s want no local-offset rendering", before, after)
	}
	for _, date := range []string{"2026-08-03", "2026-08-04"} {
		if !strings.Contains(after, date) {
			t.Fatalf("after=%s want employment date %s retained", after, date)
		}
	}
}

func TestMismatchDetailRendersUTC(t *testing.T) {
	pool := testPool(t)
	emp, no := chain(t, pool, "E", "2026-08-03")
	csv := `source,source_event_id,employee_no,occurred_at,timezone,kind
vendor-x,uz-1,` + no + `,2026-08-03T09:00:00+08:00,Asia/Singapore,in
vendor-x,uz-2,` + no + `,2026-08-04T08:00:00+08:00,Asia/Singapore,out
`
	rep, err := Import(ctx(t), pool, emp, strings.NewReader(csv), "test-op", timesheet.RegenerateDraft, timesheet.ChainTerminal)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	for _, e := range rep.Exceptions {
		if e.Code != ExcSpanDateMismatch {
			continue
		}
		if !strings.Contains(e.Detail, "2026-08-03T01:00:00Z") {
			t.Fatalf("detail=%q want the UTC instant", e.Detail)
		}
		return
	}
	t.Fatalf("expected span_date_mismatch, got %+v", rep.Exceptions)
}
