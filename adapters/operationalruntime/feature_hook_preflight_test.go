package operationalruntime

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/tekroo-ai/teams/kernel"
	"github.com/tekroo-ai/teams/organization"
)

func TestConfiguredRuntimeHookIsInstalledAndVerified(t *testing.T) {
	workspace := t.TempDir()
	secondWorkspace := t.TempDir()
	service := &ProductionService{workspacesByID: map[string]ProductionWorkspace{
		"product-owner-1": {WorkspaceID: "product-owner-1", WorkingDirectory: workspace},
		"coder-1":         {WorkspaceID: "coder-1", WorkingDirectory: secondWorkspace},
	}}
	if err := service.ensureConfiguredRuntimeHooks(); err != nil {
		t.Fatalf("install missing hook: %v", err)
	}
	hook := filepath.Join(workspace, ".openhands", "hooks", "sma_context_hook.py")
	want, err := canonicalRuntimeHook()
	if err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprintf("%x", sha256.Sum256(want)); got != "a65942b7b76106e419238875537859db8a8116dcf959399624371e9e53f13ce1" {
		t.Fatalf("embedded hook is not the Teams canonical fixture: %s", got)
	}
	got, err := os.ReadFile(hook)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("installed hook differs from fixture: %v", err)
	}
	other, err := os.ReadFile(filepath.Join(secondWorkspace, ".openhands", "hooks", "sma_context_hook.py"))
	if err != nil || !bytes.Equal(other, want) {
		t.Fatalf("second workspace hook differs from fixture: %v", err)
	}
	info, err := os.Lstat(hook)
	if err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("installed hook mode: %v, %v", info, err)
	}
	if err := service.ensureConfiguredRuntimeHooks(); err != nil {
		t.Fatalf("idempotent verification: %v", err)
	}
	if err := os.WriteFile(hook, []byte("print('different')\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := service.ensureConfiguredRuntimeHooks(); !errors.Is(err, errRuntimeHookUnavailable) {
		t.Fatalf("divergent hook accepted: %v", err)
	}
	got, err = os.ReadFile(hook)
	if err != nil || !bytes.Equal(got, []byte("print('different')\n")) {
		t.Fatalf("divergent hook overwritten: %q, %v", got, err)
	}
}

func TestConfiguredRuntimeHookRejectsSymlinksAndLooseMode(t *testing.T) {
	for _, test := range []struct {
		name  string
		setup func(t *testing.T, workspace string)
	}{
		{"hook symlink", func(t *testing.T, workspace string) {
			path := filepath.Join(workspace, ".openhands", "hooks")
			if err := os.MkdirAll(path, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(filepath.Join(workspace, "other.py"), filepath.Join(path, "sma_context_hook.py")); err != nil {
				t.Fatal(err)
			}
		}},
		{"directory symlink", func(t *testing.T, workspace string) {
			if err := os.Symlink(t.TempDir(), filepath.Join(workspace, ".openhands")); err != nil {
				t.Fatal(err)
			}
		}},
		{"loose mode", func(t *testing.T, workspace string) {
			path := filepath.Join(workspace, ".openhands", "hooks")
			if err := os.MkdirAll(path, 0o700); err != nil {
				t.Fatal(err)
			}
			hook, err := canonicalRuntimeHook()
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(path, "sma_context_hook.py"), hook, 0o644); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			workspace := t.TempDir()
			test.setup(t, workspace)
			if err := ensureConfiguredRuntimeHooks([]ProductionWorkspace{{WorkspaceID: "coder-1", WorkingDirectory: workspace}}); !errors.Is(err, errRuntimeHookUnavailable) {
				t.Fatalf("unsafe hook accepted: %v", err)
			}
		})
	}
}

func TestSubmitFeatureInstallsMissingHookBeforeDurableSubmit(t *testing.T) {
	workspace := t.TempDir()
	service := &ProductionService{
		Features: &organization.FeatureCoordinator{},
		workspacesByID: map[string]ProductionWorkspace{
			"product-owner-1": {WorkspaceID: "product-owner-1", WorkingDirectory: workspace},
		},
	}
	feature, created, err := service.SubmitFeature(context.Background(), kernel.PrincipalRef{}, organization.FeatureRequestInput{})
	if !errors.Is(err, organization.ErrInvalidFeature) || created || feature.ID != "" {
		t.Fatalf("feature=%#v created=%t err=%v", feature, created, err)
	}
	if err := service.ensureConfiguredRuntimeHooks(); err != nil {
		t.Fatalf("hook not installed before feature validation: %v", err)
	}
}
