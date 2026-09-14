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

func TestAcceptedCatalogueValidatesHandlerBoundAuthorization(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate catalogue test")
	}
	repositoryRoot := filepath.Clean(filepath.Join(filepath.Dir(file), ".."))
	catalogue, err := contract.LoadExpected(
		os.DirFS(repositoryRoot),
		"CONTRACTS/tekroo.kernel.contracts/0.12.0",
		"tekroo.kernel.contracts/0.12.0",
		kernel.CatalogueRevision,
	)
	if err != nil {
		t.Fatal(err)
	}

	payload := json.RawMessage(`{
		"actor_fqn":"teams::coder-1",
		"admission_policy_digest":"4444444444444444444444444444444444444444444444444444444444444444",
		"admission_policy_revision":1,
		"allowed_terminal_outcomes":["SUCCEEDED","FAILED","TIMED_OUT","CANCELLED","START_FAILED"],
		"attempt_family":"implementation",
		"attempt_ordinal":1,
		"budget_account_id":"00000000-0000-7000-8000-000000000901",
		"condition_digest":"dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd",
		"deadline_at":"2026-09-30T00:00:00Z",
		"effect_policy_digest":"1111111111111111111111111111111111111111111111111111111111111111",
		"execution_id":"00000000-0000-7000-8000-000000000908",
		"expected_budget_revision":2,
		"expected_task_revision":4,
		"fencing_epoch":1,
		"handler_dispatch":{
			"message_id":"00000000-0000-7000-8000-000000000920",
			"message_type":"tekroo.message.task.assigned",
			"message_purpose":"HANDOFF",
			"message_body_digest":"5555555555555555555555555555555555555555555555555555555555555555",
			"subscription_purpose":"IMPLEMENTATION",
			"role_bundle_digest":"6666666666666666666666666666666666666666666666666666666666666666",
			"charter_digest":"7777777777777777777777777777777777777777777777777777777777777777",
			"handler_digest":"8888888888888888888888888888888888888888888888888888888888888888",
			"input_schema_digest":"9999999999999999999999999999999999999999999999999999999999999999",
			"result_schema_digest":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			"allowed_results":["completed","failed","blocked","needs_decision"],
			"allowed_message_proposals":[]
		},
		"idempotency_key":"invoke-task-903-1",
		"invocation_id":"00000000-0000-7000-8000-000000000905",
		"lifecycle_epoch":1,
		"model_profile_digest":"2222222222222222222222222222222222222222222222222222222222222222",
		"output_predicate_digest":"eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee",
		"parent_event_id":"00000000-0000-7000-8000-000000000909",
		"purpose":"IMPLEMENTATION",
		"qualified_assignment_id":"00000000-0000-7000-8000-000000000907",
		"retry_of_invocation_id":null,
		"retry_ordinal":0,
		"runtime_identity_digest":"3333333333333333333333333333333333333333333333333333333333333333",
		"scope_revision":1,
		"task_id":"00000000-0000-7000-8000-000000000903",
		"tool_policy_digest":"ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff",
		"work_profile":{"lifecycle_epoch":1,"profile_digest":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","profile_id":"00000000-0000-7000-8000-000000000906","profile_revision":1,"scope_revision":1},
		"workspace_id":"workspace-task-903"
	}`)
	if _, err := catalogue.ValidateFixtureCommand("tekroo.command.work-invocation.authorize", payload); err != nil {
		t.Fatalf("handler-bound successor authorization rejected: %v", err)
	}
	var malformed map[string]any
	if err := json.Unmarshal(payload, &malformed); err != nil {
		t.Fatal(err)
	}
	delete(malformed["handler_dispatch"].(map[string]any), "handler_digest")
	malformedPayload, err := json.Marshal(malformed)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := catalogue.ValidateFixtureCommand("tekroo.command.work-invocation.authorize", malformedPayload); !errors.Is(err, contract.ErrInvalidCommand) {
		t.Fatalf("malformed handler binding error = %v, want ErrInvalidCommand", err)
	}
	if kernel.ContractIdentity != "tekroo.kernel.contracts/0.12.0" {
		t.Fatalf("accepted contract identity = %q", kernel.ContractIdentity)
	}
}

func loadCatalogue(t *testing.T) *contract.Catalogue {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate catalogue test")
	}
	repositoryRoot := filepath.Clean(filepath.Join(filepath.Dir(file), ".."))
	catalogue, err := contract.Load(os.DirFS(repositoryRoot), "CONTRACTS/tekroo.kernel.contracts/0.12.0")
	if err != nil {
		t.Fatal(err)
	}
	return catalogue
}
