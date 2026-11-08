package attendance

import (
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/PithomLabs/a1/internal/timesheet"
)

// H5 (B-DAY-01): exception status and generation blocking stay
// consistent in both directions. An unresolved overlap flags the day
// instead of displaying it as fully reviewed; a fully dispositioned
// duplicate returns the day to reviewed and unblocks generation;
// a half-confirmed duplicate keeps both.
func dayStatus(t *testing.T, pool *pgxpool.Pool, emp, date string) string {
	t.Helper()
	var s string
	if err := pool.QueryRow(ctx(t),
		`SELECT status FROM attendance_record WHERE employer_id = $1 AND business_date = $2::date`,
		emp, date).Scan(&s); err != nil {
		t.Fatalf("day status: %v", err)
	}
	return s
}

func TestOverlapDayStatusBothDirections(t *testing.T) {
	pool := testPool(t)
	emp, no := dayChain(t, pool, "DS")
	importCSV(t, pool, emp, spanCSV(no, "ds-1", "ds-2",
		"2026-08-03T09:00:00+08:00", "2026-08-03T18:00:00+08:00"))
	importCSV(t, pool, emp, spanCSV(no, "ds-3", "ds-4",
		"2026-08-03T09:00:00+08:00", "2026-08-03T18:00:00+08:00"))
	if got := dayStatus(t, pool, emp, "2026-08-03"); got != "exception" {
		t.Fatalf("status=%s want exception while the overlap is unresolved", got)
	}
	period := mkPeriod(t, pool, emp)
	gen1, err := timesheet.Generate(ctx(t), pool, emp, period, "test-op")
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if len(gen1.Generated) != 0 {
		t.Fatalf("gen=%+v want the employee skipped", gen1)
	}
	// Half-confirmed: one endpoint settled, the day still flagged.
	if _, err := DispositionDuplicate(ctx(t), pool, emp, DispositionInput{
		EventID: eventID(t, pool, emp, "ds-3"), CanonicalID: eventID(t, pool, emp, "ds-1"),
		Reason: "resend",
	}, "operator-1", timesheet.RegenerateDraft, timesheet.ChainTerminal); err != nil {
		t.Fatalf("disposition in: %v", err)
	}
	if got := dayStatus(t, pool, emp, "2026-08-03"); got != "exception" {
		t.Fatalf("status=%s want exception while one endpoint is unsettled", got)
	}
	if _, err := DispositionDuplicate(ctx(t), pool, emp, DispositionInput{
		EventID: eventID(t, pool, emp, "ds-4"), CanonicalID: eventID(t, pool, emp, "ds-2"),
		Reason: "resend",
	}, "operator-1", timesheet.RegenerateDraft, timesheet.ChainTerminal); err != nil {
		t.Fatalf("disposition out: %v", err)
	}
	if got := dayStatus(t, pool, emp, "2026-08-03"); got != "reviewed" {
		t.Fatalf("status=%s want reviewed once fully dispositioned", got)
	}
	gen2, err := timesheet.Generate(ctx(t), pool, emp, period, "test-op")
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if len(gen2.Generated) != 1 {
		t.Fatalf("gen=%+v want exactly one sheet", gen2)
	}
}
