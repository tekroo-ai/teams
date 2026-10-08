package operationalruntime

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/tekroo-ai/teams/application"
	"github.com/tekroo-ai/teams/kernel"
	"github.com/tekroo-ai/teams/organization"
)

func TestRejectedConsultationCompletionFilterPreservesSourceAndOtherResults(t *testing.T) {
	question := kernel.UUIDv7("00000000-0000-7000-8000-000000043001")
	completed := kernel.UUIDv7("00000000-0000-7000-8000-000000043002")
	validation := kernel.UUIDv7("00000000-0000-7000-8000-000000043003")
	questionOutput := []byte(application.OrganizationalResultMarker + `{"outcome":"needs_decision","summary":"Who owns the missing interface?"}`)
	completedOutput := []byte(application.OrganizationalResultMarker + `{"outcome":"completed"}`)
	qDigest, cDigest := digestBytes(questionOutput), digestBytes(completedOutput)
	plan := organization.FeaturePlan{Tasks: []organization.PlannedTask{
		{ID: question, Purpose: kernel.PurposeImplementation},
		{ID: completed, Purpose: kernel.PurposeImplementation},
		{ID: validation, Purpose: kernel.PurposeValidation},
	}}
	invocations := map[kernel.UUIDv7]kernel.WorkInvocation{
		question:   {State: kernel.InvocationSucceeded, OutputDigest: &qDigest},
		completed:  {State: kernel.InvocationSucceeded, OutputDigest: &cDigest},
		validation: {State: kernel.InvocationSucceeded, OutputDigest: &cDigest},
	}
	before := mustJSON(invocations)
	reads := 0
	eligible, err := completionInvocationsWithoutOpenQuestions(t.Context(), plan, invocations, func(_ context.Context, digest kernel.Digest) ([]byte, error) {
		reads++
		if digest == qDigest {
			return questionOutput, nil
		}
		return completedOutput, nil
	})
	if err != nil || reads != 2 || len(eligible) != 2 || !reflect.DeepEqual(before, mustJSON(invocations)) {
		t.Fatal("filter changed source or lost unrelated result", err, reads)
	}
	if _, exists := eligible[question]; exists {
		t.Fatal("old question is eligible for completion/re-blocking")
	}
	if _, exists := eligible[completed]; !exists {
		t.Fatal("unrelated completed implementation lost")
	}
	if _, exists := eligible[validation]; !exists {
		t.Fatal("unrelated validator lost")
	}
}

func TestRejectedConsultationCompletionFilterFailsClosedOnUnavailableOrChangedOutput(t *testing.T) {
	task := kernel.UUIDv7("00000000-0000-7000-8000-000000043001")
	digest := repeatedDigest('a')
	plan := organization.FeaturePlan{Tasks: []organization.PlannedTask{{ID: task, Purpose: kernel.PurposeImplementation}}}
	invocations := map[kernel.UUIDv7]kernel.WorkInvocation{task: {State: kernel.InvocationSucceeded, OutputDigest: &digest}}
	missing := errors.New("evidence unavailable")
	if eligible, err := completionInvocationsWithoutOpenQuestions(t.Context(), plan, invocations, func(context.Context, kernel.Digest) ([]byte, error) { return nil, missing }); eligible != nil || !errors.Is(err, missing) {
		t.Fatal("unavailable output allowed completion", err)
	}
	if eligible, err := completionInvocationsWithoutOpenQuestions(t.Context(), plan, invocations, func(context.Context, kernel.Digest) ([]byte, error) { return []byte("changed"), nil }); eligible != nil || !errors.Is(err, application.ErrInvalidOperationalExecution) {
		t.Fatal("changed output allowed completion", err)
	}
}

func TestDurableCommandRejectionRetainsReceiptAndErrorIdentity(t *testing.T) {
	receipt := kernel.CommandReceipt{CommandID: kernel.UUIDv7("00000000-0000-7000-8000-000000043001"), OutcomeCode: kernel.OutcomeRejectedUnauthorized, ReasonCode: "UNAUTHORIZED"}
	err := &deterministicCommandRejection{CommandType: "tekroo.command.task.bind-work-profile", Receipt: receipt}
	if err.Error() != "tekroo.command.task.bind-work-profile rejected: UNAUTHORIZED" || !reflect.DeepEqual(err.Receipt, receipt) {
		t.Fatal("rejection lost its durable receipt or changed diagnostic", err)
	}
}

func TestConsultationFaultFallbackRequiresKnownUnchangedProfileRejection(t *testing.T) {
	id := kernel.UUIDv7("00000000-0000-7000-8000-000000043001")
	known := &deterministicCommandRejection{CommandType: "tekroo.command.task.bind-work-profile", Receipt: kernel.CommandReceipt{CommandID: id, OutcomeCode: kernel.OutcomeRejectedUnauthorized, ReasonCode: "UNAUTHORIZED"}}
	changed := *known
	changed.Receipt.StateChanged = true
	other := *known
	other.CommandType = "tekroo.command.task.create"
	unbound := *known
	unbound.Receipt.CommandID = ""
	for _, tc := range []struct {
		name  string
		fault error
		calls int
	}{
		{"known", known, 1},
		{"wrapped-known", errors.Join(errors.New("consultation"), known), 1},
		{"unknown-outcome", errors.New("external execution outcome is unknown"), 0},
		{"changed-state", &changed, 0},
		{"other-command", &other, 0},
		{"no-receipt-id", &unbound, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			completionErr := errors.New("terminal recording failed")
			err := preserveTerminalResultsOnConsultationFault(tc.fault, func() (bool, error) { calls++; return false, completionErr })
			if calls != tc.calls || !errors.Is(err, tc.fault) || errors.Is(err, completionErr) != (tc.calls == 1) {
				t.Fatal("fallback crossed fault boundary or lost an error", calls, err)
			}
		})
	}
}

func TestFeatureBatchFaultDoesNotStarveLaterFeaturesOrHideReceipts(t *testing.T) {
	plan := organization.FeaturePlan{}
	features := []organization.FeatureRequest{
		{ID: "00000000-0000-7000-8000-000000043001"},
		{ID: "00000000-0000-7000-8000-000000043002", Plan: &plan},
		{ID: "00000000-0000-7000-8000-000000043003", Plan: &plan},
	}
	fault := &deterministicCommandRejection{CommandType: "tekroo.command.task.bind-work-profile", Receipt: kernel.CommandReceipt{CommandID: features[1].ID, OutcomeCode: kernel.OutcomeRejectedUnauthorized, ReasonCode: "UNAUTHORIZED"}}
	var visited []kernel.UUIDv7
	err := reconcileFeatureBatch(t.Context(), features, func(_ context.Context, feature organization.FeatureRequest, _ organization.FeaturePlan) error {
		visited = append(visited, feature.ID)
		if feature.ID == features[1].ID {
			return fault
		}
		return nil
	})
	var retained *deterministicCommandRejection
	if !reflect.DeepEqual(visited, []kernel.UUIDv7{features[1].ID, features[2].ID}) || !errors.Is(err, organization.ErrInvalidFeature) || !errors.As(err, &retained) || retained != fault {
		t.Fatal("fault starved later work or lost the receipt", visited, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	visited = nil
	err = reconcileFeatureBatch(ctx, features[1:], func(_ context.Context, feature organization.FeatureRequest, _ organization.FeaturePlan) error {
		visited = append(visited, feature.ID)
		cancel()
		return fault
	})
	if len(visited) != 1 || !errors.Is(err, context.Canceled) || !errors.Is(err, fault) {
		t.Fatal("cancellation did not stop the batch or preserve prior fault", visited, err)
	}
}
