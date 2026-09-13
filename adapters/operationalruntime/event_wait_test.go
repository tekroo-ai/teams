package operationalruntime

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/tekroo-ai/teams/kernel"
)

func TestEventWaitRequestValidation(t *testing.T) {
	request := EventWaitRequest{
		Aggregate:     kernel.AggregateRef{Kind: kernel.AggregateWorkInvocation, ID: "00000000-0000-7000-8000-000000000003"},
		AfterRevision: 3,
		EventTypes:    []string{"tekroo.event.work-invocation.terminal-recorded"},
		TimeoutMillis: 90_000,
	}
	if !request.Valid() {
		t.Fatal("valid event wait rejected")
	}
	request.EventTypes = append(request.EventTypes, request.EventTypes[0])
	if request.Valid() {
		t.Fatal("duplicate event type accepted")
	}
	request.EventTypes = nil
	request.TimeoutMillis = 0
	if request.Valid() {
		t.Fatal("unbounded event wait accepted")
	}
}

func TestEventWaitResultExposesNoticeWithoutDomainPayload(t *testing.T) {
	result := EventWaitResult{
		Outcome:       EventWaitMatched,
		Aggregate:     kernel.AggregateRef{Kind: kernel.AggregateWorkInvocation, ID: "00000000-0000-7000-8000-000000000003"},
		AfterRevision: 3,
		Event: &EventNotice{
			EventID:           "00000000-0000-7000-8000-000000000004",
			EventType:         "tekroo.event.work-invocation.terminal-recorded",
			Aggregate:         kernel.AggregateRef{Kind: kernel.AggregateWorkInvocation, ID: "00000000-0000-7000-8000-000000000003"},
			AggregateRevision: 4,
			CommittedAt:       time.Date(2026, time.September, 12, 12, 0, 0, 0, time.UTC),
		},
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"payload", "authority", "actor_fqn", "execution"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("event notice leaked %q: %s", forbidden, encoded)
		}
	}
}
