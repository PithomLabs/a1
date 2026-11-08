package database

import (
	"context"
	"strings"
	"testing"
	"time"
)

// E5: recycling refuses protected and non-disposable names before
// connecting anywhere — these cases need no server at all.
func TestRecycleDatabaseTargetPolicy(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for _, tc := range []struct {
		name string
		want string
	}{
		{"", "no name"},
		{"postgres", "protected"},
		{"template0", "protected"},
		{"template1", "protected"},
		{"POSTGRES", "protected"},
		{"myapp_prod", "recognizably disposable"},
		{"a1", "recognizably disposable"},
		{"workforceops", "recognizably disposable"},
	} {
		t.Run("refuse "+tc.name, func(t *testing.T) {
			err := RecycleDatabase(ctx, "postgresql://invalid:1/x", tc.name, "")
			if err == nil {
				t.Fatalf("recycling %q accepted", tc.name)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want it to mention %q", err, tc.want)
			}
		})
	}
	for _, ok := range []string{
		"demo", "demo_pwreset", "a1test", "a1test_seedreset",
		"test_foo", "tmp_scratch", "scratch_1", "meridian_demo", "wip_test",
	} {
		t.Run("allow "+ok, func(t *testing.T) {
			if !disposableDatabaseName(ok) {
				t.Fatalf("%q rejected by the disposable policy", ok)
			}
		})
	}
}
