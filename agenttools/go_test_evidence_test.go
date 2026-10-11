package agenttools

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/tekroo-ai/teams/kernel"
	"testing"
	"time"
)

func TestGoTestEvidenceDistinguishesExecutionSkipAndIncomplete(t *testing.T) {
	for _, tc := range []struct {
		name, events, status string
		complete             bool
	}{
		{"pass", `{"Action":"run","Package":"p","Test":"TestOne"}` + "\n" + `{"Action":"pass","Package":"p","Test":"TestOne"}` + "\n", "PASS", true},
		{"skip", `{"Action":"run","Package":"p","Test":"TestOne"}` + "\n" + `{"Action":"skip","Package":"p","Test":"TestOne"}` + "\n", "SKIP", true},
		{"fail", `{"Action":"run","Package":"p","Test":"TestOne"}` + "\n" + `{"Action":"fail","Package":"p","Test":"TestOne"}` + "\n", "FAIL", true},
		{"unfinished", `{"Action":"run","Package":"p","Test":"TestOne"}` + "\n", "RUNNING", false},
		{"missing-run", `{"Action":"pass","Package":"p","Test":"TestOne"}` + "\n", "PASS", false},
		{"malformed", "not json\n", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			w := goTestObservations{output: &out, tests: map[string]GoTestCase{}, complete: true}
			// Exercise fragmented event delivery, not only complete lines.
			for _, b := range []byte(tc.events) {
				if _, err := w.Write([]byte{b}); err != nil {
					t.Fatal(err)
				}
			}
			e := &GoTestEvidence{}
			w.finish(e)
			if e.Complete != tc.complete || tc.status != "" && (len(e.Tests) != 1 || e.Tests[0].Status != tc.status) {
				t.Fatalf("unexpected evidence: %+v", e)
			}
		})
	}
}

func TestGoTestArgumentsRejectUnqualifiedTags(t *testing.T) {
	for _, args := range []goTestArguments{
		{Tags: []string{"native_qualification"}}, {Tags: []string{"soak"}},
		{Tags: []string{"mongo_integration", "mongo_integration"}},
	} {
		if args.valid() {
			t.Fatalf("accepted invalid authority: %+v", args)
		}
	}
	if !(goTestArguments{Tags: []string{"mongo_integration"}}).valid() {
		t.Fatal("qualified selection rejected")
	}
}

func TestGoTestEvidenceDistinguishesTaggedAndSkippedExecution(t *testing.T) {
	root := t.TempDir()
	writeSnapshotFixture(t, root, "go.mod", "module example.test/evidence\n\ngo 1.26.0\n")
	writeSnapshotFixture(t, root, "plain_test.go", "package evidence\nimport \"testing\"\nfunc TestPlain(t *testing.T) {}\nfunc TestSkipped(t *testing.T) { t.Skip(\"fixture\") }\n")
	writeSnapshotFixture(t, root, "tagged_test.go", "//go:build mongo_integration\n\npackage evidence\nimport \"testing\"\nfunc TestTagged(t *testing.T) {}\n")
	fixtureGit(t, root, "init", "-q")
	fixtureGit(t, root, "add", ".")
	fixtureGit(t, root, "-c", "user.name=Test", "-c", "user.email=test@example.test", "commit", "-qm", "fixture")
	host := cachedFixtureHost(t, Host{Timeout: 2 * time.Minute, LocalTestExecution: true})
	run := func(args goTestArguments) Result {
		t.Helper()
		args.Package = "."
		encoded, err := json.Marshal(args)
		if err != nil {
			t.Fatal(err)
		}
		r, err := host.execute(context.Background(), Authority{WorkspaceRoot: root, Permissions: []string{"repository.read", "test.execute"}, Purpose: kernel.PurposeImplementation}, Call{Name: "run_go_tests", Arguments: encoded})
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	plain := run(goTestArguments{})
	if plain.ExitCode != 0 || plain.TestEvidence == nil || !plain.TestEvidence.Complete || len(plain.TestEvidence.Excluded) != 1 || plain.TestEvidence.Excluded[0].Name != "TestTagged" {
		t.Fatalf("excluded test manufactured coverage: %+v", plain)
	}
	tagged := run(goTestArguments{Tags: []string{"mongo_integration"}})
	if tagged.ExitCode != 0 || tagged.ExecutionError != "" || !tagged.TestEvidence.Complete || len(tagged.TestEvidence.Excluded) != 0 || len(tagged.TestEvidence.Tests) != 3 {
		t.Fatalf("tagged execution: %+v", tagged)
	}
	for _, c := range tagged.TestEvidence.Tests {
		if c.Name == "TestSkipped" && c.Status != "SKIP" {
			t.Fatal("skipped test counted as execution proof")
		}
	}
	encoded, err := json.Marshal(tagged)
	if err != nil {
		t.Fatal(err)
	}
	var restored Result
	if json.Unmarshal(encoded, &restored) != nil || restored.TestEvidence == nil || len(restored.TestEvidence.Tests) != 3 {
		t.Fatal("evidence lost in durable result round trip")
	}
}
