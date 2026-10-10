package agenttools

import (
	"fmt"
	"os"
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

func cachedFixtureHost(t *testing.T, host Host) Host {
	t.Helper()
	if fixtureCompilationCache == "" {
		t.Fatal("fixture compiler cache was not initialized")
	}
	host.CompilationCache = fixtureCompilationCache
	return host
}
