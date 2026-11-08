package provisioning

import (
	"fmt"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/PithomLabs/a1/tests/dbutil"
)

// S7-P2-FRESHFLAKE residual: roles are cluster-global while test
// databases are per-package, so parallel clean-server setups raced
// CREATE ROLE past the EXISTS guard. The global role lock must live
// on the shared maintenance database — advisory locks do not cross
// database boundaries. Eight parallel setups against a genuinely
// clean ephemeral server must all succeed.
func TestParallelCleanServerRoleProvisioning(t *testing.T) {
	adminDSN := ephemeralCluster(t)
	const workers = 8
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := dbutil.SetupDB(ctx(t), adminDSN, fmt.Sprintf("race_roles_%d", i), nil)
			errs <- err
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("parallel setup: %v", err)
		}
	}
	maint, err := pgx.Connect(ctx(t), adminDSN)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer maint.Close(ctx(t))
	var n int
	if err := maint.QueryRow(ctx(t),
		`SELECT count(*) FROM pg_roles WHERE rolname IN ('migration_admin', 'app')`).Scan(&n); err != nil || n != 2 {
		t.Fatalf("roles=%d err=%v want exactly the pair", n, err)
	}
}
