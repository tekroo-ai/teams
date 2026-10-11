package nativeagent

import (
	"testing"

	"github.com/tekroo-ai/teams/application"
	"github.com/tekroo-ai/teams/internal/rolefixtures"
)

type signedPlanFixture struct {
	name    string
	handler application.MessageHandlerGrounding
}

func signedPlanFixtures(t *testing.T) []signedPlanFixture {
	t.Helper()
	return []signedPlanFixture{
		{"historical", rolefixtures.HistoricalPlanHandler(t)},
		{"current", rolefixtures.Handler(t, rolefixtures.Current(t), "project-manager", "tekroo.message.feature.design-proposed")},
	}
}
