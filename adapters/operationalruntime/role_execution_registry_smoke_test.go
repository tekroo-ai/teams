//go:build local_smoke

package operationalruntime

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/tekroo-ai/teams/kernel"
)

// TestProductionRoleExecutionSurvivesDaemonRestart exercises the real
// production assembly and Mongo transaction path without creating model work.
// It is opt-in because it requires the installed local service configuration.
func TestProductionRoleExecutionSurvivesDaemonRestart(t *testing.T) {
	configPath := os.Getenv("TEKROO_SMOKE_CONFIG")
	if configPath == "" {
		t.Skip("TEKROO_SMOKE_CONFIG is not set")
	}
	config, err := LoadProductionConfig(configPath)
	if err != nil {
		t.Fatal(err)
	}
	config.Mongo.Database = "tekroo_teams_v4_execution_registry_smoke"
	config.TeamsDatabaseIdentity = config.Mongo.Database
	config.Operator.Address = "127.0.0.1:8798"
	config.EvidenceRoot = t.TempDir()
	actor := kernel.ActorFQN("teams::product-owner-1")

	first := startSmokeService(t, config)
	firstRegistration, found, err := first.Store.ReadExecutionRegistration(smokeContext(t), actor)
	if err != nil || !found || firstRegistration.Execution.FencingEpoch != 1 || firstRegistration.AggregateRevision != 1 {
		t.Fatalf("first registration = %#v found=%v err=%v", firstRegistration, found, err)
	}
	closeSmokeService(t, first)

	second := startSmokeService(t, config)
	defer closeSmokeService(t, second)
	secondRegistration, found, err := second.Store.ReadExecutionRegistration(smokeContext(t), actor)
	if err != nil || !found || secondRegistration.Execution.FencingEpoch != 2 || secondRegistration.AggregateID != firstRegistration.AggregateID || secondRegistration.AggregateRevision != 2 || secondRegistration.LastEventID == firstRegistration.LastEventID {
		t.Fatalf("second registration = %#v found=%v err=%v; first=%#v", secondRegistration, found, err, firstRegistration)
	}
}

func startSmokeService(t *testing.T, config ProductionConfig) *ProductionService {
	t.Helper()
	service, err := NewProductionService(smokeContext(t), config)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Start(smokeContext(t)); err != nil {
		_ = service.Close(smokeContext(t))
		t.Fatal(err)
	}
	return service
}

func closeSmokeService(t *testing.T, service *ProductionService) {
	t.Helper()
	if err := service.Close(smokeContext(t)); err != nil {
		t.Fatal(err)
	}
}

func smokeContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	return ctx
}
