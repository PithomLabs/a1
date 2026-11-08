// Contract test: every served route exists in openapi.yaml as implemented,
// and every implemented spec operation is served. Draft operations must
// never be served. Param names differ between echo (:employee_ref) and
// the spec ({id}), so both normalize to {*} per segment.
package httpapi

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
	"gopkg.in/yaml.v3"
)

var (
	echoParam = regexp.MustCompile(`:[A-Za-z0-9_]+`)
	specParam = regexp.MustCompile(`\{[^}]+\}`)
)

func norm(path string) string {
	path = echoParam.ReplaceAllString(path, "{*}")
	path = specParam.ReplaceAllString(path, "{*}")
	return path
}

type specOp struct {
	maturity string
}

func loadSpec(t *testing.T) map[string]specOp {
	t.Helper()
	raw, err := os.ReadFile("../../openapi.yaml")
	if err != nil {
		t.Fatalf("read openapi.yaml: %v", err)
	}
	// Decode paths generically: method -> {x-maturity, ...}.
	var full struct {
		Paths map[string]map[string]map[string]any `yaml:"paths"`
	}
	if err := yaml.Unmarshal(raw, &full); err != nil {
		t.Fatalf("parse openapi.yaml: %v", err)
	}
	out := map[string]specOp{}
	for p, ops := range full.Paths {
		for method, op := range ops {
			m, _ := op["x-maturity"].(string)
			out[strings.ToUpper(method)+" "+norm(p)] = specOp{maturity: m}
		}
	}
	return out
}

func TestServedSubsetOfSpec(t *testing.T) {
	spec := loadSpec(t)
	e := echo.New()
	if err := Register(e, nil); err != nil {
		t.Fatalf("Register: %v", err)
	}
	for _, r := range e.Routes() {
		if !strings.HasPrefix(r.Path, "/api/v1/") {
			continue
		}
		key := r.Method + " " + norm(r.Path)
		op, ok := spec[key]
		if !ok {
			t.Errorf("served %s has no spec path", key)
			continue
		}
		if op.maturity != "implemented" {
			t.Errorf("served %s is spec %q, want implemented", key, op.maturity)
		}
	}
}

func TestSpecImplementedAllServed(t *testing.T) {
	spec := loadSpec(t)
	e := echo.New()
	if err := Register(e, nil); err != nil {
		t.Fatalf("Register: %v", err)
	}
	served := map[string]bool{}
	for _, r := range e.Routes() {
		served[r.Method+" "+norm(r.Path)] = true
	}
	for key, op := range spec {
		if op.maturity != "implemented" {
			continue
		}
		if strings.HasSuffix(key, "/healthz") || strings.HasSuffix(key, "/readyz") {
			continue // served by core/api, not httpapi.Register
		}
		if !served[key] {
			t.Errorf("spec %s marked implemented but not served", key)
		}
	}
}
