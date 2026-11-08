package database

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/PithomLabs/a1/tests/dbutil"
)

// D2: runtime entry points refuse connections that bypass the
// application-role protections. The verdict reads the actual
// current_user off each connection.
func TestRefusePrivilegedRuntime(t *testing.T) {
	setup := dbutil.Setup(t, func(ctx context.Context, migDSN string) error { return nil })
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	for _, tc := range []struct {
		name string
		dsn  string
		want string
	}{
		{"app allowed", setup.AppDSN, ""},
		{"superuser refused", setup.AdminDSN, "superuser"},
		{"migration_admin refused", setup.MigDSN, "migration_admin"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pool, err := Open(ctx, tc.dsn)
			if err != nil {
				t.Fatalf("open: %v", err)
			}
			defer pool.Close()
			err = RefusePrivilegedRuntime(ctx, pool)
			if tc.want == "" && err != nil {
				t.Fatalf("refused: %v", err)
			}
			if tc.want != "" {
				if err == nil {
					t.Fatalf("accepted, want refusal naming %s", tc.want)
				}
				if !strings.Contains(err.Error(), tc.want) {
					t.Fatalf("err = %v, want it to name %s", err, tc.want)
				}
			}
		})
	}
}
