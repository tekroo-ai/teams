package nativeagent

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/tekroo-ai/teams/agentruntime"
	"github.com/tekroo-ai/teams/application"
	"github.com/tekroo-ai/teams/kernel"
)

// Exercise every signed model handler, not only a generic envelope. For a
// deployment audit TEKROO_ROLE_AUDIT_ROOT selects its pinned bundle directory;
// repository defaults remain the portable regression fixtures.
func TestSignedRoleHandlersPreserveEveryNonCompletionOutcome(t *testing.T) {
	for _, dir := range []string{"architect", "coder", "operator", "product-owner", "project-manager-2.2.2", "security", "senior-coder", "tester"} {
		root := filepath.Join("../../config/starter-team/roles-v4", dir)
		if deployed := os.Getenv("TEKROO_ROLE_AUDIT_ROOT"); deployed != "" {
			roleDir := dir
			if dir == "project-manager-2.2.2" {
				roleDir = "project-manager"
			}
			root = filepath.Join(deployed, roleDir)
		}
		raw, err := os.ReadFile(filepath.Join(root, "role.json"))
		if err != nil {
			t.Fatal(err)
		}
		var role struct {
			Version         string `json:"version"`
			HandlerBindings map[string]struct {
				AllowedResults []string `json:"allowed_results"`
			} `json:"handler_bindings"`
		}
		// Resource keys use snake_case in the authenticated package.
		var binding struct {
			HandlerBindings map[string]json.RawMessage `json:"handler_bindings"`
		}
		if json.Unmarshal(raw, &role) != nil || json.Unmarshal(raw, &binding) != nil {
			t.Fatal("invalid role")
		}
		t.Logf("auditing %s version %s from %s", dir, role.Version, root)
		for message, h := range role.HandlerBindings {
			var resource struct {
				Result struct {
					Path   string
					SHA256 string
				} `json:"result_schema"`
			}
			if json.Unmarshal(binding.HandlerBindings[message], &resource) != nil {
				t.Fatal("invalid handler")
			}
			signed, err := os.ReadFile(filepath.Join(root, resource.Result.Path))
			if err != nil {
				t.Fatal(err)
			}
			if fmt.Sprintf("%x", sha256.Sum256(signed)) != resource.Result.SHA256 {
				t.Fatal("signed schema digest mismatch")
			}
			purpose := kernel.PurposeHandoff
			if dir == "coder" {
				purpose = kernel.PurposeImplementation
			}
			if dir == "security" || dir == "tester" || message == "tekroo.message.release.ready" {
				purpose = kernel.PurposeValidation
			}
			handler := application.MessageHandlerGrounding{ResultSchema: signed, AllowedResults: h.AllowedResults}
			model, err := modelFacingHandlerSchema(handler, purpose)
			if err != nil {
				t.Fatal(err)
			}
			var plan *PlanFinalizationBinding
			if message == "tekroo.message.feature.design-proposed" {
				plan = &PlanFinalizationBinding{SourceOutput: []byte(application.OrganizationalResultMarker + `{"work_product":{"result_type":"FEATURE_PLAN","tasks":[{"purpose":"IMPLEMENTATION","depends_on":[]}]}}`)}
				model, err = modelFacingPlanSchema(model, plan)
				if err != nil {
					t.Fatal(err)
				}
			}
			if _, err := agentruntime.NewStructuredTurnContract([]agentruntime.ToolDefinition{{Name: "submit_result", Parameters: model}}, "submit_result"); err != nil {
				t.Fatalf("%s/%s strict generation contract: %v", dir, message, err)
			}
			for _, pair := range validationOutcomePairs[1:] { // failed, blocked, needs_decision
				t.Run(dir+"/"+message+"/"+pair.envelope, func(t *testing.T) {
					work := json.RawMessage(`{}`)
					if message == "tekroo.message.feature.submitted" {
						work = json.RawMessage(`{"schema_version":"1.0.0","result_type":"FEATURE_REFINEMENT","acceptance_criteria_disposition":"PRESERVE_SUBMITTED","clarification_questions":["Which addressing interface is required?"],"priority":"NORMAL"}`)
					}
					if message == "tekroo.message.feature.refined" && role.Version != "2.2.2" {
						work = json.RawMessage(`{"schema_version":"1.0.0","result_type":"FEATURE_SPECIFICATION","stories":[{"title":"Already established product requirement","description":"Known portion of the admitted request; the missing interface decision is in summary.","acceptance_criteria":["Preserve the submitted addressing requirement"],"priority":"NORMAL"}]}`)
					}
					if plan != nil {
						work = json.RawMessage(`{"task_dependencies":[],"handoffs":[]}`)
					}
					if validationPurpose(purpose) {
						work = json.RawMessage(`{"schema_version":"1.0.0","outcome":"` + pair.verdict + `","reasons":["Missing admissible interface evidence"]}`)
					}
					raw, _ := json.Marshal(map[string]any{"outcome": pair.envelope, "summary": "Missing interface owner requires an admitted decision", "evidence": []string{}, "message_proposals": []string{}, "work_product": work})
					if !schemaAccepts(t, model, raw) {
						t.Fatalf("model-facing contract rejected disposition: %s", raw)
					}
					assembled := bindHandlerResultVersion(raw, handler)
					if plan != nil {
						assembled, err = bindHandlerPlanResult(assembled, plan)
						if err != nil {
							t.Fatal(err)
						}
					}
					if validationPurpose(purpose) {
						assembled, err = bindHandlerValidationResult(assembled, nil)
						if err != nil {
							t.Fatal(err)
						}
					}
					assembled = bindHandlerFixedConstants(assembled, signed)
					result, err := application.ValidateRoleHandlerResult(handler, []byte(application.OrganizationalResultMarker+string(assembled)))
					if err != nil || result.Outcome != pair.envelope {
						t.Fatalf("signed handler lost/rejected disposition: %v %s", err, assembled)
					}
				})
			}
		}
	}
}
