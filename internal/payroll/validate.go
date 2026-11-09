package payroll

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	coredatabase "github.com/PithomLabs/a1/core/database"
	"github.com/PithomLabs/a1/internal/apperr"
	"github.com/PithomLabs/a1/internal/audit"
)

// Validation is the verdict of one ValidateRun call. Valid means the
// run stands validated on the named snapshot; otherwise Blockers
// explains why, and Stale means a newer snapshot version was
// captured that the caller must re-offer.
type Validation struct {
	RunID           string   `json:"run_id"`
	Valid           bool     `json:"valid"`
	SnapshotID      string   `json:"snapshot_id,omitempty"`
	SnapshotVersion int      `json:"snapshot_version,omitempty"`
	Blockers        []string `json:"blockers,omitempty"`
	Stale           bool     `json:"stale,omitempty"`
}

// ValidateRun validates a payroll run against a caller-offered
// snapshot version, in one REPEATABLE READ transaction:
//
//  1. Lock the run. Refuse vacuous populations without mutating
//     runs or snapshots.
//  2. If a valid snapshot tip stands, compare it against current
//     state. A drifted tip is invalidated and a fresh version
//     captured; the call returns the stale verdict so the operator
//     re-offers explicitly — validation never silently advances.
//  3. Validate-by-version: the offered version must equal the
//     currently valid version (0 when no version exists yet),
//     otherwise refuse without mutation.
//  4. Assess the snapshot's recorded decisions and pinned packs.
//     Any non-included, non-excluded employee blocks with its
//     recorded reasons, as does any pinned pack that is not
//     IMPLEMENTABLE.
//  5. Transition draft to validated, stamping inputs_hash and the
//     rule pins, audited.
//
// Re-offering the current version of a validated, undrifted run is
// idempotent. Concurrent validations serialize on the run row; the
// loser retries once and receives a stale verdict naming the
// winner's tip, which it re-offers explicitly — it never silently
// adopts a version it never inspected. Exactly one snapshot version
// results however the calls interleave.
func ValidateRun(ctx context.Context, pool *pgxpool.Pool, employerID, runID string, offeredVersion int, operator string) (*Validation, error) {
	if strings.TrimSpace(operator) == "" {
		return nil, apperr.New(apperr.CodeValidation, "operator attribution is required")
	}
	if offeredVersion < 0 {
		return nil, apperr.New(apperr.CodeValidation, "offered snapshot version must be >= 0")
	}
	v, err := validateOnce(ctx, pool, employerID, runID, offeredVersion, operator, false)
	if err == nil {
		return v, nil
	}
	// One bounded retry names the winner's tip: the loser's snapshot
	// predates the winner's commit, so it either 40001s on the run
	// row, 23505s on the one-valid unique, or fails the one-valid
	// guard. The retry returns a stale verdict, never a silent
	// adoption.
	if isConcurrencyConflict(err) {
		return validateOnce(ctx, pool, employerID, runID, offeredVersion, operator, true)
	}
	return nil, err
}

func isConcurrencyConflict(err error) bool {
	// Inside Validate, an already-stands conflict can only come from
	// a true race: the same transaction verified no tip stood. The
	// retry then observes the winner's commit down the idempotent
	// path.
	var ae *apperr.Error
	if errors.As(err, &ae) {
		return ae.Code == apperr.CodeConflict &&
			(strings.Contains(ae.Message, "concurrent") || strings.Contains(ae.Message, "already stands"))
	}
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && (pgErr.Code == "40001" || pgErr.Code == "23505")
}

func validateOnce(ctx context.Context, pool *pgxpool.Pool, employerID, runID string, offeredVersion int, operator string, adoptStale bool) (*Validation, error) {
	tx, err := coredatabase.BeginSnapshotTx(ctx, pool)
	if err != nil {
		return nil, apperr.FromDB(err, "begin validation")
	}
	defer tx.Rollback(ctx)
	var status string
	if err := tx.QueryRow(ctx,
		`SELECT status FROM payroll_run WHERE employer_id = $1 AND id = $2 FOR UPDATE`,
		employerID, runID).Scan(&status); err != nil {
		if err == pgx.ErrNoRows {
			return nil, apperr.New(apperr.CodeNotFound, "payroll run %s not found", runID)
		}
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "40001" {
			return nil, apperr.New(apperr.CodeConflict, "concurrent validation in progress; retry the operation")
		}
		return nil, apperr.FromDB(err, "lock payroll run")
	}
	if status != "draft" && status != "validated" {
		return nil, apperr.New(apperr.CodeInvalidState,
			"payroll run is %s; validation covers draft runs", status)
	}
	var start, end string
	if err := tx.QueryRow(ctx,
		`SELECT p.starts_on::text, p.ends_on::text FROM payroll_run r JOIN pay_period p ON p.id = r.pay_period_id
		 WHERE r.employer_id = $1 AND r.id = $2`, employerID, runID).Scan(&start, &end); err != nil {
		return nil, apperr.FromDB(err, "resolve run period")
	}
	var periodID string
	if err := tx.QueryRow(ctx,
		`SELECT pay_period_id FROM payroll_run WHERE employer_id = $1 AND id = $2`,
		employerID, runID).Scan(&periodID); err != nil {
		return nil, apperr.FromDB(err, "resolve run period")
	}
	expected, err := expectedPopulation(ctx, tx, employerID, periodID, start, end)
	if err != nil {
		return nil, err
	}
	if len(expected) == 0 {
		if err := tx.Rollback(ctx); err != nil {
			return nil, apperr.FromDB(err, "abort vacuous validation")
		}
		return &Validation{RunID: runID, Blockers: []string{"empty population: nothing to validate"}}, nil
	}
	var tipID string
	var tipVersion int
	err = tx.QueryRow(ctx,
		`SELECT id, version FROM payroll_snapshot WHERE payroll_run_id = $1 AND status = 'valid'`,
		runID).Scan(&tipID, &tipVersion)
	tipExists := err == nil
	if err != nil && err != pgx.ErrNoRows {
		return nil, apperr.FromDB(err, "load snapshot tip")
	}
	if tipExists {
		drifted, err := tipDrifted(ctx, tx, employerID, periodID, start, end, runID, tipID)
		if err != nil {
			return nil, err
		}
		if drifted {
			// Stale tip: invalidate, capture fresh, and return the
			// stale verdict. The caller re-offers the new version
			// explicitly; nothing advances silently.
			if _, err := tx.Exec(ctx,
				`SELECT invalidate_payroll_snapshot($1::uuid, $2, $3)`,
				tipID, fmt.Sprintf("superseded by revalidation during validate (offered v%d)", offeredVersion), operator); err != nil {
				return nil, apperr.FromDB(err, "invalidate stale snapshot")
			}
			newID, newVersion, _, err := captureInTx(ctx, tx, employerID, runID, operator)
			if err != nil {
				return nil, err
			}
			if err := tx.Commit(ctx); err != nil {
				return nil, apperr.FromDB(err, "commit revalidation capture")
			}
			return &Validation{RunID: runID, SnapshotID: newID, SnapshotVersion: newVersion,
				Blockers: []string{fmt.Sprintf("snapshot v%d stale; v%d captured — re-offer to validate", tipVersion, newVersion)},
				Stale:    true}, nil
		}
		if offeredVersion != tipVersion {
			if err := tx.Rollback(ctx); err != nil {
				return nil, apperr.FromDB(err, "abort version lockout")
			}
			if adoptStale {
				// Race retry: the winner's tip stands. Name it
				// as a stale verdict so the caller re-offers
				// explicitly.
				return &Validation{RunID: runID, SnapshotID: tipID, SnapshotVersion: tipVersion,
					Blockers: []string{fmt.Sprintf("a concurrent validation won; snapshot version %d stands — re-offer to validate", tipVersion)},
					Stale:    true}, nil
			}
			return nil, apperr.New(apperr.CodeConflict,
				"offered snapshot version %d does not match the valid version %d", offeredVersion, tipVersion)
		}
		blockers, err := snapshotBlockers(ctx, tx, tipID)
		if err != nil {
			return nil, err
		}
		if len(blockers) > 0 {
			if err := tx.Rollback(ctx); err != nil {
				return nil, apperr.FromDB(err, "abort blocked validation")
			}
			return &Validation{RunID: runID, SnapshotID: tipID, SnapshotVersion: tipVersion, Blockers: blockers}, nil
		}
		if status == "validated" {
			// Idempotent replay: already validated on a current,
			// unblocked tip.
			if err := tx.Rollback(ctx); err != nil {
				return nil, apperr.FromDB(err, "abort replay validation")
			}
			return &Validation{RunID: runID, Valid: true, SnapshotID: tipID, SnapshotVersion: tipVersion}, nil
		}
		if err := transitionValidated(ctx, tx, employerID, runID, tipID, operator); err != nil {
			return nil, err
		}
		if err := tx.Commit(ctx); err != nil {
			return nil, apperr.FromDB(err, "commit validation")
		}
		return &Validation{RunID: runID, Valid: true, SnapshotID: tipID, SnapshotVersion: tipVersion}, nil
	}
	// No tip yet: the caller must offer 0, the explicit "no prior
	// version". Capture and validate atomically.
	if offeredVersion != 0 {
		if err := tx.Rollback(ctx); err != nil {
			return nil, apperr.FromDB(err, "abort version lockout")
		}
		return nil, apperr.New(apperr.CodeConflict,
			"offered snapshot version %d but no valid snapshot exists; offer 0 for first validation", offeredVersion)
	}
	snapID, version, _, err := captureInTx(ctx, tx, employerID, runID, operator)
	if err != nil {
		return nil, err
	}
	blockers, err := snapshotBlockers(ctx, tx, snapID)
	if err != nil {
		return nil, err
	}
	if len(blockers) > 0 {
		// The faithful snapshot stands for re-offer after the fix;
		// the run stays draft.
		if err := tx.Commit(ctx); err != nil {
			return nil, apperr.FromDB(err, "commit blocked capture")
		}
		return &Validation{RunID: runID, SnapshotID: snapID, SnapshotVersion: version, Blockers: blockers}, nil
	}
	if status == "validated" {
		// Validated run whose tip was invalidated out of band:
		// treat the fresh capture as a revalidation, not a silent
		// advance.
		if err := tx.Commit(ctx); err != nil {
			return nil, apperr.FromDB(err, "commit revalidation capture")
		}
		return &Validation{RunID: runID, SnapshotID: snapID, SnapshotVersion: version,
			Blockers: []string{fmt.Sprintf("snapshot v%d captured — re-offer to validate", version)},
			Stale:    true}, nil
	}
	if err := transitionValidated(ctx, tx, employerID, runID, snapID, operator); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "40001" {
			return nil, apperr.New(apperr.CodeConflict, "concurrent validation in progress; retry the operation")
		}
		return nil, apperr.FromDB(err, "commit validation")
	}
	return &Validation{RunID: runID, Valid: true, SnapshotID: snapID, SnapshotVersion: version}, nil
}

// tipDrifted reports whether the standing tip still matches current
// state within the validation transaction: same population, same
// per-employee sections, same pack pins.
func tipDrifted(ctx context.Context, tx pgx.Tx, employerID, periodID, start, end, runID, tipID string) (bool, error) {
	current, err := expectedPopulation(ctx, tx, employerID, periodID, start, end)
	if err != nil {
		return false, err
	}
	rows, err := tx.Query(ctx,
		`SELECT employee_id, row_hash, inputs::text FROM payroll_snapshot_employee WHERE snapshot_id = $1`,
		tipID)
	if err != nil {
		return false, apperr.FromDB(err, "load tip employees")
	}
	defer rows.Close()
	stored := map[string]struct {
		hash string
		doc  canonEmployee
	}{}
	for rows.Next() {
		var id, hash, raw string
		if err := rows.Scan(&id, &hash, &raw); err != nil {
			return false, apperr.FromDB(err, "scan tip employees")
		}
		var doc canonEmployee
		if err := json.Unmarshal([]byte(raw), &doc); err != nil {
			return false, apperr.FromDB(err, "parse tip inputs")
		}
		if hashJSON(doc) != hash {
			return true, nil
		}
		stored[id] = struct {
			hash string
			doc  canonEmployee
		}{hash, doc}
	}
	if err := rows.Err(); err != nil {
		return false, apperr.FromDB(err, "load tip employees")
	}
	if len(current) != len(stored) {
		return true, nil
	}
	exclusions, err := runExclusions(ctx, tx, employerID, runID)
	if err != nil {
		return false, err
	}
	for _, e := range current {
		se, ok := stored[e.id]
		if !ok {
			return true, nil
		}
		doc, err := resolveEmployee(ctx, tx, employerID, periodID, start, end, e, exclusions[e.id])
		if err != nil {
			return false, err
		}
		doc.Sections = currentSections(doc)
		if hashJSON(doc) != se.hash {
			return true, nil
		}
	}
	prows, err := tx.Query(ctx,
		`SELECT pack, version, status, manifest_digest FROM payroll_snapshot_pack WHERE snapshot_id = $1`,
		tipID)
	if err != nil {
		return false, apperr.FromDB(err, "load tip packs")
	}
	defer prows.Close()
	required := []string{}
	pinned := map[string][3]string{}
	for prows.Next() {
		var pack, version, status string
		var digest *string
		if err := prows.Scan(&pack, &version, &status, &digest); err != nil {
			return false, apperr.FromDB(err, "scan tip packs")
		}
		required = append(required, pack)
		d := ""
		if digest != nil {
			d = *digest
		}
		pinned[pack] = [3]string{version, status, d}
	}
	if err := prows.Err(); err != nil {
		return false, apperr.FromDB(err, "load tip packs")
	}
	now, err := resolvePacks(ctx, tx, employerID, required)
	if err != nil {
		return false, err
	}
	for _, p := range now {
		d := ""
		if p.Digest != nil {
			d = *p.Digest
		}
		if pinned[p.Pack] != [3]string{p.Version, p.Status, d} {
			return true, nil
		}
	}
	return false, nil
}

// snapshotBlockers renders one snapshot's recorded employee
// decisions and pinned pack statuses as validation blockers, in
// stable order. Only included/excluded employees and IMPLEMENTABLE
// packs validate.
func snapshotBlockers(ctx context.Context, tx pgx.Tx, snapshotID string) ([]string, error) {
	out, err := tipBlockers(ctx, tx, snapshotID)
	if err != nil {
		return nil, err
	}
	packs, err := packBlockers(ctx, tx, snapshotID)
	if err != nil {
		return nil, err
	}
	out = append(out, packs...)
	sort.Strings(out)
	return out, nil
}

// packBlockers names the snapshot's pinned packs that are not
// IMPLEMENTABLE. Capture records whatever the activations say;
// validation refuses to stand on anything less.
func packBlockers(ctx context.Context, tx pgx.Tx, snapshotID string) ([]string, error) {
	rows, err := tx.Query(ctx,
		`SELECT pack, version, status FROM payroll_snapshot_pack
		 WHERE snapshot_id = $1 AND status <> 'IMPLEMENTABLE' ORDER BY pack`,
		snapshotID)
	if err != nil {
		return nil, apperr.FromDB(err, "assess pack blockers")
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var pack, version, status string
		if err := rows.Scan(&pack, &version, &status); err != nil {
			return nil, apperr.FromDB(err, "scan pack blockers")
		}
		if strings.TrimSpace(version) == "" {
			version = "unpinned"
		}
		out = append(out, fmt.Sprintf("pack %s %s (%s): only IMPLEMENTABLE packs validate",
			pack, version, status))
	}
	if err := rows.Err(); err != nil {
		return nil, apperr.FromDB(err, "assess pack blockers")
	}
	return out, nil
}

// tipBlockers renders the recorded non-included decisions of one
// snapshot as validation blockers.
func tipBlockers(ctx context.Context, tx pgx.Tx, snapshotID string) ([]string, error) {
	rows, err := tx.Query(ctx,
		`SELECT e.employee_no, s.decision, s.blockers::text
		 FROM payroll_snapshot_employee s JOIN employee e ON e.id = s.employee_id
		 WHERE s.snapshot_id = $1 AND s.decision NOT IN ('included', 'excluded')
		 ORDER BY e.employee_no`,
		snapshotID)
	if err != nil {
		return nil, apperr.FromDB(err, "assess snapshot blockers")
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var no, decision, blockersRaw string
		if err := rows.Scan(&no, &decision, &blockersRaw); err != nil {
			return nil, apperr.FromDB(err, "scan snapshot blockers")
		}
		var blockers []string
		if err := json.Unmarshal([]byte(blockersRaw), &blockers); err != nil {
			return nil, apperr.FromDB(err, "parse snapshot blockers")
		}
		if len(blockers) == 0 {
			out = append(out, no+": "+decision)
			continue
		}
		for _, b := range blockers {
			out = append(out, no+": "+decision+": "+b)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, apperr.FromDB(err, "assess snapshot blockers")
	}
	sort.Strings(out)
	return out, nil
}

// transitionValidated moves a draft run to validated, stamping the
// snapshot content hash and the rule pins, audited.
func transitionValidated(ctx context.Context, tx pgx.Tx, employerID, runID, snapshotID, operator string) error {
	var contentHash string
	if err := tx.QueryRow(ctx,
		`SELECT content_hash FROM payroll_snapshot WHERE id = $1`, snapshotID).Scan(&contentHash); err != nil {
		return apperr.FromDB(err, "load snapshot hash")
	}
	prows, err := tx.Query(ctx,
		`SELECT pack, version, status FROM payroll_snapshot_pack WHERE snapshot_id = $1 ORDER BY pack`,
		snapshotID)
	if err != nil {
		return apperr.FromDB(err, "load snapshot packs")
	}
	pins := []map[string]string{}
	for prows.Next() {
		var pack, version, status string
		if err := prows.Scan(&pack, &version, &status); err != nil {
			prows.Close()
			return apperr.FromDB(err, "scan snapshot packs")
		}
		pins = append(pins, map[string]string{"pack": pack, "version": version, "status": status})
	}
	prows.Close()
	if err := prows.Err(); err != nil {
		return apperr.FromDB(err, "load snapshot packs")
	}
	pinsRaw, err := json.Marshal(pins)
	if err != nil {
		return apperr.FromDB(err, "marshal rule pins")
	}
	if _, err := tx.Exec(ctx,
		`UPDATE payroll_run SET status = 'validated', inputs_hash = $3, rule_versions = $4::jsonb
		 WHERE employer_id = $1 AND id = $2`,
		employerID, runID, contentHash, string(pinsRaw)); err != nil {
		return apperr.FromDB(err, "transition run to validated")
	}
	return audit.Write(ctx, tx, employerID, operator, "payroll",
		"run.validate", "payroll_run", &runID, map[string]any{"status": "draft"},
		map[string]any{"status": "validated", "snapshot_id": snapshotID, "inputs_hash": contentHash})
}
