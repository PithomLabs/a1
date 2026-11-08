// Package arch enforces repository-wide architectural boundaries as tests.
package arch

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const modulePath = "github.com/PithomLabs/a1"

// TestCoreDoesNotImportModules enforces the kernel dependency rule with the
// real Go dependency graph: nothing under core/ may depend on modules/ or
// examples/. Terminology greps are heuristic only and live nowhere in gates.
func TestCoreDoesNotImportModules(t *testing.T) {
	out := goList(t, "-deps", modulePath+"/core/...")
	violations := []string{}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if strings.Contains(line, modulePath+"/modules/") || strings.Contains(line, modulePath+"/examples/") {
			violations = append(violations, line)
		}
	}
	if len(violations) > 0 {
		t.Fatalf("core imports business modules:\n%s", strings.Join(violations, "\n"))
	}
}

// TestNoForbiddenDrivers keeps lib/pq out of the dependency tree (pgx only).
func TestNoForbiddenDrivers(t *testing.T) {
	root := moduleRoot(t)
	raw, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "lib/pq") {
		t.Fatal("go.mod must not contain lib/pq (pgx is the sole driver)")
	}
}

func goList(t *testing.T, args ...string) string {
	t.Helper()
	cmd := exec.Command("go", append([]string{"list"}, args...)...)
	cmd.Dir = moduleRoot(t)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go list %v: %v\n%s", args, err, out)
	}
	return string(out)
}

// moduleRoot finds the directory containing go.mod by walking up from this file.
func moduleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found walking up from test directory")
		}
		dir = parent
	}
}
