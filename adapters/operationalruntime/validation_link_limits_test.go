package operationalruntime

import (
	"reflect"
	"testing"

	"github.com/tekroo-ai/teams/kernel"
)

func TestAuthoredValidationLinkLimitsFollowWholePurposePolicy(t *testing.T) {
	policy := structuralTaskRoutingPolicy()
	limits := authoredValidationLinkLimits(policy)
	if len(limits) != 1 || limits[0].MaxItems != 0 || !reflect.DeepEqual(limits[0].PropertyPath, []string{"work_product", "tasks", "*", "validates"}) {
		t.Fatalf("wrong limits: %+v", limits)
	}
	policy.PurposeRoutes = append(policy.PurposeRoutes, purposeTaskRoutingPolicy{Purpose: kernel.PurposeValidation, MayAuthorValidationLinks: true})
	if len(authoredValidationLinkLimits(policy)) != 0 {
		t.Fatal("mixed policy lost authorized links")
	}
	if len(authoredValidationLinkLimits(workflowTaskRoutingPolicy{})) != 0 {
		t.Fatal("missing policy treated as authority")
	}
}
