package organization_test

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/tekroo-ai/teams/organization"
)

func TestPhase10ActorNameFixtureIsDirectlySubmittable(t *testing.T) {
	raw, err := os.ReadFile("../testdata/phase10/actor-name-feature-request.json")
	if err != nil {
		t.Fatalf("read Phase 10 actor-name fixture: %v", err)
	}
	var input organization.FeatureRequestInput
	if err := json.Unmarshal(raw, &input); err != nil {
		t.Fatalf("decode Phase 10 actor-name fixture: %v", err)
	}
	if err := input.Validate(); err != nil {
		t.Fatalf("validate Phase 10 actor-name fixture: %v", err)
	}
	if input.IdempotencyKey != "phase9-run060-nohands-repeat-v1" || len(input.AcceptanceCriteria) != 23 {
		t.Fatalf("fixture identity or acceptance criteria changed: key=%q criteria=%d", input.IdempotencyKey, len(input.AcceptanceCriteria))
	}
}
