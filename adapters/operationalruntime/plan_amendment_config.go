package operationalruntime

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/tekroo-ai/teams/kernel"
	"github.com/tekroo-ai/teams/organization"
)

// Optional, administrator-owned activation. A model proposal never supplies
// this block. The development candidate is deliberately not activatable.
type ProductionPlanAmendment struct {
	Policy           organization.PlanAmendmentPolicy `json:"policy"`
	ManifestFile     string                           `json:"manifest_file"`
	AcceptanceFile   string                           `json:"acceptance_file"`
	AcceptanceSHA256 kernel.Digest                    `json:"acceptance_sha256"`
}

type planAmendmentAcceptance struct {
	ContractVersion string              `json:"contract_version"`
	ManifestDigest  kernel.Digest       `json:"manifest_digest"`
	Decision        string              `json:"decision"`
	AcceptedBy      kernel.PrincipalRef `json:"accepted_by"`
	AcceptedAt      time.Time           `json:"accepted_at"`
	PolicyDigest    kernel.Digest       `json:"policy_digest"`
}

func (config *ProductionPlanAmendment) validate() error {
	if config == nil {
		return nil
	}
	if !config.Policy.Valid() || !filepath.IsAbs(config.ManifestFile) || !filepath.IsAbs(config.AcceptanceFile) || !config.AcceptanceSHA256.Valid() {
		return invalidConfig("plan amendment authority requires a pinned policy, frozen manifest and separate principal acceptance")
	}
	read := func(path string) ([]byte, error) {
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() || info.Size() < 1 || info.Size() > 1<<20 {
			return nil, invalidConfig("plan amendment authority file missing, linked or oversized")
		}
		return os.ReadFile(path)
	}
	raw, err := read(config.ManifestFile)
	if err != nil || digestBytes(raw) != config.Policy.ManifestDigest {
		return invalidConfig("plan amendment manifest identity mismatch")
	}
	var manifest struct {
		Contract struct {
			Version string `json:"version"`
			Status  string `json:"status"`
		} `json:"contract"`
		Files []struct {
			Path   string        `json:"path"`
			SHA256 kernel.Digest `json:"sha256"`
		} `json:"files"`
	}
	if json.Unmarshal(raw, &manifest) != nil || manifest.Contract.Version != organization.PlanAmendmentContractVersion || manifest.Contract.Status != "FROZEN_CANDIDATE" {
		return invalidConfig("plan amendment contract is not frozen for acceptance")
	}
	schemaBound := false
	for _, file := range manifest.Files {
		if file.Path == "schemas/feature-plan-amendment-proposal.schema.json" {
			encoded, err := read(filepath.Join(filepath.Dir(config.ManifestFile), file.Path))
			if err != nil || digestBytes(encoded) != file.SHA256 {
				return invalidConfig("plan amendment schema manifest mismatch")
			}
			var expected, actual any
			if json.Unmarshal(organization.PlanAmendmentProposalSchema(), &expected) != nil || json.Unmarshal(encoded, &actual) != nil || organization.PlanAmendmentDigest(expected) != organization.PlanAmendmentDigest(actual) {
				return invalidConfig("plan amendment schema differs from runtime contract")
			}
			schemaBound = true
		}
	}
	if !schemaBound {
		return invalidConfig("plan amendment schema not declared in manifest")
	}
	raw, err = read(config.AcceptanceFile)
	if err != nil || digestBytes(raw) != config.AcceptanceSHA256 {
		return invalidConfig("plan amendment acceptance receipt mismatch")
	}
	var acceptance planAmendmentAcceptance
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&acceptance) != nil || d.Decode(new(any)) != io.EOF || acceptance.ContractVersion != config.Policy.ContractVersion || acceptance.ManifestDigest != config.Policy.ManifestDigest || acceptance.PolicyDigest != organization.PlanAmendmentDigest(config.Policy) || acceptance.Decision != "ACCEPT/FREEZE" || acceptance.AcceptedBy.Kind != kernel.PrincipalHuman || !acceptance.AcceptedBy.Valid() || acceptance.AcceptedAt.IsZero() || acceptance.AcceptedAt.After(time.Now().UTC()) {
		return invalidConfig("plan amendment acceptance does not bind this exact policy and manifest")
	}
	return nil
}
