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
		fake.NewIDSource(eventID),
	)
	if err != nil {
		t.Fatal(err)
	}
	command := kernel.KernelCommand{
		ContractManifest: kernel.ContractIdentity,
		CommandID:        kernel.UUIDv7("00000000-0000-7000-8000-000000000001"),
		CommandType:      "tekroo.command.story.create",
		CommandVersion:   kernel.SchemaVersion,
		Target:           kernel.AggregateRef{Kind: kernel.AggregateStory, ID: kernel.UUIDv7("00000000-0000-7000-8000-000000000002")},
		Authority:        kernel.PrincipalRef{Kind: kernel.PrincipalHuman, ID: "principal"},
		ExpectedRevision: kernel.MustNotExist(),
		IdempotencyKey:   "create-story-1",
		CorrelationID:    kernel.UUIDv7("00000000-0000-7000-8000-000000000003"),
		Payload:          json.RawMessage(`{"acceptance_criteria":["works"],"description":"description","title":"title"}`),
	}
	provenance := kernel.Digest("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")

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
	conflicting := command
	conflicting.Payload = json.RawMessage(`{"acceptance_criteria":["different"],"description":"description","title":"title"}`)
	if _, err := handler.Handle(context.Background(), conflicting, provenance); !errors.Is(err, memory.ErrCommandIdentityConflict) {
		t.Fatalf("command identity reuse error = %v, want ErrCommandIdentityConflict", err)
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
		fake.NewIDSource(kernel.UUIDv7("00000000-0000-7000-8000-000000000004")),
	)
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := handler.Handle(context.Background(), command, kernel.Digest("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"))
	if err != nil {
		t.Fatalf("handle: %v", err)
	}
	if receipt.CommandID != stored.CommandID || store.lookups != 2 {
		t.Fatalf("reconciled receipt = %#v, lookups = %d", receipt, store.lookups)
	}
}

type winnerStore struct {
	lookups int
	receipt kernel.CommandReceipt
}

func (s *winnerStore) Load(context.Context, kernel.AggregateRef) (kernel.Snapshot, error) {
	return kernel.Snapshot{}, nil
}

func (s *winnerStore) LookupReceipt(context.Context, kernel.KernelCommand) (kernel.CommandReceipt, bool, error) {
	s.lookups++
	return s.receipt, s.lookups > 1, nil
}

func (s *winnerStore) Commit(context.Context, kernel.Snapshot, kernel.Decision) error {
	return kernel.ErrDecisionAlreadyCommitted
}

func testStoryCreateCommand() kernel.KernelCommand {
	return kernel.KernelCommand{
		ContractManifest: kernel.ContractIdentity,
		CommandID:        kernel.UUIDv7("00000000-0000-7000-8000-000000000001"),
		CommandType:      "tekroo.command.story.create",
		CommandVersion:   kernel.SchemaVersion,
		Target:           kernel.AggregateRef{Kind: kernel.AggregateStory, ID: kernel.UUIDv7("00000000-0000-7000-8000-000000000002")},
		Authority:        kernel.PrincipalRef{Kind: kernel.PrincipalHuman, ID: "principal"},
		ExpectedRevision: kernel.MustNotExist(),
		IdempotencyKey:   "create-story-1",
		CorrelationID:    kernel.UUIDv7("00000000-0000-7000-8000-000000000003"),
		Payload:          json.RawMessage(`{"acceptance_criteria":["works"],"description":"description","title":"title"}`),
	}
}

func loadCatalogue(t *testing.T) *contract.Catalogue {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate handler test")
	}
	repositoryRoot := filepath.Clean(filepath.Join(filepath.Dir(file), ".."))
	catalogue, err := contract.Load(os.DirFS(repositoryRoot), "CONTRACTS/tekroo.kernel.contracts/0.1.0")
	if err != nil {
		t.Fatal(err)
	}
	return catalogue
}
