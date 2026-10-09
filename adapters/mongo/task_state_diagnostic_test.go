package mongo

import (
	"encoding/json"
	"testing"

	"github.com/tekroo-ai/teams/kernel"
)

func TestPreActivationBlockDiagnosticPreservesRawStateWithoutBudget(t *testing.T) {
	id := kernel.UUIDv7("00000000-0000-7000-8000-000000088001")
	head := kernel.UUIDv7("00000000-0000-7000-8000-000000088004")
	blockID := kernel.UUIDv7("00000000-0000-7000-8000-000000088003")
	ref := kernel.AggregateRef{Kind: kernel.AggregateTask, ID: id}
	state := kernel.AggregateState{Kind: kernel.AggregateTask, ID: id, Revision: 4, Phase: kernel.PhasePlanned, Condition: kernel.ConditionBlocked}
	created := kernel.DomainEvent{Aggregate: ref, AggregateRevision: 1, EventType: "tekroo.event.task.created", Payload: json.RawMessage(`{"title":"Whole feature validation"}`)}
	blocked := kernel.DomainEvent{Aggregate: ref, AggregateRevision: 3, EventID: blockID, EventType: "tekroo.event.work.blocked", Payload: json.RawMessage(`{"reason":"verification execution timed out; candidate verdict INCONCLUSIVE","blocker_refs":["teams://evidence/receipt"],"review_policy":"operator recovery"}`)}
	got, err := taskStateDiagnostic(state, head, created, &blocked)
	if err != nil || got.Condition != kernel.ConditionBlocked || got.LastEventID != head || got.Block.EventID != blockID || got.ProjectionStatus != "NOT_READY" {
		t.Fatalf("%+v %v", got, err)
	}
	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(encoded, &fields)
	if fields["budget"] != nil || fields["latest_invocation"] != nil {
		t.Fatal("invented admission data", string(encoded))
	}
	if _, err := taskStateDiagnostic(state, head, created, nil); err == nil {
		t.Fatal("hidden missing block receipt")
	}
	blocked.AggregateRevision = 5
	if _, err := taskStateDiagnostic(state, head, created, &blocked); err == nil {
		t.Fatal("accepted a future block")
	}
}
