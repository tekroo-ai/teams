package operationalruntime

import (
	"testing"

	"github.com/tekroo-ai/teams/kernel"
	"github.com/tekroo-ai/teams/organization"
)

func TestImplementationChainDependencyReadyBeforeSharedValidation(t *testing.T) {
	owner := kernel.ActorFQN("teams::senior-coder-1")
	upstream := organization.PlannedTask{Purpose: kernel.PurposeImplementation, Owner: owner}
	downstream := organization.PlannedTask{Purpose: kernel.PurposeImplementation, Owner: owner}
	digest := kernel.Digest("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	invocation := kernel.WorkInvocation{State: kernel.InvocationSucceeded, OutputDigest: &digest}

	if !implementationChainDependencyReady(downstream, upstream, invocation, true) {
		t.Fatal("successful same-owner implementation dependency must allow the next implementation stage to run before shared validation")
	}

	otherOwner := downstream
	otherOwner.Owner = kernel.ActorFQN("teams::senior-coder-2")
	failed := invocation
	failed.State = kernel.InvocationFailed
	missingOutput := invocation
	missingOutput.OutputDigest = nil
	validation := downstream
	validation.Purpose = kernel.PurposeValidation

	for name, candidate := range map[string]bool{
		"missing invocation": implementationChainDependencyReady(downstream, upstream, invocation, false),
		"different owner":    implementationChainDependencyReady(otherOwner, upstream, invocation, true),
		"failed invocation":  implementationChainDependencyReady(downstream, upstream, failed, true),
		"missing output":     implementationChainDependencyReady(downstream, upstream, missingOutput, true),
		"non-implementation": implementationChainDependencyReady(validation, upstream, invocation, true),
	} {
		if candidate {
			t.Fatalf("%s must retain the completed-phase dependency requirement", name)
		}
	}
}
