package operationalruntime

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tekroo-ai/teams/kernel"
	"github.com/tekroo-ai/teams/organization"
)

func TestAmendmentActivationRequiresFrozenContractAndBoundHumanAcceptance(t *testing.T) {
	root := t.TempDir()
	schemaPath := filepath.Join(root, "schemas", "feature-plan-amendment-proposal.schema.json")
	if err := os.MkdirAll(filepath.Dir(schemaPath), 0700); err != nil {
		t.Fatal(err)
	}
	schema := organization.PlanAmendmentProposalSchema()
	if err := os.WriteFile(schemaPath, schema, 0600); err != nil {
		t.Fatal(err)
	}
	manifestPath, acceptancePath := filepath.Join(root, "manifest.json"), filepath.Join(root, "acceptance.json")
	for _, test := range []struct {
		name, status string
		mutate       func(*ProductionPlanAmendment, *planAmendmentAcceptance)
		ok           bool
	}{
		{name: "unaccepted candidate", status: "IMPLEMENTATION_IN_PROGRESS"},
		{name: "accepted frozen", status: "FROZEN_CANDIDATE", ok: true},
		{name: "wrong policy", status: "FROZEN_CANDIDATE", mutate: func(c *ProductionPlanAmendment, a *planAmendmentAcceptance) {
			c.Policy.AllowedRoots = []string{"other"}
		}},
		{name: "model cannot accept", status: "FROZEN_CANDIDATE", mutate: func(c *ProductionPlanAmendment, a *planAmendmentAcceptance) {
			a.AcceptedBy = kernel.PrincipalRef{Kind: kernel.PrincipalActor, ID: "teams::project-manager-1"}
		}},
		{name: "wrong manifest", status: "FROZEN_CANDIDATE", mutate: func(c *ProductionPlanAmendment, a *planAmendmentAcceptance) {
			a.ManifestDigest = candidateTestDigest('b')
		}},
		{name: "future acceptance", status: "FROZEN_CANDIDATE", mutate: func(c *ProductionPlanAmendment, a *planAmendmentAcceptance) {
			a.AcceptedAt = time.Now().UTC().Add(time.Hour)
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			manifest := mustJSON(map[string]any{"contract": map[string]any{"version": organization.PlanAmendmentContractVersion, "status": test.status}, "files": []map[string]any{{"path": "schemas/feature-plan-amendment-proposal.schema.json", "sha256": digestBytes(schema)}}})
			if err := os.WriteFile(manifestPath, manifest, 0600); err != nil {
				t.Fatal(err)
			}
			c := ProductionPlanAmendment{Policy: organization.PlanAmendmentPolicy{ContractVersion: organization.PlanAmendmentContractVersion, ManifestDigest: digestBytes(manifest), Authority: kernel.PrincipalRef{Kind: kernel.PrincipalPolicy, ID: "amendment-policy"}, AllowedRoots: []string{"service", "cmd"}}, ManifestFile: manifestPath, AcceptanceFile: acceptancePath}
			a := planAmendmentAcceptance{ContractVersion: c.Policy.ContractVersion, ManifestDigest: c.Policy.ManifestDigest, Decision: "ACCEPT/FREEZE", AcceptedBy: kernel.PrincipalRef{Kind: kernel.PrincipalHuman, ID: "principal"}, AcceptedAt: time.Now().UTC().Add(-time.Minute), PolicyDigest: organization.PlanAmendmentDigest(c.Policy)}
			if test.mutate != nil {
				test.mutate(&c, &a)
			}
			raw, _ := json.Marshal(a)
			if err := os.WriteFile(acceptancePath, raw, 0600); err != nil {
				t.Fatal(err)
			}
			c.AcceptanceSHA256 = digestBytes(raw)
			if err := c.validate(); (err == nil) != test.ok {
				t.Fatalf("expected accepted=%v, got %v", test.ok, err)
			}
			if test.ok {
				if err := os.WriteFile(acceptancePath, append(raw, ' '), 0600); err != nil {
					t.Fatal(err)
				}
				if err := c.validate(); err == nil {
					t.Fatal("unpinned acceptance accepted")
				}
				if err := os.WriteFile(acceptancePath, raw, 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(schemaPath, []byte(`{"type":"string"}`), 0600); err != nil {
					t.Fatal(err)
				}
				if err := c.validate(); err == nil {
					t.Fatal("changed schema accepted")
				}
				if err := os.WriteFile(schemaPath, schema, 0600); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
	var disabled *ProductionPlanAmendment
	if err := disabled.validate(); err != nil {
		t.Fatal("disabled config changed existing behavior", err)
	}
}
