package main

import (
	"github.com/spf13/cobra"

	"github.com/PithomLabs/a1/internal/employer"
	"github.com/PithomLabs/a1/internal/hr"
)

func employerCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "employer", Short: "Employer context"}
	cmd.AddCommand(&cobra.Command{
		Use:   "show",
		Short: "Show the resolved employer",
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := resolveCLI(cmd, true, false)
			if err != nil {
				return out(cmd, nil, nil, err)
			}
			defer c.close()
			return out(cmd, c, c.employer, nil)
		},
	})
	createCmd := &cobra.Command{
		Use:   "create",
		Short: "Bootstrap the v0.1 employer from a JSON file (no employer context needed)",
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := resolveCLI(cmd, false, true)
			if err != nil {
				return out(cmd, nil, nil, err)
			}
			defer c.close()
			var in employer.EmployerInput
			if err := readJSONFile(cmd, &in); err != nil {
				return out(cmd, c, nil, err)
			}
			created, err := employer.Create(c.ctx, c.pool, in, c.operator)
			return out(cmd, c, created, err)
		},
	}
	createCmd.Flags().String("file", "", "JSON input file")
	cmd.AddCommand(createCmd)
	return cmd
}

func employeeCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "employee", Short: "Employee administration"}
	createCmd := &cobra.Command{
		Use:   "create",
		Short: "Create an employee from a JSON file",
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := resolveCLI(cmd, true, true)
			if err != nil {
				return out(cmd, nil, nil, err)
			}
			defer c.close()
			var in hr.EmployeeInput
			if err := readJSONFile(cmd, &in); err != nil {
				return out(cmd, c, nil, err)
			}
			e, err := hr.CreateEmployee(c.ctx, c.pool, c.employer.ID, in, c.operator)
			return out(cmd, c, e, err)
		},
	}
	createCmd.Flags().String("file", "", "JSON input file")
	cmd.AddCommand(createCmd)
	cmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List employees",
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := resolveCLI(cmd, true, false)
			if err != nil {
				return out(cmd, nil, nil, err)
			}
			defer c.close()
			list, err := hr.ListEmployees(c.ctx, c.pool, c.employer.ID)
			return out(cmd, c, list, err)
		},
	})
	statusCmd := &cobra.Command{
		Use:   "status <employee>",
		Short: "Change employee status (requires --to and --reason)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := resolveCLI(cmd, true, true)
			if err != nil {
				return out(cmd, nil, nil, err)
			}
			defer c.close()
			e, err := hr.GetEmployee(c.ctx, c.pool, c.employer.ID, args[0])
			if err != nil {
				return out(cmd, c, nil, err)
			}
			to, _ := cmd.Flags().GetString("to")
			reason, _ := cmd.Flags().GetString("reason")
			effective, _ := cmd.Flags().GetString("effective")
			updated, err := hr.SetStatus(c.ctx, c.pool, c.employer.ID, e.ID, to, effective, reason, c.operator)
			return out(cmd, c, updated, err)
		},
	}
	statusCmd.Flags().String("to", "", "new status")
	statusCmd.Flags().String("reason", "", "change reason")
	statusCmd.Flags().String("effective", "", "effective date YYYY-MM-DD (default today)")
	cmd.AddCommand(statusCmd)
	shgCmd := &cobra.Command{
		Use:   "shg <employee>",
		Short: "Record an SHG instruction from a JSON file {fund, status, amount_or_basis?, source_declaration, effective_from, effective_to?}",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := resolveCLI(cmd, true, true)
			if err != nil {
				return out(cmd, nil, nil, err)
			}
			defer c.close()
			var in hr.SHGInput
			if err := readJSONFile(cmd, &in); err != nil {
				return out(cmd, c, nil, err)
			}
			s, err := hr.SetSHG(c.ctx, c.pool, c.employer.ID, args[0], in, c.operator)
			return out(cmd, c, s, err)
		},
	}
	shgCmd.Flags().String("file", "", "JSON input file")
	cmd.AddCommand(shgCmd)
	termsCmd := &cobra.Command{Use: "terms", Short: "Employment terms"}
	termsSet := &cobra.Command{
		Use:   "set <employee>",
		Short: "Set effective-dated terms from a JSON file",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := resolveCLI(cmd, true, true)
			if err != nil {
				return out(cmd, nil, nil, err)
			}
			defer c.close()
			e, err := hr.GetEmployee(c.ctx, c.pool, c.employer.ID, args[0])
			if err != nil {
				return out(cmd, c, nil, err)
			}
			var in hr.TermsInput
			if err := readJSONFile(cmd, &in); err != nil {
				return out(cmd, c, nil, err)
			}
			t, err := hr.SetTerms(c.ctx, c.pool, c.employer.ID, e.ID, in, c.operator)
			return out(cmd, c, t, err)
		},
	}
	termsSet.Flags().String("file", "", "JSON input file")
	termsCmd.AddCommand(termsSet)
	termsCmd.AddCommand(&cobra.Command{
		Use:   "list <employee>",
		Short: "List effective-dated terms rows",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := resolveCLI(cmd, true, false)
			if err != nil {
				return out(cmd, nil, nil, err)
			}
			defer c.close()
			e, err := hr.GetEmployee(c.ctx, c.pool, c.employer.ID, args[0])
			if err != nil {
				return out(cmd, c, nil, err)
			}
			list, err := hr.ListTerms(c.ctx, c.pool, c.employer.ID, e.ID)
			return out(cmd, c, list, err)
		},
	})
	comp := &cobra.Command{
		Use:   "compensate <terms-id>",
		Short: "Add a compensation line from a JSON file {kind, code, amount_monthly, effective_from, effective_to?, reason}",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := resolveCLI(cmd, true, true)
			if err != nil {
				return out(cmd, nil, nil, err)
			}
			defer c.close()
			var in hr.CompensationInput
			if err := readJSONFile(cmd, &in); err != nil {
				return out(cmd, c, nil, err)
			}
			id, err := hr.AddCompensation(c.ctx, c.pool, c.employer.ID, args[0], in, c.operator)
			return out(cmd, c, map[string]string{"id": id}, err)
		},
	}
	comp.Flags().String("file", "", "JSON input file")
	termsCmd.AddCommand(comp)
	correct := &cobra.Command{
		Use:   "compensate-correct <line-id>",
		Short: "Correct a compensation line from a JSON file {amount_monthly, effective_from, effective_to?, reason}",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := resolveCLI(cmd, true, true)
			if err != nil {
				return out(cmd, nil, nil, err)
			}
			defer c.close()
			var in hr.CorrectionInput
			if err := readJSONFile(cmd, &in); err != nil {
				return out(cmd, c, nil, err)
			}
			id, err := hr.CorrectCompensation(c.ctx, c.pool, c.employer.ID, args[0], in, c.operator)
			return out(cmd, c, map[string]string{"id": id}, err)
		},
	}
	correct.Flags().String("file", "", "JSON input file")
	termsCmd.AddCommand(correct)
	cmd.AddCommand(termsCmd)
	return cmd
}
