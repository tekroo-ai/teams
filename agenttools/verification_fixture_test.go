package agenttools

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

var fixtureCompilationCache string

func TestMain(m *testing.M) {
	// Share compiler artifacts across the suite, not a cold toolchain per fixture.
	// The real runner still snapshots, compiles and executes every requested
	// test, with independent writable caches and unchanged deadlines/quotas.
	path, err := os.MkdirTemp("", "tekroo-fixture-compilation-*")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fixtureCompilationCache, err = filepath.EvalSymlinks(path)
	if err != nil {
		_ = os.RemoveAll(path)
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	code := m.Run()
	if err := os.RemoveAll(path); err != nil {
		fmt.Fprintln(os.Stderr, err)
		code = 1
	}
	os.Exit(code)
}

// Bootstrap independent compiler-cache fixtures with already compiled standard
// library archives. The fixture's path/module still has a distinct Go action ID;
// its compiler/linker counts and every real -count=1 execution remain asserted.
// Cold/no-cache execution and complete-cache eviction have dedicated coverage.
func seedFixtureCompilerArtifacts(t *testing.T, destination string) {
	t.Helper()
	binary, err := exec.LookPath("go")
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(data)
	namespace := filepath.Join(fixtureCompilationCache, hex.EncodeToString(hash[:]))
	if _, err := os.Stat(namespace); os.IsNotExist(err) {
		return
	} else if err != nil {
		t.Fatal(err)
	}
	unlock, err := lockEffectPath(t.Context(), namespace, "")
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	if err := copyCompilationCache(t.Context(), namespace, destination); err != nil {
		t.Fatal(err)
	}
}

func cachedFixtureHost(t *testing.T, host Host) Host {
	t.Helper()
	if fixtureCompilationCache == "" {
		t.Fatal("fixture compiler cache was not initialized")
	}
	host.CompilationCache = fixtureCompilationCache
	return host
}
