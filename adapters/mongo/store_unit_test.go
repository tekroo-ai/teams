package mongo

import (
	"testing"

	"github.com/tekroo-ai/teams/kernel"
)

func TestMongoFailsClosedForUnimplementedReleaseProjection(t *testing.T) {
	release := kernel.Decision{Events: []kernel.DomainEvent{{EventType: "tekroo.event.release-plan.created"}}}
	if !releaseProjectionUnsupported(release) {
		t.Fatal("release-plan event was not fenced")
	}
	storyApproval := kernel.Decision{Events: []kernel.DomainEvent{{EventType: "tekroo.event.story.release-approved"}}}
	if releaseProjectionUnsupported(storyApproval) {
		t.Fatal("generic story approval was incorrectly fenced")
	}
}
