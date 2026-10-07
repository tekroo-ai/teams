//go:build mongo_integration

package operationalruntime

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/tekroo-ai/teams/kernel"
	"github.com/tekroo-ai/teams/organization"
)

func TestDurableExecutionReconciliationRetainsRegisteredHistoricalProfile(t *testing.T) {
	process, uri := startRuntimeMongod(t)
	defer stopRuntimeMongod(process)
	path, config := writeNativeProductionFixture(t)
	writeText(t, filepath.Join(filepath.Dir(path), config.Mongo.URIFile), uri+"\n", 0600)
	writeJSON(t, path, config, 0600)
	loaded, err := LoadProductionConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	service, err := NewProductionService(ctx, loaded)
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close(context.WithoutCancel(ctx))
	if err := service.Start(ctx); err != nil {
		t.Fatal(err)
	}
	actor := kernel.ActorFQN("fixture::coder-1")
	state, found, err := service.RoleHost.Status(ctx, actor)
	if err != nil || !found {
		t.Fatalf("role state found=%v err=%v", found, err)
	}
	before, found, err := service.Store.ReadExecutionRegistration(ctx, actor)
	if err != nil || !found || before.Execution != state.Execution {
		t.Fatalf("registration=%#v found=%v err=%v", before, found, err)
	}
	// The old profile disappears when a newly qualified profile is activated.
	// Registry reconciliation must retain its exact durable execution, not
	// reconstruct historical state from today's profile configuration.
	service.profilesByModel = map[kernel.Digest]ProductionProfile{}
	if err := service.reconcileDurableRoleExecutions(ctx); err != nil {
		t.Fatalf("registered historical execution must survive profile replacement: %v", err)
	}
	after, found, err := service.Store.ReadExecutionRegistration(ctx, actor)
	if err != nil || !found || after != before {
		t.Fatalf("historical registration changed: before=%#v after=%#v err=%v", before, after, err)
	}
	// Admission of a new execution must still require a configured profile.
	if err := service.registerRoleState(ctx, state); !errors.Is(err, organization.ErrRoleNotConfigured) {
		t.Fatalf("new execution registration bypassed profile validation: %v", err)
	}
	state.Execution.ExecutionID = "00000000-0000-7000-8000-000000000999"
	state.Execution.FencingEpoch++
	if err := service.reconcileRoleExecution(ctx, state); !errors.Is(err, organization.ErrRoleNotConfigured) {
		t.Fatalf("unregistered transition bypassed profile validation: %v", err)
	}
	state.ActorFQN = "fixture::coder-2"
	if err := service.reconcileRoleExecution(ctx, state); !errors.Is(err, organization.ErrRoleNotConfigured) {
		t.Fatalf("missing registration bypassed profile validation: %v", err)
	}
}
