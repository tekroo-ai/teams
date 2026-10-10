package organization_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"

	"github.com/tekroo-ai/teams/kernel"
	"github.com/tekroo-ai/teams/organization"
)

// Opt-in replay of the private, decoded raw journal. No database is opened and
// all hypothetical corrections below affect local values only, never r60.
func TestR60CapturedAmendmentBudgetAndAttemptReplay(t *testing.T) {
	path := os.Getenv("TEKROO_R60_REPLAY_RECEIPT")
	if path == "" {
		t.Skip("private r60 raw receipt not supplied")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	if hex.EncodeToString(sum[:]) != "7e4157c1bd893eff86ece02bdbcfb97afb955b39feb18de91cc367c67b088d47" {
		t.Fatal("raw receipt changed")
	}
	var receipt struct {
		Features []struct {
			Data organization.FeatureRequest `json:"data"`
		} `json:"feature_requests"`
		Invocations []struct {
			Data struct {
				Invocation kernel.WorkInvocation `json:"invocation"`
			} `json:"data"`
		} `json:"work_invocations"`
		Entries []struct {
			ID      kernel.UUIDv7   `json:"invocation_id"`
			Kind    string          `json:"kind"`
			Payload json.RawMessage `json:"payload"`
		} `json:"agent_turn_entries"`
	}
	if err := json.Unmarshal(raw, &receipt); err != nil {
		t.Fatal(err)
	}
	f := receipt.Features[0].Data
	p := *f.PlanAmendment.Proposal
	var prompt struct {
		Input struct {
			Value struct {
				Reservation organization.FeaturePlanAmendment       `json:"reservation"`
				Tasks       map[kernel.UUIDv7]kernel.AggregateState `json:"task_states"`
				Budget      kernel.WorkBudgetAccount                `json:"remaining_budget"`
				Roots       []string                                `json:"allowed_roots"`
			} `json:"value"`
		} `json:"result_input"`
	}
	for _, entry := range receipt.Entries {
		if entry.ID == f.PlanAmendment.AssessmentInvocationID && entry.Kind == "started" {
			var payload struct {
				Prompt string `json:"prompt"`
			}
			if json.Unmarshal(entry.Payload, &payload) != nil || json.Unmarshal([]byte(payload.Prompt), &prompt) != nil {
				t.Fatal("started prompt decode")
			}
		}
	}
	a := prompt.Input.Value.Reservation
	f.PlanAmendment, f.Plan, f.Revision, f.UpdatedAt = &a, &a.PriorPlan, a.FeatureRevision+1, a.RequestedAt
	w := organization.PlanAmendmentWitness{Tasks: prompt.Input.Value.Tasks, Budget: prompt.Input.Value.Budget, Now: *receipt.Features[0].Data.PlanAmendment.DecidedAt, ScopeChangeRequired: true, Invocations: map[kernel.AggregateRef]kernel.WorkInvocation{}, Evidence: map[kernel.UUIDv7]kernel.EvidenceMetadata{}}
	for _, record := range receipt.Invocations {
		i := record.Data.Invocation
		w.Invocations[i.Ref()] = i
		if i.ID == receipt.Features[0].Data.PlanAmendment.AssessmentInvocationID {
			w.PM, w.PMExecution = i.ActorFQN, i.Execution
		}
		if i.ID == a.ConsultationInvocationID {
			w.Consultation = i
		}
		if i.ID != a.BlockedInvocationID && i.ID != a.ConsultationInvocationID {
			continue
		}
		verified := false
		for _, entry := range receipt.Entries {
			if entry.ID != i.ID || entry.Kind != "finished" {
				continue
			}
			var output string
			if json.Unmarshal(entry.Payload, &output) != nil {
				t.Fatal("finished payload decode")
			}
			digest := sha256.Sum256([]byte(output))
			verified = i.OutputDigest != nil && hex.EncodeToString(digest[:]) == string(*i.OutputDigest)
		}
		if !verified {
			t.Fatal("terminal output hash mismatch", i.ID)
		}
		// Synthetic evidence-availability witness, bound to the verified raw
		// terminal bytes: this replay does not claim to reload Mongo metadata.
		for _, id := range i.TerminalEvidenceIDs {
			w.Evidence[id] = kernel.EvidenceMetadata{Available: true, SHA256: *i.OutputDigest}
		}
	}
	policy := organization.PlanAmendmentPolicy{ContractVersion: organization.PlanAmendmentContractVersion, ManifestDigest: featureDigest('f'), Authority: kernel.PrincipalRef{Kind: kernel.PrincipalPolicy, ID: "receipt-replay"}, AllowedRoots: prompt.Input.Value.Roots}
	check := func(want string) {
		t.Helper()
		body, _ := json.Marshal(p)
		d := organization.CheckPlanAmendmentDraft(f, body, w, policy)
		if d.Code != want {
			t.Fatalf("got %+v want %s", d, want)
		}
		t.Logf("COMPUTED raw proposal replay: %s", d.Code)
	}
	check("BUDGET_EXHAUSTED")
	f.Input.MaximumTasks = 34 // prospective r61 capacity, not an r60 mutation
	check("ATTEMPT_ALLOWANCE_EXHAUSTED")
	for _, replacement := range p.Replacements {
		for _, old := range a.PriorPlan.Tasks {
			if old.ID != replacement.PriorTaskID {
				continue
			}
			ordinary, repair := organization.RemainingPlanTaskAllowances(old, w.Invocations)
			for index := range p.SuccessorPlan.Tasks {
				if p.SuccessorPlan.Tasks[index].ID == replacement.SuccessorTaskID {
					p.SuccessorPlan.Tasks[index].AttemptLimit = uint32(ordinary)
					p.SuccessorPlan.Tasks[index].ReviewRoundLimit = uint32(repair)
				}
			}
		}
	}
	check("APPROVED")
}
