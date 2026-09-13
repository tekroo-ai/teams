package organization

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/tekroo-ai/teams/kernel"
)

func organizationWorkflowDefinition(t *testing.T, name string) kernel.WorkflowDefinition {
	t.Helper()
	definition := kernel.WorkflowDefinition{
		SchemaVersion:   kernel.WorkflowDefinitionSchemaVersion,
		Name:            name,
		Version:         "1.0.0",
		TriggerTypes:    []string{"request.created"},
		Stages:          []kernel.WorkflowStageDefinition{{StageID: "perform", DependsOn: []string{}, InputSchema: "request/v1", OutputSchema: "result/v1", RequiredCapabilities: []string{"perform"}, PreferredFQRNs: []kernel.RoleFQRN{}, Purpose: kernel.WorkflowPurposeImplementation, Risk: kernel.WorkflowRiskLow, ConcurrencyGroup: "default", MaximumParallelism: 1, AttemptLimit: 1, AllowedOutgoingPurposes: []string{}, TargetSelection: kernel.WorkflowTargetCapability, ValidationPolicy: kernel.WorkflowValidationDeterministic}},
		RootBudgets:     kernel.WorkflowBudgetLimits{MaximumModelInvocations: 1, MaximumHops: 2, MaximumAttempts: 1},
		ProjectionRules: []string{},
	}
	digest, err := definition.CalculatedDigest()
	if err != nil {
		t.Fatal(err)
	}
	definition.ContentDigest = digest
	return definition
}

func TestLoadWorkflowDefinitionAndVersionLookup(t *testing.T) {
	definition := organizationWorkflowDefinition(t, "publication")
	raw, err := json.Marshal(definition)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "publication.json")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadWorkflowDefinition(path, definition.ContentDigest)
	if err != nil || loaded.ContentDigest != definition.ContentDigest {
		t.Fatalf("load failed: %v", err)
	}
	library := NewWorkflowLibrary()
	if err := library.Add(loaded); err != nil {
		t.Fatal(err)
	}
	resolved, err := library.Lookup("publication", "1.0.0")
	if err != nil || resolved.ContentDigest != definition.ContentDigest {
		t.Fatalf("lookup failed: %v", err)
	}
	if _, err := library.Lookup("publication", "2.0.0"); !errors.Is(err, ErrWorkflowDefinitionNotFound) {
		t.Fatalf("missing version returned %v", err)
	}
	resolved.Stages[0].RequiredCapabilities[0] = "mutated"
	again, err := library.Lookup("publication", "1.0.0")
	if err != nil || again.Stages[0].RequiredCapabilities[0] != "perform" {
		t.Fatal("library exposed mutable definition state")
	}
}

func TestWorkflowLoaderRejectsWrongDigestUnknownFieldsAndConflicts(t *testing.T) {
	definition := organizationWorkflowDefinition(t, "publication")
	raw, err := json.Marshal(definition)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "publication.json")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	wrong := kernel.Digest("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	if _, err := LoadWorkflowDefinition(path, wrong); err == nil {
		t.Fatal("wrong digest accepted")
	}
	invalidPath := filepath.Join(t.TempDir(), "invalid.json")
	invalidRaw := append(raw[:len(raw)-1], []byte(`,"unknown":true}`)...)
	if err := os.WriteFile(invalidPath, invalidRaw, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadWorkflowDefinition(invalidPath, definition.ContentDigest); err == nil {
		t.Fatal("unknown field accepted")
	}
	library := NewWorkflowLibrary()
	if err := library.Add(definition); err != nil {
		t.Fatal(err)
	}
	conflict := organizationWorkflowDefinition(t, "publication")
	conflict.TriggerTypes = []string{"different.request"}
	digest, err := conflict.CalculatedDigest()
	if err != nil {
		t.Fatal(err)
	}
	conflict.ContentDigest = digest
	if err := library.Add(conflict); err == nil {
		t.Fatal("same name and version with different digest accepted")
	}
}

func TestWorkProposalFromMessageDistinguishesWorkFromInformation(t *testing.T) {
	message := testMessage(400)
	workflowID := testUUID(450)
	message.Work.WorkflowInstanceID = &workflowID
	message.Work.WorkflowStageID = "produce"
	execution := testExecution(451, 2)
	proposal, err := WorkProposalFromMessage(message, testUUID(452), execution, []kernel.UUIDv7{testUUID(453)}, []kernel.UUIDv7{}, message.CreatedAt)
	if err != nil || proposal.WorkflowInstanceID != workflowID || proposal.NodeID != message.Work.DAGNodeID || proposal.ActorFQN != message.Recipient || proposal.Execution != execution {
		t.Fatalf("proposal mismatch: %+v err=%v", proposal, err)
	}
	message.Purpose = PurposeEvidence
	if MessageCreatesWorkflowWork(message) {
		t.Fatal("evidence message classified as executable work")
	}
	if _, err := WorkProposalFromMessage(message, testUUID(454), execution, []kernel.UUIDv7{testUUID(455)}, nil, message.CreatedAt); err == nil {
		t.Fatal("informational message produced work")
	}
}

func TestShippedSoftwareWorkflowLoadsByFrozenDigest(t *testing.T) {
	workingDirectory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(filepath.Dir(workingDirectory), "config", "workflows", "software-development.v1.json")
	digest := kernel.Digest("0fad72aa1ffd2a5a671a610165d8686fa68331f8206627874bf6ce3628b4bfa6")
	definition, err := LoadWorkflowDefinition(path, digest)
	if err != nil {
		t.Fatal(err)
	}
	if definition.Name != "software-development" || len(definition.Stages) != 3 || definition.Stages[2].StageID != "design" || definition.Stages[2].ComplexityMinimum != 2 {
		t.Fatalf("unexpected shipped workflow: %+v", definition)
	}
}
