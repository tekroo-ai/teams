package fake

import "github.com/tekroo-ai/teams/kernel"

func ProvenanceBasis() (kernel.ProvenanceBasis, error) {
	sourceDigest := kernel.Digest("1111111111111111111111111111111111111111111111111111111111111111")
	overlay := kernel.OverlayIdentity{
		SourceTreeDigest: sourceDigest,
		ChangeDigest:     kernel.Digest("2222222222222222222222222222222222222222222222222222222222222222"),
	}
	overlayDigest, err := overlay.Digest()
	if err != nil {
		return kernel.ProvenanceBasis{}, err
	}
	artifactDigest := kernel.Digest("7777777777777777777777777777777777777777777777777777777777777777")
	return kernel.ProvenanceBasis{
		CatalogueDigest: kernel.Digest("8888888888888888888888888888888888888888888888888888888888888888"),
		PolicyDigest:    kernel.Digest("9999999999999999999999999999999999999999999999999999999999999999"),
		PolicyRevision:  1,
		GrantDigests:    []kernel.Digest{kernel.Digest("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")},
		Source: kernel.SourceIdentity{
			Repository: "github.com/tekroo-ai/teams", Commit: "deterministic-fake", TreeDigest: sourceDigest, Scope: ".",
		},
		Overlay: overlay,
		Build: kernel.BuildIdentity{
			SourceTreeDigest: sourceDigest, OverlayDigest: overlayDigest,
			DependencyLockDigest:  kernel.Digest("3333333333333333333333333333333333333333333333333333333333333333"),
			ToolchainDigest:       kernel.Digest("4444444444444444444444444444444444444444444444444444444444444444"),
			BuildDefinitionDigest: kernel.Digest("5555555555555555555555555555555555555555555555555555555555555555"),
			ArtifactDigest:        artifactDigest,
		},
		Runtime: kernel.RuntimeIdentity{
			BuildArtifactDigest: artifactDigest, ContractManifest: kernel.ContractIdentity,
			ConfigurationDigest: kernel.Digest("bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"),
			EnvironmentDigest:   kernel.Digest("cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"),
			RoleLibraryDigests:  []kernel.Digest{}, Capabilities: []string{"kernel-evaluate"}, ProviderIdentities: []string{},
		},
	}, nil
}
