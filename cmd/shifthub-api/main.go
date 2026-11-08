// Command shifthub-api serves the ShiftHub workforce source system.
// The Support Engineering Lab console is co-hosted here in v0.1 (see ADR-002).
package main

import (
	"os"

	"github.com/labstack/echo/v4"

	"github.com/PithomLabs/a1/core/api"
	"github.com/PithomLabs/a1/core/module"
	sgwfmpayroll "github.com/PithomLabs/a1/modules/sg-wfm-payroll"
)

func main() {
	os.Exit(api.Run(api.ServiceSpec{
		Name:        "shifthub-api",
		PortEnv:     "SHIFTHUB",
		DefaultPort: 8081,
		Register: func(e *echo.Echo) error {
			reg := module.NewRegistry()
			if err := reg.Use(sgwfmpayroll.Module{}); err != nil {
				return err
			}
			reg.MountShiftHub(e)
			reg.MountConsole(e)
			return nil
		},
	}))
}
