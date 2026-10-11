package operationalruntime

import (
	"encoding/json"
	"testing"

	"github.com/tekroo-ai/teams/application"
	"github.com/tekroo-ai/teams/internal/rolefixtures"
)

func TestCurrentRuntimeFixtureLoadsExactSignedRoles(t *testing.T) {
	team := rolefixtures.Current(t)
	if len(team.Roles) == 0 || len(team.Roles) != len(team.Manifest.Roles) || team.Digest != rolefixtures.CurrentManifestDigest {
		t.Fatal("incomplete role fixture")
	}
	for _, role := range team.Roles {
		if role.Bundle.Validate() != nil || role.Package == nil {
			t.Fatalf("unqualified fixture role %s version=%s", role.Binding.Role, role.Bundle.Version)
		}
	}
}

func TestHistoricalSpecificationSchemaRemainsDistinctFromCurrent(t *testing.T) {
	old := rolefixtures.Handler(t, loadStarterSuccessorTeam(t), "project-manager", "tekroo.message.feature.refined")
	current := rolefixtures.Handler(t, rolefixtures.Current(t), "project-manager", "tekroo.message.feature.refined")
	work := map[string]any{"schema_version": "1.0.0", "result_type": "FEATURE_SPECIFICATION", "stories": []map[string]any{{"title": "Bounded work", "description": "Preserve the admitted behavior", "acceptance_criteria": []string{"the requested behavior works"}, "priority": "HIGH"}}}
	output := func() []byte {
		raw, err := json.Marshal(map[string]any{"schema_version": "1.0.0", "outcome": "completed", "summary": "specified", "evidence": []string{}, "message_proposals": []any{}, "work_product": work})
		if err != nil {
			t.Fatal(err)
		}
		return append([]byte(application.OrganizationalResultMarker+"\n"), raw...)
	}
	if _, err := application.ValidateRoleHandlerResult(old, output()); err == nil {
		t.Fatal("historical required field was weakened")
	}
	if _, err := application.ValidateRoleHandlerResult(current, output()); err != nil {
		t.Fatal(err)
	}
	work["design_constraints"] = []string{"preserve current interfaces"}
	if _, err := application.ValidateRoleHandlerResult(old, output()); err != nil {
		t.Fatalf("historical output no longer validates: %v", err)
	}
	if _, err := application.ValidateRoleHandlerResult(current, output()); err == nil {
		t.Fatal("current schema accepted forbidden historical field")
	}
	delete(work, "design_constraints")
	work["unexpected"] = true
	if _, err := application.ValidateRoleHandlerResult(current, output()); err == nil {
		t.Fatal("current additionalProperties boundary was weakened")
	}
}
