package main

import (
	"github.com/spf13/cobra"

	"github.com/PithomLabs/a1/internal/wfm"
)

func clientCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "client", Short: "Client administration"}
	create := &cobra.Command{
		Use:   "create",
		Short: "Create a client from a JSON file {code, name}",
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := resolveCLI(cmd, true, true)
			if err != nil {
				return out(cmd, nil, nil, err)
			}
			defer c.close()
			var in struct {
				Code string `json:"code"`
				Name string `json:"name"`
			}
			if err := readJSONFile(cmd, &in); err != nil {
				return out(cmd, c, nil, err)
			}
			cl, err := wfm.CreateClient(c.ctx, c.pool, c.employer.ID, in.Code, in.Name)
			return out(cmd, c, cl, err)
		},
	}
	create.Flags().String("file", "", "JSON input file")
	cmd.AddCommand(create)
	cmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List clients",
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := resolveCLI(cmd, true, false)
			if err != nil {
				return out(cmd, nil, nil, err)
			}
			defer c.close()
			list, err := wfm.ListClients(c.ctx, c.pool, c.employer.ID)
			return out(cmd, c, list, err)
		},
	})
	return cmd
}

func programmeCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "programme", Short: "Programme administration"}
	create := &cobra.Command{
		Use:   "create",
		Short: "Create a programme from a JSON file",
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := resolveCLI(cmd, true, true)
			if err != nil {
				return out(cmd, nil, nil, err)
			}
			defer c.close()
			var in wfm.ProgrammeInput
			if err := readJSONFile(cmd, &in); err != nil {
				return out(cmd, c, nil, err)
			}
			p, err := wfm.CreateProgramme(c.ctx, c.pool, c.employer.ID, in)
			return out(cmd, c, p, err)
		},
	}
	create.Flags().String("file", "", "JSON input file")
	cmd.AddCommand(create)
	cmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List programmes",
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := resolveCLI(cmd, true, false)
			if err != nil {
				return out(cmd, nil, nil, err)
			}
			defer c.close()
			list, err := wfm.ListProgrammes(c.ctx, c.pool, c.employer.ID)
			return out(cmd, c, list, err)
		},
	})
	require := &cobra.Command{
		Use:   "require-skills <programme>",
		Short: "Set required programme skills from a JSON file [{skill, level}]",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := resolveCLI(cmd, true, true)
			if err != nil {
				return out(cmd, nil, nil, err)
			}
			defer c.close()
			var grants []wfm.SkillGrant
			if err := readJSONFile(cmd, &grants); err != nil {
				return out(cmd, c, nil, err)
			}
			err = wfm.RequireSkills(c.ctx, c.pool, c.employer.ID, args[0], grants, c.operator)
			return out(cmd, c, map[string]string{"programme": args[0], "result": "ok"}, err)
		},
	}
	require.Flags().String("file", "", "JSON input file")
	cmd.AddCommand(require)
	return cmd
}

func teamCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "team", Short: "Team administration"}
	create := &cobra.Command{
		Use:   "create",
		Short: "Create a team from a JSON file",
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := resolveCLI(cmd, true, true)
			if err != nil {
				return out(cmd, nil, nil, err)
			}
			defer c.close()
			var in wfm.TeamInput
			if err := readJSONFile(cmd, &in); err != nil {
				return out(cmd, c, nil, err)
			}
			t, err := wfm.CreateTeam(c.ctx, c.pool, c.employer.ID, in)
			return out(cmd, c, t, err)
		},
	}
	create.Flags().String("file", "", "JSON input file")
	cmd.AddCommand(create)
	cmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List teams",
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := resolveCLI(cmd, true, false)
			if err != nil {
				return out(cmd, nil, nil, err)
			}
			defer c.close()
			list, err := wfm.ListTeams(c.ctx, c.pool, c.employer.ID)
			return out(cmd, c, list, err)
		},
	})
	add := &cobra.Command{
		Use:   "add-member <team> <employee>",
		Short: "Add a team member (requires --role and --from)",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := resolveCLI(cmd, true, true)
			if err != nil {
				return out(cmd, nil, nil, err)
			}
			defer c.close()
			role, _ := cmd.Flags().GetString("role")
			from, _ := cmd.Flags().GetString("from")
			id, err := wfm.AddMembership(c.ctx, c.pool, c.employer.ID, args[0], args[1], role, from, c.operator)
			return out(cmd, c, map[string]string{"id": id}, err)
		},
	}
	add.Flags().String("role", "", "agent, lead or supervisor")
	add.Flags().String("from", "", "effective date YYYY-MM-DD")
	cmd.AddCommand(add)
	return cmd
}

func skillCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "skill", Short: "Skill catalogue and grants"}
	cmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List the global skill catalogue",
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := resolveCLI(cmd, false, false)
			if err != nil {
				return out(cmd, nil, nil, err)
			}
			defer c.close()
			list, err := wfm.ListSkills(c.ctx, c.pool)
			return out(cmd, c, list, err)
		},
	})
	assign := &cobra.Command{
		Use:   "assign <employee>",
		Short: "Assign skill levels from a JSON file [{skill, level}] (requires --from)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := resolveCLI(cmd, true, true)
			if err != nil {
				return out(cmd, nil, nil, err)
			}
			defer c.close()
			var grants []wfm.SkillGrant
			if err := readJSONFile(cmd, &grants); err != nil {
				return out(cmd, c, nil, err)
			}
			from, _ := cmd.Flags().GetString("from")
			err = wfm.AssignSkills(c.ctx, c.pool, c.employer.ID, args[0], from, grants, c.operator)
			return out(cmd, c, map[string]string{"employee": args[0], "result": "ok"}, err)
		},
	}
	assign.Flags().String("file", "", "JSON input file")
	assign.Flags().String("from", "", "valid-from date YYYY-MM-DD")
	cmd.AddCommand(assign)
	return cmd
}

func shiftTemplateCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "shift-template", Short: "Shift template administration"}
	create := &cobra.Command{
		Use:   "create",
		Short: "Create a shift template with segments from a JSON file",
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := resolveCLI(cmd, true, true)
			if err != nil {
				return out(cmd, nil, nil, err)
			}
			defer c.close()
			var in wfm.TemplateInput
			if err := readJSONFile(cmd, &in); err != nil {
				return out(cmd, c, nil, err)
			}
			t, err := wfm.CreateTemplate(c.ctx, c.pool, c.employer.ID, in)
			return out(cmd, c, t, err)
		},
	}
	create.Flags().String("file", "", "JSON input file")
	cmd.AddCommand(create)
	cmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List shift templates",
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := resolveCLI(cmd, true, false)
			if err != nil {
				return out(cmd, nil, nil, err)
			}
			defer c.close()
			list, err := wfm.ListTemplates(c.ctx, c.pool, c.employer.ID)
			return out(cmd, c, list, err)
		},
	})
	return cmd
}

func rosterCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "roster", Short: "Draft rosters and publication"}
	create := &cobra.Command{
		Use:   "create",
		Short: "Create a draft roster from a JSON file {programme, week_starting}",
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := resolveCLI(cmd, true, true)
			if err != nil {
				return out(cmd, nil, nil, err)
			}
			defer c.close()
			var in wfm.RosterInput
			if err := readJSONFile(cmd, &in); err != nil {
				return out(cmd, c, nil, err)
			}
			r, err := wfm.CreateRoster(c.ctx, c.pool, c.employer.ID, in)
			return out(cmd, c, r, err)
		},
	}
	create.Flags().String("file", "", "JSON input file")
	cmd.AddCommand(create)
	assign := &cobra.Command{
		Use:   "assign <roster-id>",
		Short: "Assign one employee from a JSON file {employee, date, template?, rest_day?}",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := resolveCLI(cmd, true, true)
			if err != nil {
				return out(cmd, nil, nil, err)
			}
			defer c.close()
			var in wfm.AssignmentInput
			if err := readJSONFile(cmd, &in); err != nil {
				return out(cmd, c, nil, err)
			}
			a, err := wfm.Assign(c.ctx, c.pool, c.employer.ID, args[0], in)
			return out(cmd, c, a, err)
		},
	}
	assign.Flags().String("file", "", "JSON input file")
	cmd.AddCommand(assign)
	cmd.AddCommand(&cobra.Command{
		Use:   "show <roster-id>",
		Short: "Show a roster with current assignments",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := resolveCLI(cmd, true, false)
			if err != nil {
				return out(cmd, nil, nil, err)
			}
			defer c.close()
			d, err := wfm.Show(c.ctx, c.pool, c.employer.ID, args[0])
			return out(cmd, c, d, err)
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "publish <roster-id>",
		Short: "Publish a draft roster (legal validation stays unavailable)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := resolveCLI(cmd, true, true)
			if err != nil {
				return out(cmd, nil, nil, err)
			}
			defer c.close()
			r, err := wfm.Publish(c.ctx, c.pool, c.employer.ID, args[0], c.operator)
			return out(cmd, c, r, err)
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List rosters",
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := resolveCLI(cmd, true, false)
			if err != nil {
				return out(cmd, nil, nil, err)
			}
			defer c.close()
			list, err := wfm.ListRosters(c.ctx, c.pool, c.employer.ID)
			return out(cmd, c, list, err)
		},
	})
	revise := &cobra.Command{
		Use:   "revise <roster-id>",
		Short: "Open a new draft revision of a published roster (requires --reason)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := resolveCLI(cmd, true, true)
			if err != nil {
				return out(cmd, nil, nil, err)
			}
			defer c.close()
			reason, _ := cmd.Flags().GetString("reason")
			r, err := wfm.Revise(c.ctx, c.pool, c.employer.ID, args[0], reason, c.operator)
			return out(cmd, c, r, err)
		},
	}
	revise.Flags().String("reason", "", "revision reason")
	cmd.AddCommand(revise)
	return cmd
}

func rotationCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "rotation", Short: "Rotation patterns and assignments"}
	create := &cobra.Command{
		Use:   "pattern create",
		Short: "Create a rotation pattern from a JSON file {code, name, cycle_days, rows:[{offset, weekday, template, is_off?}]}",
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := resolveCLI(cmd, true, true)
			if err != nil {
				return out(cmd, nil, nil, err)
			}
			defer c.close()
			var file struct {
				Code      string            `json:"code"`
				Name      string            `json:"name"`
				CycleDays int               `json:"cycle_days"`
				Rows      []wfm.RotationRow `json:"rows"`
			}
			if err := readJSONFile(cmd, &file); err != nil {
				return out(cmd, c, nil, err)
			}
			p, err := wfm.CreateRotation(c.ctx, c.pool, c.employer.ID, wfm.CreateRotationInput{
				Code: file.Code, Name: file.Name, CycleDays: file.CycleDays, Rows: file.Rows,
			})
			return out(cmd, c, p, err)
		},
	}
	create.Flags().String("file", "", "JSON input file")
	cmd.AddCommand(create)
	cmd.AddCommand(&cobra.Command{
		Use:   "pattern list",
		Short: "List rotation patterns",
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := resolveCLI(cmd, true, false)
			if err != nil {
				return out(cmd, nil, nil, err)
			}
			defer c.close()
			list, err := wfm.ListRotations(c.ctx, c.pool, c.employer.ID)
			return out(cmd, c, list, err)
		},
	})
	assign := &cobra.Command{
		Use:   "assign",
		Short: "Assign a pattern from a JSON file {pattern, employee?, team?, effective_from}",
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := resolveCLI(cmd, true, true)
			if err != nil {
				return out(cmd, nil, nil, err)
			}
			defer c.close()
			var in wfm.CreateRotationAssignInput
			if err := readJSONFile(cmd, &in); err != nil {
				return out(cmd, c, nil, err)
			}
			a, err := wfm.CreateRotationAssign(c.ctx, c.pool, c.employer.ID, in)
			return out(cmd, c, a, err)
		},
	}
	assign.Flags().String("file", "", "JSON input file")
	cmd.AddCommand(assign)
	cmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List rotation assignments",
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := resolveCLI(cmd, true, false)
			if err != nil {
				return out(cmd, nil, nil, err)
			}
			defer c.close()
			list, err := wfm.ListRotationAssigns(c.ctx, c.pool, c.employer.ID)
			return out(cmd, c, list, err)
		},
	})
	return cmd
}
