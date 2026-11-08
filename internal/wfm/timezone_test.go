package wfm

import (
	"strings"
	"testing"

	"github.com/PithomLabs/a1/internal/apperr"
)

// H4 (C-TZ-05): programme timezones are operational configuration,
// so unknown IANA names fail at the boundary instead of detonating
// a later conversion.
func TestCreateProgrammeRejectsUnknownTimezone(t *testing.T) {
	pool := testPool(t)
	emp := mkEmployer(t, pool, uniq(t, "TZP"))
	c, err := CreateClient(ctx(t), pool, emp, "CLI", "Client")
	if err != nil {
		t.Fatalf("client: %v", err)
	}
	if _, err := CreateProgramme(ctx(t), pool, emp, ProgrammeInput{
		Client: c.Code, Code: "BAD", Name: "Bad", Timezone: "Mars/Olympus",
	}); err == nil {
		t.Fatal("expected an unknown timezone to fail")
	} else if apperr.AsCode(err) != apperr.CodeValidation {
		t.Fatalf("code=%s want validation", apperr.AsCode(err))
	}
	p, err := CreateProgramme(ctx(t), pool, emp, ProgrammeInput{
		Client: c.Code, Code: "OK", Name: "Ok", Timezone: "Pacific/Auckland",
	})
	if err != nil {
		t.Fatalf("valid zone: %v", err)
	}
	if p.Timezone != "Pacific/Auckland" {
		t.Fatalf("timezone=%s want the accepted zone", p.Timezone)
	}
	if !strings.Contains(p.ID, "-") {
		t.Fatalf("id=%s want a uuid", p.ID)
	}
}
