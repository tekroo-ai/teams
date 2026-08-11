package contract_test

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/tekroo-ai/teams/contract"
	"github.com/tekroo-ai/teams/kernel"
)

func TestPayloadValidationEnforcesUniqueItems(t *testing.T) {
	catalogue := loadCatalogue(t)
	payload := json.RawMessage(`{"acceptance_criteria":["same","same"],"description":"description","title":"title"}`)
	_, err := catalogue.ValidateFixtureCommand("tekroo.command.story.create", payload)
	if !errors.Is(err, contract.ErrInvalidCommand) {
		t.Fatalf("error = %v, want ErrInvalidCommand", err)
	}
}

func TestPayloadValidationCountsUnicodeCodePoints(t *testing.T) {
	catalogue := loadCatalogue(t)
	payload := json.RawMessage(`{"acceptance_criteria":["works"],"description":"description","title":"界"}`)
	if _, err := catalogue.ValidateFixtureCommand("tekroo.command.story.create", payload); err != nil {
		t.Fatalf("unicode payload rejected: %v", err)
	}
}

func TestResolveCommandRejectsWrongTargetKind(t *testing.T) {
	catalogue := loadCatalogue(t)
	payload := json.RawMessage(`{"acceptance_criteria":["works"],"description":"description","title":"title"}`)
	_, err := catalogue.ResolveCommand("tekroo.command.story.create", kernel.SchemaVersion, kernel.AggregateTask, payload)
	if !errors.Is(err, contract.ErrInvalidCommand) {
		t.Fatalf("error = %v, want ErrInvalidCommand", err)
	}
}

func loadCatalogue(t *testing.T) *contract.Catalogue {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate catalogue test")
	}
	repositoryRoot := filepath.Clean(filepath.Join(filepath.Dir(file), ".."))
	catalogue, err := contract.Load(os.DirFS(repositoryRoot), "CONTRACTS/tekroo.kernel.contracts/0.2.0")
	if err != nil {
		t.Fatal(err)
	}
	return catalogue
}
