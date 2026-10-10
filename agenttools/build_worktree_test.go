package agenttools

import (
	"path/filepath"
	"testing"
	"time"
)

func TestCompileCheckNeverRunsInitOrTestMainAndFindsTestCompileErrors(t *testing.T) {
	root := t.TempDir()
	writeSnapshotFixture(t, root, "go.mod", "module example.test/compile\n\ngo 1.26.0\n")
	writeSnapshotFixture(t, root, "value.go", "package compile\nfunc init() { panic(\"must never execute\") }\nconst Value = 1\n")
	writeSnapshotFixture(t, root, "value_test.go", "package compile\nimport (\"testing\"; \"os\")\nfunc TestMain(m *testing.M) { panic(\"must never execute TestMain\"); os.Exit(m.Run()) }\nfunc TestValue(t *testing.T) { if Value!=1 { t.Fatal(Value) } }\n")
	fixtureGit(t, root, "init", "-q")
	fixtureGit(t, root, "add", "--", ".")
	fixtureGit(t, root, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "-qm", "baseline")
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	host := cachedFixtureHost(t, Host{Timeout: time.Minute})
	result, err := host.runIsolatedGoTestsFrom(t.Context(), root, "./...", true, true)
	if err != nil || result.ExitCode != 0 || !validExpectedSHA(result.SnapshotSHA256) {
		t.Fatalf("compile ran candidate: %+v %v", result, err)
	}
	writeSnapshotFixture(t, root, "value_test.go", "package compile\nvar Broken = MissingSymbol\n")
	result, err = host.runIsolatedGoTestsFrom(t.Context(), root, "./...", true, true)
	if err != nil || result.ExitCode == 0 {
		t.Fatalf("test compilation failure missed: %+v %v", result, err)
	}
}
