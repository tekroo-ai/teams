package nativeagent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tekroo-ai/teams/agentruntime"
	"github.com/tekroo-ai/teams/agenttools"
	"github.com/tekroo-ai/teams/application"
	"github.com/tekroo-ai/teams/kernel"
	"github.com/tekroo-ai/teams/organization"
)

// This qualifies the actual native prompt/grammar/history boundary, not the
// domain policy's semantic acceptance of the fixture's proposed task graph.
func TestPreparedAmendmentRetainsHistoryAndUsesRawInputAndFormalGrammar(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	journal, brief, source, _ := contextFixture(t)
	brief.ActorFQN, source.ActorFQN = "teams::project-manager-1", "teams::project-manager-1"
	brief.RoleGrounding.ActorFQN, brief.RoleGrounding.RoleFQRN = brief.ActorFQN, "project-manager"
	brief.Purpose = kernel.PurposeHandoff
	// The transport protocol stays 1.0.0; the proposal's interface version is
	// independently constrained by its admitted 0.15.0 result schema.
	brief.ResultProtocol = &application.ExecutionResultProtocol{SchemaVersion: "1.0.0", Marker: application.OrganizationalResultMarker, Outcomes: []string{"STRUCTURED_HANDOFF"}}
	brief.Task.TaskID = kernel.UUIDv7("00000000-0000-7000-8000-000000001008")
	brief.Task.SourceDigest = kernel.Digest(strings.Repeat("a", 64))
	ref, err := BindContextFork(ctx, journal, brief, source)
	if err != nil {
		t.Fatal(err)
	}
	hash := func(raw []byte) kernel.Digest {
		sum := sha256.Sum256(raw)
		return kernel.Digest(hex.EncodeToString(sum[:]))
	}
	id := func(tail string) kernel.UUIDv7 { return kernel.UUIDv7("00000000-0000-7000-8000-00000000" + tail) }
	sha := kernel.Digest(strings.Repeat("a", 64))
	story := organization.PlannedStory{ID: id("1001"), Title: "Unchanged story", Description: "Existing product", AcceptanceCriteria: []string{"verified"}, Priority: organization.PriorityNormal}
	task := organization.PlannedTask{ID: id("1002"), StoryID: story.ID, Title: "Bounded successor", Description: "Cover known implementation gap", AcceptanceCriteria: []string{"verified"}, Owner: "teams::coder-1", ModelProfile: sha, DecisionRoute: kernel.RouteBoundedExecution, Purpose: kernel.PurposeImplementation, Complexity: 2, Risk: organization.RiskLow, AttemptLimit: 1, ReviewRoundLimit: 1, WriteScope: []string{"service/consumer.go"}}
	proposal := organization.PlanAmendmentProposal{SchemaVersion: organization.PlanAmendmentContractVersion, FeatureID: id("1003"), FeatureRevision: 6, PlanVersion: 1, PlanDigest: sha, SpecificationDigest: sha, BlockedTaskID: id("1004"), BlockedTaskRevision: 10, BlockedInvocationID: id("1005"), ConsultationID: id("1006"), AnswerDigest: sha, Reason: "Preserve accepted work and repair assignment coverage",
		SuccessorPlan: organization.FeaturePlan{Version: 2, PreparedBy: brief.ActorFQN, PreparedExecution: source.Execution, Architecture: "Unchanged architecture", SourceDesignDigest: sha, Stories: []organization.PlannedStory{story}, Tasks: []organization.PlannedTask{task}, CreatedAt: time.Now().UTC()},
		Replacements:  []organization.PlanTaskReplacement{{PriorTaskID: id("1004"), SuccessorTaskID: task.ID}}, Impacts: []organization.PlanAmendmentImpact{{Kind: "CALLER", Path: "service/consumer.go", OwnerTaskID: task.ID, Verification: "Compile consumer"}}}
	raw, err := json.Marshal(proposal)
	if err != nil || organization.ValidatePlanAmendmentProposalJSON(raw) != nil {
		t.Fatal("invalid syntactic proposal fixture", err)
	}
	schema := organization.PlanAmendmentProposalSchema()
	contract := ResultContract{TaskID: brief.Task.TaskID, TaskSourceDigest: brief.Task.SourceDigest, SchemaRef: "tekroo.feature.plan-amendment", SchemaSHA256: hash(schema), Schema: schema}
	inputRaw := json.RawMessage(`{"schema_version":"0.15.0","accepted_plan":{"version":1},"source_outputs":{"architect_answer":{"disposition":"NEEDS_SCOPE_CHANGE"}},"remaining_budget":{"model_invocations":3}}`)
	input := ResultInput{Evidence: kernel.EvidenceRef{EvidenceID: id("1007"), SHA256: hash(inputRaw)}, Value: inputRaw}
	brief.Evidence = append(brief.Evidence, input.Evidence)
	encoded, _ := json.Marshal(brief)
	requestDigest := hash(encoded)
	response, _ := json.Marshal(map[string]json.RawMessage{"submit_result": raw})
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		var wire struct {
			Messages []agentruntime.Message `json:"messages"`
			Format   struct {
				Type       string `json:"type"`
				JSONSchema struct {
					Schema json.RawMessage `json:"schema"`
				} `json:"json_schema"`
			} `json:"response_format"`
		}
		if err := json.NewDecoder(r.Body).Decode(&wire); err != nil {
			t.Error(err)
		}
		messages, _ := json.Marshal(wire.Messages)
		if !strings.Contains(string(messages), "Original design rationale with retained assumptions") {
			t.Error("lost original reasoning")
		}
		found := false
		for _, message := range wire.Messages {
			var initial struct {
				Input *struct {
					Value map[string]json.RawMessage `json:"value"`
				} `json:"result_input"`
			}
			if json.Unmarshal([]byte(message.Content), &initial) == nil && initial.Input != nil {
				found = true
				var outputs map[string]json.RawMessage
				if json.Unmarshal(initial.Input.Value["source_outputs"], &outputs) != nil || len(outputs["architect_answer"]) == 0 || outputs["architect_answer"][0] != '{' {
					t.Error("source outputs are not raw nested objects")
				}
			}
		}
		if !found || wire.Format.Type != "json_schema" || !schemaAccepts(t, wire.Format.JSONSchema.Schema, response) {
			t.Error("missing raw input or valid formal response grammar")
		}
		encodedString, _ := json.Marshal(string(raw))
		bad, _ := json.Marshal(map[string]json.RawMessage{"submit_result": encodedString})
		if schemaAccepts(t, wire.Format.JSONSchema.Schema, bad) {
			t.Error("grammar admits string-encoded result")
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "amendment-assessment-1", "choices": []any{map[string]any{"finish_reason": "stop", "message": map[string]any{"content": string(response)}}}})
	}))
	defer server.Close()
	_, _, profile := testBriefAndProfile()
	profile.RoleFQRN = "project-manager"
	profile.BaseURL, profile.ResponseMode = server.URL+"/v1", ResponseModeJSONSchemaActions
	bindings := &testBinding{root: t.TempDir()}
	config := Config{Bindings: bindings, Gateway: agenttools.Gateway{Bindings: bindings}, Journal: journal, Profile: profile, HTTP: server.Client(), ContextSource: ref, ContextInvocation: &source, ResultContract: &contract, ResultInput: &input}
	for pass := 0; pass < 2; pass++ {
		session, err := PrepareReadOnly(ctx, brief, requestDigest, config)
		if err != nil {
			t.Fatal(err)
		}
		result, err := session.Run(ctx)
		if err != nil || !strings.HasPrefix(result, application.OrganizationalResultMarker) || organization.ValidatePlanAmendmentProposalJSON([]byte(strings.TrimSpace(strings.TrimPrefix(result, application.OrganizationalResultMarker)))) != nil {
			t.Fatalf("result=%q error=%v", result, err)
		}
	}
	if requests != 1 {
		t.Fatal("replay repeated the model assessment", requests)
	}
}
