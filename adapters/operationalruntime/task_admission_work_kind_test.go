package operationalruntime

import (
	"testing"

	"github.com/tekroo-ai/teams/kernel"
	"github.com/tekroo-ai/teams/organization"
)

func TestWorkKindForPurposeKeepsValidationAndSecurityReviewDistinct(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		purpose kernel.WorkPurpose
		risk    organization.RiskLevel
		want    kernel.WorkKind
	}{
		{name: "ordinary validation", purpose: kernel.PurposeValidation, risk: organization.RiskLow, want: kernel.WorkValidation},
		{name: "high risk validation", purpose: kernel.PurposeValidation, risk: organization.RiskHigh, want: kernel.WorkValidation},
		{name: "critical risk validation", purpose: kernel.PurposeValidation, risk: organization.RiskCritical, want: kernel.WorkValidation},
		{name: "ordinary review", purpose: kernel.PurposeReview, risk: organization.RiskModerate, want: kernel.WorkValidation},
		{name: "high risk review", purpose: kernel.PurposeReview, risk: organization.RiskHigh, want: kernel.WorkSecurityReview},
		{name: "critical risk review", purpose: kernel.PurposeReview, risk: organization.RiskCritical, want: kernel.WorkSecurityReview},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := workKindForPurpose(test.purpose, test.risk); got != test.want {
				t.Fatalf("workKindForPurpose(%s, %s) = %s, want %s", test.purpose, test.risk, got, test.want)
			}
		})
	}
}

func TestInvocationWorkKindKeepsRepairInsideQualifiedTaskClassification(t *testing.T) {
	t.Parallel()
	task := &trackedTask{
		plan:    organization.PlannedTask{Purpose: kernel.PurposeImplementation, Risk: organization.RiskHigh},
		profile: kernel.WorkRiskProfile{WorkKind: kernel.WorkImplementation},
	}
	if got := invocationWorkKind(task, kernel.PurposeRepair); got != kernel.WorkImplementation {
		t.Fatalf("repair work kind = %s, want %s", got, kernel.WorkImplementation)
	}
	if got := invocationWorkKind(task, kernel.PurposeValidation); got != kernel.WorkValidation {
		t.Fatalf("validation work kind = %s, want %s", got, kernel.WorkValidation)
	}
}
