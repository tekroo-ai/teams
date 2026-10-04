package nativeagent

import (
	"encoding/json"

	"github.com/tekroo-ai/teams/application"
	"github.com/tekroo-ai/teams/kernel"
)

const nativeResultGuidance = "Use the submit_result tool's parameter schema for the fields you author. Teams supplies the fixed outer schema_version in the stored signed envelope; omit that outer field from the tool call. The handler result_schema describes the assembled result and may include Teams-supplied fields."

func nativeHandlerPrompt(brief application.ExecutionBrief, digest kernel.Digest, encoded []byte, planFinalization bool) (string, error) {
	guidance := nativeResultGuidance
	if planFinalization {
		guidance += " For plan finalization, work_product contains only task_dependencies and handoffs. Teams preserves the accepted architecture, decisions, assumptions, tasks, and source digest and assembles FEATURE_EXECUTION_PLAN."
	} else if validationPurpose(brief.Purpose) {
		guidance += " For validation or acceptance, work_product contains only the verdict fields allowed by the tool schema; Teams binds candidate identity."
	}
	adapted, err := json.Marshal(struct {
		SourceRequestDigest  kernel.Digest   `json:"source_request_digest"`
		ExecutionBrief       json.RawMessage `json:"execution_brief"`
		NativeResultGuidance string          `json:"native_result_guidance"`
	}{digest, encoded, guidance})
	return string(adapted), err
}

// The journal binds either the legacy exact brief or one of the exact native
// handler prompts that this package can construct from that brief. No model-
// supplied prompt body is trusted as an invocation identity.
func nativePromptMatchesBrief(brief application.ExecutionBrief, digest kernel.Digest, encoded []byte, prompt string) bool {
	if prompt == string(encoded) {
		return true
	}
	if brief.MessageHandler == nil {
		return false
	}
	regular, err := nativeHandlerPrompt(brief, digest, encoded, false)
	if err == nil && prompt == regular {
		return true
	}
	if brief.Purpose == kernel.PurposeHandoff {
		plan, err := nativeHandlerPrompt(brief, digest, encoded, true)
		return err == nil && prompt == plan
	}
	return false
}
