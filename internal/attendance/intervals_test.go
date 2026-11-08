package attendance

import (
	"strings"
	"testing"

	"github.com/PithomLabs/a1/internal/timesheet"
)

func TestListIntervals(t *testing.T) {
	pool := testPool(t)
	emp, no := chain(t, pool, "E", "2026-08-03")
	csv := `source,source_event_id,employee_no,occurred_at,timezone,kind
vendor-x,li-1,` + no + `,2026-08-03T09:00:00+08:00,Asia/Singapore,in
vendor-x,li-2,` + no + `,2026-08-03T12:00:00+08:00,Asia/Singapore,break_start
vendor-x,li-3,` + no + `,2026-08-03T13:00:00+08:00,Asia/Singapore,break_end
vendor-x,li-4,` + no + `,2026-08-03T18:00:00+08:00,Asia/Singapore,out
`
	rep, err := Import(ctx(t), pool, emp, strings.NewReader(csv), "test-op", timesheet.RegenerateDraft, timesheet.ChainTerminal)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if rep.IntervalsBuilt == 0 {
		t.Fatalf("rep=%+v", rep)
	}
	list, err := ListIntervals(ctx(t), pool, emp, "", "2026-08-03", "2026-08-03")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) == 0 {
		t.Fatal("expected intervals")
	}
	for _, d := range list {
		if d.ID == "" || d.EmployeeNo != no || d.BusinessDate != "2026-08-03" {
			t.Fatalf("detail=%+v", d)
		}
		if d.Start == "" || d.End == "" || d.Timezone == "" || d.SourceRefs == nil {
			t.Fatalf("Detail missing evidence: %+v", d)
		}
		if d.LinkedSheets == nil || d.Exceptions == nil {
			t.Fatalf("Detail slices must be non-nil: %+v", d)
		}
	}
	// Employee filter narrows; unknown filter matches nothing.
	one, err := ListIntervals(ctx(t), pool, emp, no, "2026-08-03", "2026-08-03")
	if err != nil || len(one) != len(list) {
		t.Fatalf("filtered=%d unfiltered=%d err=%v", len(one), len(list), err)
	}
	none, err := ListIntervals(ctx(t), pool, emp, "NOBODY", "2026-08-03", "2026-08-03")
	if err != nil || len(none) != 0 {
		t.Fatalf("unknown filter: %+v err=%v", none, err)
	}
	if _, err := ListIntervals(ctx(t), pool, emp, "", "08/03/2026", "2026-08-03"); err == nil {
		t.Fatal("expected validation error for bad from date")
	}
}
