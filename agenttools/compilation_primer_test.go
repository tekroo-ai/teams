package agenttools

import (
	"crypto/sha256"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestCompilationPrimerRetainsAlternatingSelectorsAndLanesWithinBound(t *testing.T) {
	namespace := t.TempDir()
	first := sha256.Sum256([]byte("lane0/all"))
	second := sha256.Sum256([]byte("lane1/focused"))
	rememberCompilationPrimer(namespace, first)
	rememberCompilationPrimer(namespace, second)
	if !hasCompilationPrimer(namespace, first) || !hasCompilationPrimer(namespace, second) {
		t.Fatal("alternating selectors discarded compiler preparation")
	}
	if hasCompilationPrimer(namespace, sha256.Sum256([]byte("changed source"))) {
		t.Fatal("changed source inherited preparation")
	}
	rememberCompilationPrimer(namespace, first)
	compilationPrimers.Lock()
	n := len(compilationPrimers.last[namespace])
	compilationPrimers.Unlock()
	if n != 2 {
		t.Fatal("duplicate hints inflated retention", n)
	}
	for i := 0; i < 32; i++ {
		rememberCompilationPrimer(namespace, sha256.Sum256([]byte(fmt.Sprint(i))))
	}
	compilationPrimers.Lock()
	n = len(compilationPrimers.last[namespace])
	compilationPrimers.Unlock()
	if n != 16 || hasCompilationPrimer(namespace, first) {
		t.Fatal("primer retention not bounded", n)
	}
}

func TestAlternatingCompilationSelectorsRetainPreparationNotTestVerdicts(t *testing.T) {
	root, _ := localTestFixture(t, "package localexecution\nimport \"testing\"\nfunc TestExecuted(t *testing.T) { t.Fatal(\"FRESH_EXECUTION_EVERY_TIME\") }\n")
	host := cachedFixtureHost(t, Host{Timeout: time.Minute})
	for i, pattern := range []string{"./...", ".", "./..."} {
		result, err := host.runIsolatedGoTests(t.Context(), root, pattern)
		if err != nil || result.ExitCode != 1 || !strings.Contains(result.Output, "FRESH_EXECUTION_EVERY_TIME") {
			t.Fatalf("selector %s did not execute the failing test: %+v %v", pattern, result, err)
		}
		if (result.VerificationTimings.CacheCompileNS > 0) != (i < 2) {
			t.Fatalf("alternating selectors repeated preparation: %+v", result.VerificationTimings)
		}
	}
}
