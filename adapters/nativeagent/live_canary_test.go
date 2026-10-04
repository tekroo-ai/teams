package nativeagent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tekroo-ai/teams/agentruntime"
	"github.com/tekroo-ai/teams/agenttools"
	"github.com/tekroo-ai/teams/application"
	"github.com/tekroo-ai/teams/kernel"
)

type canaryBinding struct{ authority agenttools.Authority }

func (binding canaryBinding) BindToolInvocation(context.Context, kernel.UUIDv7, kernel.Digest) (agenttools.Authority, error) {
	return binding.authority, nil
}

type canaryEffectLedger struct {
	mu      sync.Mutex
	records map[string]agenttools.EffectRecord
}

func (ledger *canaryEffectLedger) Reserve(_ context.Context, intent agenttools.EffectRecord) (agenttools.EffectRecord, bool, error) {
	ledger.mu.Lock()
	defer ledger.mu.Unlock()
	if ledger.records == nil {
		ledger.records = make(map[string]agenttools.EffectRecord)
	}
	if stored, ok := ledger.records[intent.ToolCallID]; ok {
		if !sameCanaryIntent(stored, intent) {
			return agenttools.EffectRecord{}, false, agenttools.ErrConflict
		}
		return stored, false, nil
	}
	ledger.records[intent.ToolCallID] = intent
	return intent, true, nil
}

func (ledger *canaryEffectLedger) Lookup(_ context.Context, intent agenttools.EffectRecord) (agenttools.EffectRecord, bool, error) {
	ledger.mu.Lock()
	defer ledger.mu.Unlock()
	stored, ok := ledger.records[intent.ToolCallID]
	if ok && !sameCanaryIntent(stored, intent) {
		return agenttools.EffectRecord{}, false, agenttools.ErrConflict
	}
	return stored, ok, nil
}

func (ledger *canaryEffectLedger) Complete(_ context.Context, intent agenttools.EffectRecord, result json.RawMessage) error {
	ledger.mu.Lock()
	defer ledger.mu.Unlock()
	stored, ok := ledger.records[intent.ToolCallID]
	if !ok || !sameCanaryIntent(stored, intent) {
		return agenttools.ErrConflict
	}
	if len(stored.Result) > 0 && string(stored.Result) != string(result) {
		return agenttools.ErrConflict
	}
	stored.Result = append(json.RawMessage(nil), result...)
	ledger.records[intent.ToolCallID] = stored
	return nil
}

func sameCanaryIntent(left, right agenttools.EffectRecord) bool {
	return left.InvocationID == right.InvocationID && left.RequestDigest == right.RequestDigest &&
		left.ToolCallID == right.ToolCallID && left.Name == right.Name && left.ArgumentsSHA256 == right.ArgumentsSHA256 &&
		left.WorkspaceRoot == right.WorkspaceRoot && left.EffectPolicyHash == right.EffectPolicyHash
}

// Set both environment values explicitly to run this opt-in local-model canary.
// It creates one disposable Git repository, makes no Teams production calls,
// and does not claim to qualify the complete organizational feature workflow.
func TestLiveNativeCoderTesterCanary(t *testing.T) {
	baseURL, model := os.Getenv("TEKROO_NATIVE_CANARY_BASE_URL"), os.Getenv("TEKROO_NATIVE_CANARY_MODEL")
	if baseURL == "" || model == "" {
		t.Skip("requires an explicit local model endpoint and model identity")
	}
	testerOnly := os.Getenv("TEKROO_NATIVE_CANARY_TESTER_ONLY") == "1"
	root := t.TempDir()
	implementation := "package greeting\n\nfunc Greeting(name string) string { return \"\" }\n"
	if testerOnly {
		implementation = "package greeting\n\nfunc Greeting(name string) string { return \"Hello, \" + name + \"!\" }\n"
	}
	for name, content := range map[string]string{
		"go.mod":           "module example.test/greeting\n\ngo 1.26.0\n",
		"greeting.go":      implementation,
		"greeting_test.go": "package greeting\nimport \"testing\"\nfunc TestGreeting(t *testing.T) { if got := Greeting(\"Ada\"); got != \"Hello, Ada!\" { t.Fatalf(\"got %q\", got) } }\n",
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for _, arguments := range [][]string{{"init", "-q"}, {"add", "go.mod", "greeting.go", "greeting_test.go"}, {"-c", "user.name=Canary", "-c", "user.email=canary@example.test", "commit", "-qm", "baseline"}} {
		command := exec.Command("git", arguments...)
		command.Dir = root
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", arguments, err, output)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	client := &http.Client{Timeout: 3 * time.Minute}
	for _, stage := range []struct {
		name        string
		invocation  kernel.UUIDv7
		purpose     kernel.WorkPurpose
		actor       kernel.ActorFQN
		role        kernel.RoleFQRN
		permissions []string
		readTools   []string
		effects     []string
		instruction string
		description string
	}{
		{name: "coder", invocation: "00000000-0000-7000-8000-000000000a01", purpose: kernel.PurposeImplementation,
			actor: "teams::coder-1", role: "coder", permissions: []string{"repository.edit", "test.execute"},
			readTools: []string{"git_status", "list_files", "read_file"}, effects: []string{"git_commit", "git_stage_files", "run_go_tests_worktree", "write_file"},
			instruction: "Implement the assigned repository task using the available tools. Use run_go_tests_worktree on uncommitted source before the single candidate commit, then report the result.",
			description: "Make Greeting return Hello, followed by the supplied name and an exclamation mark. Keep the implementation simple and commit it."},
		{name: "tester", invocation: "00000000-0000-7000-8000-000000000a02", purpose: kernel.PurposeValidation,
			actor: "teams::tester-1", role: "tester", permissions: []string{"repository.read", "test.execute"},
			readTools: []string{"git_status", "read_file"}, effects: []string{"run_go_tests"},
			instruction: "Independently validate the assigned repository task using the available tools. Report the test result and any defect; do not edit the repository.",
			description: "Check that Greeting returns Hello, Ada! and run the Go tests for the committed candidate."},
	} {
		if testerOnly && stage.name == "coder" {
			continue
		}
		t.Run(stage.name, func(t *testing.T) {
			permissions := slices.Clone(stage.permissions)
			slices.Sort(permissions)
			brief, _, profile := testBriefAndProfile()
			brief.InvocationID, brief.Purpose, brief.ActorFQN = stage.invocation, stage.purpose, stage.actor
			brief.Task.Title = "Disposable greeting canary"
			brief.Task.Description = stage.description
			brief.Task.AcceptanceCriteria = []string{"Greeting(\"Ada\") returns \"Hello, Ada!\"; Go tests pass."}
			brief.RoleGrounding = application.RoleExecutionGrounding{ActorFQN: stage.actor, RoleFQRN: stage.role,
				BundleVersion: "canary-only", BundleDigest: brief.ToolPolicyDigest,
				Capabilities: []string{"qualification"}, Permissions: permissions, Instructions: stage.instruction}
			encoded, _ := json.Marshal(brief)
			sum := sha256.Sum256(encoded)
			digest := kernel.Digest(hex.EncodeToString(sum[:]))
			profile.RoleFQRN, profile.BaseURL, profile.Model = stage.role, baseURL, model
			profile.AllowedReadTools, profile.AllowedEffectTools = stage.readTools, stage.effects
			profile.MaxOutputTokens, profile.MaxTurns = 2048, 24
			binding := canaryBinding{authority: agenttools.Authority{WorkspaceRoot: root, Permissions: permissions,
				Purpose: stage.purpose, EffectPolicyDigest: brief.EffectPolicyDigest}}
			ledger := &canaryEffectLedger{}
			journal := &testJournal{}
			config := Config{Bindings: binding, Gateway: agenttools.Gateway{Bindings: binding, Host: agenttools.Host{Timeout: 2 * time.Minute}},
				Journal: journal, Effects: ledger, HTTP: client, Profile: profile}
			session, err := PrepareWithEffects(ctx, brief, digest, config)
			if err != nil {
				t.Fatal(err)
			}
			output, err := session.Run(ctx)
			if err != nil {
				for _, entry := range journal.entries {
					if entry.Kind == agentruntime.ModelTurn || entry.Kind == agentruntime.ToolDone {
						t.Logf("%s turn %d %s %s", stage.name, entry.Sequence, entry.Kind, entry.Payload)
					}
				}
				t.Fatalf("native %s session: %v", stage.name, err)
			}
			ledger.mu.Lock()
			effects := make(map[string]int)
			for _, record := range ledger.records {
				if len(record.Result) == 0 {
					ledger.mu.Unlock()
					t.Fatalf("unresolved %s effect %s", stage.name, record.Name)
				}
				effects[record.Name]++
			}
			ledger.mu.Unlock()
			if stage.name == "coder" && (effects["write_file"] == 0 || effects["run_go_tests_worktree"] == 0 || effects["git_stage_files"] == 0 || effects["git_commit"] != 1) {
				t.Fatalf("coder effects = %v", effects)
			}
			if stage.name == "tester" && effects["run_go_tests"] == 0 {
				t.Fatalf("tester did not execute Go tests: %v", effects)
			}
			t.Logf("%s completed: %s", stage.name, strings.TrimSpace(output))
		})
		if t.Failed() {
			return
		}
	}
	git := exec.Command("git", "status", "--porcelain=v1")
	git.Dir = root
	status, err := git.CombinedOutput()
	if err != nil || len(status) != 0 {
		t.Fatalf("candidate workspace not clean: %q, %v", status, err)
	}
	git = exec.Command("git", "rev-list", "--count", "HEAD")
	git.Dir = root
	count, err := git.CombinedOutput()
	wantCommits := "2"
	if testerOnly {
		wantCommits = "1"
	}
	if err != nil || strings.TrimSpace(string(count)) != wantCommits {
		t.Fatalf("unexpected candidate commit count: %q, %v", count, err)
	}
	content, err := os.ReadFile(filepath.Join(root, "greeting.go"))
	if err != nil || !strings.Contains(string(content), "Hello, ") {
		t.Fatalf("candidate implementation not found: %q, %v", content, err)
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		t.Fatal("qualification exceeded its deadline")
	}
}
