package agenttools

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"
)

// Measures the whole real tool, including snapshot, compiler seed/private copy,
// test execution and cleanup. Warm-up is explicit and excluded from ns/op;
// phase receipts are retained in benchmark output, not a forecasted speedup.
func BenchmarkWarmGoVerification(b *testing.B) {
	root, err := filepath.Abs("..")
	if err != nil {
		b.Fatal(err)
	}
	cache, err := filepath.EvalSymlinks(b.TempDir())
	if err != nil {
		b.Fatal(err)
	}
	host := Host{Timeout: 3 * time.Minute, CompilationCache: cache, LocalTestExecution: true}
	b.StopTimer()
	result, err := host.runIsolatedGoTestsWorktree(b.Context(), root, "./adapters/operatorhttp")
	if err != nil || result.ExitCode != 0 {
		b.Fatalf("warm-up: %+v %v", result, err)
	}
	raw, _ := json.Marshal(result.VerificationTimings)
	b.Logf("cold raw phase receipt: %s", raw)
	b.StartTimer()
	for b.Loop() {
		result, err := host.runIsolatedGoTestsWorktree(b.Context(), root, "./adapters/operatorhttp")
		if err != nil || result.ExitCode != 0 {
			b.Fatalf("warm execution: %+v %v", result, err)
		}
		b.StopTimer()
		raw, _ := json.Marshal(result.VerificationTimings)
		b.Logf("warm raw phase receipt: %s", raw)
		b.StartTimer()
	}
}

// Mirrors r62's macro sequence: compile the whole working tree, then test one
// package without a source edit. Each requested check still runs; no PASS cache.
func BenchmarkBuildThenPackageVerification(b *testing.B) {
	root, err := filepath.Abs("..")
	if err != nil {
		b.Fatal(err)
	}
	cache, err := filepath.EvalSymlinks(b.TempDir())
	if err != nil {
		b.Fatal(err)
	}
	host := Host{Timeout: 5 * time.Minute, CompilationCache: cache, LocalTestExecution: true}
	b.ResetTimer()
	for b.Loop() {
		for _, check := range []struct {
			pattern string
			build   bool
		}{{"./...", true}, {"./adapters/mongo", false}} {
			result, err := host.runIsolatedGoTestsFrom(b.Context(), root, check.pattern, true, check.build)
			if err != nil || result.ExitCode != 0 {
				b.Fatalf("build=%t: %+v %v", check.build, result, err)
			}
			b.StopTimer()
			raw, _ := json.Marshal(result.VerificationTimings)
			b.Logf("build=%t snapshot=%s raw phase receipt: %s", check.build, result.SnapshotSHA256, raw)
			b.StartTimer()
		}
	}
}
