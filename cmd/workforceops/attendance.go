package main

import (
	"os"

	"github.com/spf13/cobra"

	"github.com/PithomLabs/a1/internal/attendance"
	"github.com/PithomLabs/a1/internal/timesheet"
)

func attendanceCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "attendance", Short: "Clock import, review and corrections"}
	imp := &cobra.Command{
		Use:   "import",
		Short: "Import a clock-event CSV file (see contract §7)",
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := resolveCLI(cmd, true, true)
			if err != nil {
				return out(cmd, nil, nil, err)
			}
			defer c.close()
			path, _ := cmd.Flags().GetString("file")
			if path == "" {
				return out(cmd, c, nil, cmdError("--file is required"))
			}
			f, err := os.Open(path)
			if err != nil {
				return out(cmd, c, nil, err)
			}
			defer f.Close()
			rep, err := attendance.Import(c.ctx, c.pool, c.employer.ID, f, c.operator, timesheet.RegenerateDraft, timesheet.ChainTerminal)
			return out(cmd, c, rep, err)
		},
	}
	imp.Flags().String("file", "", "CSV file to import")
	cmd.AddCommand(imp)
	rev := &cobra.Command{
		Use:   "review",
		Short: "Review day records, unpaired events and monthly excess",
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := resolveCLI(cmd, true, false)
			if err != nil {
				return out(cmd, nil, nil, err)
			}
			defer c.close()
			from, _ := cmd.Flags().GetString("from")
			to, _ := cmd.Flags().GetString("to")
			if from == "" || to == "" {
				return out(cmd, c, nil, cmdError("--from and --to (YYYY-MM-DD) are required"))
			}
			r, err := attendance.ReviewAttendance(c.ctx, c.pool, c.employer.ID, from, to)
			return out(cmd, c, r, err)
		},
	}
	rev.Flags().String("from", "", "start date YYYY-MM-DD")
	rev.Flags().String("to", "", "end date YYYY-MM-DD")
	cmd.AddCommand(rev)
	corr := &cobra.Command{
		Use:   "correct",
		Short: "Supersede one interval or a whole correction group from a JSON file {interval_ids, start, end, kind, reason}",
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := resolveCLI(cmd, true, true)
			if err != nil {
				return out(cmd, nil, nil, err)
			}
			defer c.close()
			var in attendance.CorrectionInput
			if err := readJSONFile(cmd, &in); err != nil {
				return out(cmd, c, nil, err)
			}
			rep, err := attendance.Correct(c.ctx, c.pool, c.employer.ID, in, c.operator, timesheet.RegenerateDraft, timesheet.CorrectionDrafts)
			return out(cmd, c, rep, err)
		},
	}
	corr.Flags().String("file", "", "JSON input file")
	cmd.AddCommand(corr)
	disp := &cobra.Command{
		Use:   "disposition",
		Short: "Confirm one clock event as a duplicate delivery from a JSON file {event_id, canonical_event_id, reason}",
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := resolveCLI(cmd, true, true)
			if err != nil {
				return out(cmd, nil, nil, err)
			}
			defer c.close()
			var in attendance.DispositionInput
			if err := readJSONFile(cmd, &in); err != nil {
				return out(cmd, c, nil, err)
			}
			rep, err := attendance.DispositionDuplicate(c.ctx, c.pool, c.employer.ID, in, c.operator, timesheet.RegenerateDraft, timesheet.ChainTerminal)
			return out(cmd, c, rep, err)
		},
	}
	disp.Flags().String("file", "", "JSON input file")
	cmd.AddCommand(disp)
	att := &cobra.Command{
		Use:   "attach",
		Short: "Attach held late evidence (e.g. breaks kept by an evidence conflict) after reissue",
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := resolveCLI(cmd, true, true)
			if err != nil {
				return out(cmd, nil, nil, err)
			}
			defer c.close()
			emp, _ := cmd.Flags().GetString("employee")
			if emp == "" {
				return out(cmd, c, nil, cmdError("--employee is required"))
			}
			rep, err := attendance.AttachPending(c.ctx, c.pool, c.employer.ID, emp, c.operator, timesheet.RegenerateDraft, timesheet.ChainTerminal)
			return out(cmd, c, rep, err)
		},
	}
	att.Flags().String("employee", "", "employee number")
	cmd.AddCommand(att)
	iv := &cobra.Command{
		Use:   "intervals",
		Short: "List intervals with lineage, linked sheets and exceptions",
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := resolveCLI(cmd, true, false)
			if err != nil {
				return out(cmd, nil, nil, err)
			}
			defer c.close()
			from, _ := cmd.Flags().GetString("from")
			to, _ := cmd.Flags().GetString("to")
			if from == "" || to == "" {
				return out(cmd, c, nil, cmdError("--from and --to (YYYY-MM-DD) are required"))
			}
			emp, _ := cmd.Flags().GetString("employee")
			list, err := attendance.ListIntervals(c.ctx, c.pool, c.employer.ID, emp, from, to)
			return out(cmd, c, list, err)
		},
	}
	iv.Flags().String("from", "", "start date YYYY-MM-DD")
	iv.Flags().String("to", "", "end date YYYY-MM-DD")
	iv.Flags().String("employee", "", "employee number or ID (all when empty)")
	cmd.AddCommand(iv)
	return cmd
}

func cmdError(msg string) error {
	return &cmdErr{msg: msg}
}

type cmdErr struct{ msg string }

func (e *cmdErr) Error() string { return e.msg }
