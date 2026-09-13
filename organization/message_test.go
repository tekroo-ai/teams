package organization

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/tekroo-ai/teams/kernel"
)

func TestMessageThreadAllowsProgressAndRejectsRenamedLoopBudgetResetAndCycle(t *testing.T) {
	store := NewMemoryOrganizationalMessageStore()
	first := testMessage(0)
	if err := store.AppendMessage(context.Background(), first); err != nil {
		t.Fatal(err)
	}
	second := nextTestMessage(first, 1)
	if err := store.AppendMessage(context.Background(), second); err != nil {
		t.Fatal(err)
	}

	renamed := nextTestMessage(second, 2)
	renamed.Type = "tekroo.message.unrelated-renamed-type"
	renamed.Flow.ProgressDigest = second.Flow.ProgressDigest
	if err := store.AppendMessage(context.Background(), renamed); !errors.Is(err, ErrOrganizationalLoop) {
		t.Fatalf("renamed no-progress loop error=%v", err)
	}

	budgetReset := nextTestMessage(second, 3)
	budgetReset.Flow.BudgetAccountID = testUUID(15)
	if err := store.AppendMessage(context.Background(), budgetReset); !errors.Is(err, ErrOrganizationalLoop) {
		t.Fatalf("budget reset error=%v", err)
	}

	cycle := nextTestMessage(second, 4)
	cycle.Flow.StepID = first.Flow.StepID
	cycle.Work.DAGNodeID = cycle.Flow.StepID
	if err := store.AppendMessage(context.Background(), cycle); !errors.Is(err, ErrOrganizationalLoop) {
		t.Fatalf("DAG-node cycle error=%v", err)
	}
	roleReuse := nextTestMessage(second, 4)
	roleReuse.Recipient = "teams::architect-2"
	if err := store.AppendMessage(context.Background(), roleReuse); err != nil {
		t.Fatalf("legitimate later role use rejected: %v", err)
	}

	third := nextTestMessage(roleReuse, 5)
	if err := store.AppendMessage(context.Background(), third); err != nil {
		t.Fatalf("legitimate progress rejected: %v", err)
	}
}

func TestMessageClaimIsFencedAcrossLeaseRedelivery(t *testing.T) {
	roles := NewMemoryRoleStore()
	recipient := kernel.ActorFQN("teams::coder-1")
	firstExecution := testExecution(20, 1)
	if err := roles.CompareAndSwapRole(context.Background(), 0, testRoleState("teams::architect-1", testExecution(2, 1))); err != nil {
		t.Fatal(err)
	}
	if err := roles.CompareAndSwapRole(context.Background(), 0, testRoleState(recipient, firstExecution)); err != nil {
		t.Fatal(err)
	}
	store := NewMemoryOrganizationalMessageStore()
	bus, err := NewMessageBus(store, roles)
	if err != nil {
		t.Fatal(err)
	}
	message := testMessage(0)
	message.Recipient = recipient
	if err := bus.Send(context.Background(), message); err != nil {
		t.Fatal(err)
	}
	now := message.CreatedAt.Add(time.Second)
	first, err := bus.Claim(context.Background(), recipient, firstExecution, now, time.Second, 3)
	if err != nil {
		t.Fatal(err)
	}
	second, err := bus.Claim(context.Background(), recipient, firstExecution, now.Add(2*time.Second), time.Second, 3)
	if err != nil {
		t.Fatal(err)
	}
	if second.ClaimEpoch != first.ClaimEpoch+1 {
		t.Fatalf("claim epochs first=%d second=%d", first.ClaimEpoch, second.ClaimEpoch)
	}
	evidence := testDigest('e')
	if err := store.ResolveMessage(context.Background(), message.ID, recipient, firstExecution, first.ClaimEpoch, now.Add(2*time.Second), "stale", evidence); !errors.Is(err, ErrStaleOrganizationalClaim) {
		t.Fatalf("stale resolution error=%v", err)
	}
	if err := store.ResolveMessage(context.Background(), message.ID, recipient, firstExecution, second.ClaimEpoch, now.Add(2500*time.Millisecond), "handled", evidence); err != nil {
		t.Fatal(err)
	}
}

func TestMessageBusRejectsStaleRoleExecutionBeforeClaim(t *testing.T) {
	roles := NewMemoryRoleStore()
	recipient := kernel.ActorFQN("teams::coder-1")
	current := testExecution(30, 2)
	if err := roles.CompareAndSwapRole(context.Background(), 0, testRoleState("teams::architect-1", testExecution(2, 1))); err != nil {
		t.Fatal(err)
	}
	if err := roles.CompareAndSwapRole(context.Background(), 0, testRoleState(recipient, current)); err != nil {
		t.Fatal(err)
	}
	store := NewMemoryOrganizationalMessageStore()
	bus, _ := NewMessageBus(store, roles)
	message := testMessage(0)
	message.Recipient = recipient
	if err := bus.Send(context.Background(), message); err != nil {
		t.Fatal(err)
	}
	if _, err := bus.Claim(context.Background(), recipient, testExecution(31, 1), message.CreatedAt.Add(time.Second), time.Second, 3); !errors.Is(err, ErrStaleOrganizationalClaim) {
		t.Fatalf("stale execution claim error=%v", err)
	}
}

func TestMessageBusRejectsSpoofedOrStaleSenderExecution(t *testing.T) {
	roles := NewMemoryRoleStore()
	if err := roles.CompareAndSwapRole(context.Background(), 0, testRoleState("teams::architect-1", testExecution(2, 1))); err != nil {
		t.Fatal(err)
	}
	store := NewMemoryOrganizationalMessageStore()
	bus, _ := NewMessageBus(store, roles)
	message := testMessage(0)
	message.SenderExecution = testExecution(3, 1)
	if err := bus.Send(context.Background(), message); !errors.Is(err, ErrStaleOrganizationalClaim) {
		t.Fatalf("spoofed sender error=%v", err)
	}
}

func TestMessageFanoutCreatesOneAtomicDeliveryPerExactRecipient(t *testing.T) {
	roles := NewMemoryRoleStore()
	firstRecipient := kernel.ActorFQN("teams::coder-1")
	secondRecipient := kernel.ActorFQN("teams::coder-2")
	firstExecution := testExecution(11, 1)
	secondExecution := testExecution(12, 1)
	if err := roles.CompareAndSwapRole(context.Background(), 0, testRoleState("teams::architect-1", testExecution(2, 1))); err != nil {
		t.Fatal(err)
	}
	if err := roles.CompareAndSwapRole(context.Background(), 0, testRoleState(firstRecipient, firstExecution)); err != nil {
		t.Fatal(err)
	}
	if err := roles.CompareAndSwapRole(context.Background(), 0, testRoleState(secondRecipient, secondExecution)); err != nil {
		t.Fatal(err)
	}
	store := NewMemoryOrganizationalMessageStore()
	bus, _ := NewMessageBus(store, roles)
	fanoutID := testUUID(13)
	first := testMessage(0)
	first.Recipient = firstRecipient
	first.FanoutID = &fanoutID
	second := first
	second.ID = testUUID(14)
	second.Recipient = secondRecipient
	second.CorrelationID = testUUID(15)
	second.Flow.ThreadID = second.CorrelationID
	second.Flow.StepID = testUUID(4)
	second.Work.DAGNodeID = second.Flow.StepID
	second.Flow.ProgressDigest = testDigest('b')
	if err := bus.SendFanout(context.Background(), []OrganizationalMessage{first, second}); err != nil {
		t.Fatal(err)
	}
	now := first.CreatedAt.Add(time.Second)
	firstClaim, err := bus.Claim(context.Background(), firstRecipient, firstExecution, now, time.Second, 3)
	if err != nil {
		t.Fatal(err)
	}
	secondClaim, err := bus.Claim(context.Background(), secondRecipient, secondExecution, now, time.Second, 3)
	if err != nil {
		t.Fatal(err)
	}
	if firstClaim.Message.FanoutID == nil || secondClaim.Message.FanoutID == nil || *firstClaim.Message.FanoutID != fanoutID || *secondClaim.Message.FanoutID != fanoutID {
		t.Fatalf("fanout identities first=%v second=%v", firstClaim.Message.FanoutID, secondClaim.Message.FanoutID)
	}
}

func TestMessageBatchAppendIsAtomic(t *testing.T) {
	store := NewMemoryOrganizationalMessageStore()
	first := testMessage(0)
	invalid := testMessage(1)
	invalid.Flow.Hop = 2
	if err := store.AppendMessages(context.Background(), []OrganizationalMessage{first, invalid}); err == nil {
		t.Fatal("invalid batch accepted")
	}
	if _, err := store.AcquireMessage(context.Background(), first.Recipient, testExecution(5, 1), first.CreatedAt.Add(time.Second), time.Second, 3); !errors.Is(err, ErrOrganizationalMessageNotFound) {
		t.Fatalf("partial fanout persisted: %v", err)
	}
}

func TestMessagePoisonDeliveryTerminatesInInspectableDeadLetter(t *testing.T) {
	store := NewMemoryOrganizationalMessageStore()
	message := testMessage(0)
	if err := store.AppendMessage(context.Background(), message); err != nil {
		t.Fatal(err)
	}
	execution := testExecution(5, 1)
	now := message.CreatedAt.Add(time.Second)
	if _, err := store.AcquireMessage(context.Background(), message.Recipient, execution, now, time.Second, 2); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AcquireMessage(context.Background(), message.Recipient, execution, now.Add(2*time.Second), time.Second, 2); err != nil {
		t.Fatal(err)
	}
	released, dead, err := store.SweepMessages(context.Background(), now.Add(4*time.Second), 2)
	if err != nil || released != 0 || dead != 1 {
		t.Fatalf("sweep released=%d dead=%d err=%v", released, dead, err)
	}
	letters, err := store.ListDeadLetters(context.Background(), message.Recipient, 10)
	if err != nil || len(letters) != 1 || letters[0].Message.ID != message.ID {
		t.Fatalf("dead letters=%+v err=%v", letters, err)
	}
}

func TestPendingMessageReaddressIsExactAuditedAndDoesNotResetBudget(t *testing.T) {
	roles := NewMemoryRoleStore()
	senderExecution := testExecution(2, 1)
	coderExecution := testExecution(5, 1)
	testerExecution := testExecution(6, 1)
	for _, state := range []RoleInstanceState{
		testRoleState("teams::architect-1", senderExecution),
		testRoleState("teams::coder-1", coderExecution),
		testRoleState("teams::tester-1", testerExecution),
	} {
		if err := roles.CompareAndSwapRole(context.Background(), 0, state); err != nil {
			t.Fatal(err)
		}
	}
	store := NewMemoryOrganizationalMessageStore()
	bus, _ := NewMessageBus(store, roles)
	message := testMessage(0)
	if err := bus.Send(context.Background(), message); err != nil {
		t.Fatal(err)
	}
	updated, err := bus.Readdress(context.Background(), message.ID, message.Sender, senderExecution, "teams::tester-1", message.CreatedAt.Add(time.Second), 2)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Recipient != "teams::tester-1" || updated.Flow.BudgetAccountID != message.Flow.BudgetAccountID || len(updated.ReaddressHistory) != 1 || updated.ReaddressHistory[0].From != message.Recipient {
		t.Fatalf("readdressed message=%+v", updated)
	}
	if _, err := bus.Claim(context.Background(), message.Recipient, coderExecution, message.CreatedAt.Add(2*time.Second), time.Second, 3); !errors.Is(err, ErrOrganizationalMessageNotFound) {
		t.Fatalf("old recipient still claimable: %v", err)
	}
	if _, err := bus.Claim(context.Background(), updated.Recipient, testerExecution, message.CreatedAt.Add(2*time.Second), time.Second, 3); err != nil {
		t.Fatal(err)
	}
}

func TestDeadLetterRepairAppendsSuccessorWithoutReopeningOrResettingBudget(t *testing.T) {
	roles := NewMemoryRoleStore()
	for _, state := range []RoleInstanceState{
		testRoleState("teams::architect-1", testExecution(2, 1)),
		testRoleState("teams::coder-1", testExecution(5, 1)),
		testRoleState("teams::tester-1", testExecution(6, 1)),
	} {
		if err := roles.CompareAndSwapRole(context.Background(), 0, state); err != nil {
			t.Fatal(err)
		}
	}
	store := NewMemoryOrganizationalMessageStore()
	bus, _ := NewMessageBus(store, roles)
	failed := testMessage(0)
	if err := bus.Send(context.Background(), failed); err != nil {
		t.Fatal(err)
	}
	now := failed.CreatedAt.Add(time.Second)
	if _, err := store.AcquireMessage(context.Background(), failed.Recipient, testExecution(5, 1), now, time.Second, 1); err != nil {
		t.Fatal(err)
	}
	if _, dead, err := store.SweepMessages(context.Background(), now.Add(2*time.Second), 1); err != nil || dead != 1 {
		t.Fatalf("dead-letter sweep dead=%d err=%v", dead, err)
	}
	successor := nextTestMessage(failed, 1)
	successor.SenderExecution = testExecution(5, 1)
	if err := bus.RepairDeadLetter(context.Background(), failed.ID, successor); err != nil {
		t.Fatal(err)
	}
	prior, found, err := bus.Read(context.Background(), failed.ID)
	if err != nil || !found || prior.State != MessageDeadLetter || prior.Attempts != 1 {
		t.Fatalf("failed delivery mutated: %+v found=%v err=%v", prior, found, err)
	}
	trace, err := bus.Trace(context.Background(), failed.Flow.ThreadID)
	if err != nil || len(trace) != 2 || trace[1].State != MessagePending || trace[1].Message.Flow.BudgetAccountID != failed.Flow.BudgetAccountID {
		t.Fatalf("repair trace=%+v err=%v", trace, err)
	}
	reset := successor
	reset.ID = testUUID(14)
	reset.Flow.StepID = testUUID(15)
	reset.Work.DAGNodeID = reset.Flow.StepID
	reset.Flow.BudgetAccountID = testUUID(13)
	if err := bus.RepairDeadLetter(context.Background(), failed.ID, reset); !errors.Is(err, ErrOrganizationalMessageConflict) {
		t.Fatalf("budget reset repair error=%v", err)
	}
}

func testMessage(offset int) OrganizationalMessage {
	created := time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)
	story := testUUID(6)
	task := testUUID(7)
	return OrganizationalMessage{
		SchemaVersion: OrganizationalMessageSchemaVersion,
		ID:            testUUID(1 + offset), Type: "tekroo.message.task.assigned", Purpose: PurposeHandoff,
		Sender: "teams::architect-1", SenderExecution: testExecution(2, 1), Recipient: "teams::coder-1",
		CorrelationID: testUUID(3), Work: MessageWorkLink{StoryID: &story, TaskID: &task, DAGNodeID: testUUID(9)},
		Flow: MessageFlow{ThreadID: testUUID(3), StepID: testUUID(9), Hop: 1, MaximumHops: 8, BudgetAccountID: testUUID(10), LifecycleEpoch: 1, ScopeRevision: 1, ProgressDigest: testDigest('a')},
		Body: json.RawMessage(`{"task":"implement bounded change"}`), CreatedAt: created, ExpiresAt: created.Add(time.Hour),
	}
}

func nextTestMessage(previous OrganizationalMessage, offset int) OrganizationalMessage {
	next := previous
	next.ID = testUUID(40 + offset)
	next.Sender, next.Recipient = previous.Recipient, "teams::tester-1"
	if next.Sender == next.Recipient {
		next.Recipient = "teams::product-owner-1"
	}
	next.CausationID = &previous.ID
	next.Flow.ParentStepID = &previous.Flow.StepID
	next.Flow.StepID = testUUID(50 + offset)
	next.Flow.Hop++
	next.Flow.ProgressDigest = testDigest("bcdef"[offset%5])
	next.Work.DAGNodeID = next.Flow.StepID
	next.CreatedAt = previous.CreatedAt.Add(time.Duration(next.Flow.Hop) * time.Second)
	next.ExpiresAt = next.CreatedAt.Add(time.Hour)
	return next
}

func testRoleState(actor kernel.ActorFQN, execution kernel.ExecutionTuple) RoleInstanceState {
	instance := uint32(1)
	if actor == "teams::coder-2" {
		instance = 2
	}
	return RoleInstanceState{
		ActorFQN: actor, Team: "teams", Role: actorRole(actor), Instance: instance, Revision: 1,
		BundleDigest: testDigest('1'), ModelProfile: testDigest('2'), WorkspaceID: "coder-1", Status: RoleIdle,
		Execution: execution, ProcessIdentity: "process", StartedAt: time.Now().UTC(), LastHeartbeatAt: time.Now().UTC(),
		ManifestDigest: testDigest('3'), ManifestVersion: "1.0.0", BundleVersion: "1.0.0", RuntimeGeneration: execution.FencingEpoch,
	}
}

func testExecution(id int, epoch uint64) kernel.ExecutionTuple {
	return kernel.ExecutionTuple{ExecutionID: testUUID(id), FencingEpoch: epoch}
}

func testUUID(value int) kernel.UUIDv7 {
	const digits = "0123456789abcdef"
	last := digits[value%16]
	return kernel.UUIDv7("00000000-0000-7000-8000-00000000000" + string(last))
}

func testDigest(value byte) kernel.Digest {
	raw := make([]byte, 64)
	for index := range raw {
		raw[index] = value
	}
	return kernel.Digest(raw)
}
