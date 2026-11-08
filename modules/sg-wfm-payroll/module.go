// Package sgwfmpayroll is the Singapore WFM/payroll reference business module.
// It owns all Singapore-domain logic; the platform kernel must never import it.
package sgwfmpayroll

import (
	"net/http"

	"github.com/labstack/echo/v4"

	"github.com/PithomLabs/a1/core/module"
)

// Module implements module.Module for the reference domain.
type Module struct{}

// Descriptor implements module.Module.
func (Module) Descriptor() module.Descriptor {
	return module.Descriptor{
		ID:          "sg-wfm-payroll",
		Version:     "0.1.0",
		MinimumCore: "0.1",
		Capabilities: []module.Capability{
			module.CapDomainModel,
			module.CapBusinessRules,
			module.CapSyntheticData,
			module.CapReferenceOracle,
			module.CapIntegrations,
			module.CapFaultInjection,
			module.CapScenarios,
			module.CapInvestigationTools,
			module.CapEvaluation,
		},
	}
}

// Register implements module.Module. Only status routes exist in Phase 1;
// domain routes, tools, scenarios and evaluators arrive with their phases.
func (Module) Register(r module.Registrar) error {
	return r.RegisterRoutes(module.RouteSet{
		ShiftHub: []module.Route{
			{Method: http.MethodGet, Path: "/api/v1/status", Handler: status("shifthub")},
		},
		PayLoop: []module.Route{
			{Method: http.MethodGet, Path: "/api/v1/status", Handler: status("payloop")},
		},
	})
}

func status(service string) echo.HandlerFunc {
	return func(c echo.Context) error {
		return c.JSON(http.StatusOK, map[string]string{
			"module":  "sg-wfm-payroll",
			"service": service,
			"version": "0.1.0",
			"status":  "skeleton",
		})
	}
}
