package httpapi_test

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/tekroo-ai/teams/adapters/fake"
	"github.com/tekroo-ai/teams/adapters/httpapi"
	"github.com/tekroo-ai/teams/adapters/memory"
	"github.com/tekroo-ai/teams/adapters/protocol"
	"github.com/tekroo-ai/teams/application"
	"github.com/tekroo-ai/teams/contract"
	"github.com/tekroo-ai/teams/kernel"
)

func TestHTTPHandlerThroughGatewayAndApplicationCommitsOnceOnReplay(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate integration test")
	}
	repositoryRoot := filepath.Clean(filepath.Join(filepath.Dir(file), "../.."))
	catalogue, err := contract.Load(os.DirFS(repositoryRoot), "CONTRACTS/tekroo.kernel.contracts/0.8.0")
	if err != nil {
		t.Fatal(err)
	}
	store := memory.NewStore()
	eventID := kernel.UUIDv7("00000000-0000-7000-8000-000000000014")
	applicationHandler, err := application.NewHandler(
		store,
		kernel.Evaluator{Catalogue: catalogue},
		fake.NewClock(time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)),
		fake.NewIDSource(eventID, kernel.UUIDv7("00000000-0000-7000-8000-000000000015")),
	)
	if err != nil {
		t.Fatal(err)
	}
	identity := protocol.AuthenticatedContext{Principal: testIdentity().Principal}
	invocation := testInvocation(t)
	invocation.Command.ActorFQN = nil
	invocation.Command.Execution = nil
	invocation.Command.Payload = json.RawMessage(`{"acceptance_criteria":["works"],"description":"description","title":"title"}`)
	basis := invocation.Provenance
	store.SetAuthorizationPolicy(kernel.AuthorizationPolicy{
		PolicyDigest: basis.PolicyDigest,
		Revision:     basis.PolicyRevision,
		Grants: []kernel.AuthorityGrant{{
			GrantDigest: basis.GrantDigests[0], Grantee: identity.Principal,
			Scope: kernel.AuthorityScope{
				CommandTypes: []string{invocation.Command.CommandType}, TargetKinds: []kernel.AggregateKind{invocation.Command.Target.Kind},
				TargetIDs: []kernel.UUIDv7{invocation.Command.Target.ID}, CanReadTarget: true,
			},
		}},
	})
	gateway, err := protocol.NewGateway(applicationHandler, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	handler := newHandler(t, gateway, identity, httpapi.OriginPolicyFunc(func(string) bool { return true }), httpapi.RateLimiterFunc(func(protocol.AuthenticatedContext) bool { return true }), httpapi.DefaultMaxBodyBytes)

	first := httptest.NewRecorder()
	handler.ServeHTTP(first, commandRequest(t, invocation))
	second := httptest.NewRecorder()
	handler.ServeHTTP(second, commandRequest(t, invocation))
	if first.Code != 200 || second.Code != 200 || store.EventCount() != 1 {
		t.Fatalf("statuses=%d,%d eventCount=%d first=%s second=%s", first.Code, second.Code, store.EventCount(), first.Body.String(), second.Body.String())
	}
	var firstResponse, secondResponse protocol.Response
	if err := json.Unmarshal(first.Body.Bytes(), &firstResponse); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(second.Body.Bytes(), &secondResponse); err != nil {
		t.Fatal(err)
	}
	if firstResponse.Receipt == nil || secondResponse.Receipt == nil || firstResponse.Receipt.EventIDs[0] != eventID || secondResponse.Receipt.EventIDs[0] != eventID {
		t.Fatalf("receipts=%#v %#v", firstResponse.Receipt, secondResponse.Receipt)
	}
}
