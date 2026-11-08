package payroll

import (
	"errors"
	"testing"
)

func TestAuthorizeApproval(t *testing.T) {
	impl := map[string]Activation{
		"SG-MOM-OT":  {Pack: "SG-MOM-OT", Version: "v0.1", Status: Implementable},
		"SG-MOM-KET": {Pack: "SG-MOM-KET", Version: "v0.1", Status: Implementable},
	}
	blockedActs := map[string]Activation{
		"SG-MOM-OT": {Pack: "SG-MOM-OT", Version: "v0.1", Status: Implementable},
		"SG-CPF":    {Pack: "SG-CPF", Version: "v0.1", Status: "BLOCKED"},
	}

	tests := []struct {
		name    string
		req     ApprovalRequest
		wantErr bool
	}{
		{
			name: "all implementable approves",
			req: ApprovalRequest{
				Included: []EmployeeRequirement{
					{EmployeeID: "e1", RequiredPacks: []string{"SG-MOM-OT"}},
					{EmployeeID: "e2", RequiredPacks: []string{"SG-MOM-OT", "SG-MOM-KET"}},
				},
				Activations: impl,
			},
			wantErr: false,
		},
		{
			name: "blocked pack stops approval",
			req: ApprovalRequest{
				Included: []EmployeeRequirement{
					{EmployeeID: "e2", RequiredPacks: []string{"SG-CPF"}},
				},
				Activations: blockedActs,
			},
			wantErr: true,
		},
		{
			name: "unactivated pack stops approval",
			req: ApprovalRequest{
				Included: []EmployeeRequirement{
					{EmployeeID: "e3", RequiredPacks: []string{"SG-SDL"}},
				},
				Activations: impl,
			},
			wantErr: true,
		},
		{
			name: "excluded employee with reason and actor proceeds",
			req: ApprovalRequest{
				Included: []EmployeeRequirement{
					{EmployeeID: "e1", RequiredPacks: []string{"SG-MOM-OT"}},
					{EmployeeID: "e2", RequiredPacks: []string{"SG-CPF"}},
				},
				Exclusions: []Exclusion{
					{EmployeeID: "e2", Reason: "CPF pack not verified", Actor: "ops-lead"},
				},
				Activations: blockedActs,
			},
			wantErr: false,
		},
		{
			name: "exclusion without reason fails",
			req: ApprovalRequest{
				Included: []EmployeeRequirement{
					{EmployeeID: "e2", RequiredPacks: []string{"SG-CPF"}},
				},
				Exclusions: []Exclusion{
					{EmployeeID: "e2", Actor: "ops-lead"},
				},
				Activations: blockedActs,
			},
			wantErr: true,
		},
		{
			name: "exclusion without actor fails",
			req: ApprovalRequest{
				Included: []EmployeeRequirement{
					{EmployeeID: "e2", RequiredPacks: []string{"SG-CPF"}},
				},
				Exclusions: []Exclusion{
					{EmployeeID: "e2", Reason: "CPF pack not verified"},
				},
				Activations: blockedActs,
			},
			wantErr: true,
		},
		{
			name: "empty population never approves",
			req: ApprovalRequest{
				Activations: impl,
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := AuthorizeApproval(tt.req)
			if tt.wantErr && err == nil {
				t.Fatal("expected error, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("expected nil, got %v", err)
			}
			if tt.wantErr {
				var blocked *BlockedError
				if !errors.As(err, &blocked) {
					t.Fatalf("expected *BlockedError, got %T", err)
				}
				if len(blocked.Blocked) == 0 {
					t.Fatal("expected at least one blocked entry")
				}
			}
		})
	}
}
