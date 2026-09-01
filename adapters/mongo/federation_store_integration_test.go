//go:build mongo_integration

package mongo

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/tekroo-ai/teams/kernel"
	"github.com/tekroo-ai/teams/organization"
)

func TestFederationReceiptAndMessageAppendAreExactlyOnce(t *testing.T) {
	store := openTestStore(t)
	now := time.Date(2026, 9, 1, 18, 0, 0, 0, time.UTC)
	message := mongoFederationMessage(now, 1)
	envelope := mongoFederationEnvelope(message)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	receipt, created, err := store.AcceptFederatedMessage(ctx, envelope, message, now)
	if err != nil || !created || !receipt.Valid() {
		t.Fatalf("accept = %#v %v %v", receipt, created, err)
	}
	repeated, created, err := store.AcceptFederatedMessage(ctx, envelope, message, now.Add(time.Second))
	if err != nil || created || repeated != receipt {
		t.Fatalf("repeat = %#v %v %v", repeated, created, err)
	}
	claim, found, err := store.ReadMessage(ctx, message.ID)
	if err != nil || !found || claim.Message.ID != message.ID {
		t.Fatalf("message = %#v %v %v", claim, found, err)
	}
	conflicting := envelope
	conflicting.DeliveryID = "00000000-0000-7000-8000-000000000339"
	if _, _, err := store.AcceptFederatedMessage(ctx, conflicting, message, now); !errors.Is(err, organization.ErrFederationReplayConflict) {
		t.Fatalf("conflict = %v", err)
	}
}

func TestFederationOutboundRecordPreservesThreadForReply(t *testing.T) {
	store := openTestStore(t)
	now := time.Date(2026, 9, 1, 18, 0, 0, 0, time.UTC)
	outbound := mongoFederationMessage(now, 1)
	envelope := mongoFederationEnvelope(outbound)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	created, err := store.RecordFederatedOutbound(ctx, envelope, outbound, now)
	if err != nil || !created {
		t.Fatalf("record = %v %v", created, err)
	}
	claim, found, err := store.ReadMessage(ctx, outbound.ID)
	if err != nil || !found || claim.State != organization.MessageResolved || claim.Resolution != "FEDERATED_OUTBOUND" {
		t.Fatalf("outbound = %#v %v %v", claim, found, err)
	}
	reply := mongoFederationMessage(now.Add(time.Second), 2)
	reply.ID = "00000000-0000-7000-8000-000000000341"
	reply.Sender = outbound.Recipient
	reply.Recipient = "source::reviewer-1"
	reply.CausationID = &outbound.ID
	reply.CorrelationID = outbound.CorrelationID
	reply.Work.DAGNodeID = "00000000-0000-7000-8000-000000000342"
	reply.Flow.ThreadID = outbound.Flow.ThreadID
	reply.Flow.StepID = reply.Work.DAGNodeID
	reply.Flow.ParentStepID = &outbound.Flow.StepID
	reply.Flow.Hop = 2
	reply.Flow.ProgressDigest = "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"
	replyEnvelope := mongoFederationEnvelope(reply)
	replyEnvelope.DeliveryID = "00000000-0000-7000-8000-000000000343"
	replyEnvelope.ReplayID = "00000000-0000-7000-8000-000000000344"
	replyEnvelope.MessageSHA256 = digestFederationMessage(t, reply)
	if _, created, err := store.AcceptFederatedMessage(ctx, replyEnvelope, reply, now.Add(time.Second)); err != nil || !created {
		t.Fatalf("reply = %v %v", created, err)
	}
	trace, err := store.TraceMessageThread(ctx, outbound.Flow.ThreadID)
	if err != nil || len(trace) != 2 || trace[0].Message.ID != outbound.ID || trace[1].Message.ID != reply.ID {
		t.Fatalf("trace = %#v %v", trace, err)
	}
}

func mongoFederationMessage(now time.Time, hop uint32) organization.OrganizationalMessage {
	return organization.OrganizationalMessage{SchemaVersion: organization.OrganizationalMessageSchemaVersion, ID: "00000000-0000-7000-8000-000000000320", Type: "tekroo.message.feature.request", Purpose: organization.PurposeRequest, Sender: "source::product-owner-1", SenderExecution: kernel.ExecutionTuple{ExecutionID: "00000000-0000-7000-8000-000000000311", FencingEpoch: 1}, Recipient: "destination::architect-1", CorrelationID: "00000000-0000-7000-8000-000000000312", Work: organization.MessageWorkLink{DAGNodeID: "00000000-0000-7000-8000-000000000313"}, Flow: organization.MessageFlow{ThreadID: "00000000-0000-7000-8000-000000000312", StepID: "00000000-0000-7000-8000-000000000313", Hop: hop, MaximumHops: 8, BudgetAccountID: "00000000-0000-7000-8000-000000000314", LifecycleEpoch: 1, ScopeRevision: 1, ProgressDigest: "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"}, Body: json.RawMessage(`{"request":"design"}`), CreatedAt: now, ExpiresAt: now.Add(time.Hour)}
}

func mongoFederationEnvelope(message organization.OrganizationalMessage) organization.FederatedEnvelope {
	return organization.FederatedEnvelope{SchemaVersion: organization.FederationSchemaVersion, DeliveryID: "00000000-0000-7000-8000-000000000321", ReplayID: "00000000-0000-7000-8000-000000000322", RouteID: "00000000-0000-7000-8000-000000000310", RouteRevision: 1, SourceDeployment: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", DestinationDeployment: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", SourceActor: message.Sender, DestinationActor: message.Recipient, KeyID: "source", KeyEpoch: 1, IssuedAt: message.CreatedAt, ExpiresAt: message.CreatedAt.Add(time.Minute), MessageSHA256: digestFederationMessage(nil, message), Message: json.RawMessage(`{"retained":true}`), RouteTrace: []kernel.Digest{"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}, Signature: "unused-by-store"}
}

func digestFederationMessage(t *testing.T, message organization.OrganizationalMessage) kernel.Digest {
	raw, err := json.Marshal(message)
	if err != nil {
		if t != nil {
			t.Fatal(err)
		}
		return ""
	}
	digest := sha256.Sum256(raw)
	return kernel.Digest(hex.EncodeToString(digest[:]))
}
