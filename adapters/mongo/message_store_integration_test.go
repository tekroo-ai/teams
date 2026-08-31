//go:build mongo_integration

package mongo

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/tekroo-ai/teams/kernel"
	"github.com/tekroo-ai/teams/organization"
)

func TestOrganizationalMessageFeedClaimRecoveryAndFencing(t *testing.T) {
	store := openTestStore(t)
	recipient := kernel.ActorFQN("teams::coder-1")
	execution := kernel.ExecutionTuple{ExecutionID: testUUID(9801), FencingEpoch: 1}
	role := organization.RoleInstanceState{
		ActorFQN: recipient, Team: "teams", Role: "coder", Instance: 1, Revision: 1,
		BundleDigest: messageTestDigest('1'), ModelProfile: messageTestDigest('2'), WorkspaceID: "coder-1", Status: organization.RoleIdle,
		Execution: execution, ProcessIdentity: "test-process", StartedAt: testNow(), LastHeartbeatAt: testNow(),
		ManifestDigest: messageTestDigest('3'), ManifestVersion: "1.0.0", BundleVersion: "1.0.0", RuntimeGeneration: 1,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := store.CompareAndSwapRole(ctx, 0, role); err != nil {
		t.Fatal(err)
	}
	feed, err := store.OpenOrganizationalMessageFeed(ctx, "coder-worker", recipient)
	if err != nil {
		t.Fatal(err)
	}
	defer feed.Close(ctx)
	message := mongoTestMessage(recipient)
	if err := store.AppendMessage(ctx, message); err != nil {
		t.Fatal(err)
	}

	var observed organization.OrganizationalMessage
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		pollContext, pollCancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
		observed, err = feed.Poll(pollContext)
		pollCancel()
		if err == nil {
			break
		}
		if !errors.Is(err, organization.ErrOrganizationalMessageNotFound) {
			t.Fatal(err)
		}
	}
	if observed.ID != message.ID {
		t.Fatalf("observed message=%s want=%s", observed.ID, message.ID)
	}

	now := testNow().Add(time.Second)
	first, err := store.AcquireMessage(ctx, recipient, execution, now, time.Second, 3)
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.AcquireMessage(ctx, recipient, execution, now.Add(2*time.Second), time.Second, 3)
	if err != nil {
		t.Fatal(err)
	}
	if second.ClaimEpoch != first.ClaimEpoch+1 {
		t.Fatalf("epochs first=%d second=%d", first.ClaimEpoch, second.ClaimEpoch)
	}
	if err := store.ResolveMessage(ctx, message.ID, recipient, execution, first.ClaimEpoch, now.Add(2500*time.Millisecond), "stale", messageTestDigest('4')); !errors.Is(err, organization.ErrStaleOrganizationalClaim) {
		t.Fatalf("stale resolution error=%v", err)
	}
	if err := store.ResolveMessage(ctx, message.ID, recipient, execution, second.ClaimEpoch, now.Add(2500*time.Millisecond), "handled", messageTestDigest('4')); err != nil {
		t.Fatal(err)
	}
}

func mongoTestMessage(recipient kernel.ActorFQN) organization.OrganizationalMessage {
	story := testUUID(9802)
	task := testUUID(9803)
	created := testNow()
	thread := testUUID(9804)
	return organization.OrganizationalMessage{
		SchemaVersion: organization.OrganizationalMessageSchemaVersion,
		ID:            testUUID(9805), Type: "tekroo.message.task.assigned", Purpose: organization.PurposeHandoff,
		Sender: "teams::architect-1", SenderExecution: kernel.ExecutionTuple{ExecutionID: testUUID(9809), FencingEpoch: 1}, Recipient: recipient, CorrelationID: thread,
		Work: organization.MessageWorkLink{StoryID: &story, TaskID: &task, DAGNodeID: testUUID(9807)},
		Flow: organization.MessageFlow{ThreadID: thread, StepID: testUUID(9807), Hop: 1, MaximumHops: 8, BudgetAccountID: testUUID(9808), LifecycleEpoch: 1, ScopeRevision: 1, ProgressDigest: messageTestDigest('a')},
		Body: json.RawMessage(`{"task":"bounded integration work"}`), CreatedAt: created, ExpiresAt: created.Add(time.Hour),
	}
}

func messageTestDigest(value byte) kernel.Digest {
	raw := make([]byte, 64)
	for index := range raw {
		raw[index] = value
	}
	return kernel.Digest(raw)
}
