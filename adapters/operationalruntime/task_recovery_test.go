package operationalruntime

import (
	"errors"
	"testing"
	"time"

	"github.com/tekroo-ai/teams/kernel"
	"github.com/tekroo-ai/teams/organization"
)

func TestTaskRecoveryDeadlineUsesDurableHostSuspensionExtension(t *testing.T) {
	now := time.Date(2026, 9, 28, 1, 0, 0, 0, time.UTC)
	terminal := now.Add(44 * time.Hour)
	requested := now.Add(4 * time.Hour)
	effective := effectiveTaskRecoveryDeadline(requested, now.Add(43*time.Hour), terminal.Add(2*time.Minute))
	if !validTaskRecoveryDeadline(now, requested, effective, terminal, 8*time.Hour) {
		t.Fatal("existing durable deadline extension made task recovery impossible")
	}
	if validTaskRecoveryDeadline(now, requested, terminal, terminal, 8*time.Hour) {
		t.Fatal("successor deadline did not extend the terminal")
	}
	if validTaskRecoveryDeadline(now, now.Add(9*time.Hour), effective, terminal, 8*time.Hour) {
		t.Fatal("operator request exceeded the normal planning window")
	}
}

func TestPlanRecoveryRoleTransitionTreatsStoppedRecordAsStartable(t *testing.T) {
	terminal := kernel.ExecutionTuple{ExecutionID: "00000000-0000-7000-8000-000000000001", FencingEpoch: 7}
	successor := kernel.ExecutionTuple{ExecutionID: "00000000-0000-7000-8000-000000000002", FencingEpoch: 8}
	tests := []struct {
		name    string
		owner   organization.RoleInstanceState
		found   bool
		want    recoveryRoleTransition
		wantErr error
	}{
		{name: "no durable record", found: false, want: recoveryRoleStart},
		{name: "stopped durable record", found: true, owner: organization.RoleInstanceState{Status: organization.RoleStopped, Execution: terminal}, want: recoveryRoleStart},
		{name: "same idle execution", found: true, owner: organization.RoleInstanceState{Status: organization.RoleIdle, Execution: terminal}, want: recoveryRoleRestart},
		{name: "new idle execution", found: true, owner: organization.RoleInstanceState{Status: organization.RoleIdle, Execution: successor}, want: recoveryRoleReuse},
		{name: "failed execution", found: true, owner: organization.RoleInstanceState{Status: organization.RoleFailed, Execution: terminal}, want: recoveryRoleRestart},
		{name: "paused execution", found: true, owner: organization.RoleInstanceState{Status: organization.RolePaused, Execution: terminal}, wantErr: organization.ErrRoleNotRunning},
		{name: "starting execution", found: true, owner: organization.RoleInstanceState{Status: organization.RoleStarting, Execution: terminal}, wantErr: organization.ErrRoleNotRunning},
		{name: "stopping execution", found: true, owner: organization.RoleInstanceState{Status: organization.RoleStopping, Execution: terminal}, wantErr: organization.ErrRoleNotRunning},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := planRecoveryRoleTransition(test.owner, test.found, terminal)
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("error = %v, want %v", err, test.wantErr)
			}
			if got != test.want {
				t.Fatalf("transition = %v, want %v", got, test.want)
			}
		})
	}
}
