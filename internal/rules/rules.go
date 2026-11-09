// Package rules implements the controlled rule-pack activation path:
// manifest loading, evidence verification against the on-disk package,
// and submission to the database activation function. It performs no
// statutory interpretation: a passing verification means the package
// claims IMPLEMENTABLE with complete referenced evidence, not that
// the interpretation is correct.
package rules

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/PithomLabs/a1/internal/apperr"
)

// ManifestSource is one pinned source entry in a rule manifest.
type ManifestSource struct {
	ID          string `json:"id"`
	Digest      string `json:"digest"`
	RetrievedAt string `json:"retrieved_at"`
}

// ManifestReview is the independent-review record in a rule manifest.
type ManifestReview struct {
	Reviewer string `json:"reviewer"`
	Date     string `json:"date"`
	Protocol string `json:"protocol"`
	Outcome  string `json:"outcome"`
}

// ManifestImplementation is the implementation pedigree in a manifest.
type ManifestImplementation struct {
	Owner        string   `json:"owner"`
	Version      string   `json:"version"`
	FixturesGate []string `json:"fixtures_gate"`
}

// Manifest is the parsed rule-pack manifest.json.
type Manifest struct {
	RuleID         string                 `json:"rule_id"`
	Name           string                 `json:"name"`
	Description    string                 `json:"description"`
	Jurisdiction   string                 `json:"jurisdiction"`
	Authority      string                 `json:"authority"`
	Rigor          string                 `json:"rigor"`
	Version        string                 `json:"version"`
	Status         string                 `json:"status"`
	BlockedReason  string                 `json:"blocked_reason"`
	EffectiveFrom  *string                `json:"effective_from"`
	EffectiveUntil *string                `json:"effective_until"`
	Applicability  map[string]any         `json:"applicability"`
	Sources        []ManifestSource       `json:"sources"`
	Dependencies   []string               `json:"dependencies"`
	SpecFiles      []string               `json:"spec_files"`
	Review         ManifestReview         `json:"review"`
	Implementation ManifestImplementation `json:"implementation"`
}

// Evidence is the verified activation submission: identity, version,
// pinned digests and recorded references.
type Evidence struct {
	Pack               string
	Version            string
	Jurisdiction       string
	EffectiveFrom      *string
	EffectiveTo        *string
	ManifestDigest     string
	SpecVersion        string
	ReviewRef          string
	VerificationStatus string
	RefsJSON           string
}

var digestRe = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

// LoadManifest reads and parses the manifest.json of the pack in dir.
func LoadManifest(dir string) (*Manifest, error) {
	raw, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		return nil, apperr.New(apperr.CodeNotFound, "read manifest.json: %v", err)
	}
	var m Manifest
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		return nil, apperr.New(apperr.CodeValidation, "parse manifest.json: %v", err)
	}
	return &m, nil
}

// Verify checks that the package claims IMPLEMENTABLE with complete
// referenced evidence: pinned sources, present spec/review/sources
// files, a spec with no unresolved load-bearing markers, a complete
// review record, and present golden fixtures. It returns the pinned
// evidence submission. Anything less is rejected with the reason.
func (m *Manifest) Verify(dir string) (*Evidence, error) {
	if m.Status != "IMPLEMENTABLE" {
		return nil, apperr.New(apperr.CodeValidation,
			"rule pack %s status is %s, not IMPLEMENTABLE", m.RuleID, m.Status)
	}
	if strings.TrimSpace(m.RuleID) == "" {
		return nil, apperr.New(apperr.CodeValidation, "manifest rule_id is required")
	}
	if strings.TrimSpace(m.Version) == "" {
		return nil, apperr.New(apperr.CodeValidation, "manifest version is required")
	}
	if strings.TrimSpace(m.Jurisdiction) == "" {
		return nil, apperr.New(apperr.CodeValidation, "manifest jurisdiction is required")
	}
	if err := checkEffectiveRange(m.EffectiveFrom, m.EffectiveUntil); err != nil {
		return nil, err
	}
	if len(m.Sources) == 0 {
		return nil, apperr.New(apperr.CodeValidation, "manifest sources are incomplete: no pinned sources")
	}
	for _, s := range m.Sources {
		if strings.TrimSpace(s.ID) == "" || !digestRe.MatchString(strings.TrimSpace(s.Digest)) {
			return nil, apperr.New(apperr.CodeValidation,
				"manifest source %q lacks a pinned sha256 digest", s.ID)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "SOURCES.md")); err != nil {
		return nil, apperr.New(apperr.CodeValidation, "SOURCES.md is missing from the pack")
	}
	if len(m.SpecFiles) == 0 {
		return nil, apperr.New(apperr.CodeValidation, "manifest spec_files is empty")
	}
	specBlobs, err := readPackFiles(dir, m.SpecFiles, "spec")
	if err != nil {
		return nil, err
	}
	for name, body := range specBlobs {
		if strings.Contains(body, "?") {
			return nil, apperr.New(apperr.CodeValidation,
				"%s has unresolved load-bearing markers (?)", name)
		}
		for _, word := range []string{"IMPLEMENTABLE", "VERIFIED"} {
			if strings.Contains(body, word) {
				return nil, apperr.New(apperr.CodeValidation,
					"%s carries the status word %s: status is manifest prose, never spec syntax", name, word)
			}
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "REVIEW.md")); err != nil {
		return nil, apperr.New(apperr.CodeValidation, "REVIEW.md is missing from the pack")
	}
	r := m.Review
	if strings.TrimSpace(r.Reviewer) == "" || strings.TrimSpace(r.Protocol) == "" ||
		strings.TrimSpace(r.Outcome) == "" {
		return nil, apperr.New(apperr.CodeValidation, "manifest review record is incomplete")
	}
	if _, err := time.Parse("2006-01-02", strings.TrimSpace(r.Date)); err != nil {
		return nil, apperr.New(apperr.CodeValidation, "manifest review date must be YYYY-MM-DD")
	}
	if strings.TrimSpace(m.Implementation.Owner) == "" || strings.TrimSpace(m.Implementation.Version) == "" {
		return nil, apperr.New(apperr.CodeValidation, "manifest implementation pedigree is incomplete")
	}
	if len(m.Implementation.FixturesGate) == 0 {
		return nil, apperr.New(apperr.CodeValidation, "manifest fixtures_gate is empty: no golden verification status")
	}
	gateBlobs, err := readPackFiles(dir, m.Implementation.FixturesGate, "fixtures gate")
	if err != nil {
		return nil, err
	}
	manifestRaw, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		return nil, apperr.New(apperr.CodeNotFound, "read manifest.json: %v", err)
	}
	reviewRaw, err := os.ReadFile(filepath.Join(dir, "REVIEW.md"))
	if err != nil {
		return nil, apperr.New(apperr.CodeNotFound, "read REVIEW.md: %v", err)
	}
	ev := &Evidence{
		Pack:               m.RuleID,
		Version:            m.Version,
		Jurisdiction:       m.Jurisdiction,
		EffectiveFrom:      nonEmpty(m.EffectiveFrom),
		EffectiveTo:        nonEmpty(m.EffectiveUntil),
		ManifestDigest:     bundleDigest(manifestRaw, reviewRaw, specBlobs, gateBlobs),
		SpecVersion:        filesDigest(specBlobs),
		ReviewRef:          fmt.Sprintf("%s (%s, %s): %s", r.Reviewer, r.Protocol, r.Date, r.Outcome),
		VerificationStatus: "GOLDENS_VERIFIED",
	}
	refs, err := json.Marshal(map[string]any{
		"sources":       m.Sources,
		"spec_files":    m.SpecFiles,
		"review":        m.Review,
		"fixtures_gate": m.Implementation.FixturesGate,
	})
	if err != nil {
		return nil, apperr.FromDB(err, "marshal evidence refs")
	}
	ev.RefsJSON = string(refs)
	return ev, nil
}

// Activate submits verified evidence through the controlled database
// activation function. pool must be a privileged connection: the app
// role holds no EXECUTE on the function and is refused.
func Activate(ctx context.Context, pool *pgxpool.Pool, employerID, scope string, ev *Evidence, actor, reason string) (string, error) {
	if strings.TrimSpace(actor) == "" {
		return "", apperr.New(apperr.CodeValidation, "operator attribution is required")
	}
	if strings.TrimSpace(reason) == "" {
		return "", apperr.New(apperr.CodeValidation, "a reason is required")
	}
	if strings.TrimSpace(scope) == "" {
		return "", apperr.New(apperr.CodeValidation, "scope is required")
	}
	var id string
	err := pool.QueryRow(ctx,
		`SELECT activate_rule_pack($1::uuid, $2, $3, $4, $5, $6::date, $7::date, $8, $9::jsonb,
		  $10, $11, $12, $13, $14)::text`,
		employerID, ev.Pack, ev.Version, ev.Jurisdiction, scope,
		nullStr(ev.EffectiveFrom), nullStr(ev.EffectiveTo), ev.ManifestDigest, ev.RefsJSON,
		ev.SpecVersion, ev.ReviewRef, ev.VerificationStatus,
		actor, reason).Scan(&id)
	if err != nil {
		return "", apperr.FromDB(err, "activate rule pack")
	}
	return id, nil
}

func checkEffectiveRange(from, until *string) error {
	var f *time.Time
	if from != nil && strings.TrimSpace(*from) != "" {
		t, err := time.Parse("2006-01-02", strings.TrimSpace(*from))
		if err != nil {
			return apperr.New(apperr.CodeValidation, "effective_from must be YYYY-MM-DD or null")
		}
		f = &t
	}
	if until != nil && strings.TrimSpace(*until) != "" {
		t, err := time.Parse("2006-01-02", strings.TrimSpace(*until))
		if err != nil {
			return apperr.New(apperr.CodeValidation, "effective_until must be YYYY-MM-DD or null")
		}
		if f != nil && t.Before(*f) {
			return apperr.New(apperr.CodeValidation, "effective_until must not precede effective_from")
		}
	}
	return nil
}

// readPackFiles reads manifest-referenced files, refusing anything
// that escapes the pack directory.
func readPackFiles(dir string, names []string, what string) (map[string]string, error) {
	out := map[string]string{}
	for _, name := range names {
		if strings.TrimSpace(name) == "" {
			return nil, apperr.New(apperr.CodeValidation, "manifest %s entry is blank", what)
		}
		clean := filepath.Clean(name)
		if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
			return nil, apperr.New(apperr.CodeValidation,
				"manifest %s entry %q escapes the pack directory", what, name)
		}
		raw, err := os.ReadFile(filepath.Join(dir, clean))
		if err != nil {
			return nil, apperr.New(apperr.CodeValidation,
				"manifest %s file %q is missing from the pack", what, name)
		}
		out[clean] = string(raw)
	}
	return out, nil
}

// bundleDigest pins the whole evidence bundle: the manifest, the
// review, and every referenced spec and fixture file.
func bundleDigest(manifest, review []byte, specBlobs, gateBlobs map[string]string) string {
	h := sha256.New()
	h.Write(manifest)
	h.Write([]byte{0})
	h.Write(review)
	h.Write([]byte{0})
	for _, name := range sortedKeys(specBlobs) {
		h.Write([]byte(name))
		h.Write([]byte{0})
		h.Write([]byte(specBlobs[name]))
		h.Write([]byte{0})
	}
	for _, name := range sortedKeys(gateBlobs) {
		h.Write([]byte(name))
		h.Write([]byte{0})
		h.Write([]byte(gateBlobs[name]))
		h.Write([]byte{0})
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil))
}

// filesDigest pins the spec bytes alone as the specification version.
func filesDigest(blobs map[string]string) string {
	h := sha256.New()
	for _, name := range sortedKeys(blobs) {
		h.Write([]byte(name))
		h.Write([]byte{0})
		h.Write([]byte(blobs[name]))
		h.Write([]byte{0})
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil))
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func nonEmpty(s *string) *string {
	if s == nil || strings.TrimSpace(*s) == "" {
		return nil
	}
	v := strings.TrimSpace(*s)
	return &v
}

func nullStr(s *string) any {
	if s == nil {
		return nil
	}
	return *s
}

// Activation is one pack's current activation state for an employer.
// It is the read-only status behind CLI and REST; activation itself
// stays on the controlled administrative path.
type Activation struct {
	Pack    string `json:"pack"`
	Version string `json:"version"`
	Status  string `json:"status"`
}

// ListActivations reads one employer's current pack states. It is a
// read: nothing is locked or audited.
func ListActivations(ctx context.Context, pool *pgxpool.Pool, employerID string) ([]Activation, error) {
	rows, err := pool.Query(ctx,
		`SELECT pack, version, status FROM rule_pack_activation WHERE employer_id = $1 ORDER BY pack`, employerID)
	if err != nil {
		return nil, apperr.FromDB(err, "list activations")
	}
	defer rows.Close()
	out := []Activation{}
	for rows.Next() {
		var a Activation
		if err := rows.Scan(&a.Pack, &a.Version, &a.Status); err != nil {
			return nil, apperr.FromDB(err, "scan activation")
		}
		out = append(out, a)
	}
	return out, apperr.FromDB(rows.Err(), "list activations")
}
