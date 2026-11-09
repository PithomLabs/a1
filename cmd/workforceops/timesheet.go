package main

import (
	"github.com/spf13/cobra"

	"github.com/PithomLabs/a1/internal/payroll"
	"github.com/PithomLabs/a1/internal/rules"
	"github.com/PithomLabs/a1/internal/timesheet"
)

func timesheetCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "timesheet", Short: "Timesheet generation and approval"}
	gen := &cobra.Command{
		Use:   "generate <pay-period>",
		Short: "Generate draft timesheets for a pay period",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := resolveCLI(cmd, true, true)
			if err != nil {
				return out(cmd, nil, nil, err)
			}
			defer c.close()
			g, err := timesheet.Generate(c.ctx, c.pool, c.employer.ID, args[0], c.operator)
			return out(cmd, c, g, err)
		},
	}
	cmd.AddCommand(gen)
	cmd.AddCommand(&cobra.Command{
		Use:   "show <timesheet-id>",
		Short: "Show a timesheet with lines and source intervals",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := resolveCLI(cmd, true, false)
			if err != nil {
				return out(cmd, nil, nil, err)
			}
			defer c.close()
			t, err := timesheet.Get(c.ctx, c.pool, c.employer.ID, args[0])
			return out(cmd, c, t, err)
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "submit <timesheet-id>",
		Short: "Submit a draft timesheet",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := resolveCLI(cmd, true, true)
			if err != nil {
				return out(cmd, nil, nil, err)
			}
			defer c.close()
			t, err := timesheet.Submit(c.ctx, c.pool, c.employer.ID, args[0], c.operator)
			return out(cmd, c, t, err)
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "approve <timesheet-id>",
		Short: "Approve a submitted timesheet (idempotent)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := resolveCLI(cmd, true, true)
			if err != nil {
				return out(cmd, nil, nil, err)
			}
			defer c.close()
			t, err := timesheet.Approve(c.ctx, c.pool, c.employer.ID, args[0], c.operator)
			return out(cmd, c, t, err)
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "reject <timesheet-id>",
		Short: "Reject a submitted timesheet",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := resolveCLI(cmd, true, true)
			if err != nil {
				return out(cmd, nil, nil, err)
			}
			defer c.close()
			t, err := timesheet.Reject(c.ctx, c.pool, c.employer.ID, args[0], c.operator)
			return out(cmd, c, t, err)
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "reissue <timesheet-id>",
		Short: "Open a draft correction version of an approved or rejected timesheet",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := resolveCLI(cmd, true, true)
			if err != nil {
				return out(cmd, nil, nil, err)
			}
			defer c.close()
			t, err := timesheet.Reissue(c.ctx, c.pool, c.employer.ID, args[0], c.operator)
			return out(cmd, c, t, err)
		},
	})
	return cmd
}

func payPeriodCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "pay-period", Short: "Pay period administration"}
	create := &cobra.Command{
		Use:   "create",
		Short: "Create a pay period from a JSON file {code, starts_on, ends_on, pay_date, salary_deadline, ot_deadline}",
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := resolveCLI(cmd, true, true)
			if err != nil {
				return out(cmd, nil, nil, err)
			}
			defer c.close()
			var in timesheet.PeriodInput
			if err := readJSONFile(cmd, &in); err != nil {
				return out(cmd, c, nil, err)
			}
			p, err := timesheet.CreatePeriod(c.ctx, c.pool, c.employer.ID, in, c.operator)
			return out(cmd, c, p, err)
		},
	}
	create.Flags().String("file", "", "JSON input file")
	cmd.AddCommand(create)
	cmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List pay periods",
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := resolveCLI(cmd, true, false)
			if err != nil {
				return out(cmd, nil, nil, err)
			}
			defer c.close()
			list, err := timesheet.ListPeriods(c.ctx, c.pool, c.employer.ID)
			return out(cmd, c, list, err)
		},
	})
	return cmd
}

func payrollCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "payroll", Short: "Payroll readiness (no calculations)"}
	readiness := &cobra.Command{
		Use:   "readiness <pay-period>",
		Short: "Assess payroll readiness for a pay period",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := resolveCLI(cmd, true, false)
			if err != nil {
				return out(cmd, nil, nil, err)
			}
			defer c.close()
			run, _ := cmd.Flags().GetString("run")
			var runID *string
			if run != "" {
				runID = &run
			}
			r, err := payroll.Assess(c.ctx, c.pool, c.employer.ID, args[0], runID)
			return out(cmd, c, r, err)
		},
	}
	readiness.Flags().String("run", "", "payroll run ID to apply exclusions from")
	cmd.AddCommand(readiness)
	runCreate := &cobra.Command{
		Use:   "run-create <pay-period>",
		Short: "Open a draft payroll run (exclusion container; no calculations)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := resolveCLI(cmd, true, true)
			if err != nil {
				return out(cmd, nil, nil, err)
			}
			defer c.close()
			kind, _ := cmd.Flags().GetString("kind")
			corrects, _ := cmd.Flags().GetString("corrects")
			r, err := payroll.CreateRun(c.ctx, c.pool, c.employer.ID, args[0], kind, corrects, c.operator)
			return out(cmd, c, r, err)
		},
	}
	runCreate.Flags().String("kind", "regular", "regular or correction")
	runCreate.Flags().String("corrects", "", "original run ID (required for correction runs)")
	cmd.AddCommand(runCreate)
	exclude := &cobra.Command{
		Use:   "exclude <run-id> <employee>",
		Short: "Exclude an employee from a draft run (requires --reason)",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := resolveCLI(cmd, true, true)
			if err != nil {
				return out(cmd, nil, nil, err)
			}
			defer c.close()
			reason, _ := cmd.Flags().GetString("reason")
			id, err := payroll.AddExclusion(c.ctx, c.pool, c.employer.ID, args[0], args[1], reason, c.operator)
			return out(cmd, c, map[string]string{"id": id}, err)
		},
	}
	exclude.Flags().String("reason", "", "exclusion reason")
	cmd.AddCommand(exclude)

	runValidate := &cobra.Command{
		Use:   "run-validate <run-id>",
		Short: "Validate a draft run against an offered snapshot version (no calculations)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := resolveCLI(cmd, true, true)
			if err != nil {
				return out(cmd, nil, nil, err)
			}
			defer c.close()
			offered, _ := cmd.Flags().GetInt("offered-version")
			v, err := payroll.ValidateRun(c.ctx, c.pool, c.employer.ID, args[0], offered, c.operator)
			return out(cmd, c, v, err)
		},
	}
	runValidate.Flags().Int("offered-version", 0, "snapshot version the operator inspected (0 when none stands)")
	cmd.AddCommand(runValidate)

	runShow := &cobra.Command{
		Use:   "run-show <run-id>",
		Short: "Inspect a payroll run: status, exclusions and valid snapshot tip",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := resolveCLI(cmd, true, false)
			if err != nil {
				return out(cmd, nil, nil, err)
			}
			defer c.close()
			d, err := payroll.InspectRun(c.ctx, c.pool, c.employer.ID, args[0])
			return out(cmd, c, d, err)
		},
	}
	cmd.AddCommand(runShow)

	snapshotShow := &cobra.Command{
		Use:   "snapshot-show <snapshot-id>",
		Short: "Inspect an input snapshot: pins, employees and content hash",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := resolveCLI(cmd, true, false)
			if err != nil {
				return out(cmd, nil, nil, err)
			}
			defer c.close()
			d, err := payroll.InspectSnapshot(c.ctx, c.pool, c.employer.ID, args[0])
			return out(cmd, c, d, err)
		},
	}
	cmd.AddCommand(snapshotShow)

	rulePacks := &cobra.Command{
		Use:   "rule-packs",
		Short: "Read-only rule-pack activation status for the employer",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := resolveCLI(cmd, true, false)
			if err != nil {
				return out(cmd, nil, nil, err)
			}
			defer c.close()
			list, err := rules.ListActivations(c.ctx, c.pool, c.employer.ID)
			return out(cmd, c, list, err)
		},
	}
	cmd.AddCommand(rulePacks)
	return cmd
}
