package application_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/tekroo-ai/teams/adapters/fake"
	"github.com/tekroo-ai/teams/adapters/memory"
	"github.com/tekroo-ai/teams/application"
	"github.com/tekroo-ai/teams/contract"
	"github.com/tekroo-ai/teams/kernel"
)

func TestHandlerCommitsOnceAndReturnsStoredReceiptOnReplay(t *testing.T) {
	catalogue := loadCatalogue(t)
	store := memory.NewStore()
	eventID := kernel.UUIDv7("00000000-0000-7000-8000-000000000004")
	handler, err := application.NewHandler(
		store,
		kernel.Evaluator{Catalogue: catalogue},
		fake.NewClock(time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)),
		fake.NewIDSource(eventID, kernel.UUIDv7("00000000-0000-7000-8000-000000000005")),
	)
	if err != nil {
		t.Fatal(err)
	}
	command := kernel.KernelCommand{
		ContractManifest:          kernel.ContractIdentity,
		CommandID:                 kernel.UUIDv7("00000000-0000-7000-8000-000000000001"),
		CommandType:               "tekroo.command.story.create",
		CommandVersion:            kernel.SchemaVersion,
		Target:                    kernel.AggregateRef{Kind: kernel.AggregateStory, ID: kernel.UUIDv7("00000000-0000-7000-8000-000000000002")},
		Authority:                 kernel.PrincipalRef{Kind: kernel.PrincipalHuman, ID: "principal"},
		ExpectedRevision:          kernel.MustNotExist(),
		ExpectedPolicyRevision:    1,
		ExpectedCatalogueRevision: kernel.CatalogueRevision,
		IdempotencyKey:            "create-story-1",
		CorrelationID:             kernel.UUIDv7("00000000-0000-7000-8000-000000000003"),
		Payload:                   json.RawMessage(`{"acceptance_criteria":["works"],"description":"description","title":"title"}`),
	}
	provenance := testProvenance(t)
	store.SetAuthorizationPolicy(testAuthorizationPolicy(command, provenance))

	first, err := handler.Handle(context.Background(), command, provenance)
	if err != nil {
		t.Fatalf("first handle: %v", err)
	}
	second, err := handler.Handle(context.Background(), command, provenance)
	if err != nil {
		t.Fatalf("replayed handle: %v", err)
	}
	if first.CommandID != second.CommandID || first.EventIDs[0] != eventID || second.EventIDs[0] != eventID {
		t.Fatalf("receipts differ: %#v %#v", first, second)
	}
	if store.EventCount() != 1 {
		t.Fatalf("event count = %d, want 1", store.EventCount())
	}
	sameScope := command
	sameScope.CommandID = kernel.UUIDv7("00000000-0000-7000-8000-000000000011")
	sameScope.CorrelationID = kernel.UUIDv7("00000000-0000-7000-8000-000000000012")
	scopedReceipt, err := handler.Handle(context.Background(), sameScope, provenance)
	if err != nil {
		t.Fatalf("same idempotency scope replay: %v", err)
	}
	if scopedReceipt.CommandID != command.CommandID || store.EventCount() != 1 {
		t.Fatalf("scope replay receipt = %#v, event count = %d", scopedReceipt, store.EventCount())
	}
	changedScopeContent := sameScope
	changedScopeContent.CommandID = kernel.UUIDv7("00000000-0000-7000-8000-000000000013")
	changedScopeContent.Payload = json.RawMessage(`{"acceptance_criteria":["different"],"description":"description","title":"title"}`)
	idempotencyConflict, err := handler.Handle(context.Background(), changedScopeContent, provenance)
	if err != nil || idempotencyConflict.OutcomeCode != kernel.OutcomeRejectedConflict || idempotencyConflict.ReasonCode != "IDEMPOTENCY_KEY_REUSE" {
		t.Fatalf("idempotency key reuse receipt = %#v, error = %v", idempotencyConflict, err)
	}
	conflicting := command
	conflicting.Payload = json.RawMessage(`{"acceptance_criteria":["different"],"description":"description","title":"title"}`)
	commandConflict, err := handler.Handle(context.Background(), conflicting, provenance)
	if err != nil || commandConflict.OutcomeCode != kernel.OutcomeRejectedConflict || commandConflict.ReasonCode != "COMMAND_ID_REUSE" {
		t.Fatalf("command identity reuse receipt = %#v, error = %v", commandConflict, err)
	}
	if store.IdentityConflictCount() != 2 {
		t.Fatalf("identity conflict audits = %d, want 2", store.IdentityConflictCount())
	}
	revoked := testAuthorizationPolicy(command, provenance)
	revoked.Grants[0].Revoked = true
	store.SetAuthorizationPolicy(revoked)
	if _, err := handler.Handle(context.Background(), command, provenance); !errors.Is(err, kernel.ErrReceiptAccessDenied) {
		t.Fatalf("revoked receipt replay error = %v, want ErrReceiptAccessDenied", err)
	}
}

func TestHandlerReconcilesConcurrentWinner(t *testing.T) {
	catalogue := loadCatalogue(t)
	command := testStoryCreateCommand()
	stored := kernel.CommandReceipt{CommandID: command.CommandID, OutcomeCode: kernel.OutcomeApplied}
	store := &winnerStore{receipt: stored}
	handler, err := application.NewHandler(
		store,
		kernel.Evaluator{Catalogue: catalogue},
		fake.NewClock(time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)),
		fake.NewIDSource(
			kernel.UUIDv7("00000000-0000-7000-8000-000000000004"),
			kernel.UUIDv7("00000000-0000-7000-8000-000000000005"),
		),
	)
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := handler.Handle(context.Background(), command, testProvenance(t))
	if err != nil {
		t.Fatalf("handle: %v", err)
	}
	if receipt.CommandID != stored.CommandID || store.lookups != 2 {
		t.Fatalf("reconciled receipt = %#v, lookups = %d", receipt, store.lookups)
	}
}

func TestHandlerReconcilesLostCommitAcknowledgement(t *testing.T) {
	catalogue := loadCatalogue(t)
	store := memory.NewStoreWithFault(memory.FaultAfterCommitBeforeAck)
	eventID := kernel.UUIDv7("00000000-0000-7000-8000-000000000004")
	handler, err := application.NewHandler(
		store,
		kernel.Evaluator{Catalogue: catalogue},
		fake.NewClock(time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)),
		fake.NewIDSource(eventID, kernel.UUIDv7("00000000-0000-7000-8000-000000000005")),
	)
	if err != nil {
		t.Fatal(err)
	}
	command := testStoryCreateCommand()
	provenance := testProvenance(t)
	store.SetAuthorizationPolicy(testAuthorizationPolicy(command, provenance))
	receipt, err := handler.Handle(context.Background(), command, provenance)
	if err != nil {
		t.Fatalf("reconcile uncertain commit: %v", err)
	}
	if receipt.OutcomeCode != kernel.OutcomeApplied || len(receipt.EventIDs) != 1 || receipt.EventIDs[0] != eventID {
		t.Fatalf("reconciled receipt = %#v", receipt)
	}
	if store.EventCount() != 1 {
		t.Fatalf("event count = %d, want 1", store.EventCount())
	}
}

type winnerStore struct {
	lookups int
	receipt kernel.CommandReceipt
}

func (s *winnerStore) LoadDecision(_ context.Context, command kernel.KernelCommand) (kernel.Snapshot, error) {
	basis, _ := fake.ProvenanceBasis()
	return kernel.Snapshot{Authorization: testAuthorizationPolicy(command, basis)}, nil
}

func (s *winnerStore) LookupReceipt(context.Context, kernel.KernelCommand, time.Time) (kernel.CommandReceipt, bool, error) {
	s.lookups++
	return s.receipt, s.lookups > 1, nil
}

func (s *winnerStore) RecordIdentityConflict(context.Context, kernel.IdentityConflictAudit) error {
	return nil
}

func (s *winnerStore) Commit(context.Context, kernel.Snapshot, kernel.Decision) error {
	return kernel.ErrDecisionAlreadyCommitted
}

func testStoryCreateCommand() kernel.KernelCommand {
	return kernel.KernelCommand{
		ContractManifest:          kernel.ContractIdentity,
		CommandID:                 kernel.UUIDv7("00000000-0000-7000-8000-000000000001"),
		CommandType:               "tekroo.command.story.create",
		CommandVersion:            kernel.SchemaVersion,
		Target:                    kernel.AggregateRef{Kind: kernel.AggregateStory, ID: kernel.UUIDv7("00000000-0000-7000-8000-000000000002")},
		Authority:                 kernel.PrincipalRef{Kind: kernel.PrincipalHuman, ID: "principal"},
		ExpectedRevision:          kernel.MustNotExist(),
		ExpectedPolicyRevision:    1,
		ExpectedCatalogueRevision: kernel.CatalogueRevision,
		IdempotencyKey:            "create-story-1",
		CorrelationID:             kernel.UUIDv7("00000000-0000-7000-8000-000000000003"),
		Payload:                   json.RawMessage(`{"acceptance_criteria":["works"],"description":"description","title":"title"}`),
	}
}

func loadCatalogue(t *testing.T) *contract.Catalogue {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate handler test")
	}
	repositoryRoot := filepath.Clean(filepath.Join(filepath.Dir(file), ".."))
	catalogue, err := contract.Load(os.DirFS(repositoryRoot), "CONTRACTS/tekroo.kernel.contracts/0.11.0")
	if err != nil {
		t.Fatal(err)
	}
	return catalogue
}

func testProvenance(t *testing.T) kernel.ProvenanceBasis {
	t.Helper()
	basis, err := fake.ProvenanceBasis()
	if err != nil {
		t.Fatal(err)
	}
	return basis
}

func testAuthorizationPolicy(command kernel.KernelCommand, basis kernel.ProvenanceBasis) kernel.AuthorizationPolicy {
	return kernel.AuthorizationPolicy{
		PolicyDigest: basis.PolicyDigest,
		Revision:     basis.PolicyRevision,
		Grants: []kernel.AuthorityGrant{{
			GrantDigest: basis.GrantDigests[0],
			Grantee:     command.Authority,
			Scope: kernel.AuthorityScope{
				CommandTypes:  []string{command.CommandType},
				TargetKinds:   []kernel.AggregateKind{command.Target.Kind},
				TargetIDs:     []kernel.UUIDv7{command.Target.ID},
				CanReadTarget: true,
			},
		}},
	}
}
