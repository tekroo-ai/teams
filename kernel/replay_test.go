package kernel_test

import (
	"encoding/json"
	"testing"

	"github.com/tekroo-ai/teams/kernel"
)

func TestUnknownEventReplayStopsAndQuarantinesLosslessly(t *testing.T) {
	aggregate := kernel.AggregateRef{Kind: kernel.AggregateStory, ID: mustUUID(t, "00000000-0000-7000-8000-0000000000a1")}
	created := kernel.DomainEvent{
		EventID: mustUUID(t, "00000000-0000-7000-8000-0000000000a2"), EventType: "tekroo.event.story.created", EventVersion: kernel.SchemaVersion,
		Aggregate: aggregate, AggregateRevision: 1, LifecycleEpoch: 1,
		Payload: json.RawMessage(`{"acceptance_criteria":["works"],"description":"description","title":"title"}`),
	}
	unknown := kernel.DomainEvent{
		EventID: mustUUID(t, "00000000-0000-7000-8000-0000000000a3"), EventType: "tekroo.event.story.mystery", EventVersion: kernel.SchemaVersion,
		Aggregate: aggregate, AggregateRevision: 2, LifecycleEpoch: 1, Payload: json.RawMessage(`{"uninterpreted":true}`),
	}
	result := kernel.ReplayAuthoritative(loadCatalogue(t), []kernel.DomainEvent{created, unknown})
	if result.Status != kernel.ReplayQuarantined || result.Reason != "UNKNOWN_EVENT_TYPE" || result.LastRevision != 1 {
		t.Fatalf("replay result = %#v", result)
	}
	if result.State == nil || result.State.Revision != 1 || result.State.Phase != kernel.PhaseDraft {
		t.Fatalf("last understood state = %#v", result.State)
	}
	if result.QuarantinedEvent == nil || string(result.QuarantinedEvent.Payload) != string(unknown.Payload) || result.QuarantinedEvent.EventID != unknown.EventID {
		t.Fatalf("quarantined event was not preserved: %#v", result.QuarantinedEvent)
	}

	unsupported := unknown
	unsupported.EventType = "tekroo.event.story.authorized"
	unsupported.EventVersion = "9.9.9"
	unsupported.Payload = json.RawMessage(`{"scope_revision":1,"reason":"approved"}`)
	result = kernel.ReplayAuthoritative(loadCatalogue(t), []kernel.DomainEvent{created, unsupported})
	if result.Status != kernel.ReplayQuarantined || result.Reason != "UNSUPPORTED_EVENT_VERSION" || result.LastRevision != 1 {
		t.Fatalf("unsupported replay result = %#v", result)
	}
}
