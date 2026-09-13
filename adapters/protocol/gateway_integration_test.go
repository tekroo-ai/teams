package protocol_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/tekroo-ai/teams/adapters/fake"
	"github.com/tekroo-ai/teams/adapters/memory"
	"github.com/tekroo-ai/teams/adapters/protocol"
	"github.com/tekroo-ai/teams/application"
	"github.com/tekroo-ai/teams/contract"
	"github.com/tekroo-ai/teams/kernel"
)

func TestGatewayInvokesApplicationHandlerAndPreservesIdempotentReceipt(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate integration test")
	}
	repositoryRoot := filepath.Clean(filepath.Join(filepath.Dir(file), "../.."))
	catalogue, err := contract.Load(os.DirFS(repositoryRoot), "CONTRACTS/tekroo.kernel.contracts/0.11.0")
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
	request := validRequest(t)
	request.Command.Payload = json.RawMessage(`{"acceptance_criteria":["works"],"description":"description","title":"title"}`)
	basis := request.Provenance
	store.SetAuthorizationPolicy(kernel.AuthorizationPolicy{
		PolicyDigest: basis.PolicyDigest,
		Revision:     basis.PolicyRevision,
		Grants: []kernel.AuthorityGrant{{
			GrantDigest: basis.GrantDigests[0],
			Grantee:     request.Command.Authority,
			Scope: kernel.AuthorityScope{
				CommandTypes:  []string{request.Command.CommandType},
				TargetKinds:   []kernel.AggregateKind{request.Command.Target.Kind},
				TargetIDs:     []kernel.UUIDv7{request.Command.Target.ID},
				CanReadTarget: true,
			},
		}},
	})
	gateway, err := protocol.NewGateway(handler, time.Second)
	if err != nil {
		t.Fatal(err)
	}

	first := gateway.Invoke(context.Background(), request)
	second := gateway.Invoke(context.Background(), request)
	if first.Status != protocol.StatusReceipt || second.Status != protocol.StatusReceipt || first.Receipt == nil || second.Receipt == nil {
		t.Fatalf("responses = %#v %#v", first, second)
	}
	if len(first.Receipt.EventIDs) != 1 || first.Receipt.EventIDs[0] != eventID || second.Receipt.EventIDs[0] != eventID {
		t.Fatalf("receipts = %#v %#v", first.Receipt, second.Receipt)
	}
	if store.EventCount() != 1 {
		t.Fatalf("event count = %d, want 1", store.EventCount())
	}
}
