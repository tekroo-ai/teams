//go:build mongo_integration

package mongo

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/tekroo-ai/teams/kernel"
)

func TestUnprojectedTaskDiagnosticsRevealPreActivationBlock(t *testing.T) {
	store := openTestStore(t)
	ref := kernel.AggregateRef{Kind: kernel.AggregateTask, ID: testUUID(88101)}
	state := kernel.AggregateState{Kind: kernel.AggregateTask, ID: ref.ID, Revision: 3, LifecycleEpoch: 1, ScopeRevision: 1, Phase: kernel.PhasePlanned, Condition: kernel.ConditionBlocked}
	insertProjectionAggregate(t, store, state)
	created := projectionMongoEvent(ref, 1, testUUID(88111), "tekroo.event.task.created", json.RawMessage(`{"title":"Whole feature validation"}`))
	block := projectionMongoEvent(ref, 2, testUUID(88112), "tekroo.event.work.blocked", json.RawMessage(`{"reason":"verification execution timed out; candidate verdict INCONCLUSIVE","blocker_refs":["teams://evidence/timeout"],"review_policy":"operator recovery"}`))
	profile, _ := json.Marshal(mongoTestWorkProfile(ref.ID))
	maintenance := projectionMongoEvent(ref, 3, testUUID(88113), "tekroo.event.task.work-profile-bound", profile)
	for _, event := range []kernel.DomainEvent{created, block, maintenance} {
		insertProjectionEvent(t, store, event)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	rows, err := store.ListUnprojectedTasks(ctx, 1000)
	if err != nil || len(rows) != 1 || rows[0].Condition != kernel.ConditionBlocked || rows[0].Block.EventID != block.EventID || rows[0].LastEventID != maintenance.EventID {
		t.Fatalf("%+v %v", rows, err)
	}
	if _, found, err := store.ReadTaskProjection(ctx, ref.ID); err != nil || found {
		t.Fatal("invented a budget-bound projection", found, err)
	}
	other := kernel.AggregateRef{Kind: kernel.AggregateTask, ID: testUUID(88102)}
	insertProjectionAggregate(t, store, kernel.AggregateState{Kind: kernel.AggregateTask, ID: other.ID, Revision: 1, LifecycleEpoch: 1, ScopeRevision: 1, Phase: kernel.PhasePlanned, Condition: kernel.ConditionRunnable})
	insertProjectionEvent(t, store, projectionMongoEvent(other, 1, testUUID(88114), "tekroo.event.task.created", json.RawMessage(`{"title":"Promotion waiting"}`)))
	if _, err := store.ListUnprojectedTasks(ctx, 1); !errors.Is(err, ErrBacklogLimit) {
		t.Fatal("silent truncation", err)
	}
	rows, err = store.ListUnprojectedTasks(ctx, 1000)
	if err != nil || len(rows) != 2 {
		t.Fatalf("%+v %v", rows, err)
	}
}
