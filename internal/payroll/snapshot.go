package payroll

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	coredatabase "github.com/PithomLabs/a1/core/database"
	"github.com/PithomLabs/a1/internal/apperr"
	"github.com/PithomLabs/a1/internal/audit"
	"github.com/PithomLabs/a1/internal/compbasis"
	"github.com/PithomLabs/a1/internal/employer"
	"github.com/PithomLabs/a1/internal/timesheet"
)

// SnapshotSchemaVersion pins the canonical snapshot document shape.
// Any canonical-form change bumps this and keeps old readers honest.
const SnapshotSchemaVersion = "m2/v1"

// snapshotCurrency is the single payroll currency. No currency column
// exists in the schema yet; the snapshot records the assumption
// explicitly rather than leaving currency ambiguous.
const snapshotCurrency = "SGD"

// qtx is the query surface shared by *pgxpool.Pool and pgx.Tx, so
// resolution runs identically inside the capture transaction and in
// plain drift-detection reads.
type qtx interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// Snapshot is one captured snapshot version header.
type Snapshot struct {
	ID                string  `json:"id"`
	EmployerID        string  `json:"employer_id"`
	RunID             string  `json:"run_id"`
	PeriodID          string  `json:"pay_period_id"`
	Version           int     `json:"version"`
	Status            string  `json:"status"`
	SchemaVersion     string  `json:"schema_version"`
	ContentHash       string  `json:"content_hash"`
	CalcEngine        string  `json:"calc_engine"`
	InvalidatedReason *string `json:"invalidated_reason,omitempty"`
}

// SnapSegment is one resolved effective compensation segment.
type SnapSegment struct {
	Code    string   `json:"code"`
	Amount  string   `json:"amount"`
	From    string   `json:"from"`
	To      *string  `json:"to"`
	Lineage []string `json:"lineage"`
}

// SnapSHG is one captured SHG chain tip.
type SnapSHG struct {
	ID           string  `json:"id"`
	Fund         string  `json:"fund"`
	Status       string  `json:"status"`
	Tripled      bool    `json:"tripled"`
	From         string  `json:"from"`
	To           *string `json:"to"`
	BasisSeq     int64   `json:"basis_seq"`
	SupersedesID *string `json:"supersedes_id,omitempty"`
}

// SnapshotEmployee is one captured employee row.
type SnapshotEmployee struct {
	EmployeeID      string            `json:"employee_id"`
	EmployeeNo      string            `json:"employee_no"`
	Decision        string            `json:"decision"`
	Blockers        []string          `json:"blockers"`
	TermsIDs        []string          `json:"terms_ids"`
	StatusIDs       []string          `json:"status_ids"`
	BasisSeq        int64             `json:"basis_seq"`
	ApprovedSheetID *string           `json:"approved_sheet_id"`
	CompSegments    []SnapSegment     `json:"comp_segments"`
	SHGTips         []SnapSHG         `json:"shg_tips"`
	Sections        map[string]string `json:"sections"`
	RowHash         string            `json:"row_hash"`
}

// SnapshotPack is one captured rule-pack pin.
type SnapshotPack struct {
	Pack          string  `json:"pack"`
	Version       string  `json:"version"`
	Status        string  `json:"status"`
	Digest        *string `json:"digest"`
	EffectiveFrom *string `json:"effective_from"`
	EffectiveTo   *string `json:"effective_to"`
	ReviewRef     *string `json:"review_ref"`
}

// SnapshotDetail is a snapshot with its captured rows.
type SnapshotDetail struct {
	Snapshot
	Employees []SnapshotEmployee `json:"employees"`
	Packs     []SnapshotPack     `json:"packs"`
}

// DriftReport compares a preserved snapshot against current state.
type DriftReport struct {
	SnapshotID string   `json:"snapshot_id"`
	Drifted    bool     `json:"drifted"`
	Reasons    []string `json:"reasons"`
}

// canonTerms pins one overlapping employment-terms row.
type canonTerms struct {
	ID       string  `json:"id"`
	From     string  `json:"from"`
	To       *string `json:"to"`
	Basic    string  `json:"basic"`
	BasisSeq int64   `json:"basis_seq"`
}

// canonStatus pins one status-history row affecting the period.
type canonStatus struct {
	ID     string  `json:"id"`
	Status string  `json:"status"`
	From   string  `json:"from"`
	To     *string `json:"to"`
}

// canonSheet pins the effective approved sheet with its lineage,
// lines, linked intervals and unpaired clock evidence.
type canonSheet struct {
	ID         string      `json:"id"`
	ApprovedAt string      `json:"approved_at"`
	ApprovedBy string      `json:"approved_by"`
	BasisSeq   int64       `json:"basis_seq"`
	Chain      []string    `json:"chain"`
	Lines      []canonLine `json:"lines"`
	Intervals  []string    `json:"intervals"`
	Unpaired   []string    `json:"unpaired_events"`
}

// canonLine pins one payable sheet line.
type canonLine struct {
	ID    string `json:"id"`
	Code  string `json:"code"`
	Hours string `json:"hours"`
}

// canonEmployee is the canonical per-employee inputs document. Field
// order is the hash order: struct marshaling is declaration-ordered
// and every collection is sorted before hashing.
type canonEmployee struct {
	EmployeeID string            `json:"employee_id"`
	EmployeeNo string            `json:"employee_no"`
	Terms      []canonTerms      `json:"terms"`
	Statuses   []canonStatus     `json:"status_history"`
	BasisSeq   int64             `json:"employee_basis_seq"`
	Decision   string            `json:"decision"`
	Blockers   []string          `json:"blockers"`
	Sheet      *canonSheet       `json:"sheet"`
	Comp       []SnapSegment     `json:"compensation"`
	SHG        []SnapSHG         `json:"shg"`
	Exclusion  *canonExcl        `json:"exclusion"`
	Sections   map[string]string `json:"sections"`
}

// canonExcl pins an explicit run exclusion.
type canonExcl struct {
	Reason string `json:"reason"`
	Actor  string `json:"actor"`
}

// canonPack pins one required rule pack.
type canonPack struct {
	Pack      string  `json:"pack"`
	Version   string  `json:"version"`
	Status    string  `json:"status"`
	Digest    *string `json:"digest"`
	From      *string `json:"from"`
	To        *string `json:"to"`
	ReviewRef *string `json:"review_ref"`
}

// canonSnapshot is the canonical hashed form. Run identity, version
// and capture metadata are deliberately excluded: the same
// substantive inputs and rule versions hash the same whatever run
// captured them.
type canonSnapshot struct {
	Employer     string        `json:"employer_id"`
	Jurisdiction string        `json:"jurisdiction"`
	Currency     string        `json:"currency"`
	PeriodID     string        `json:"period_id"`
	PeriodCode   string        `json:"period_code"`
	PeriodStart  string        `json:"period_start"`
	PeriodEnd    string        `json:"period_end"`
	Schema       string        `json:"schema_version"`
	CalcEngine   string        `json:"calc_engine"`
	Employees    []canonEmpRef `json:"employees"`
	Packs        []canonPack   `json:"packs"`
}

// canonEmpRef binds one employee to its row hash.
type canonEmpRef struct {
	EmployeeID string `json:"employee_id"`
	RowHash    string `json:"row_hash"`
}

func hashJSON(v any) string {
	raw, err := json.Marshal(v)
	if err != nil {
		panic("canonical marshal: " + err.Error())
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// CaptureSnapshot resolves the run's expected population and pins
// every input into a new immutable snapshot version, atomically:
//
//  1. REPEATABLE READ transaction (one committed view).
//  2. Run row lock (serializes captures per run).
//  3. Employer population lock.
//  4. Expected population derivation.
//  5. Employee locks in UUID-sorted order.
//  6. Required rule-pack keys in sorted order.
//  7. Population revalidation.
//  8. Per-employee and pack resolution with blocker assessment.
//  9. Canonical content hash and atomic persist.
//
// Capture records blockers faithfully; it never refuses a blocked
// population. Refusing to validate on blockers is P1's job. A second
// capture while a valid version stands is rejected: invalidate, then
// recapture.
func CaptureSnapshot(ctx context.Context, pool *pgxpool.Pool, employerID, runID, operator string) (*Snapshot, error) {
	if strings.TrimSpace(operator) == "" {
		return nil, apperr.New(apperr.CodeValidation, "operator attribution is required")
	}
	tx, err := coredatabase.BeginSnapshotTx(ctx, pool)
	if err != nil {
		return nil, apperr.FromDB(err, "begin snapshot capture")
	}
	defer tx.Rollback(ctx)
	snapID, _, _, err := captureInTx(ctx, tx, employerID, runID, operator)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "40001" {
			return nil, apperr.New(apperr.CodeConflict, "concurrent snapshot capture; retry the operation")
		}
		return nil, apperr.FromDB(err, "commit snapshot capture")
	}
	// Re-read the persisted header: the returned snapshot reflects
	// stored state, not pre-commit memory.
	var out Snapshot
	err = pool.QueryRow(ctx,
		`SELECT id, employer_id, payroll_run_id, pay_period_id, version, status,
		  schema_version, content_hash, calc_engine, invalidated_reason
		 FROM payroll_snapshot WHERE id = $1`, snapID).Scan(
		&out.ID, &out.EmployerID, &out.RunID, &out.PeriodID, &out.Version, &out.Status,
		&out.SchemaVersion, &out.ContentHash, &out.CalcEngine, &out.InvalidatedReason)
	if err != nil {
		return nil, apperr.FromDB(err, "read snapshot")
	}
	return &out, nil
}

// captureInTx runs the capture steps inside the caller's snapshot
// transaction: run lock, population lock, employee locks, pack locks,
// revalidation, resolution and persist. It commits nothing; the
// caller commits. Validate reuses it so validation and capture share
// one atomic unit.
func captureInTx(ctx context.Context, tx pgx.Tx, employerID, runID, operator string) (string, int, string, error) {
	var run struct {
		periodID, periodCode, start, end, status string
		jurisdiction                             string
	}
	err := tx.QueryRow(ctx,
		`SELECT r.pay_period_id, p.code, p.starts_on::text, p.ends_on::text, r.status, e.jurisdiction
		 FROM payroll_run r JOIN pay_period p ON p.id = r.pay_period_id JOIN employer e ON e.id = r.employer_id
		 WHERE r.employer_id = $1 AND r.id = $2 FOR UPDATE OF r`,
		employerID, runID).Scan(&run.periodID, &run.periodCode, &run.start, &run.end, &run.status, &run.jurisdiction)
	if err != nil {
		if err == pgx.ErrNoRows {
			return "", 0, "", apperr.New(apperr.CodeNotFound, "payroll run %s not found", runID)
		}
		return "", 0, "", apperr.FromDB(err, "lock payroll run")
	}
	if run.status != "draft" && run.status != "validated" {
		return "", 0, "", apperr.New(apperr.CodeInvalidState,
			"payroll run is %s; snapshots capture on draft or validated runs", run.status)
	}
	if err := coredatabase.LockPayrollPopulation(ctx, tx, employerID); err != nil {
		return "", 0, "", err
	}
	expected, err := expectedPopulation(ctx, tx, employerID, run.periodID, run.start, run.end)
	if err != nil {
		return "", 0, "", err
	}
	sort.Slice(expected, func(i, j int) bool { return expected[i].id < expected[j].id })
	for _, e := range expected {
		if err := coredatabase.LockPayrollInput(ctx, tx, employerID, e.id); err != nil {
			return "", 0, "", err
		}
	}
	// Required packs derive from locked-employee data, so the set is
	// stable before its keys are taken in sorted order.
	required, err := requiredPackSet(ctx, tx, employerID, run.periodID, expected)
	if err != nil {
		return "", 0, "", err
	}
	for _, p := range required {
		if err := coredatabase.LockRulePack(ctx, tx, employerID, p); err != nil {
			return "", 0, "", err
		}
	}
	// Population revalidation: under REPEATABLE READ with every
	// population writer locked out, this re-derivation is identical
	// by construction. The comparison stays as defense in depth: any
	// lock-convention violator fails loudly instead of mixing.
	again, err := expectedPopulation(ctx, tx, employerID, run.periodID, run.start, run.end)
	if err != nil {
		return "", 0, "", err
	}
	if len(again) != len(expected) {
		return "", 0, "", apperr.New(apperr.CodeConflict, "expected population changed during lock acquisition")
	}
	seen := map[string]bool{}
	for _, e := range expected {
		seen[e.id] = true
	}
	for _, e := range again {
		if !seen[e.id] {
			return "", 0, "", apperr.New(apperr.CodeConflict, "expected population changed during lock acquisition")
		}
	}
	var validTip bool
	if err := tx.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM payroll_snapshot WHERE payroll_run_id = $1 AND status = 'valid')`,
		runID).Scan(&validTip); err != nil {
		return "", 0, "", apperr.FromDB(err, "check snapshot tip")
	}
	if validTip {
		return "", 0, "", apperr.New(apperr.CodeConflict,
			"a valid snapshot already stands for this run; invalidate it first")
	}
	var version int
	if err := tx.QueryRow(ctx,
		`SELECT COALESCE(max(version), 0) + 1 FROM payroll_snapshot WHERE payroll_run_id = $1`,
		runID).Scan(&version); err != nil {
		return "", 0, "", apperr.FromDB(err, "assign snapshot version")
	}
	exclusions, err := runExclusions(ctx, tx, employerID, runID)
	if err != nil {
		return "", 0, "", err
	}
	type resolved struct {
		doc canonEmployee
		row SnapshotEmployee
	}
	resolvedEmps := make([]resolved, 0, len(expected))
	for _, e := range expected {
		doc, err := resolveEmployee(ctx, tx, employerID, run.periodID, run.start, run.end, e, exclusions[e.id])
		if err != nil {
			return "", 0, "", err
		}
		doc.Sections = currentSections(doc)
		rowHash := hashJSON(doc)
		termsIDs := make([]string, 0, len(doc.Terms))
		for _, t := range doc.Terms {
			termsIDs = append(termsIDs, t.ID)
		}
		statusIDs := make([]string, 0, len(doc.Statuses))
		for _, s := range doc.Statuses {
			statusIDs = append(statusIDs, s.ID)
		}
		row := SnapshotEmployee{
			EmployeeID: e.id, EmployeeNo: e.no, Decision: doc.Decision, Blockers: doc.Blockers,
			TermsIDs: termsIDs, StatusIDs: statusIDs, BasisSeq: doc.BasisSeq,
			CompSegments: doc.Comp, SHGTips: doc.SHG, Sections: doc.Sections, RowHash: rowHash,
		}
		if doc.Sheet != nil {
			id := doc.Sheet.ID
			row.ApprovedSheetID = &id
		}
		resolvedEmps = append(resolvedEmps, resolved{doc: doc, row: row})
	}
	packs, err := resolvePacks(ctx, tx, employerID, required)
	if err != nil {
		return "", 0, "", err
	}
	canon := canonSnapshot{
		Employer: employerID, Jurisdiction: run.jurisdiction, Currency: snapshotCurrency,
		PeriodID: run.periodID, PeriodCode: run.periodCode, PeriodStart: run.start, PeriodEnd: run.end,
		Schema: SnapshotSchemaVersion, CalcEngine: "unimplemented",
	}
	for _, r := range resolvedEmps {
		canon.Employees = append(canon.Employees, canonEmpRef{EmployeeID: r.row.EmployeeID, RowHash: r.row.RowHash})
	}
	for _, p := range packs {
		canon.Packs = append(canon.Packs, canonPack{
			Pack: p.Pack, Version: p.Version, Status: p.Status, Digest: p.Digest,
			From: p.EffectiveFrom, To: p.EffectiveTo, ReviewRef: p.ReviewRef,
		})
	}
	contentHash := hashJSON(canon)
	var snapID string
	err = tx.QueryRow(ctx,
		`INSERT INTO payroll_snapshot (employer_id, payroll_run_id, pay_period_id, version, status,
		  schema_version, content_hash, calc_engine, jurisdiction, currency, captured_by)
		 VALUES ($1, $2, $3, $4, 'valid', $5, $6, 'unimplemented', $7, $8, $9)
		 RETURNING id`,
		employerID, runID, run.periodID, version, SnapshotSchemaVersion,
		contentHash, run.jurisdiction, snapshotCurrency, operator).Scan(&snapID)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return "", 0, "", apperr.New(apperr.CodeConflict,
				"a valid snapshot already stands for this run; invalidate it first")
		}
		return "", 0, "", apperr.FromDB(err, "persist snapshot")
	}
	for _, r := range resolvedEmps {
		inputsRaw, err := json.Marshal(r.doc)
		if err != nil {
			return "", 0, "", apperr.FromDB(err, "marshal snapshot inputs")
		}
		blockersRaw, err := json.Marshal(r.row.Blockers)
		if err != nil {
			return "", 0, "", apperr.FromDB(err, "marshal snapshot blockers")
		}
		var sheetID *string
		if r.row.ApprovedSheetID != nil {
			sheetID = r.row.ApprovedSheetID
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO payroll_snapshot_employee (snapshot_id, employee_id, employment_terms_ids, status_history_ids,
			  employee_basis_seq, approved_sheet_id, decision, blockers, inputs, row_hash)
			 VALUES ($1, $2, $3::uuid[], $4::uuid[], $5, $6, $7, $8::jsonb, $9::jsonb, $10)`,
			snapID, r.row.EmployeeID, uuidArrayLit(r.row.TermsIDs), uuidArrayLit(r.row.StatusIDs), r.row.BasisSeq,
			nullUUID(sheetID), r.row.Decision, string(blockersRaw), string(inputsRaw), r.row.RowHash); err != nil {
			return "", 0, "", apperr.FromDB(err, "persist snapshot employee")
		}
	}
	for _, p := range packs {
		if _, err := tx.Exec(ctx,
			`INSERT INTO payroll_snapshot_pack (snapshot_id, pack, version, status, manifest_digest,
			  effective_from, effective_to, review_ref)
			 VALUES ($1, $2, $3, $4, $5, $6::date, $7::date, $8)`,
			snapID, p.Pack, p.Version, p.Status, nullStr(p.Digest),
			nullStr(p.EffectiveFrom), nullStr(p.EffectiveTo), nullStr(p.ReviewRef)); err != nil {
			return "", 0, "", apperr.FromDB(err, "persist snapshot pack")
		}
	}
	if err := audit.Write(ctx, tx, employerID, operator, "payroll",
		"snapshot.capture", "payroll_snapshot", &snapID, nil,
		map[string]any{"version": version, "content_hash": contentHash, "employees": len(resolvedEmps)}); err != nil {
		return "", 0, "", err
	}
	return snapID, version, contentHash, nil
}

func GetSnapshot(ctx context.Context, pool *pgxpool.Pool, snapshotID string) (*SnapshotDetail, error) {
	var out SnapshotDetail
	err := pool.QueryRow(ctx,
		`SELECT id, employer_id, payroll_run_id, pay_period_id, version, status,
		  schema_version, content_hash, calc_engine, invalidated_reason
		 FROM payroll_snapshot WHERE id = $1`, snapshotID).Scan(
		&out.ID, &out.EmployerID, &out.RunID, &out.PeriodID, &out.Version, &out.Status,
		&out.SchemaVersion, &out.ContentHash, &out.CalcEngine, &out.InvalidatedReason)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, apperr.New(apperr.CodeNotFound, "snapshot %s not found", snapshotID)
		}
		return nil, apperr.FromDB(err, "read snapshot")
	}
	rows, err := pool.Query(ctx,
		`SELECT employee_id, decision, blockers::text, employment_terms_ids::text, status_history_ids::text,
		  employee_basis_seq, approved_sheet_id::text, inputs::text, row_hash
		 FROM payroll_snapshot_employee WHERE snapshot_id = $1 ORDER BY employee_id`, snapshotID)
	if err != nil {
		return nil, apperr.FromDB(err, "read snapshot employees")
	}
	defer rows.Close()
	out.Employees = []SnapshotEmployee{}
	for rows.Next() {
		var se SnapshotEmployee
		var blockersRaw, termsRaw, statusRaw, inputsRaw string
		var sheetID *string
		var termsIDs, statusIDs []string
		if err := rows.Scan(&se.EmployeeID, &se.Decision, &blockersRaw, &termsRaw, &statusRaw,
			&se.BasisSeq, &sheetID, &inputsRaw, &se.RowHash); err != nil {
			return nil, apperr.FromDB(err, "scan snapshot employee")
		}
		termsIDs, statusIDs = parseUUIDArray(termsRaw), parseUUIDArray(statusRaw)
		se.TermsIDs, se.StatusIDs = termsIDs, statusIDs
		se.ApprovedSheetID = sheetID
		if err := json.Unmarshal([]byte(blockersRaw), &se.Blockers); err != nil {
			return nil, apperr.FromDB(err, "parse snapshot blockers")
		}
		var doc canonEmployee
		if err := json.Unmarshal([]byte(inputsRaw), &doc); err != nil {
			return nil, apperr.FromDB(err, "parse snapshot inputs")
		}
		se.EmployeeNo, se.CompSegments, se.SHGTips, se.Sections = doc.EmployeeNo, doc.Comp, doc.SHG, doc.Sections
		if se.Blockers == nil {
			se.Blockers = []string{}
		}
		if se.CompSegments == nil {
			se.CompSegments = []SnapSegment{}
		}
		if se.SHGTips == nil {
			se.SHGTips = []SnapSHG{}
		}
		out.Employees = append(out.Employees, se)
	}
	if err := rows.Err(); err != nil {
		return nil, apperr.FromDB(err, "read snapshot employees")
	}
	prows, err := pool.Query(ctx,
		`SELECT pack, version, status, manifest_digest, effective_from::text, effective_to::text, review_ref
		 FROM payroll_snapshot_pack WHERE snapshot_id = $1 ORDER BY pack`, snapshotID)
	if err != nil {
		return nil, apperr.FromDB(err, "read snapshot packs")
	}
	defer prows.Close()
	out.Packs = []SnapshotPack{}
	for prows.Next() {
		var p SnapshotPack
		if err := prows.Scan(&p.Pack, &p.Version, &p.Status, &p.Digest,
			&p.EffectiveFrom, &p.EffectiveTo, &p.ReviewRef); err != nil {
			return nil, apperr.FromDB(err, "scan snapshot pack")
		}
		out.Packs = append(out.Packs, p)
	}
	return &out, apperr.FromDB(prows.Err(), "read snapshot packs")
}

// DetectDrift re-resolves current state and compares it against the
// preserved snapshot: population membership, per-employee section
// hashes and rule-pack pins. It reads plain committed state and
// changes nothing.
func DetectDrift(ctx context.Context, pool *pgxpool.Pool, employerID, snapshotID string) (*DriftReport, error) {
	detail, err := GetSnapshot(ctx, pool, snapshotID)
	if err != nil {
		return nil, err
	}
	var start, end string
	if err := pool.QueryRow(ctx,
		`SELECT starts_on::text, ends_on::text FROM pay_period WHERE id = $1`, detail.PeriodID).Scan(&start, &end); err != nil {
		return nil, apperr.FromDB(err, "resolve snapshot period")
	}
	rep := &DriftReport{SnapshotID: snapshotID, Reasons: []string{}}
	stored := map[string]SnapshotEmployee{}
	for _, se := range detail.Employees {
		stored[se.EmployeeID] = se
	}
	current, err := expectedPopulation(ctx, pool, employerID, detail.PeriodID, start, end)
	if err != nil {
		return nil, err
	}
	exclusions, err := runExclusions(ctx, pool, employerID, detail.RunID)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for _, e := range current {
		seen[e.id] = true
		se, ok := stored[e.id]
		if !ok {
			rep.Reasons = append(rep.Reasons,
				fmt.Sprintf("population: employee %s added since capture", e.no))
			continue
		}
		doc, err := resolveEmployee(ctx, pool, employerID, detail.PeriodID, start, end, e, exclusions[e.id])
		if err != nil {
			return nil, err
		}
		now := currentSections(doc)
		for _, section := range []string{"terms", "status", "compensation", "timesheet", "attendance", "shg", "decision"} {
			if now[section] != se.Sections[section] {
				rep.Reasons = append(rep.Reasons,
					fmt.Sprintf("employee %s: %s changed since capture", se.EmployeeNo, section))
			}
		}
	}
	for id, se := range stored {
		if !seen[id] {
			rep.Reasons = append(rep.Reasons,
				fmt.Sprintf("population: employee %s removed since capture", se.EmployeeNo))
		}
	}
	required := make([]string, 0, len(detail.Packs))
	for _, p := range detail.Packs {
		required = append(required, p.Pack)
	}
	now, err := resolvePacks(ctx, pool, employerID, required)
	if err != nil {
		return nil, err
	}
	byPack := map[string]SnapshotPack{}
	for _, n := range now {
		byPack[n.Pack] = n
	}
	for _, p := range detail.Packs {
		n := byPack[p.Pack]
		if n.Version != p.Version || n.Status != p.Status || !sameStr(n.Digest, p.Digest) {
			rep.Reasons = append(rep.Reasons,
				fmt.Sprintf("pack %s changed since capture (%s/%s -> %s/%s)",
					p.Pack, p.Version, p.Status, n.Version, n.Status))
		}
	}
	sort.Strings(rep.Reasons)
	if len(rep.Reasons) > 25 {
		rep.Reasons = append(rep.Reasons[:25], fmt.Sprintf("and %d more", len(rep.Reasons)-25))
	}
	rep.Drifted = len(rep.Reasons) > 0
	return rep, nil
}

// InvalidateSnapshot marks a valid snapshot invalidated with cause
// through the one narrow function. The content stays preserved.
func InvalidateSnapshot(ctx context.Context, pool *pgxpool.Pool, snapshotID, reason, actor string) error {
	if strings.TrimSpace(actor) == "" {
		return apperr.New(apperr.CodeValidation, "operator attribution is required")
	}
	if strings.TrimSpace(reason) == "" {
		return apperr.New(apperr.CodeValidation, "invalidation requires a reason")
	}
	if _, err := pool.Exec(ctx,
		`SELECT invalidate_payroll_snapshot($1::uuid, $2, $3)`, snapshotID, reason, actor); err != nil {
		return apperr.FromDB(err, "invalidate snapshot")
	}
	return nil
}

// currentSections hashes the resolvable sections of one employee doc
// the same way capture does. Timesheet covers the approval, lineage
// and lines; attendance covers the linked intervals and unpaired
// clock evidence, so each drift kind names its own section.
func currentSections(doc canonEmployee) map[string]string {
	type sheetCore struct {
		ID         string      `json:"id"`
		ApprovedAt string      `json:"approved_at"`
		ApprovedBy string      `json:"approved_by"`
		BasisSeq   int64       `json:"basis_seq"`
		Chain      []string    `json:"chain"`
		Lines      []canonLine `json:"lines"`
	}
	type sheetAtt struct {
		Intervals []string `json:"intervals"`
		Unpaired  []string `json:"unpaired_events"`
	}
	var core *sheetCore
	var att *sheetAtt
	if doc.Sheet != nil {
		core = &sheetCore{ID: doc.Sheet.ID, ApprovedAt: doc.Sheet.ApprovedAt,
			ApprovedBy: doc.Sheet.ApprovedBy, BasisSeq: doc.Sheet.BasisSeq,
			Chain: doc.Sheet.Chain, Lines: doc.Sheet.Lines}
		att = &sheetAtt{Intervals: doc.Sheet.Intervals, Unpaired: doc.Sheet.Unpaired}
	}
	return map[string]string{
		"terms":        hashJSON(doc.Terms),
		"status":       hashJSON(doc.Statuses),
		"compensation": hashJSON(doc.Comp),
		"timesheet":    hashJSON(core),
		"attendance":   hashJSON(att),
		"shg":          hashJSON(doc.SHG),
		"decision": hashJSON(struct {
			Decision  string     `json:"decision"`
			Blockers  []string   `json:"blockers"`
			Exclusion *canonExcl `json:"exclusion"`
		}{doc.Decision, doc.Blockers, doc.Exclusion}),
	}
}

type expectedEmp struct {
	id, no string
}

// expectedPopulation derives the expected set exactly like
// readiness: terms overlapping the period, union approved-sheet
// holders. Terminated staff with approved history stay visible.
func expectedPopulation(ctx context.Context, q qtx, employerID, periodID, start, end string) ([]expectedEmp, error) {
	rows, err := q.Query(ctx,
		`SELECT id, employee_no FROM employee WHERE employer_id = $1 AND (
		   EXISTS (SELECT 1 FROM employment_terms t
		     WHERE t.employer_id = employee.employer_id AND t.employee_id = employee.id
		       AND t.effective_from <= $4::date AND (t.effective_to IS NULL OR t.effective_to >= $3::date))
		   OR EXISTS (SELECT 1 FROM timesheet t
		     WHERE t.employer_id = employee.employer_id AND t.employee_id = employee.id
		       AND t.pay_period_id = $2 AND t.status = 'approved'))
		 ORDER BY id`,
		employerID, periodID, start, end)
	if err != nil {
		return nil, apperr.FromDB(err, "derive expected population")
	}
	defer rows.Close()
	var out []expectedEmp
	for rows.Next() {
		var e expectedEmp
		if err := rows.Scan(&e.id, &e.no); err != nil {
			return nil, apperr.FromDB(err, "scan expected population")
		}
		out = append(out, e)
	}
	return out, apperr.FromDB(rows.Err(), "derive expected population")
}

// runExclusions loads the run's explicit exclusions by employee.
func runExclusions(ctx context.Context, q qtx, employerID, runID string) (map[string]canonExcl, error) {
	rows, err := q.Query(ctx,
		`SELECT employee_id, reason, actor FROM payroll_run_exclusion
		 WHERE employer_id = $1 AND payroll_run_id = $2`, employerID, runID)
	if err != nil {
		return nil, apperr.FromDB(err, "load run exclusions")
	}
	defer rows.Close()
	out := map[string]canonExcl{}
	for rows.Next() {
		var id, reason, actor string
		if err := rows.Scan(&id, &reason, &actor); err != nil {
			return nil, apperr.FromDB(err, "scan run exclusions")
		}
		out[id] = canonExcl{Reason: reason, Actor: actor}
	}
	return out, apperr.FromDB(rows.Err(), "load run exclusions")
}

// requiredPackSet is the base set union every effective sheet's packs.
func requiredPackSet(ctx context.Context, q qtx, employerID, periodID string, expected []expectedEmp) ([]string, error) {
	seen := map[string]bool{}
	var out []string
	add := func(p string) {
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	for _, p := range BasePacks {
		add(p)
	}
	for _, e := range expected {
		sheet, err := timesheet.EffectiveApproved(ctx, q, employerID, e.id, periodID)
		if err != nil {
			return nil, err
		}
		if sheet == "" {
			continue
		}
		packs, err := requiredPacks(ctx, q, employerID, sheet)
		if err != nil {
			return nil, err
		}
		for _, p := range packs {
			add(p)
		}
	}
	sort.Strings(out)
	return out, nil
}

// resolveEmployee pins one employee's effective inputs and assesses
// its decision and blockers in readiness order.
func resolveEmployee(ctx context.Context, q qtx, employerID, periodID, start, end string, e expectedEmp, excl canonExcl) (canonEmployee, error) {
	doc := canonEmployee{
		EmployeeID: e.id, EmployeeNo: e.no,
		Terms: []canonTerms{}, Statuses: []canonStatus{},
		Blockers: []string{}, Comp: []SnapSegment{}, SHG: []SnapSHG{},
	}
	trows, err := q.Query(ctx,
		`SELECT id::text, effective_from::text, effective_to::text, monthly_basic::text, basis_seq
		 FROM employment_terms
		 WHERE employer_id = $1 AND employee_id = $2
		   AND effective_from <= $4::date AND (effective_to IS NULL OR effective_to >= $3::date)
		 ORDER BY effective_from, id`,
		employerID, e.id, start, end)
	if err != nil {
		return doc, apperr.FromDB(err, "resolve terms")
	}
	for trows.Next() {
		var t canonTerms
		if err := trows.Scan(&t.ID, &t.From, &t.To, &t.Basic, &t.BasisSeq); err != nil {
			trows.Close()
			return doc, apperr.FromDB(err, "scan terms")
		}
		doc.Terms = append(doc.Terms, t)
	}
	trows.Close()
	if err := trows.Err(); err != nil {
		return doc, apperr.FromDB(err, "resolve terms")
	}
	srows, err := q.Query(ctx,
		`SELECT id::text, status, effective_from::text, effective_to::text
		 FROM employee_status_history
		 WHERE employer_id = $1 AND employee_id = $2 AND effective_from <= $3::date
		 ORDER BY effective_from, id`,
		employerID, e.id, end)
	if err != nil {
		return doc, apperr.FromDB(err, "resolve status history")
	}
	for srows.Next() {
		var s canonStatus
		if err := srows.Scan(&s.ID, &s.Status, &s.From, &s.To); err != nil {
			srows.Close()
			return doc, apperr.FromDB(err, "scan status history")
		}
		doc.Statuses = append(doc.Statuses, s)
	}
	srows.Close()
	if err := srows.Err(); err != nil {
		return doc, apperr.FromDB(err, "resolve status history")
	}
	if err := q.QueryRow(ctx,
		`SELECT COALESCE(max(basis_seq), 0) FROM (
		   SELECT basis_seq FROM employment_terms WHERE employer_id = $1 AND employee_id = $2
		   UNION ALL SELECT c.basis_seq FROM compensation_line c WHERE c.employer_id = $1 AND c.employee_id = $2
		   UNION ALL SELECT basis_seq FROM timesheet WHERE employer_id = $1 AND employee_id = $2 AND pay_period_id = $3
		   UNION ALL SELECT basis_seq FROM shg_instruction WHERE employer_id = $1 AND employee_id = $2
		   UNION ALL SELECT basis_seq FROM attendance_interval WHERE employer_id = $1 AND employee_id = $2
		   UNION ALL SELECT basis_seq FROM clock_event WHERE employer_id = $1 AND employee_id = $2
		 ) s`,
		employerID, e.id, periodID).Scan(&doc.BasisSeq); err != nil {
		return doc, apperr.FromDB(err, "resolve employee basis")
	}
	if excl.Reason != "" {
		doc.Decision = "excluded"
		doc.Exclusion = &canonExcl{Reason: excl.Reason, Actor: excl.Actor}
	}
	sheetID, err := timesheet.EffectiveApproved(ctx, q, employerID, e.id, periodID)
	if err != nil {
		return doc, err
	}
	if sheetID != "" {
		sheet, err := resolveSheet(ctx, q, employerID, e.id, periodID, sheetID)
		if err != nil {
			return doc, err
		}
		doc.Sheet = sheet
	}
	segs, conflict, err := resolveComp(ctx, q, employerID, e.id, start, end)
	if err != nil {
		return doc, err
	}
	doc.Comp = segs
	tips, shgBlocker, err := resolveSHG(ctx, q, employerID, e.id, start, end)
	if err != nil {
		return doc, err
	}
	doc.SHG = tips
	if doc.Decision == "" {
		switch {
		case sheetID == "":
			doc.Decision, doc.Blockers = "missing-timesheet", []string{"no approved timesheet for period"}
		case doc.Sheet != nil && len(doc.Sheet.Lines) == 0:
			doc.Decision, doc.Blockers = "incomplete", []string{"approved timesheet has no payable lines"}
		case len(doc.Terms) == 0:
			doc.Decision, doc.Blockers = "incomplete", []string{"no effective employment terms for period"}
		case shgBlocker != "":
			doc.Decision, doc.Blockers = "incomplete", []string{shgBlocker}
		case conflict != "":
			doc.Decision, doc.Blockers = "conflict", []string{conflict}
		default:
			if c, err := checkTermConflict(ctx, q, employerID, e.id, periodID, sheetID); err != nil {
				return doc, err
			} else if c != "" {
				doc.Decision, doc.Blockers = "conflict", []string{c}
			} else if c, err := checkAttendanceConflict(ctx, q, employerID, e.id, periodID, sheetID); err != nil {
				return doc, err
			} else if c != "" {
				doc.Decision, doc.Blockers = "conflict", []string{c}
			} else if c, err := checkCompConflict(ctx, q, employerID, e.id, periodID, sheetID); err != nil {
				return doc, err
			} else if c != "" {
				doc.Decision, doc.Blockers = "conflict", []string{c}
			} else if c, err := checkSHGConflict(ctx, q, employerID, e.id, periodID, sheetID); err != nil {
				return doc, err
			} else if c != "" {
				doc.Decision, doc.Blockers = "conflict", []string{c}
			} else {
				doc.Decision = "included"
			}
		}
	}
	return doc, nil
}

// resolveSheet pins the effective approved sheet: approval stamps,
// correction chain, payable lines, linked intervals and unpaired
// clock evidence attributed in the employment timezone.
func resolveSheet(ctx context.Context, q qtx, employerID, employeeID, periodID, sheetID string) (*canonSheet, error) {
	sheet := &canonSheet{Chain: []string{}, Lines: []canonLine{}, Intervals: []string{}, Unpaired: []string{}}
	var approvedAt *time.Time
	var approvedBy *string
	err := q.QueryRow(ctx,
		`SELECT approved_at, approved_by, basis_seq FROM timesheet WHERE employer_id = $1 AND id = $2`,
		employerID, sheetID).Scan(&approvedAt, &approvedBy, &sheet.BasisSeq)
	if err != nil {
		return nil, apperr.FromDB(err, "resolve sheet")
	}
	sheet.ID = sheetID
	if approvedAt != nil {
		sheet.ApprovedAt = approvedAt.UTC().Format(time.RFC3339Nano)
	}
	if approvedBy != nil {
		sheet.ApprovedBy = *approvedBy
	}
	chain, err := sheetChain(ctx, q, employerID, sheetID)
	if err != nil {
		return nil, err
	}
	sheet.Chain = chain
	lrows, err := q.Query(ctx,
		`SELECT l.id::text, l.pay_code, l.hours::text
		 FROM timesheet_line l WHERE l.employer_id = $1 AND l.timesheet_id = $2 ORDER BY l.id`,
		employerID, sheetID)
	if err != nil {
		return nil, apperr.FromDB(err, "resolve sheet lines")
	}
	lineIDs := []string{}
	for lrows.Next() {
		var l canonLine
		if err := lrows.Scan(&l.ID, &l.Code, &l.Hours); err != nil {
			lrows.Close()
			return nil, apperr.FromDB(err, "scan sheet lines")
		}
		sheet.Lines = append(sheet.Lines, l)
		lineIDs = append(lineIDs, l.ID)
	}
	lrows.Close()
	if err := lrows.Err(); err != nil {
		return nil, apperr.FromDB(err, "resolve sheet lines")
	}
	if len(lineIDs) > 0 {
		irows, err := q.Query(ctx,
			`SELECT DISTINCT interval_id::text FROM timesheet_line_interval
			 WHERE employer_id = $1 AND line_id = ANY($2::uuid[]) ORDER BY 1`,
			employerID, lineIDs)
		if err != nil {
			return nil, apperr.FromDB(err, "resolve linked intervals")
		}
		for irows.Next() {
			var id string
			if err := irows.Scan(&id); err != nil {
				irows.Close()
				return nil, apperr.FromDB(err, "scan linked intervals")
			}
			sheet.Intervals = append(sheet.Intervals, id)
		}
		irows.Close()
		if err := irows.Err(); err != nil {
			return nil, apperr.FromDB(err, "resolve linked intervals")
		}
	}
	loc, err := employer.EmploymentLocation(ctx, q, employerID)
	if err != nil {
		return nil, err
	}
	urows, err := q.Query(ctx,
		`SELECT e.id::text FROM clock_event e JOIN pay_period p ON p.id = $3
		 WHERE e.employer_id = $1 AND e.employee_id = $2
		   AND (e.occurred_at AT TIME ZONE $4)::date BETWEEN p.starts_on AND p.ends_on
		   AND NOT EXISTS (SELECT 1 FROM attendance_interval i
		     WHERE i.employer_id = e.employer_id AND i.source_refs -> 'clock_event_ids' ? e.id::text)
		   AND NOT EXISTS (SELECT 1 FROM clock_event_disposition d WHERE d.event_id = e.id)
		 ORDER BY e.occurred_at, e.id`,
		employerID, employeeID, periodID, loc.String())
	if err != nil {
		return nil, apperr.FromDB(err, "resolve unpaired events")
	}
	for urows.Next() {
		var id string
		if err := urows.Scan(&id); err != nil {
			urows.Close()
			return nil, apperr.FromDB(err, "scan unpaired events")
		}
		sheet.Unpaired = append(sheet.Unpaired, id)
	}
	urows.Close()
	return sheet, apperr.FromDB(urows.Err(), "resolve unpaired events")
}

// sheetChain walks the supersedes lineage from the root to the
// effective sheet, bounded against pathological chains.
func sheetChain(ctx context.Context, q qtx, employerID, sheetID string) ([]string, error) {
	chain := []string{}
	seen := map[string]bool{}
	for id := sheetID; id != ""; {
		if seen[id] || len(chain) > 32 {
			return nil, apperr.New(apperr.CodeInvalidState, "timesheet supersedes chain is cyclic or too deep")
		}
		seen[id] = true
		chain = append([]string{id}, chain...)
		var parent *string
		if err := q.QueryRow(ctx,
			`SELECT supersedes_id::text FROM timesheet WHERE employer_id = $1 AND id = $2`,
			employerID, id).Scan(&parent); err != nil {
			return nil, apperr.FromDB(err, "walk sheet lineage")
		}
		if parent == nil {
			break
		}
		id = *parent
	}
	return chain, nil
}

// resolveComp resolves effective compensation segments through the
// single shared resolver. Ambiguity returns a conflict blocker, not
// an error: capture records, validation refuses.
func resolveComp(ctx context.Context, q qtx, employerID, employeeID, start, end string) ([]SnapSegment, string, error) {
	segs, err := compbasis.ResolveEffectiveCompensation(ctx, q, employerID, employeeID, start, end)
	if err != nil {
		var bc *compbasis.BasisConflict
		if errors.As(err, &bc) {
			return []SnapSegment{}, bc.Error(), nil
		}
		return nil, "", err
	}
	out := []SnapSegment{}
	for class, list := range segs {
		for _, s := range list {
			out = append(out, SnapSegment{
				Code: class, Amount: s.Amount,
				From: s.From, To: s.To, Lineage: append([]string{}, s.Lineage...),
			})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Code != out[j].Code {
			return out[i].Code < out[j].Code
		}
		return out[i].From < out[j].From
	})
	return out, "", nil
}

// resolveSHG pins the covering SHG chain tips and reuses the shared
// coverage check for the blocker text.
func resolveSHG(ctx context.Context, q qtx, employerID, employeeID, start, end string) ([]SnapSHG, string, error) {
	rows, err := q.Query(ctx,
		`SELECT s.id::text, s.fund, s.status,
		  (s.verified_at IS NOT NULL AND s.verified_by IS NOT NULL AND s.verification_basis IS NOT NULL),
		  s.effective_from::text, s.effective_to::text, s.basis_seq, s.supersedes_id::text
		 FROM shg_instruction s
		 WHERE s.employer_id = $1 AND s.employee_id = $2
		   AND s.effective_from <= $4::date AND (s.effective_to IS NULL OR s.effective_to >= $3::date)
		   AND NOT EXISTS (SELECT 1 FROM shg_instruction t WHERE t.supersedes_id = s.id)
		 ORDER BY s.fund, s.id`,
		employerID, employeeID, start, end)
	if err != nil {
		return nil, "", apperr.FromDB(err, "resolve SHG tips")
	}
	defer rows.Close()
	tips := []SnapSHG{}
	for rows.Next() {
		var tip SnapSHG
		if err := rows.Scan(&tip.ID, &tip.Fund, &tip.Status, &tip.Tripled,
			&tip.From, &tip.To, &tip.BasisSeq, &tip.SupersedesID); err != nil {
			return nil, "", apperr.FromDB(err, "scan SHG tips")
		}
		tips = append(tips, tip)
	}
	if err := rows.Err(); err != nil {
		return nil, "", apperr.FromDB(err, "resolve SHG tips")
	}
	blocker, err := checkSHGCoverage(ctx, q, employerID, employeeID, start, end)
	if err != nil {
		return nil, "", err
	}
	return tips, blocker, nil
}

// resolvePacks pins the current activation of every required pack,
// with the evidence pin where the version was governed.
func resolvePacks(ctx context.Context, q qtx, employerID string, required []string) ([]SnapshotPack, error) {
	out := []SnapshotPack{}
	for _, pack := range required {
		var p SnapshotPack
		p.Pack = pack
		var version, status *string
		err := q.QueryRow(ctx,
			`SELECT version, status FROM rule_pack_activation WHERE employer_id = $1 AND pack = $2`,
			employerID, pack).Scan(&version, &status)
		if err != nil && err != pgx.ErrNoRows {
			return nil, apperr.FromDB(err, "resolve pack activation")
		}
		if err == pgx.ErrNoRows {
			p.Status = "not activated"
			out = append(out, p)
			continue
		}
		p.Version, p.Status = *version, *status
		err = q.QueryRow(ctx,
			`SELECT manifest_digest, effective_from::text, effective_to::text, review_ref
			 FROM rule_pack_activation_history
			 WHERE employer_id = $1 AND pack = $2 AND version = $3
			 ORDER BY activated_at DESC LIMIT 1`,
			employerID, pack, p.Version).Scan(&p.Digest, &p.EffectiveFrom, &p.EffectiveTo, &p.ReviewRef)
		if err != nil && err != pgx.ErrNoRows {
			return nil, apperr.FromDB(err, "resolve pack evidence")
		}
		out = append(out, p)
	}
	return out, nil
}

func nullStr(s *string) any {
	if s == nil {
		return nil
	}
	return *s
}

func nullUUID(s *string) any {
	if s == nil {
		return nil
	}
	return *s
}

func sameStr(a, b *string) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

// uuidArrayLit renders ids as a Postgres uuid[] literal.
func uuidArrayLit(ids []string) string {
	if len(ids) == 0 {
		return "{}"
	}
	return "{" + strings.Join(ids, ",") + "}"
}

// parseUUIDArray parses a Postgres uuid[] literal into sorted text ids.
func parseUUIDArray(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "{}" {
		return []string{}
	}
	raw = strings.TrimPrefix(strings.TrimSuffix(raw, "}"), "{")
	if raw == "" {
		return []string{}
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		out = append(out, strings.Trim(p, `"`))
	}
	sort.Strings(out)
	return out
}

// InspectSnapshot reads one snapshot, refusing snapshots from other
// employers. CLI and REST scope every read to the resolved employer;
// the unscoped GetSnapshot stays for services and tests that already
// hold the run.
func InspectSnapshot(ctx context.Context, pool *pgxpool.Pool, employerID, snapshotID string) (*SnapshotDetail, error) {
	d, err := GetSnapshot(ctx, pool, snapshotID)
	if err != nil {
		return nil, err
	}
	if d.EmployerID != employerID {
		return nil, apperr.New(apperr.CodeNotFound, "snapshot %s not found", snapshotID)
	}
	return d, nil
}
