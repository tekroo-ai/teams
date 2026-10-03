package operationalruntime

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tekroo-ai/teams/application"
	"github.com/tekroo-ai/teams/kernel"
	"github.com/tekroo-ai/teams/organization"
)

func TestResolveNativeWorkflowResultContractBindsExactStageAndSchema(t *testing.T) {
	path, err := filepath.Abs(filepath.Join("..", "..", "config", "workflows", "software-development.v2.1.json"))
	if err != nil {
		t.Fatal(err)
	}
	definition, err := organization.LoadWorkflowDefinition(path, kernel.Digest("55c9018d4726bf54943fd3afb41ed07b1d2395b9d01e89626c693a30ab2dd7b3"))
	if err != nil {
		t.Fatal(err)
	}
	stage, found := configuredWorkflowStage(definition, "specify")
	if !found {
		t.Fatal("missing stage")
	}
	prefix := fmt.Sprintf("Perform workflow stage %q using the immutable role bundle. Input schema: %s. Output schema: %s. Completion contract: %s", stage.StageID, stage.InputSchema, stage.OutputSchema, stage.CompletionCondition)
	brief := application.ExecutionBrief{
		Task: application.TaskExecutionSpecification{TaskID: kernel.UUIDv7("00000000-0000-7000-8000-000000000955"), SourceDigest: kernel.Digest(strings.Repeat("a", 64)),
			Title: "specify: software-development", Description: prefix + "\n\nAUTHORITATIVE_FEATURE_STATE_JSON:\n{}"},
		Purpose: kernel.PurposeHandoff, RoleGrounding: application.RoleExecutionGrounding{RoleFQRN: "project-manager"},
		ResultProtocol: &application.ExecutionResultProtocol{SchemaVersion: "1.0.0", Marker: application.OrganizationalResultMarker},
	}
	schema := json.RawMessage(`{"type":"object","additionalProperties":false,"required":["result_type"],"properties":{"result_type":{"const":"FEATURE_SPECIFICATION"}}}`)
	sum := sha256.Sum256(schema)
	entry := NativeResultSchema{SHA256: kernel.Digest(hex.EncodeToString(sum[:])), JSON: schema}
	contract, err := ResolveNativeWorkflowResultContract(brief, definition, map[string]NativeResultSchema{stage.OutputSchema: entry})
	if err != nil || contract == nil || contract.TaskID != brief.Task.TaskID || contract.TaskSourceDigest != brief.Task.SourceDigest || contract.SchemaRef != stage.OutputSchema || string(contract.Schema) != string(schema) {
		t.Fatalf("contract=%+v err=%v", contract, err)
	}
	changed := brief
	changed.Task.Description = strings.Replace(brief.Task.Description, stage.OutputSchema, "different/schema", 1)
	if _, err := ResolveNativeWorkflowResultContract(changed, definition, map[string]NativeResultSchema{stage.OutputSchema: entry}); !errors.Is(err, errUnboundNativeResultSchema) {
		t.Fatalf("accepted source description drift: %v", err)
	}
	changed = brief
	changed.RoleGrounding.RoleFQRN = "architect"
	if _, err := ResolveNativeWorkflowResultContract(changed, definition, map[string]NativeResultSchema{stage.OutputSchema: entry}); !errors.Is(err, errUnboundNativeResultSchema) {
		t.Fatalf("accepted role drift: %v", err)
	}
	entry.SHA256 = kernel.Digest(strings.Repeat("b", 64))
	if _, err := ResolveNativeWorkflowResultContract(brief, definition, map[string]NativeResultSchema{stage.OutputSchema: entry}); !errors.Is(err, errUnboundNativeResultSchema) {
		t.Fatalf("accepted schema digest drift: %v", err)
	}
}
