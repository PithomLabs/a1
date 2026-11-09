// Command workforceops is the single WorkforceOps Lab executable: HR,
// WFM, attendance, timesheet and payroll-readiness operations over one
// Postgres database, through subcommands or the loopback API server.
package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var version = "dev"

func main() {
	if err := rootCmd().Execute(); err != nil {
		os.Exit(1)
	}
}

func rootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "workforceops",
		Short: "WorkforceOps Lab: single-binary HR/WFM/payroll backend",
	}
	root.PersistentFlags().String("dsn", "", "Postgres DSN (default $DATABASE_URL)")
	root.PersistentFlags().String("format", "json", "output format: json or text")
	root.PersistentFlags().String("employer", "", "employer code or UUID (default when exactly one exists)")
	root.PersistentFlags().String("operator", "", "operator attribution (default $USER)")
	root.AddCommand(versionCmd(), migrateCmd(), adminCmd())
	root.AddCommand(seedCmd(), serveCmd())
	root.AddCommand(employerCmd(), employeeCmd())
	root.AddCommand(clientCmd(), programmeCmd(), teamCmd(), skillCmd(), shiftTemplateCmd())
	root.AddCommand(rosterCmd())
	root.AddCommand(rotationCmd())
	root.AddCommand(attendanceCmd())
	root.AddCommand(timesheetCmd())
	root.AddCommand(payPeriodCmd())
	root.AddCommand(payrollCmd())
	return root
}

func versionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the build version",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Fprintf(cmd.OutOrStdout(), "workforceops %s\n", version)
		},
	}
}
