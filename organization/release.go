package organization

import "github.com/tekroo-ai/teams/kernel"

type FeatureAcceptanceMode string

const (
	FeatureAcceptanceNoRelease FeatureAcceptanceMode = "NO_RELEASE_REQUIRED"
	FeatureAcceptanceCode      FeatureAcceptanceMode = "CODE"
)

type FeatureAcceptanceInput struct {
	ExpectedRevision uint64                `json:"expected_revision"`
	Mode             FeatureAcceptanceMode `json:"mode"`
	NoReleaseReason  string                `json:"no_release_reason,omitempty"`
	CodeReleases     []StoryCodeRelease    `json:"code_releases,omitempty"`
}

type StoryCodeRelease struct {
	StoryID                   kernel.UUIDv7 `json:"story_id"`
	RepositoryURL             string        `json:"repository_url"`
	BaseRef                   string        `json:"base_ref"`
	BaseCommit                string        `json:"base_commit"`
	ExpectedQualifiedTree     string        `json:"expected_qualified_tree"`
	ChangeRef                 string        `json:"change_ref"`
	HeadCommit                string        `json:"head_commit"`
	GitVersion                string        `json:"git_version"`
	GateDefinitionDigest      kernel.Digest `json:"gate_definition_digest"`
	ToolchainDigest           kernel.Digest `json:"toolchain_digest"`
	DependencyLockDigest      kernel.Digest `json:"dependency_lock_digest"`
	QualificationArtifactHash kernel.Digest `json:"qualification_artifact_digest"`
}

func (input FeatureAcceptanceInput) Validate(stories []PlannedStory) error {
	if input.ExpectedRevision == 0 {
		return ErrInvalidFeature
	}
	switch input.Mode {
	case FeatureAcceptanceNoRelease:
		if input.NoReleaseReason == "" || len(input.NoReleaseReason) > 4096 || len(input.CodeReleases) != 0 {
			return ErrInvalidFeature
		}
		return nil
	case FeatureAcceptanceCode:
		if input.NoReleaseReason != "" || len(input.CodeReleases) != len(stories) || len(stories) == 0 {
			return ErrInvalidFeature
		}
	default:
		return ErrInvalidFeature
	}
	wanted := make(map[kernel.UUIDv7]struct{}, len(stories))
	for _, story := range stories {
		wanted[story.ID] = struct{}{}
	}
	for _, release := range input.CodeReleases {
		if !release.StoryID.Valid() || release.RepositoryURL == "" || release.BaseRef == "" || len(release.BaseCommit) < 40 || release.ExpectedQualifiedTree == "" || release.ChangeRef == "" || len(release.HeadCommit) < 40 || release.GitVersion == "" || !release.GateDefinitionDigest.Valid() || !release.ToolchainDigest.Valid() || !release.DependencyLockDigest.Valid() || !release.QualificationArtifactHash.Valid() {
			return ErrInvalidFeature
		}
		if _, found := wanted[release.StoryID]; !found {
			return ErrInvalidFeature
		}
		delete(wanted, release.StoryID)
	}
	if len(wanted) != 0 {
		return ErrInvalidFeature
	}
	return nil
}
