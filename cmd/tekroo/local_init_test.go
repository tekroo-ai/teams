package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/tekroo-ai/teams/adapters/operationalruntime"
)

func TestInitializeLocalDeploymentProducesValidatedIsolatedInstallation(t *testing.T) {
	sourceRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	temporary := t.TempDir()
	repository := filepath.Join(temporary, "repository")
	if err := os.Mkdir(repository, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, arguments := range [][]string{{"init", "-b", "main"}, {"config", "user.email", "test@tekroo.local"}, {"config", "user.name", "Tekroo Test"}} {
		localInitGit(t, repository, arguments...)
	}
	if err := os.WriteFile(filepath.Join(repository, "README.md"), []byte("# fixture\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	localInitGit(t, repository, "add", "README.md")
	localInitGit(t, repository, "commit", "-m", "fixture")
	key := filepath.Join(temporary, "openhands-key")
	hook := filepath.Join(temporary, "sma_context_hook.py")
	if err := os.WriteFile(key, []byte("local-session-key\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(hook, []byte("#!/usr/bin/env python3\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(temporary, "deployment")
	result, err := initializeLocalDeployment(context.Background(), localInitOptions{
		Root: root, SourceRoot: sourceRoot, RepositoryRoot: repository,
		OpenHandsKeyFile: key, SMAHookFile: hook, TekroodPath: "/usr/bin/true",
		MongoURI: "mongodb://127.0.0.1:27017", Database: "teams_test_prod",
		SMADatabase: "sma_test", OperatorAddress: "127.0.0.1:18787",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Team != "teams" || result.WorkspaceCount != 13 || result.AutomaticStartup || result.ContractIdentity != "tekroo.kernel.contracts/0.10.0" {
		t.Fatalf("result = %#v", result)
	}
	config, err := operationalruntime.LoadProductionConfig(result.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if config.Mongo.Database != "teams_test_prod" || config.SMADatabaseIdentity != "sma_test" || len(config.Workspaces) != 13 || len(config.Profiles) != 8 {
		t.Fatalf("config = %#v", config)
	}
	for _, workspace := range config.Workspaces {
		if _, err := os.Stat(filepath.Join(workspace.WorkingDirectory, ".openhands", "hooks", "sma_context_hook.py")); err != nil {
			t.Fatalf("workspace %s hook: %v", workspace.WorkspaceID, err)
		}
	}
}

func TestInitializeLocalDeploymentFailsClosed(t *testing.T) {
	base := localInitOptions{
		Root: "/tmp/tekroo-test-root", SourceRoot: "/tmp/source", RepositoryRoot: "/tmp/repository",
		OpenHandsKeyFile: "/tmp/key", SMAHookFile: "/tmp/hook", TekroodPath: "/usr/bin/true",
		MongoURI: "mongodb://remote.example:27017", Database: "teams", SMADatabase: "sma",
		OperatorAddress: "127.0.0.1:8787",
	}
	if err := validateLocalInitOptions(base); err == nil {
		t.Fatal("remote MongoDB URI accepted")
	}
	base.MongoURI = "mongodb://127.0.0.1:27017"
	base.SMADatabase = base.Database
	if err := validateLocalInitOptions(base); err == nil {
		t.Fatal("shared Teams/SMA database identity accepted")
	}
}

func localInitGit(t *testing.T, directory string, arguments ...string) {
	t.Helper()
	command := exec.Command("git", arguments...)
	command.Dir = directory
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", arguments, err, output)
	}
}
