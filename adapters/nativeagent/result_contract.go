package nativeagent

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/tekroo-ai/teams/agentruntime"
	"github.com/tekroo-ai/teams/application"
	"github.com/tekroo-ai/teams/kernel"
)

// ResultContract binds a server-resolved workflow output schema to the exact
// task-created source, not to a role name or model-supplied schema reference.
// The resolver must verify the schema reference against the admitted workflow
// stage before constructing this value.
type ResultContract struct {
	TaskID           kernel.UUIDv7   `json:"task_id"`
	TaskSourceDigest kernel.Digest   `json:"task_source_digest"`
	SchemaRef        string          `json:"schema_ref"`
	SchemaSHA256     kernel.Digest   `json:"schema_sha256"`
	Schema           json.RawMessage `json:"-"`
}

func (contract ResultContract) validFor(brief application.ExecutionBrief) bool {
	if contract.TaskID != brief.Task.TaskID || !contract.TaskID.Valid() || contract.TaskSourceDigest != brief.Task.SourceDigest || !contract.TaskSourceDigest.Valid() ||
		contract.SchemaRef == "" || len(contract.SchemaRef) > 256 || !contract.SchemaSHA256.Valid() || len(contract.Schema) == 0 || len(contract.Schema) > 1<<20 {
		return false
	}
	sum := sha256.Sum256(contract.Schema)
	return contract.SchemaSHA256 == kernel.Digest(hex.EncodeToString(sum[:]))
}

type localSchemaLoader struct{}

func (localSchemaLoader) Load(url string) (any, error) {
	return nil, fmt.Errorf("external schema reference is not bound: %s", url)
}

func nativeOrganizationalResult(brief application.ExecutionBrief, contract ResultContract) (agentruntime.ToolDefinition, func(json.RawMessage) (string, error), error) {
	if brief.ResultProtocol == nil || brief.ResultProtocol.Marker != application.OrganizationalResultMarker || brief.ResultProtocol.SchemaVersion != "1.0.0" ||
		(brief.Purpose != kernel.PurposeHandoff && brief.Purpose != kernel.PurposeReplan) || !contract.validFor(brief) {
		return agentruntime.ToolDefinition{}, nil, ErrInvalidBinding
	}
	document, err := jsonschema.UnmarshalJSON(bytes.NewReader(contract.Schema))
	if err != nil {
		return agentruntime.ToolDefinition{}, nil, errors.Join(ErrInvalidBinding, err)
	}
	compiler := jsonschema.NewCompiler()
	compiler.DefaultDraft(jsonschema.Draft2020)
	compiler.AssertFormat()
	compiler.UseLoader(localSchemaLoader{})
	const schemaURL = "urn:tekroo:native-result"
	if err := compiler.AddResource(schemaURL, document); err != nil {
		return agentruntime.ToolDefinition{}, nil, errors.Join(ErrInvalidBinding, err)
	}
	compiled, err := compiler.Compile(schemaURL)
	if err != nil {
		return agentruntime.ToolDefinition{}, nil, errors.Join(ErrInvalidBinding, err)
	}
	definition := agentruntime.ToolDefinition{Name: "submit_result", Description: "Submit the structured result required by the admitted workflow stage.", Parameters: append(json.RawMessage(nil), contract.Schema...)}
	return definition, func(arguments json.RawMessage) (string, error) {
		instance, err := jsonschema.UnmarshalJSON(bytes.NewReader(arguments))
		if err != nil {
			return "", errors.Join(ErrInvalidBinding, err)
		}
		if _, object := instance.(map[string]any); !object {
			return "", ErrInvalidBinding
		}
		if err := compiled.Validate(instance); err != nil {
			return "", errors.Join(ErrInvalidBinding, err)
		}
		return application.OrganizationalResultMarker + "\n" + string(arguments), nil
	}, nil
}
