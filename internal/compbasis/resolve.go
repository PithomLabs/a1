package compbasis

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/PithomLabs/a1/internal/apperr"
)

// P3: one deterministic effective compensation basis per
// employee/classification/period. Readiness and snapshot capture
// must call ResolveEffectiveCompensation; no second implementation
// of "effective compensation" may live in another package.

// EffectiveSegment is one maximal interval over which a single
// compensation record is effective for a classification. Segments
// are ordered and non-overlapping; amounts are exact strings.
type EffectiveSegment struct {
	RecordID   string   `json:"record_id"`
	Kind       string   `json:"kind"`
	Code       string   `json:"code"`
	From       string   `json:"from"`
	To         *string  `json:"to"`
	Amount     string   `json:"amount"`
	TermsID    string   `json:"terms_id"`
	Supersedes *string  `json:"supersedes_id"`
	Lineage    []string `json:"lineage"`
}

// BasisConflict is a machine-readable ambiguous/invalid basis:
// classification, stable reason code, involved records, detail.
type BasisConflict struct {
	Classification string   `json:"classification"`
	Reason         string   `json:"reason"`
	RecordIDs      []string `json:"record_ids"`
	Detail         string   `json:"detail"`
}

func (e *BasisConflict) Error() string {
	return fmt.Sprintf("compensation basis conflict %s [%s]: %s (%s)",
		e.Classification, e.Reason, e.Detail, strings.Join(e.RecordIDs, ","))
}

// compRow is one loaded compensation line for carving.
type compRow struct {
	id         string
	kind       string
	code       string
	amount     string
	from       string
	to         *string
	terms      string
	supersedes *string
	termsFrom  string
	termsTo    *string
}

type resolveQuerier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// ResolveEffectiveCompensation resolves the effective segments per
// classification for one employee over [periodStart, periodEnd]
// (YYYY-MM-DD, inclusive). Superseded predecessors are excluded
// except where a successor leaves part of their range uncovered;
// ambiguous or invalid chains return *BasisConflict, never a guess.
func ResolveEffectiveCompensation(ctx context.Context, q resolveQuerier, employerID, employeeID, periodStart, periodEnd string) (map[string][]EffectiveSegment, error) {
	if _, err := time.Parse("2006-01-02", periodStart); err != nil {
		return nil, apperr.New(apperr.CodeValidation, "period start must be YYYY-MM-DD")
	}
	if _, err := time.Parse("2006-01-02", periodEnd); err != nil {
		return nil, apperr.New(apperr.CodeValidation, "period end must be YYYY-MM-DD")
	}
	if periodEnd < periodStart {
		return nil, apperr.New(apperr.CodeValidation, "period end must not precede period start")
	}
	rows, err := q.Query(ctx,
		`SELECT c.id::text, c.kind, c.code, c.amount_monthly::text,
		   c.effective_from::text, c.effective_to::text, c.terms_id::text,
		   c.supersedes_id::text, t.effective_from::text, t.effective_to::text
		 FROM compensation_line c JOIN employment_terms t
		   ON t.employer_id = c.employer_id AND t.id = c.terms_id
		 WHERE c.employer_id = $1 AND c.employee_id = $2
		   AND c.effective_from <= $4::date
		   AND (c.effective_to IS NULL OR c.effective_to >= $3::date)
		 ORDER BY c.kind, c.code, c.effective_from, c.id`,
		employerID, employeeID, periodStart, periodEnd)
	if err != nil {
		return nil, apperr.FromDB(err, "load compensation")
	}
	defer rows.Close()
	var loaded []compRow
	for rows.Next() {
		var r compRow
		if err := rows.Scan(&r.id, &r.kind, &r.code, &r.amount, &r.from, &r.to,
			&r.terms, &r.supersedes, &r.termsFrom, &r.termsTo); err != nil {
			return nil, apperr.FromDB(err, "scan compensation")
		}
		loaded = append(loaded, r)
	}
	if err := rows.Err(); err != nil {
		return nil, apperr.FromDB(err, "load compensation")
	}
	out := map[string][]EffectiveSegment{}
	byClass := map[string][]compRow{}
	for _, r := range loaded {
		key := r.kind + ":" + r.code
		byClass[key] = append(byClass[key], r)
	}
	for key, group := range byClass {
		segs, err := carveSegments(group, periodStart, periodEnd)
		if err != nil {
			return nil, err
		}
		if len(segs) > 0 {
			out[key] = segs
		}
	}
	return out, nil
}

// carveSegments turns one classification's rows into ordered,
// non-overlapping effective segments over the period.
func carveSegments(rows []compRow, periodStart, periodEnd string) ([]EffectiveSegment, error) {
	if len(rows) == 0 {
		return nil, nil
	}
	class := rows[0].kind + ":" + rows[0].code
	byID := map[string]*compRow{}
	for i := range rows {
		byID[rows[i].id] = &rows[i]
	}
	// Chain integrity: single successor, no dangling parents, no cycles.
	children := map[string][]string{}
	for i := range rows {
		r := &rows[i]
		if r.supersedes == nil {
			continue
		}
		if _, ok := byID[*r.supersedes]; !ok {
			// The parent may sit outside the queried period: that
			// is a broken chain for resolution purposes.
			return nil, &BasisConflict{Classification: class, Reason: "dangling-supersedes",
				RecordIDs: []string{r.id}, Detail: "supersedes " + *r.supersedes + " which is not visible"}
		}
		children[*r.supersedes] = append(children[*r.supersedes], r.id)
	}
	for id, kids := range children {
		if len(kids) > 1 {
			ids := append([]string{id}, kids...)
			sort.Strings(ids[1:])
			return nil, &BasisConflict{Classification: class, Reason: "competing-successors",
				RecordIDs: ids, Detail: "more than one successor of " + id}
		}
	}
	const (
		white = 0
		grey  = 1
		black = 2
	)
	color := map[string]int{}
	var visit func(id string, stack []string) *BasisConflict
	visit = func(id string, stack []string) *BasisConflict {
		color[id] = grey
		stack = append(stack, id)
		for _, kid := range children[id] {
			switch color[kid] {
			case grey:
				return &BasisConflict{Classification: class, Reason: "cycle",
					RecordIDs: append(append([]string{}, stack...), kid),
					Detail:    "supersession cycle through " + kid}
			case white:
				if c := visit(kid, stack); c != nil {
					return c
				}
			}
		}
		color[id] = black
		return nil
	}
	for _, r := range rows {
		if color[r.id] == white {
			if c := visit(r.id, nil); c != nil {
				return nil, c
			}
		}
	}
	// Coverage carving: each row owns its range minus the ranges of
	// its transitive successors, clipped to the queried period.
	type span struct {
		from string
		to   string // "" means open-ended
	}
	subtract := func(base span, cuts []span) []span {
		kept := []span{base}
		for _, cut := range cuts {
			var next []span
			for _, k := range kept {
				cutTo := cut.to
				if cutTo == "" {
					cutTo = "9999-12-31"
				}
				kTo := k.to
				if kTo == "" {
					kTo = "9999-12-31"
				}
				if cutTo < k.from || (k.to != "" && cut.from > kTo) {
					next = append(next, k)
					continue
				}
				if cut.from > k.from {
					next = append(next, span{k.from, prevDay(cut.from)})
				}
				if cutTo < kTo {
					end := nextDay(cutTo)
					if k.to == "" {
						next = append(next, span{end, ""})
					} else {
						next = append(next, span{end, k.to})
					}
				}
			}
			kept = next
		}
		return kept
	}
	descRanges := func(id string) []span {
		var out []span
		var walk func(x string)
		walk = func(x string) {
			for _, kid := range children[x] {
				r := byID[kid]
				out = append(out, span{r.from, derefStr(r.to)})
				walk(kid)
			}
		}
		walk(id)
		return out
	}
	lineage := func(id string) []string {
		var chain []string
		cur := id
		for {
			chain = append([]string{cur}, chain...)
			r := byID[cur]
			if r == nil || r.supersedes == nil {
				break
			}
			cur = *r.supersedes
		}
		return chain
	}
	type owned struct {
		row   *compRow
		spans []span
	}
	var ownedRows []owned
	for i := range rows {
		r := &rows[i]
		// Terms containment re-validated at read time.
		if r.from < r.termsFrom {
			return nil, &BasisConflict{Classification: class, Reason: "containment",
				RecordIDs: []string{r.id}, Detail: "effective " + r.from + " predates terms " + r.termsFrom}
		}
		if r.termsTo != nil && (r.to == nil || *r.to > *r.termsTo || r.from > *r.termsTo) {
			return nil, &BasisConflict{Classification: class, Reason: "containment",
				RecordIDs: []string{r.id}, Detail: "line escapes terms ending " + *r.termsTo}
		}
		base := span{maxDay(r.from, periodStart), ""}
		rowTo := derefStr(r.to)
		if rowTo == "" || rowTo > periodEnd {
			base.to = periodEnd
		} else {
			base.to = rowTo
		}
		if base.to != "" && base.to < base.from {
			continue // Outside the queried period.
		}
		spans := subtract(base, descRanges(r.id))
		if len(spans) > 0 {
			ownedRows = append(ownedRows, owned{r, spans})
		}
	}
	// Defense in depth: distinct rows must not own the same date.
	type claim struct {
		from, to string
		id       string
	}
	var claims []claim
	for _, o := range ownedRows {
		for _, s := range o.spans {
			to := s.to
			if to == "" {
				to = "9999-12-31"
			}
			claims = append(claims, claim{s.from, to, o.row.id})
		}
	}
	sort.Slice(claims, func(i, j int) bool {
		if claims[i].from != claims[j].from {
			return claims[i].from < claims[j].from
		}
		return claims[i].to < claims[j].to
	})
	for i := 1; i < len(claims); i++ {
		if claims[i].from <= claims[i-1].to && claims[i].id != claims[i-1].id {
			ids := []string{claims[i-1].id, claims[i].id}
			sort.Strings(ids)
			return nil, &BasisConflict{Classification: class, Reason: "multiple-effective",
				RecordIDs: ids, Detail: "overlapping effective rows on " + claims[i].from}
		}
	}
	var segs []EffectiveSegment
	for _, o := range ownedRows {
		for _, s := range o.spans {
			var to *string
			if s.to != "" {
				t := s.to
				to = &t
			}
			segs = append(segs, EffectiveSegment{
				RecordID: o.row.id, Kind: o.row.kind, Code: o.row.code,
				From: s.from, To: to, Amount: o.row.amount,
				TermsID: o.row.terms, Supersedes: o.row.supersedes,
				Lineage: lineage(o.row.id),
			})
		}
	}
	sort.Slice(segs, func(i, j int) bool {
		if segs[i].From != segs[j].From {
			return segs[i].From < segs[j].From
		}
		return segs[i].RecordID < segs[j].RecordID
	})
	return segs, nil
}

func derefStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func maxDay(a, b string) string {
	if a > b {
		return a
	}
	return b
}

func shiftDay(day string, delta int) string {
	t, err := time.Parse("2006-01-02", day)
	if err != nil {
		return day
	}
	return t.AddDate(0, 0, delta).Format("2006-01-02")
}

func prevDay(day string) string { return shiftDay(day, -1) }
func nextDay(day string) string { return shiftDay(day, 1) }
