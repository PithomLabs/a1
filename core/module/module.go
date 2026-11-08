// Package module defines the platform kernel's business-module contract (v0).
// Dependency rule: core/* must never import modules/* or examples/*.
package module

import (
	"fmt"

	"github.com/labstack/echo/v4"
)

// CoreVersion is the kernel version modules declare compatibility against.
const CoreVersion = "0.1.0"

// Capability names a platform extension point a module may implement.
type Capability string

const (
	CapDomainModel        Capability = "domain-model"
	CapBusinessRules      Capability = "business-rules"
	CapWorkflows          Capability = "workflows"
	CapSyntheticData      Capability = "synthetic-data"
	CapReferenceOracle    Capability = "reference-oracle"
	CapIntegrations       Capability = "integrations"
	CapFaultInjection     Capability = "fault-injection"
	CapScenarios          Capability = "scenarios"
	CapInvestigationTools Capability = "investigation-tools"
	CapEvaluation         Capability = "evaluation"
)

// Descriptor identifies a module and its declared capabilities.
type Descriptor struct {
	ID           string
	Version      string
	MinimumCore  string
	Dependencies []string
	Capabilities []Capability
}

// Module is implemented once per business-module package.
type Module interface {
	Descriptor() Descriptor
	Register(Registrar) error
}

// Route is one HTTP endpoint contributed to a named host surface.
type Route struct {
	Method  string
	Path    string
	Handler echo.HandlerFunc
}

// RouteSet groups a module's HTTP contributions by host surface.
type RouteSet struct {
	ShiftHub []Route
	PayLoop  []Route
	Console  []Route
}

// Tool describes one read-only investigation tool (handlers wired in Phase 4).
type Tool struct {
	Name        string
	Description string
}

// ToolSet is a module's contributed investigation tools.
type ToolSet struct{ Tools []Tool }

// Scenario describes one reproducible incident (definition wired in Phase 3).
type Scenario struct {
	ID         string
	Title      string
	Difficulty string
}

// ScenarioSet is a module's contributed scenarios.
type ScenarioSet struct{ Scenarios []Scenario }

// Evaluator judges investigations for one scenario family (rubric in Phase 5).
type Evaluator struct {
	ScenarioID string
}

// EvaluatorSet is a module's contributed evaluators.
type EvaluatorSet struct{ Evaluators []Evaluator }

// Registrar receives a module's contributions. Every method is optional to call.
type Registrar interface {
	RegisterRoutes(RouteSet) error
	RegisterTools(ToolSet) error
	RegisterScenarios(ScenarioSet) error
	RegisterEvaluators(EvaluatorSet) error
}

// Registry collects contributions from registered modules and mounts them.
type Registry struct {
	Routes     RouteSet
	Tools      []Tool
	Scenarios  []Scenario
	Evaluators []Evaluator
	modules    []Descriptor
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry { return &Registry{} }

// Use registers a module, rejecting duplicates and incompatible core versions.
func (r *Registry) Use(m Module) error {
	d := m.Descriptor()
	if d.ID == "" {
		return fmt.Errorf("module descriptor has empty ID")
	}
	if d.MinimumCore != "" && !coreSatisfied(d.MinimumCore) {
		return fmt.Errorf("module %s requires core %s (have %s)", d.ID, d.MinimumCore, CoreVersion)
	}
	for _, e := range r.modules {
		if e.ID == d.ID {
			return fmt.Errorf("duplicate module %q", d.ID)
		}
	}
	if err := m.Register(r); err != nil {
		return fmt.Errorf("module %s register: %w", d.ID, err)
	}
	r.modules = append(r.modules, d)
	return nil
}

// Modules returns the registered module descriptors.
func (r *Registry) Modules() []Descriptor { return append([]Descriptor(nil), r.modules...) }

// RegisterRoutes implements Registrar.
func (r *Registry) RegisterRoutes(s RouteSet) error {
	r.Routes.ShiftHub = append(r.Routes.ShiftHub, s.ShiftHub...)
	r.Routes.PayLoop = append(r.Routes.PayLoop, s.PayLoop...)
	r.Routes.Console = append(r.Routes.Console, s.Console...)
	return nil
}

// RegisterTools implements Registrar.
func (r *Registry) RegisterTools(s ToolSet) error {
	for _, t := range s.Tools {
		if t.Name == "" {
			return fmt.Errorf("tool with empty name")
		}
		for _, e := range r.Tools {
			if e.Name == t.Name {
				return fmt.Errorf("duplicate tool %q", t.Name)
			}
		}
		r.Tools = append(r.Tools, t)
	}
	return nil
}

// RegisterScenarios implements Registrar.
func (r *Registry) RegisterScenarios(s ScenarioSet) error {
	for _, sc := range s.Scenarios {
		if sc.ID == "" {
			return fmt.Errorf("scenario with empty ID")
		}
		for _, e := range r.Scenarios {
			if e.ID == sc.ID {
				return fmt.Errorf("duplicate scenario %q", sc.ID)
			}
		}
		r.Scenarios = append(r.Scenarios, sc)
	}
	return nil
}

// RegisterEvaluators implements Registrar.
func (r *Registry) RegisterEvaluators(s EvaluatorSet) error {
	r.Evaluators = append(r.Evaluators, s.Evaluators...)
	return nil
}

// MountShiftHub mounts ShiftHub-surface routes onto e.
func (r *Registry) MountShiftHub(e *echo.Echo) { mount(e, r.Routes.ShiftHub) }

// MountPayLoop mounts PayLoop-surface routes onto e.
func (r *Registry) MountPayLoop(e *echo.Echo) { mount(e, r.Routes.PayLoop) }

// MountConsole mounts console-surface routes onto e.
func (r *Registry) MountConsole(e *echo.Echo) { mount(e, r.Routes.Console) }

func mount(e *echo.Echo, routes []Route) {
	for _, rt := range routes {
		e.Add(rt.Method, rt.Path, rt.Handler)
	}
}

// coreSatisfied compares major versions only in v0 ("0.1" matches "0.1.0").
func coreSatisfied(min string) bool {
	major := func(v string) string {
		for i := 0; i < len(v); i++ {
			if v[i] == '.' {
				return v[:i]
			}
		}
		return v
	}
	return major(min) == major(CoreVersion)
}
