package timesheet

import (
	"context"

	"github.com/jackc/pgx/v5"

	"github.com/PithomLabs/a1/internal/apperr"
)

// chainWalkLimit bounds correction-chain walks: chains grow by one row
// per reissue, so anything longer signals corruption, never patience.
const chainWalkLimit = 1000

// ChainTerminal walks a timesheet supersedes chain forward to its
// terminal sheet: the version that currently speaks for the chain.
// Callers must hold the shared payroll-input lock, which serializes
// reissues against the walk.
func ChainTerminal(ctx context.Context, q pgx.Tx, employerID, sheetID string) (string, string, error) {
	terminal, status := sheetID, ""
	for range chainWalkLimit {
		var next struct {
			id     string
			status string
		}
		err := q.QueryRow(ctx,
			`SELECT id, status FROM timesheet WHERE employer_id = $1 AND supersedes_id = $2`,
			employerID, terminal).Scan(&next.id, &next.status)
		if err != nil {
			if err == pgx.ErrNoRows {
				if status == "" {
					if err := q.QueryRow(ctx,
						`SELECT status FROM timesheet WHERE employer_id = $1 AND id = $2`,
						employerID, terminal).Scan(&status); err != nil {
						if err == pgx.ErrNoRows {
							return "", "", apperr.New(apperr.CodeNotFound, "timesheet %s not found", sheetID)
						}
						return "", "", apperr.FromDB(err, "load chain terminal")
					}
				}
				return terminal, status, nil
			}
			return "", "", apperr.FromDB(err, "walk correction chain")
		}
		terminal, status = next.id, next.status
	}
	return "", "", apperr.New(apperr.CodeInvalidState, "timesheet chain exceeds %d versions", chainWalkLimit)
}

// CorrectionDrafts resolves the draft sheets a correction touching the
// given business dates must regenerate: every draft of the employee in
// the pay periods containing those dates, including the open correction
// draft at the terminal of a decided chain. A decided period with no
// open correction draft blocks the correction — the operator must
// reissue that sheet first, so decided history never goes stale under
// moved hours. A submitted sheet blocks with direction to decide it,
// since submitted sheets cannot reissue. Callers must hold the
// shared payroll-input lock.
func CorrectionDrafts(ctx context.Context, tx pgx.Tx, employerID, employeeID string, dates []string) ([]string, error) {
	if len(dates) == 0 {
		return nil, nil
	}
	rows, err := tx.Query(ctx,
		`SELECT t.id::text, t.status, p.code FROM timesheet t
		 JOIN pay_period p ON p.id = t.pay_period_id
		 WHERE t.employer_id = $1 AND t.employee_id = $2
		   AND EXISTS (SELECT 1 FROM unnest($3::date[]) d WHERE p.starts_on <= d AND p.ends_on >= d)
		 ORDER BY t.created_at, t.id`,
		employerID, employeeID, dates)
	if err != nil {
		return nil, apperr.FromDB(err, "load period sheets")
	}
	defer rows.Close()
	type sheet struct {
		id, status, period string
	}
	var sheets []sheet
	for rows.Next() {
		var s sheet
		if err := rows.Scan(&s.id, &s.status, &s.period); err != nil {
			return nil, apperr.FromDB(err, "scan period sheets")
		}
		sheets = append(sheets, s)
	}
	if err := rows.Err(); err != nil {
		return nil, apperr.FromDB(err, "load period sheets")
	}
	visited := map[string]bool{}
	var drafts []string
	seen := map[string]bool{}
	for _, s := range sheets {
		if visited[s.id] {
			continue
		}
		terminal, status, err := ChainTerminal(ctx, tx, employerID, s.id)
		if err != nil {
			return nil, err
		}
		// Mark the walked chain visited so each chain resolves once.
		for id := s.id; ; {
			visited[id] = true
			if id == terminal {
				break
			}
			var next string
			if err := tx.QueryRow(ctx,
				`SELECT id FROM timesheet WHERE employer_id = $1 AND supersedes_id = $2`,
				employerID, id).Scan(&next); err != nil {
				return nil, apperr.FromDB(err, "walk correction chain")
			}
			id = next
		}
		switch status {
		case "draft":
			if !seen[terminal] {
				seen[terminal] = true
				drafts = append(drafts, terminal)
			}
		case "submitted":
			return nil, apperr.New(apperr.CodeInvalidState,
				"pay period %s has a submitted timesheet; decide it before correcting into its period", s.period)
		default:
			return nil, apperr.New(apperr.CodeInvalidState,
				"pay period %s is decided (%s) with no open correction draft; reissue the sheet to correct into its period", s.period, status)
		}
	}
	return drafts, nil
}
