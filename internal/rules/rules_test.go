package rules

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeFixturePack writes a minimal valid IMPLEMENTABLE fixture pack to
// dir. Tests mutate the result to prove each rejection. Fixture packs
// are synthetic and isolated: activation tests never touch rules/.
func writeFixturePack(t *testing.T, dir string) {
	t.Helper()
	manifest := `{
  "rule_id": "TEST-FIXTURE-001",
  "name": "Fixture pack for activation tests",
  "description": "Synthetic pack exercising the controlled activation path.",
  "jurisdiction": "SG",
  "authority": "TEST",
  "rigor": "L1",
  "version": "1.0.0",
  "status": "IMPLEMENTABLE",
  "effective_from": "2026-01-01",
  "effective_until": null,
  "applicability": {},
  "sources": [{"id": "S1", "digest": "sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855", "retrieved_at": "2026-01-01T00:00:00Z"}],
  "dependencies": [],
  "spec_files": ["SPEC.pseudo.md"],
  "review": {"reviewer": "fixture-reviewer", "date": "2026-01-02", "protocol": "MRC", "outcome": "accepted"},
  "implementation": {"owner": "fixture-owner", "version": "1.0.0", "fixtures_gate": ["fixtures/golden.json"]}
}`
	spec := "# source: S1\n\n```text\nclaim base-rate\n  given monthly-rated\n  given basic 2600\n```\n"
	review := "# Fixture review\n\nOutcome: accepted. All load-bearing points resolved.\n"
	sources := "# S1: fixture source\n- authority: TEST\n- scope: all\n"
	golden := `{"cases": [{"id": "G1", "expect": "covered"}]}`
	for name, body := range map[string]string{
		"manifest.json":        manifest,
		"SPEC.pseudo.md":       spec,
		"REVIEW.md":            review,
		"SOURCES.md":           sources,
		"fixtures/golden.json": golden,
	} {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
}

func readManifest(t *testing.T, dir string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	return string(raw)
}

func writeManifest(t *testing.T, dir, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(body), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
}

func TestVerifyValidFixturePack(t *testing.T) {
	dir := t.TempDir()
	writeFixturePack(t, dir)
	m, err := LoadManifest(dir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	ev, err := m.Verify(dir)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if ev.Pack != "TEST-FIXTURE-001" || ev.Version != "1.0.0" || ev.Jurisdiction != "SG" {
		t.Fatalf("evidence=%+v", ev)
	}
	if !strings.HasPrefix(ev.ManifestDigest, "sha256:") || len(ev.ManifestDigest) != 7+64 {
		t.Fatalf("digest=%q want sha256 hex", ev.ManifestDigest)
	}
	if !strings.HasPrefix(ev.SpecVersion, "sha256:") {
		t.Fatalf("spec version=%q want pinned spec bytes", ev.SpecVersion)
	}
	if ev.EffectiveFrom == nil || *ev.EffectiveFrom != "2026-01-01" || ev.EffectiveTo != nil {
		t.Fatalf("effective=%v/%v", ev.EffectiveFrom, ev.EffectiveTo)
	}
	if !strings.Contains(ev.ReviewRef, "fixture-reviewer") || !strings.Contains(ev.ReviewRef, "accepted") {
		t.Fatalf("review ref=%q", ev.ReviewRef)
	}
	// Deterministic: a second verification pins the same digest.
	ev2, err := m.Verify(dir)
	if err != nil {
		t.Fatalf("re-verify: %v", err)
	}
	if ev2.ManifestDigest != ev.ManifestDigest || ev2.SpecVersion != ev.SpecVersion {
		t.Fatalf("digests differ: %q vs %q", ev.ManifestDigest, ev2.ManifestDigest)
	}
}

func TestVerifyRejects(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(t *testing.T, dir string)
		want   string
	}{
		{"blocked status", func(t *testing.T, dir string) {
			writeManifest(t, dir, strings.Replace(readManifest(t, dir), `"IMPLEMENTABLE"`, `"BLOCKED"`, 1))
		}, "not IMPLEMENTABLE"},
		{"discovered status", func(t *testing.T, dir string) {
			writeManifest(t, dir, strings.Replace(readManifest(t, dir), `"IMPLEMENTABLE"`, `"DISCOVERED"`, 1))
		}, "not IMPLEMENTABLE"},
		{"missing review outcome", func(t *testing.T, dir string) {
			writeManifest(t, dir, strings.Replace(readManifest(t, dir), `"outcome": "accepted"`, `"outcome": ""`, 1))
		}, "review"},
		{"missing reviewer", func(t *testing.T, dir string) {
			writeManifest(t, dir, strings.Replace(readManifest(t, dir), `"reviewer": "fixture-reviewer"`, `"reviewer": ""`, 1))
		}, "review"},
		{"empty sources", func(t *testing.T, dir string) {
			raw := readManifest(t, dir)
			start := strings.Index(raw, `"sources": [`)
			end := strings.Index(raw[start:], "],") + start + 1
			writeManifest(t, dir, raw[:start]+`"sources": []`+raw[end:])
		}, "sources"},
		{"source without digest", func(t *testing.T, dir string) {
			writeManifest(t, dir, strings.Replace(readManifest(t, dir),
				`"digest": "sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"`,
				`"digest": ""`, 1))
		}, "digest"},
		{"missing spec file", func(t *testing.T, dir string) {
			if err := os.Remove(filepath.Join(dir, "SPEC.pseudo.md")); err != nil {
				t.Fatalf("remove: %v", err)
			}
		}, "SPEC.pseudo.md"},
		{"unresolved marker in spec", func(t *testing.T, dir string) {
			p := filepath.Join(dir, "SPEC.pseudo.md")
			raw, _ := os.ReadFile(p)
			raw = append(raw, []byte("\n? rounding rule not pinned\n")...)
			if err := os.WriteFile(p, raw, 0o644); err != nil {
				t.Fatalf("write spec: %v", err)
			}
		}, "unresolved"},
		{"status word in spec", func(t *testing.T, dir string) {
			p := filepath.Join(dir, "SPEC.pseudo.md")
			raw, _ := os.ReadFile(p)
			raw = append(raw, []byte("\nclaim status is IMPLEMENTABLE\n")...)
			if err := os.WriteFile(p, raw, 0o644); err != nil {
				t.Fatalf("write spec: %v", err)
			}
		}, "status word"},
		{"missing review file", func(t *testing.T, dir string) {
			if err := os.Remove(filepath.Join(dir, "REVIEW.md")); err != nil {
				t.Fatalf("remove: %v", err)
			}
		}, "REVIEW.md"},
		{"missing sources file", func(t *testing.T, dir string) {
			if err := os.Remove(filepath.Join(dir, "SOURCES.md")); err != nil {
				t.Fatalf("remove: %v", err)
			}
		}, "SOURCES.md"},
		{"empty fixtures gate", func(t *testing.T, dir string) {
			writeManifest(t, dir, strings.Replace(readManifest(t, dir),
				`"fixtures_gate": ["fixtures/golden.json"]`, `"fixtures_gate": []`, 1))
		}, "fixtures_gate"},
		{"missing gate file", func(t *testing.T, dir string) {
			if err := os.Remove(filepath.Join(dir, "fixtures", "golden.json")); err != nil {
				t.Fatalf("remove: %v", err)
			}
		}, "golden.json"},
		{"missing implementation owner", func(t *testing.T, dir string) {
			writeManifest(t, dir, strings.Replace(readManifest(t, dir),
				`"owner": "fixture-owner"`, `"owner": ""`, 1))
		}, "implementation"},
		{"bad effective range", func(t *testing.T, dir string) {
			writeManifest(t, dir, strings.Replace(readManifest(t, dir),
				`"effective_from": "2026-01-01"`, `"effective_from": "01/01/2026"`, 1))
		}, "effective_from"},
		{"inverted effective range", func(t *testing.T, dir string) {
			raw := readManifest(t, dir)
			raw = strings.Replace(raw, `"effective_until": null`, `"effective_until": "2025-01-01"`, 1)
			writeManifest(t, dir, raw)
		}, "effective_until"},
		{"spec path escape", func(t *testing.T, dir string) {
			writeManifest(t, dir, strings.Replace(readManifest(t, dir),
				`"spec_files": ["SPEC.pseudo.md"]`, `"spec_files": ["../escape.md"]`, 1))
		}, "escape"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			writeFixturePack(t, dir)
			c.mutate(t, dir)
			m, err := LoadManifest(dir)
			if err != nil {
				if !strings.Contains(err.Error(), c.want) {
					t.Fatalf("load err=%v want %q", err, c.want)
				}
				return
			}
			if _, err := m.Verify(dir); err == nil {
				t.Fatalf("verified, want rejection containing %q", c.want)
			} else if !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err=%v want %q", err, c.want)
			}
		})
	}
}

func TestDigestChangesWithBytes(t *testing.T) {
	dir := t.TempDir()
	writeFixturePack(t, dir)
	m, err := LoadManifest(dir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	before, err := m.Verify(dir)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	p := filepath.Join(dir, "fixtures", "golden.json")
	raw, _ := os.ReadFile(p)
	raw = append(raw, ' ')
	if err := os.WriteFile(p, raw, 0o644); err != nil {
		t.Fatalf("tamper: %v", err)
	}
	after, err := m.Verify(dir)
	if err != nil {
		t.Fatalf("verify after tamper: %v", err)
	}
	if after.ManifestDigest == before.ManifestDigest {
		t.Fatal("tampered bundle keeps the same digest")
	}
}
