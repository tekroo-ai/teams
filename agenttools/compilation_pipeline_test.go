package agenttools

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Retain the r62 counterexample: a whole-module build check followed by a
// package test at identical source unnecessarily linked twice per operation
// and primed two incompatible compiler-flag variants.
func TestCompilationPipelineBuildThenTestTrace(t *testing.T) {
	goBinary, err := exec.LookPath("go")
	if err != nil {
		t.Fatal(err)
	}
	for _, method := range []string{"r62-duplicate-linking", "archive-only-matching-flags"} {
		t.Run(method, func(t *testing.T) {
			workspace, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			source := filepath.Join(workspace, "source")
			if err := os.Mkdir(source, 0700); err != nil {
				t.Fatal(err)
			}
			writeSnapshotFixture(t, source, "go.mod", "module example.test/pipeline\n\ngo 1.26.0\n")
			writeSnapshotFixture(t, source, "pipeline_test.go", `package pipeline
import ("fmt"; "path/filepath"; "runtime"; "testing")
func TestExecuted(t *testing.T) {
 _, file, _, _ := runtime.Caller(0)
 if !filepath.IsAbs(file) { t.Fatalf("lost fixture path: %q", file) }
 fmt.Println("TEST_ACTUALLY_EXECUTED")
}
`)
			cache := filepath.Join(workspace, "persistent-cache")
			private := filepath.Join(workspace, "private-cache")
			for _, path := range []string{cache, private, filepath.Join(workspace, "compiled"), filepath.Join(workspace, "tmp"), filepath.Join(workspace, "home"), filepath.Join(workspace, "gopath")} {
				if err := os.Mkdir(path, 0700); err != nil {
					t.Fatal(err)
				}
			}
			primer := compilationPrimerArguments(goBinary, ".")[1:]
			primer = append(primer[:len(primer)-1], "-x", ".")
			build := []string{"test", "-p=1", "-parallel=2", "-count=1", "-x", "-c", "-o", filepath.Join(workspace, "compiled", "pipeline.test"), "."}
			oldPrimer := []string{"test", "-p=1", "-parallel=2", "-x", "-c", "-o", filepath.Join(workspace, "compiled", "primer.test"), "."}
			buildPrimer, testPrimer := primer, primer
			if method == "r62-duplicate-linking" {
				buildPrimer = append([]string{"test", "-trimpath"}, oldPrimer[1:]...)
				testPrimer = oldPrimer
				build = append([]string{"test", "-trimpath"}, build[1:]...)
			}
			var phases []map[string]any
			totalLinks := 0
			run := func(phase string, args []string, cachePath string) string {
				t.Helper()
				command := exec.CommandContext(t.Context(), goBinary, args...)
				command.Dir = source
				command.Env = replaceEnvironment(goTestEnvironment(workspace, "/usr/bin/git", ""), "GOCACHE", cachePath)
				started := time.Now()
				output, err := command.CombinedOutput()
				elapsed := time.Since(started)
				if err != nil {
					t.Fatalf("%s: %s %v", phase, output, err)
				}
				compiles, links := 0, 0
				for _, line := range strings.Split(string(output), "\n") {
					if strings.Contains(line, "/compile -o ") && !strings.Contains(line, "_testmain.go") {
						compiles++
					}
					if strings.Contains(line, "/link -o ") {
						links++
					}
				}
				totalLinks += links
				phases = append(phases, map[string]any{"phase": phase, "command": append([]string{goBinary}, args...), "elapsed_ns": elapsed.Nanoseconds(), "package_compiler_processes": compiles, "linker_processes": links})
				if dir := os.Getenv("TEKROO_VERIFICATION_TRACE_DIR"); dir != "" {
					if err := os.WriteFile(filepath.Join(dir, method+"-"+phase+".txt"), output, 0600); err != nil {
						t.Fatal(err)
					}
				}
				return string(output)
			}
			run("build-primer", buildPrimer, cache)
			if err := copyCompilationCache(t.Context(), cache, private); err != nil {
				t.Fatal(err)
			}
			run("build-check", build, private)
			if err := os.RemoveAll(private); err != nil {
				t.Fatal(err)
			}
			run("test-primer", testPrimer, cache)
			if err := copyCompilationCache(t.Context(), cache, private); err != nil {
				t.Fatal(err)
			}
			output := run("test-execution", []string{"test", "-p=1", "-parallel=2", "-count=1", "-x", "-v", "."}, private)
			if !strings.Contains(output, "TEST_ACTUALLY_EXECUTED") || strings.Contains(output, "(cached)") {
				t.Fatal("compiler reuse replaced real test execution")
			}
			wantLinks := 2
			if method == "r62-duplicate-linking" {
				wantLinks = 4
			} else {
				for _, phase := range phases[1:] {
					if phase["package_compiler_processes"] != 0 {
						t.Fatalf("matching compiler archives were not reused: %+v", phases)
					}
				}
			}
			if totalLinks != wantLinks {
				t.Fatalf("linker invocations=%d want=%d; phases=%+v", totalLinks, wantLinks, phases)
			}
			raw, err := json.Marshal(phases)
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("raw pipeline trace: %s", raw)
			if dir := os.Getenv("TEKROO_VERIFICATION_TRACE_DIR"); dir != "" {
				if err := os.WriteFile(filepath.Join(dir, method+"-receipt.json"), raw, 0600); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}
