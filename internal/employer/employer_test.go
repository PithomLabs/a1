package employer

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	coredatabase "github.com/PithomLabs/a1/core/database"
	"github.com/PithomLabs/a1/internal/apperr"
	coremigrations "github.com/PithomLabs/a1/migrations/core"
	"github.com/PithomLabs/a1/tests/dbutil"
)

// The suite owns a dedicated database, dropped and recreated per run:
// employer bootstrap is only meaningful on an empty employer table,
// which the shared base database cannot guarantee.

var appPool *pgxpool.Pool

func TestMain(m *testing.M) {
	dsn := os.Getenv("WORKFORCEOPS_TEST_DATABASE_URL")
	if dsn == "" {
		fmt.Println("employer: WORKFORCEOPS_TEST_DATABASE_URL not set, skipping")
		os.Exit(0)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		fmt.Printf("employer: parse DSN: %v\n", err)
		os.Exit(1)
	}
	base := cfg.ConnConfig.Database
	if base == "" || base == "postgres" {
		fmt.Println("employer: test DSN must name a dedicated test database")
		os.Exit(1)
	}
	ownDB := base + "_employer"
	// S7-P2-FRESHFLAKE: the base database may not exist on a clean
	// server; create it before connecting (dbutil.CreateDatabase is a
	// no-op when another package already won the race).
	if err := dbutil.CreateDatabase(ctx, dsn, base); err != nil {
		fmt.Printf("employer: create base database: %v\n", err)
		os.Exit(1)
	}
	maint, err := pgx.Connect(ctx, dsn)
	if err != nil {
		fmt.Printf("employer: connect: %v\n", err)
		os.Exit(1)
	}
	if _, err := maint.Exec(ctx, `DROP DATABASE IF EXISTS `+ownDB+` WITH (FORCE)`); err != nil {
		fmt.Printf("employer: drop %s: %v\n", ownDB, err)
		os.Exit(1)
	}
	maint.Close(ctx)
	setup, err := dbutil.SetupDB(ctx, dsn, ownDB,
		func(ctx context.Context, migDSN string) error {
			_, err := coredatabase.MigrateUp(ctx, migDSN, coremigrations.FS)
			return err
		})
	if err != nil {
		fmt.Printf("employer: setup database: %v\n", err)
		os.Exit(1)
	}
	appPool, err = pgxpool.New(ctx, setup.AppDSN)
	if err != nil {
		fmt.Printf("employer: app pool: %v\n", err)
		os.Exit(1)
	}
	var who string
	if err := appPool.QueryRow(ctx, `SELECT current_user`).Scan(&who); err != nil || who != "app" {
		fmt.Printf("employer: suite pool runs as %q, want app role\n", who)
		os.Exit(1)
	}
	code := m.Run()
	appPool.Close()
	os.Exit(code)
}

func ctx(t *testing.T) context.Context {
	t.Helper()
	c, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	return c
}

func input(code string) EmployerInput {
	return EmployerInput{
		Code: code, Name: code + " Pte Ltd", UEN: "UEN-" + code,
		Address: "1 Test Row", Jurisdiction: "SG",
	}
}

func employerCount(t *testing.T) int {
	t.Helper()
	var n int
	if err := appPool.QueryRow(ctx(t), `SELECT count(*) FROM employer`).Scan(&n); err != nil {
		t.Fatalf("count employers: %v", err)
	}
	return n
}

func TestCreateEmployer(t *testing.T) {
	got, err := Create(ctx(t), appPool, input("MERIDIAN"), "op-1")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if got.Existed || got.Employer.Code != "MERIDIAN" {
		t.Fatalf("creation = %+v, want fresh MERIDIAN", got)
	}
	var actor, service, entity, after string
	if err := appPool.QueryRow(ctx(t),
		`SELECT actor, service, entity, after_state::text FROM audit_event
		 WHERE employer_id = $1 AND action = 'employer.create'`,
		got.Employer.ID).Scan(&actor, &service, &entity, &after); err != nil {
		t.Fatalf("audit: %v", err)
	}
	if actor != "op-1" || service != "employer" || entity != "employer" {
		t.Fatalf("audit = %s/%s/%s", actor, service, entity)
	}
	if after == "" || employerCount(t) != 1 {
		t.Fatalf("after = %q, employers = %d", after, employerCount(t))
	}
}

func TestCreateRepeatSameCode(t *testing.T) {
	first, err := Create(ctx(t), appPool, input("MERIDIAN"), "op-1")
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	again, err := Create(ctx(t), appPool, input("MERIDIAN"), "op-2")
	if err != nil {
		t.Fatalf("repeat: %v", err)
	}
	if !again.Existed || again.Employer.ID != first.Employer.ID {
		t.Fatalf("repeat = %+v, want the existing row flagged", again)
	}
	if employerCount(t) != 1 {
		t.Fatalf("employers = %d, want 1", employerCount(t))
	}
	var audits int
	if err := appPool.QueryRow(ctx(t),
		`SELECT count(*) FROM audit_event WHERE employer_id = $1 AND action = 'employer.create'`,
		first.Employer.ID).Scan(&audits); err != nil || audits != 1 {
		t.Fatalf("audits = %d, err = %v, want exactly the creation", audits, err)
	}
}

func TestCreateSecondCodeRejected(t *testing.T) {
	if _, err := Create(ctx(t), appPool, input("MERIDIAN"), "op-1"); err != nil {
		t.Fatalf("first: %v", err)
	}
	_, err := Create(ctx(t), appPool, input("OTHER"), "op-1")
	if err == nil {
		t.Fatal("second employer code accepted")
	}
	var ae *apperr.Error
	if !errors.As(err, &ae) || ae.Code != apperr.CodeConflict {
		t.Fatalf("err = %v, want conflict", err)
	}
	if employerCount(t) != 1 {
		t.Fatalf("employers = %d, want 1", employerCount(t))
	}
}

func TestCreateValidation(t *testing.T) {
	bad := input("MERIDIAN")
	bad.Jurisdiction = "XX"
	if _, err := Create(ctx(t), appPool, bad, "op"); err == nil {
		t.Fatal("unknown jurisdiction accepted")
	}
	empty := input(" ")
	if _, err := Create(ctx(t), appPool, empty, "op"); err == nil {
		t.Fatal("blank code accepted")
	}
	if _, err := Create(ctx(t), appPool, input("MERIDIAN"), " "); err == nil {
		t.Fatal("missing operator accepted")
	}
}

// Two concurrent bootstraps admit exactly one creator; the loser
// observes the winner through the same lock, so exactly one row and
// one audit survive however they interleave.
func TestConcurrentCreateSingleWinner(t *testing.T) {
	dsn := os.Getenv("WORKFORCEOPS_TEST_DATABASE_URL")
	cfg, _ := pgxpool.ParseConfig(dsn)
	raceDB := cfg.ConnConfig.Database + "_employer_race"
	maint, err := pgx.Connect(ctx(t), dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer maint.Close(context.Background())
	if _, err := maint.Exec(ctx(t), `DROP DATABASE IF EXISTS `+raceDB+` WITH (FORCE)`); err != nil {
		t.Fatalf("drop %s: %v", raceDB, err)
	}
	setup, err := dbutil.SetupDB(ctx(t), dsn, raceDB,
		func(ctx context.Context, migDSN string) error {
			_, err := coredatabase.MigrateUp(ctx, migDSN, coremigrations.FS)
			return err
		})
	if err != nil {
		t.Fatalf("setup %s: %v", raceDB, err)
	}
	pool, err := pgxpool.New(ctx(t), setup.AppDSN)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	defer pool.Close()
	var wg sync.WaitGroup
	gots := make([]*Creation, 2)
	errs := make([]error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			gots[n], errs[n] = Create(ctx(t), pool, input("RACE"), "op")
		}(i)
	}
	wg.Wait()
	for n, err := range errs {
		if err != nil {
			t.Fatalf("create %d: %v", n, err)
		}
	}
	if gots[0].Employer.ID != gots[1].Employer.ID {
		t.Fatal("concurrent creates produced two employers")
	}
	if gots[0].Existed == gots[1].Existed {
		t.Fatalf("existed flags = %v/%v, want one creator and one observer",
			gots[0].Existed, gots[1].Existed)
	}
	var n, audits int
	if err := pool.QueryRow(ctx(t), `SELECT count(*) FROM employer`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("employers = %d, err = %v", n, err)
	}
	if err := pool.QueryRow(ctx(t),
		`SELECT count(*) FROM audit_event WHERE action = 'employer.create'`).Scan(&audits); err != nil || audits != 1 {
		t.Fatalf("audits = %d, err = %v", audits, err)
	}
}
