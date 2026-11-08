// Command payloop-api serves the PayLoop downstream payroll system.
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
		Name:        "payloop-api",
		PortEnv:     "PAYLOOP",
		DefaultPort: 8082,
		Register: func(e *echo.Echo) error {
			reg := module.NewRegistry()
			if err := reg.Use(sgwfmpayroll.Module{}); err != nil {
				return err
			}
			reg.MountPayLoop(e)
			return nil
		},
	}))
}
