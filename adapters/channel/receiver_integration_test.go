package channel_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/tekroo-ai/teams/adapters/channel"
	"github.com/tekroo-ai/teams/adapters/fake"
	"github.com/tekroo-ai/teams/adapters/memory"
	"github.com/tekroo-ai/teams/adapters/protocol"
	"github.com/tekroo-ai/teams/application"
	"github.com/tekroo-ai/teams/contract"
	"github.com/tekroo-ai/teams/kernel"
)

func TestDuplicateChannelDeliveryCommitsOneOrganizationalEffect(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate integration test")
	}
	repositoryRoot := filepath.Clean(filepath.Join(filepath.Dir(file), "../.."))
	catalogue, err := contract.Load(os.DirFS(repositoryRoot), "CONTRACTS/tekroo.kernel.contracts/0.7.0")
	if err != nil {
		t.Fatal(err)
	}
	store := memory.NewStore()
	eventID := uuid("00000000-0000-7000-8000-000000000014")
	handler, err := application.NewHandler(
		store,
		kernel.Evaluator{Catalogue: catalogue},
		fake.NewClock(time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)),
		fake.NewIDSource(eventID, uuid("00000000-0000-7000-8000-000000000015")),
	)
	if err != nil {
		t.Fatal(err)
	}
	gateway, err := protocol.NewGateway(handler, time.Second)
	if err != nil {
		t.Fatal(err)
	}

	principal := kernel.PrincipalRef{Kind: kernel.PrincipalHuman, ID: "channel-principal"}
	intake := channel.IntakeContext{
		AuthenticatedSender: protocol.AuthenticatedContext{Principal: principal},
		ReceivedAt:          time.Date(2026, 8, 11, 12, 30, 0, 0, time.UTC),
		TransportProvenance: "test-provider:delivery-18",
	}
	invocation := testInvocation(t)
	invocation.Command.Authority = principal
	invocation.Command.ActorFQN = nil
	invocation.Command.Execution = nil
	invocation.Command.Payload = json.RawMessage(`{"acceptance_criteria":["works"],"description":"description","title":"title"}`)
	basis := invocation.Provenance
	store.SetAuthorizationPolicy(kernel.AuthorizationPolicy{
		PolicyDigest: basis.PolicyDigest,
		Revision:     basis.PolicyRevision,
		Grants: []kernel.AuthorityGrant{{
			GrantDigest: basis.GrantDigests[0],
			Grantee:     principal,
			Scope: kernel.AuthorityScope{
				CommandTypes:  []string{invocation.Command.CommandType},
				TargetKinds:   []kernel.AggregateKind{invocation.Command.Target.Kind},
				TargetIDs:     []kernel.UUIDv7{invocation.Command.Target.ID},
				CanReadTarget: true,
			},
		}},
	})
	receiver := newReceiver(t, testRecipient(), gateway, &collectingSink{}, 64*1024)
	raw := encodeFrame(t, testRecipient(), invocation)

	first := receiver.Receive(context.Background(), intake, raw)
	second := receiver.Receive(context.Background(), intake, raw)
	if first.Status != protocol.StatusReceipt || second.Status != protocol.StatusReceipt || store.EventCount() != 1 {
		t.Fatalf("statuses=%s,%s eventCount=%d first=%#v second=%#v", first.Status, second.Status, store.EventCount(), first, second)
	}
	if first.Receipt == nil || second.Receipt == nil || len(first.Receipt.EventIDs) != 1 || len(second.Receipt.EventIDs) != 1 || first.Receipt.EventIDs[0] != eventID || second.Receipt.EventIDs[0] != eventID {
		t.Fatalf("receipts=%#v %#v", first.Receipt, second.Receipt)
	}
}
