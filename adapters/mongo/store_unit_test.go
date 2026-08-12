package mongo

import (
	"testing"

	"github.com/tekroo-ai/teams/kernel"
)

func TestReleasePlanKeyIsStable(t *testing.T) {
	key := kernel.ReleasePlanKey{Story: kernel.AggregateRef{Kind: kernel.AggregateStory, ID: "00000000-0000-7000-8000-000000000001"}, LifecycleEpoch: 1}
	if releasePlanKey(key) != releasePlanKey(key) {
		t.Fatal("release-plan semantic key encoding is not stable")
	}
	otherLifecycle := key
	otherLifecycle.LifecycleEpoch = 2
	if releasePlanKey(key) == releasePlanKey(otherLifecycle) {
		t.Fatal("release-plan semantic key did not bind the lifecycle epoch")
	}
}
