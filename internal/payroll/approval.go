// Package payroll holds the approval-blocking predicate for payroll runs.
//
// Approval is all-or-nothing per regular run: a run may be approved only
// when every included employee resolves every required statutory pack to
// IMPLEMENTABLE. An operator may explicitly exclude an employee before
// validation, with a reason and actor recorded on the exclusion row; the
// run then discloses its exclusions and is never a complete payroll for
// all employees without qualification. See singapore-rule-scope.md §2.
package payroll

import (
	"fmt"
	"sort"
	"strings"
)

// Implementable is the only activation status that unblocks approval.
const Implementable = "IMPLEMENTABLE"

// Activation is one pack's verification status for an employer.
type Activation struct {
	// Pack is the stable rule-pack identifier, e.g. "SG-MOM-OT".
	Pack string
	// Version is the pinned pack version.
	Version string
	// Status is IMPLEMENTABLE, BLOCKED or SUPERSEDED.
	Status string
}

// EmployeeRequirement pairs an included employee with the statutory packs
// their payroll components depend on.
type EmployeeRequirement struct {
	// EmployeeID is the employee UUID in canonical text form.
	EmployeeID string
	// RequiredPacks lists the packs that must be IMPLEMENTABLE.
	RequiredPacks []string
}

// Exclusion records an operator's explicit decision to keep an employee
// out of a run. Reason and Actor are mandatory.
type Exclusion struct {
	EmployeeID string
	Reason     string
	Actor      string
}

// ApprovalRequest is everything the predicate needs: included employees
// with their pack dependencies, declared exclusions, and the current
// pack activations for the employer.
type ApprovalRequest struct {
	Included    []EmployeeRequirement
	Exclusions  []Exclusion
	Activations map[string]Activation
}

// BlockedPack names one unusable pack for one employee.
type BlockedPack struct {
	EmployeeID string
	Pack       string
	// Status is the activation status found, or "" when unactivated.
	Status string
}

// BlockedError reports the exact employees and packs that stop approval.
type BlockedError struct {
	Blocked []BlockedPack
}

func (e *BlockedError) Error() string {
	parts := make([]string, 0, len(e.Blocked))
	for _, b := range e.Blocked {
		status := b.Status
		if status == "" {
			status = "not activated"
		}
		parts = append(parts, fmt.Sprintf("employee %s pack %s (%s)", b.EmployeeID, b.Pack, status))
	}
	return "approval blocked: " + strings.Join(parts, "; ")
}

// AuthorizeApproval returns nil exactly when the run may be approved:
// every exclusion carries a reason and actor, and every included,
// non-excluded employee has every required pack IMPLEMENTABLE. Anything
// else yields a *BlockedError naming the first failing layer.
func AuthorizeApproval(req ApprovalRequest) error {
	// An empty population authorizes nothing: approving zero employees
	// vacuously would let an empty run pass the gate.
	if len(req.Included) == 0 {
		return &BlockedError{Blocked: []BlockedPack{{Pack: "(population)", Status: "empty"}}}
	}
	for _, x := range req.Exclusions {
		if strings.TrimSpace(x.Reason) == "" {
			return &BlockedError{Blocked: []BlockedPack{{
				EmployeeID: x.EmployeeID,
				Pack:       "(exclusion)",
				Status:     "missing reason",
			}}}
		}
		if strings.TrimSpace(x.Actor) == "" {
			return &BlockedError{Blocked: []BlockedPack{{
				EmployeeID: x.EmployeeID,
				Pack:       "(exclusion)",
				Status:     "missing actor",
			}}}
		}
	}

	excluded := make(map[string]bool, len(req.Exclusions))
	for _, x := range req.Exclusions {
		excluded[x.EmployeeID] = true
	}

	var blocked []BlockedPack
	for _, emp := range req.Included {
		if excluded[emp.EmployeeID] {
			continue
		}
		for _, pack := range emp.RequiredPacks {
			act, ok := req.Activations[pack]
			if !ok {
				blocked = append(blocked, BlockedPack{EmployeeID: emp.EmployeeID, Pack: pack})
				continue
			}
			if act.Status != Implementable {
				blocked = append(blocked, BlockedPack{EmployeeID: emp.EmployeeID, Pack: pack, Status: act.Status})
			}
		}
	}
	if len(blocked) > 0 {
		sort.Slice(blocked, func(i, j int) bool {
			if blocked[i].EmployeeID != blocked[j].EmployeeID {
				return blocked[i].EmployeeID < blocked[j].EmployeeID
			}
			return blocked[i].Pack < blocked[j].Pack
		})
		return &BlockedError{Blocked: blocked}
	}
	return nil
}
