package operationalruntime

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/tekroo-ai/teams/adapters/nativeagent"
	"github.com/tekroo-ai/teams/application"
	"github.com/tekroo-ai/teams/kernel"
)

var errUnboundNativeResultSchema = errors.New("native result schema is not bound to the admitted workflow task")

// NativeResultSchema is loaded from an operator-configured, content-addressed
// schema document. A schema reference alone is never a usable tool contract.
type NativeResultSchema struct {
	SHA256 kernel.Digest
	JSON   json.RawMessage
}

// ResolveNativeWorkflowResultContract checks the exact task-created source
// against one already-validated, immutable workflow definition before mapping
// its output-schema reference to a content-addressed JSON Schema. This is a
// server-side configurator helper, not a model-visible schema selector.
func ResolveNativeWorkflowResultContract(brief application.ExecutionBrief, definition kernel.WorkflowDefinition, schemas map[string]NativeResultSchema) (*nativeagent.ResultContract, error) {
	if definition.Validate() != nil || !brief.Task.TaskID.Valid() || !brief.Task.SourceDigest.Valid() ||
		brief.ResultProtocol == nil || brief.ResultProtocol.Marker != application.OrganizationalResultMarker || len(schemas) == 0 {
		return nil, errUnboundNativeResultSchema
	}
	var matched *kernel.WorkflowStageDefinition
	for index := range definition.Stages {
		stage := &definition.Stages[index]
		if kernel.WorkPurpose(stage.Purpose) != brief.Purpose || !slices.Contains(stage.PreferredFQRNs, brief.RoleGrounding.RoleFQRN) ||
			brief.Task.Title != stage.StageID+": "+definition.Name {
			continue
		}
		prefix := fmt.Sprintf("Perform workflow stage %q using the immutable role bundle. Input schema: %s. Output schema: %s. Completion contract: %s", stage.StageID, stage.InputSchema, stage.OutputSchema, stage.CompletionCondition)
		if !strings.HasPrefix(brief.Task.Description, prefix) || len(brief.Task.Description) > len(prefix) && brief.Task.Description[len(prefix)] != ' ' && brief.Task.Description[len(prefix)] != '\n' || matched != nil {
			return nil, errUnboundNativeResultSchema
		}
		matched = stage
	}
	if matched == nil {
		return nil, errUnboundNativeResultSchema
	}
	entry, found := schemas[matched.OutputSchema]
	if !found || !entry.SHA256.Valid() || len(entry.JSON) == 0 || len(entry.JSON) > 1<<20 || !json.Valid(entry.JSON) || !bytes.HasPrefix(bytes.TrimSpace(entry.JSON), []byte("{")) {
		return nil, errUnboundNativeResultSchema
	}
	sum := sha256.Sum256(entry.JSON)
	if entry.SHA256 != kernel.Digest(hex.EncodeToString(sum[:])) {
		return nil, errUnboundNativeResultSchema
	}
	return &nativeagent.ResultContract{TaskID: brief.Task.TaskID, TaskSourceDigest: brief.Task.SourceDigest,
		SchemaRef: matched.OutputSchema, SchemaSHA256: entry.SHA256, Schema: append(json.RawMessage(nil), entry.JSON...)}, nil
}
