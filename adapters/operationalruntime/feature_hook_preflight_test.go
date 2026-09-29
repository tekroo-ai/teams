package operationalruntime

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/tekroo-ai/teams/kernel"
	"github.com/tekroo-ai/teams/organization"
)

func TestConfiguredRuntimeHookPreflight(t *testing.T) {
	workspace := t.TempDir()
	service := &ProductionService{workspacesByID: map[string]ProductionWorkspace{
		"product-owner-1": {WorkspaceID: "product-owner-1", WorkingDirectory: workspace},
	}}
	if err := service.verifyConfiguredRuntimeHooks(); !errors.Is(err, errRuntimeHookUnavailable) {
		t.Fatalf("missing hook: %v", err)
	}
	hook := filepath.Join(workspace, ".openhands", "hooks", "sma_context_hook.py")
	if err := os.MkdirAll(filepath.Dir(hook), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(hook, nil, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := service.verifyConfiguredRuntimeHooks(); !errors.Is(err, errRuntimeHookUnavailable) {
		t.Fatalf("empty hook: %v", err)
	}
	if err := os.WriteFile(hook, []byte("print('ready')\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := service.verifyConfiguredRuntimeHooks(); err != nil {
		t.Fatalf("valid hook: %v", err)
	}
	if err := os.Remove(hook); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(workspace, "missing.py"), hook); err != nil {
		t.Fatal(err)
	}
	if err := service.verifyConfiguredRuntimeHooks(); !errors.Is(err, errRuntimeHookUnavailable) {
		t.Fatalf("symlink hook: %v", err)
	}
}

func TestSubmitFeatureRejectsMissingHookBeforeDurableSubmit(t *testing.T) {
	service := &ProductionService{
		Features: &organization.FeatureCoordinator{},
		workspacesByID: map[string]ProductionWorkspace{
			"product-owner-1": {WorkspaceID: "product-owner-1", WorkingDirectory: t.TempDir()},
		},
	}
	feature, created, err := service.SubmitFeature(context.Background(), kernel.PrincipalRef{}, organization.FeatureRequestInput{})
	if !errors.Is(err, errRuntimeHookUnavailable) || created || feature.ID != "" {
		t.Fatalf("feature=%#v created=%t err=%v", feature, created, err)
	}
}
