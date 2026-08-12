package fake_test

import (
	"context"
	"sync"
	"testing"

	"github.com/tekroo-ai/teams/adapters/fake"
	"github.com/tekroo-ai/teams/kernel"
)

func TestReleaseProviderConcurrentDuplicateUsesOneIdempotentEffect(t *testing.T) {
	provider := fake.NewReleaseProvider()
	request := kernel.ReleaseMergeRequest{
		ReleasePlanID: "00000000-0000-7000-8000-000000000751", PlanDigest: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		MergeID: "00000000-0000-7000-8000-000000000752", AttemptID: "00000000-0000-7000-8000-000000000755", Round: 1,
		ProviderIdempotencyKey: "release-751-merge-752-round-1", RepositoryURL: "https://example.invalid/tekroo/teams.git", BaseRef: "main",
		BaseCommit: "1111111111111111111111111111111111111111", ChangeRef: "refs/heads/story-1", HeadCommit: "2222222222222222222222222222222222222222", MergeStrategy: "FF_ONLY_ORDERED",
	}
	want := kernel.ReleaseProviderObservation{
		ReleasePlanID: request.ReleasePlanID, MergeID: request.MergeID, AttemptID: request.AttemptID, State: kernel.ReleaseProviderMerged, Outcome: kernel.ReleaseOutcomeMerged,
		BaseCommit: request.BaseCommit, HeadCommit: request.HeadCommit, TreeDigest: "3333333333333333333333333333333333333333", Reasons: []string{"scripted merge"},
	}
	provider.ScriptMerge(request.ProviderIdempotencyKey, fake.ReleaseProviderStep{Observation: want})

	const callers = 32
	var wait sync.WaitGroup
	errors := make(chan error, callers)
	for index := 0; index < callers; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			got, err := provider.Merge(context.Background(), request)
			if err != nil || got.ReleasePlanID != want.ReleasePlanID || got.Outcome != want.Outcome || got.TreeDigest != want.TreeDigest {
				errors <- err
			}
		}()
	}
	wait.Wait()
	close(errors)
	if len(errors) != 0 || provider.MergeCalls(request.ProviderIdempotencyKey) != callers || provider.MergeEffects(request.ProviderIdempotencyKey) != 1 {
		t.Fatalf("errors=%d calls=%d effects=%d", len(errors), provider.MergeCalls(request.ProviderIdempotencyKey), provider.MergeEffects(request.ProviderIdempotencyKey))
	}
}
