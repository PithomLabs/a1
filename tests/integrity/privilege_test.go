package integrity

import (
	"strings"
	"testing"
)

// C9 + S7-P2-MATRIX-OPEN + H6: every trigger function and controlled
// function is verified against the catalog — owner, SECURITY DEFINER
// status, search_path, schema-qualified access and execute
// privileges — and the privilege matrix pins what the app role may
// and may not do.
//
// The matrix is fail-closed: it enumerates every table in the public
// schema and every owned function, and anything without an explicit
// classification fails. A future table inheriting full DML from the
// default privileges cannot slip through green.

type funcSpec struct {
	name     string
	definer  bool
	path     string // exact proconfig element; "" means no SET clause
	contains []string
	absent   []string
	appExec  bool
}

var newFunctions = []funcSpec{
	{"maintain_interval_currency", true, "search_path=pg_catalog",
		[]string{"UPDATE public.attendance_interval"}, []string{"UPDATE attendance_interval"}, false},
	{"check_interval_supersession", false, "search_path=pg_catalog, public",
		[]string{"FROM public.attendance_interval"}, []string{"FROM attendance_interval"}, false},
	{"check_timesheet_line_identity", false, "search_path=pg_catalog, public",
		[]string{"FROM public.timesheet_line_interval"}, []string{}, false},
	{"check_timesheet_identity", false, "search_path=pg_catalog, public",
		[]string{"FROM public.timesheet_line"}, []string{}, false},
	{"check_roster_transition", false, "search_path=pg_catalog, public",
		[]string{"FROM public.roster_revision"}, []string{}, false},
	{"normalize_terms_created_at", false, "search_path=pg_catalog, public",
		[]string{}, []string{}, false},
	{"normalize_compensation_created_at", false, "search_path=pg_catalog, public",
		[]string{}, []string{}, false},
	{"check_clock_event_disposition", false, "search_path=pg_catalog, public",
		[]string{"FROM public.clock_event"}, []string{}, false},
	{"close_employment_terms", true, "search_path=pg_catalog",
		[]string{"public.employment_terms", "public.audit_event", "public.acquire_payroll_population_lock"},
		[]string{"FROM employment_terms ", "UPDATE employment_terms ", "INTO audit_event "}, true},
	{"transition_employee_status", true, "search_path=pg_catalog",
		[]string{"public.employee ", "public.employee_status_history", "public.audit_event",
			"public.acquire_payroll_population_lock", "public.acquire_payroll_input_lock"},
		[]string{"FROM employee ", "UPDATE employee ", "FROM employment_terms ",
			"UPDATE employment_terms ", "INTO audit_event ", "INTO employee_status_history "}, true},
	// 00011 barrier and enforcement objects.
	{"acquire_payroll_input_lock", false, "search_path=pg_catalog",
		[]string{"pg_advisory_xact_lock"}, []string{}, true},
	{"assign_pay_basis_seq", false, "search_path=pg_catalog",
		[]string{"public.acquire_payroll_input_lock", "public.pay_basis_seq"},
		[]string{}, false},
	{"check_timesheet_approval_barrier", false, "search_path=pg_catalog",
		[]string{"public.acquire_payroll_input_lock"}, []string{}, false},
	{"check_correction_group_open", false, "search_path=pg_catalog",
		[]string{"public.attendance_correction_group"}, []string{}, false},
	{"check_correction_group_complete", false, "search_path=pg_catalog",
		[]string{"public.attendance_correction_group_predecessor", "public.attendance_interval"}, []string{}, false},
	{"employment_tz", false, "search_path=pg_catalog",
		[]string{"Asia/Singapore"}, []string{}, true},
	{"check_interval_local_date", false, "search_path=pg_catalog",
		[]string{"public.employer", "public.employment_tz"}, []string{}, false},
	{"check_compensation_line", false, "search_path=pg_catalog",
		[]string{"public.compensation_line", "public.employment_terms"}, []string{}, false},
	{"add_compensation", true, "search_path=pg_catalog",
		[]string{"public.compensation_line", "public.audit_event", "public.acquire_payroll_input_lock"},
		[]string{"INTO compensation_line ", "INTO audit_event "}, true},
	{"correct_compensation", true, "search_path=pg_catalog",
		[]string{"public.compensation_line", "public.audit_event", "public.acquire_payroll_input_lock"},
		[]string{"INTO compensation_line ", "INTO audit_event "}, true},
	{"normalize_clock_event_created_at", false, "search_path=pg_catalog, public",
		[]string{}, []string{}, false},
	{"normalize_interval_created_at", false, "search_path=pg_catalog, public",
		[]string{}, []string{}, false},
	// 00012 SHG lifecycle objects.
	{"check_shg_instruction", false, "search_path=pg_catalog",
		[]string{"public.shg_instruction"}, []string{}, false},
	{"add_shg_instruction", true, "search_path=pg_catalog",
		[]string{"public.shg_instruction", "public.audit_event", "public.acquire_payroll_input_lock"},
		[]string{"INTO shg_instruction ", "INTO audit_event "}, true},
	{"verify_shg_instruction", true, "search_path=pg_catalog",
		[]string{"public.shg_instruction", "public.audit_event", "public.acquire_payroll_input_lock"},
		[]string{"INTO shg_instruction ", "INTO audit_event "}, true},
	{"correct_shg_instruction", true, "search_path=pg_catalog",
		[]string{"public.shg_instruction", "public.audit_event", "public.acquire_payroll_input_lock"},
		[]string{"INTO shg_instruction ", "INTO audit_event "}, true},
	{"normalize_shg_created_at", false, "search_path=pg_catalog, public",
		[]string{}, []string{}, false},
	// 00012 rule-pack activation objects. activate_rule_pack is
	// deliberately NOT executable by app: the missing grant is the
	// privilege boundary.
	{"activate_rule_pack", true, "search_path=pg_catalog",
		[]string{"public.rule_pack ", "public.rule_pack_activation", "public.rule_pack_activation_history", "public.audit_event", "public.acquire_rule_pack_lock"},
		[]string{"INTO rule_pack_activation ", "INTO audit_event "}, false},
	{"ensure_pack_blocked", true, "search_path=pg_catalog",
		[]string{"public.rule_pack ", "public.rule_pack_activation"},
		[]string{"INTO rule_pack_activation "}, true},
	// 00012 snapshot capture objects.
	{"acquire_payroll_population_lock", false, "search_path=pg_catalog",
		[]string{"pay-population"}, []string{}, true},
	{"acquire_rule_pack_lock", false, "search_path=pg_catalog",
		[]string{"pay-pack"}, []string{}, true},
	{"take_payroll_population_lock", false, "search_path=pg_catalog",
		[]string{"public.acquire_payroll_population_lock"}, []string{}, false},
	{"invalidate_payroll_snapshot", true, "search_path=pg_catalog",
		[]string{"public.payroll_snapshot", "public.audit_event"},
		[]string{"INTO payroll_snapshot ", "INTO audit_event "}, true},
	// 00012 run-lifecycle trigger: trigger-only, never executed by
	// app directly.
	{"check_run_lifecycle", false, "search_path=pg_catalog",
		[]string{"FROM public.payroll_run", "FROM public.payroll_snapshot ",
			"public.payroll_snapshot_employee", "public.payroll_snapshot_pack"},
		[]string{"FROM payroll_run ", "FROM payroll_snapshot "}, false},
	{"check_programme_timezone", false, "search_path=pg_catalog",
		[]string{"AT TIME ZONE NEW.timezone"}, []string{}, false},
	// H6 legacy trigger-only functions: EXECUTE revoked, no SET
	// clause (pinned as-is; the absence is the wart, not the goal).
	{"forbid_if_timesheet_decided", false, "",
		[]string{}, []string{}, false},
	{"forbid_unless_roster_draft_revision", false, "",
		[]string{}, []string{}, false},
	{"forbid_if_run_approved", false, "",
		[]string{}, []string{}, false},
	{"forbid_unless_run_draft", false, "",
		[]string{}, []string{}, false},
	{"forbid_if_leave_decided", false, "",
		[]string{}, []string{}, false},
	{"enforce_interval_single_chain", false, "",
		[]string{}, []string{}, false},
	{"enforce_line_interval_employee", false, "",
		[]string{}, []string{}, false},
}

func TestFunctionCatalogAttributes(t *testing.T) {
	for _, fn := range newFunctions {
		t.Run(fn.name, func(t *testing.T) {
			var owner string
			var definer bool
			var path []string
			var def string
			err := appPool.QueryRow(ctx(t),
				`SELECT u.rolname, p.prosecdef, p.proconfig, pg_get_functiondef(p.oid)
				 FROM pg_proc p JOIN pg_roles u ON u.oid = p.proowner
				 WHERE p.proname = $1`, fn.name).Scan(&owner, &definer, &path, &def)
			expectOK(t, err, "read function "+fn.name)
			if owner != "migration_admin" {
				t.Fatalf("%s owner = %s, want migration_admin", fn.name, owner)
			}
			if definer != fn.definer {
				t.Fatalf("%s SECURITY DEFINER = %v, want %v", fn.name, definer, fn.definer)
			}
			if fn.path == "" {
				if len(path) != 0 {
					t.Fatalf("%s search_path = %v, want unset", fn.name, path)
				}
			} else if len(path) != 1 || path[0] != fn.path {
				t.Fatalf("%s search_path = %v, want [%s]", fn.name, path, fn.path)
			}
			for _, want := range fn.contains {
				if !strings.Contains(def, want) {
					t.Fatalf("%s definition lacks %q", fn.name, want)
				}
			}
			for _, bad := range fn.absent {
				if strings.Contains(def, bad) {
					t.Fatalf("%s definition keeps unqualified %q", fn.name, bad)
				}
			}
		})
	}
}

func TestFunctionExecutePrivileges(t *testing.T) {
	for _, fn := range newFunctions {
		t.Run(fn.name, func(t *testing.T) {
			var oid string
			err := appPool.QueryRow(ctx(t),
				`SELECT p.oid::text FROM pg_proc p WHERE p.proname = $1`, fn.name).Scan(&oid)
			expectOK(t, err, "resolve "+fn.name)
			var appExec bool
			err = appPool.QueryRow(ctx(t),
				`SELECT has_function_privilege('app', $1::oid, 'EXECUTE')`, oid).Scan(&appExec)
			expectOK(t, err, "app execute on "+fn.name)
			if appExec != fn.appExec {
				t.Fatalf("%s app EXECUTE = %v, want %v", fn.name, appExec, fn.appExec)
			}
			// PUBLIC must hold EXECUTE on none of them: a NULL
			// proacl would mean the default PUBLIC grant.
			var acl *string
			err = appPool.QueryRow(ctx(t),
				`SELECT proacl::text FROM pg_proc WHERE oid = $1`, oid).Scan(&acl)
			expectOK(t, err, "read acl of "+fn.name)
			if acl == nil {
				t.Fatalf("%s has default ACL: PUBLIC keeps EXECUTE", fn.name)
			}
			var publicExec int
			err = appPool.QueryRow(ctx(t),
				`SELECT count(*) FROM aclexplode((SELECT proacl FROM pg_proc WHERE oid = $1))
				 AS a(grantor, grantee, priv, grantable)
				 WHERE a.grantee = 0 AND a.priv = 'EXECUTE'`, oid).Scan(&publicExec)
			expectOK(t, err, "PUBLIC execute on "+fn.name)
			if publicExec != 0 {
				t.Fatalf("%s grants EXECUTE to PUBLIC", fn.name)
			}
		})
	}
}

// Every owned function in the public schema is pinned above:
// extension helpers excluded, anything else unpinned fails.
func TestFunctionCatalogComplete(t *testing.T) {
	rows, err := appPool.Query(ctx(t),
		`SELECT p.proname FROM pg_proc p JOIN pg_namespace n ON n.oid = p.pronamespace
		 WHERE n.nspname = 'public'
		   AND NOT EXISTS (SELECT 1 FROM pg_depend d WHERE d.objid = p.oid AND d.deptype = 'e')
		 ORDER BY p.proname`)
	expectOK(t, err, "enumerate owned functions")
	defer rows.Close()
	pinned := map[string]bool{}
	for _, fn := range newFunctions {
		pinned[fn.name] = true
	}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan: %v", err)
		}
		if !pinned[name] {
			t.Fatalf("function %s has no catalog classification", name)
		}
	}
	expectOK(t, rows.Err(), "enumerate owned functions")
}

// Every new trigger exists on its table under its specified name.
func TestNewTriggersExist(t *testing.T) {
	want := map[string][]string{
		"attendance_interval_currency":           {"attendance_interval"},
		"attendance_interval_supersession":       {"attendance_interval"},
		"timesheet_line_interval_single_chain":   {"timesheet_line_interval"},
		"timesheet_line_interval_employee_match": {"timesheet_line_interval"},
		"timesheet_line_identity":                {"timesheet_line"},
		"timesheet_identity":                     {"timesheet"},
		"roster_transition":                      {"roster"},
		"employment_terms_created_at_normalize":  {"employment_terms"},
		"compensation_created_at_normalize":      {"compensation_line"},
		"clock_event_disposition_match":          {"clock_event_disposition"},
		// 00011 barrier, group, date, compensation and evidence objects.
		"compensation_line_basis_seq":              {"compensation_line"},
		"employment_terms_basis_seq":               {"employment_terms"},
		"attendance_interval_basis_seq":            {"attendance_interval"},
		"clock_event_basis_seq":                    {"clock_event"},
		"timesheet_approval_barrier":               {"timesheet"},
		"correction_group_predecessor_open":        {"attendance_correction_group_predecessor"},
		"correction_group_segment_open":            {"attendance_interval"},
		"correction_group_complete":                {"attendance_correction_group"},
		"attendance_interval_local_date":           {"attendance_interval"},
		"compensation_line_containment":            {"compensation_line"},
		"clock_event_created_at_normalize":         {"clock_event"},
		"attendance_interval_created_at_normalize": {"attendance_interval"},
		"programme_timezone_valid":                 {"programme"},
		// 00012 SHG lifecycle objects.
		"shg_instruction_basis_seq":            {"shg_instruction"},
		"shg_instruction_lifecycle":            {"shg_instruction"},
		"shg_instruction_created_at_normalize": {"shg_instruction"},
		// 00012 snapshot population barrier.
		"employee_population_barrier":         {"employee"},
		"employment_terms_population_barrier": {"employment_terms"},
	}
	for trg, tables := range want {
		for _, tbl := range tables {
			var exists bool
			err := appPool.QueryRow(ctx(t),
				`SELECT EXISTS (SELECT 1 FROM pg_trigger WHERE NOT tgisinternal AND tgname = $1 AND tgrelid = $2::regclass)`,
				trg, tbl).Scan(&exists)
			expectOK(t, err, "check trigger "+trg+" on "+tbl)
			if !exists {
				t.Fatalf("trigger %s missing on %s", trg, tbl)
			}
		}
	}
}

type tablePriv struct {
	sel, ins, upd, del bool
}

// Fail-closed table classification: history is insert-only,
// compensation is sealed behind its functions, operational tables
// hold full DML, and migration bookkeeping is pinned as-is. Any
// table without an entry fails — including future tables inheriting
// the default full-DML grant.
var tablePrivileges = map[string]tablePriv{
	// Insert-only history.
	"attendance_correction":                   {true, true, false, false},
	"attendance_correction_group":             {true, true, false, false},
	"attendance_correction_group_predecessor": {true, true, false, false},
	"attendance_exception":                    {true, true, false, false},
	"attendance_interval":                     {true, true, false, false},
	"audit_event":                             {true, true, false, false},
	"clock_event":                             {true, true, false, false},
	"clock_event_disposition":                 {true, true, false, false},
	"employee":                                {true, true, false, false},
	"employee_skill":                          {true, true, false, false},
	"employee_status_history":                 {true, true, false, false},
	"employment_terms":                        {true, true, false, false},
	"ket_record":                              {true, true, false, false},
	"payslip":                                 {true, true, false, false},
	"roster_revision":                         {true, true, false, false},
	"team_membership":                         {true, true, false, false},
	// Sealed behind controlled functions (E4).
	"compensation_line": {true, false, false, false},
	// Mutable operational tables.
	"attendance_record":              {true, true, true, true},
	"client":                         {true, true, true, true},
	"cpf_ceiling":                    {true, false, false, false},
	"cpf_rate_band":                  {true, false, false, false},
	"employer":                       {true, true, true, true},
	"holiday_calendar":               {true, true, true, true},
	"idempotency_key":                {true, true, true, true},
	"leave_entitlement_rule":         {true, true, true, true},
	"leave_request":                  {true, true, true, true},
	"leave_type":                     {true, true, true, true},
	"mom_ot_params":                  {true, false, false, false},
	"mom_shift_params":               {true, false, false, false},
	"pay_code":                       {true, true, true, true},
	"pay_period":                     {true, true, true, true},
	"payroll_correction":             {true, true, true, true},
	"payroll_line":                   {true, true, true, true},
	"payroll_run":                    {true, true, true, true},
	"payroll_snapshot":               {true, true, false, false},
	"payroll_snapshot_employee":      {true, true, false, false},
	"payroll_snapshot_pack":          {true, true, false, false},
	"payroll_run_exclusion":          {true, true, true, true},
	"programme":                      {true, true, true, true},
	"programme_required_skill":       {true, true, true, true},
	"programme_staffing_requirement": {true, true, true, true},
	"roster":                         {true, true, true, true},
	"roster_assignment":              {true, true, true, true},
	"rotation_assignment":            {true, true, true, true},
	"rotation_pattern":               {true, true, true, true},
	"rule_pack_activation":           {true, false, false, false},
	"rule_pack":                      {false, false, false, false},
	"rule_pack_activation_history":   {true, false, false, false},
	"sdl_params":                     {true, false, false, false},
	"shg_contribution":               {true, false, false, false},
	"shg_instruction":                {true, false, false, false},
	"shift_segment":                  {true, true, true, true},
	"shift_template":                 {true, true, true, true},
	"skill":                          {true, true, true, true},
	"statutory_summary":              {true, true, true, true},
	"team":                           {true, true, true, true},
	"timesheet":                      {true, true, true, true},
	"timesheet_line":                 {true, true, true, true},
	"timesheet_line_interval":        {true, true, true, true},
	// Migration bookkeeping, pinned as-is.
	"goose_db_version": {true, true, true, true},
}

// The application-role privilege matrix: what app may and may not
// do, per table, enumerated fail-closed from the catalog.
func TestAppPrivilegeMatrix(t *testing.T) {
	rows, err := appPool.Query(ctx(t),
		`SELECT c.relname,
		  has_table_privilege('app', c.oid, 'SELECT'),
		  has_table_privilege('app', c.oid, 'INSERT'),
		  has_table_privilege('app', c.oid, 'UPDATE'),
		  has_table_privilege('app', c.oid, 'DELETE')
		 FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
		 WHERE n.nspname = 'public' AND c.relkind IN ('r', 'p')
		 ORDER BY c.relname`)
	expectOK(t, err, "enumerate tables")
	defer rows.Close()
	seen := map[string]bool{}
	for rows.Next() {
		var name string
		var got tablePriv
		if err := rows.Scan(&name, &got.sel, &got.ins, &got.upd, &got.del); err != nil {
			t.Fatalf("scan: %v", err)
		}
		seen[name] = true
		want, ok := tablePrivileges[name]
		if !ok {
			t.Fatalf("table %s has no privilege classification", name)
		}
		if got != want {
			t.Fatalf("%s = select/insert/update/delete %v/%v/%v/%v, want %v/%v/%v/%v",
				name, got.sel, got.ins, got.upd, got.del, want.sel, want.ins, want.upd, want.del)
		}
	}
	expectOK(t, rows.Err(), "enumerate tables")
	for name := range tablePrivileges {
		if !seen[name] {
			t.Fatalf("classification for %s matches no table", name)
		}
	}
	t.Run("no temp or schema create", func(t *testing.T) {
		var temp, create bool
		err := appPool.QueryRow(ctx(t),
			`SELECT has_database_privilege('app', current_database(), 'TEMP'),
			        has_schema_privilege('app', 'public', 'CREATE')`).Scan(&temp, &create)
		expectOK(t, err, "temp/create privileges")
		if temp {
			t.Fatal("app holds TEMPORARY on the database")
		}
		if create {
			t.Fatal("app holds CREATE on schema public")
		}
	})
}

// PUBLIC holds no table privileges anywhere in the schema.
func TestNoPublicTablePrivileges(t *testing.T) {
	var n int
	err := appPool.QueryRow(ctx(t),
		`SELECT count(*) FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
		 JOIN aclexplode(c.relacl) AS a(grantor, grantee, priv, grantable) ON a.grantee = 0
		 WHERE n.nspname = 'public' AND c.relkind IN ('r', 'p')`).Scan(&n)
	expectOK(t, err, "count PUBLIC table grants")
	if n != 0 {
		t.Fatalf("PUBLIC holds %d table privileges", n)
	}
}

// The default-privilege state is exactly the provisioned rule:
// future tables by migration_admin grant full DML to app — the
// hazard the fail-closed matrix above guards.
func TestDefaultPrivileges(t *testing.T) {
	var rules string
	err := appPool.QueryRow(ctx(t),
		`SELECT coalesce(string_agg(defaclrole::regrole::text || ' ' ||
		  coalesce(defaclnamespace::regnamespace::text, '(global)') || ' ' ||
		  defaclobjtype::text || ' ' || defaclacl::text, E'\n'), '(none)')
		 FROM pg_default_acl`).Scan(&rules)
	expectOK(t, err, "read default ACLs")
	want := "migration_admin public r {app=arwd/migration_admin}"
	if rules != want {
		t.Fatalf("default ACLs =\n%s\nwant\n%s", rules, want)
	}
}
