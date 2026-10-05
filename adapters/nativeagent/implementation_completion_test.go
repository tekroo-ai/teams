package nativeagent

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/tekroo-ai/teams/agenttools"
	"github.com/tekroo-ai/teams/application"
	"github.com/tekroo-ai/teams/kernel"
)

type completionBinding struct {
	root   string
	policy kernel.Digest
}

func (b completionBinding) BindToolInvocation(context.Context, kernel.UUIDv7, kernel.Digest) (agenttools.Authority, error) {
	return agenttools.Authority{WorkspaceRoot: b.root, Permissions: []string{"repository.read", "repository.edit", "test.execute"}, Purpose: kernel.PurposeImplementation, EffectPolicyDigest: b.policy}, nil
}

type completionLedger struct {
	records  map[string]agenttools.EffectRecord
	reserves int
	lose     bool
}

func (l *completionLedger) Reserve(_ context.Context, r agenttools.EffectRecord) (agenttools.EffectRecord, bool, error) {
	if v, ok := l.records[r.ToolCallID]; ok {
		return v, false, nil
	}
	l.records[r.ToolCallID] = r
	l.reserves++
	return r, true, nil
}
func (l *completionLedger) Lookup(_ context.Context, r agenttools.EffectRecord) (agenttools.EffectRecord, bool, error) {
	v, ok := l.records[r.ToolCallID]
	return v, ok, nil
}
func (l *completionLedger) Complete(_ context.Context, r agenttools.EffectRecord, b json.RawMessage) error {
	if l.lose {
		return errors.New("lost completion acknowledgement")
	}
	r.Result = append(json.RawMessage(nil), b...)
	l.records[r.ToolCallID] = r
	return nil
}

func TestImplementationCompletionUsesCommittedBuildEvidence(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("qualified macOS sandbox")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	root := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		c := exec.CommandContext(ctx, "git", args...)
		c.Dir = root
		b, err := c.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v %s", args, err, b)
		}
		return strings.TrimSpace(string(b))
	}
	write := func(path, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, path), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	commit := func() string {
		t.Helper()
		git("add", ".")
		git("-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-qm", "candidate")
		return git("rev-parse", "HEAD")
	}
	git("init", "-q")
	write("go.mod", "module example.test/completion\n\ngo 1.24\n")
	write("a.go", "package completion\nvar Shared=1\n")
	baseline := commit()
	brief, digest, _ := testBriefAndProfile()
	brief.Purpose = kernel.PurposeImplementation
	brief.Scope.BaselineSHA = baseline
	brief.Scope.Branch = git("branch", "--show-current")
	brief.WorkProfile.RequiredDeterministicGateIDs = []string{"go-test"}
	binding := completionBinding{root, brief.EffectPolicyDigest}
	ledger := &completionLedger{records: map[string]agenttools.EffectRecord{}}
	goBinary, err := exec.LookPath("go")
	if err != nil {
		t.Fatal(err)
	}
	gateway := agenttools.Gateway{Bindings: binding, Host: agenttools.Host{GoBinary: goBinary, Timeout: time.Minute}}
	checks := 0
	check := implementationCompletionCheck(brief, digest, gateway, ledger, func(context.Context, []byte) error { checks++; return nil })
	complete := []byte(application.OrganizationalResultMarker + `{"outcome":"completed"}`)
	blocked := []byte(application.OrganizationalResultMarker + `{"outcome":"blocked"}`)
	if err := check(ctx, complete); err == nil || !strings.Contains(err.Error(), "successor commit") {
		t.Fatalf("baseline accepted: %v", err)
	}
	if err := check(ctx, blocked); err != nil || ledger.reserves != 0 {
		t.Fatalf("blocked result requires build: %v", err)
	}
	write("b.go", "package completion\nvar Shared=2\n")
	bad := commit()
	first := check(ctx, complete)
	if first == nil || !strings.Contains(first.Error(), "redeclared") {
		t.Fatalf("duplicate declaration advanced: %v", first)
	}
	second := check(ctx, complete)
	if second == nil || second.Error() != first.Error() || ledger.reserves != 1 {
		t.Fatalf("failed commit repeated or evidence lost: %v reserves=%d", second, ledger.reserves)
	}
	if !strings.Contains(first.Error(), bad) {
		t.Fatal("compiler failure not bound to candidate")
	}
	write("b.go", "package completion\nvar Other=2\n")
	write("a_test.go", "package completion\nimport \"testing\"\nfunc TestNotExecuted(t *testing.T){t.Fatal(\"handoff compiles; full validation runs elsewhere\")}\n")
	commit()
	if err := check(ctx, complete); err != nil {
		t.Fatalf("repaired candidate rejected: %v", err)
	}
	if err := check(ctx, complete); err != nil || ledger.reserves != 2 {
		t.Fatalf("successful compile repeated: %v reserves=%d", err, ledger.reserves)
	}
	write("b.go", "package completion\nvar Uncommitted=3\n")
	if err := check(ctx, complete); err == nil || ledger.reserves != 2 {
		t.Fatalf("dirty candidate accepted: %v", err)
	}
	if checks != 7 {
		t.Fatalf("existing final validator lost: %d", checks)
	}
}

func TestImplementationCompletionDoesNotReplayUnknownBuild(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("qualified macOS sandbox")
	}
	root := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	git := func(args ...string) string {
		t.Helper()
		c := exec.CommandContext(ctx, "git", args...)
		c.Dir = root
		b, err := c.CombinedOutput()
		if err != nil {
			t.Fatalf("git: %v %s", err, b)
		}
		return strings.TrimSpace(string(b))
	}
	git("init", "-q")
	os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.test/completion\n\ngo 1.24\n"), 0600)
	os.WriteFile(filepath.Join(root, "a.go"), []byte("package completion\n"), 0600)
	git("add", ".")
	git("-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-qm", "candidate")
	brief, digest, _ := testBriefAndProfile()
	binding := completionBinding{root, brief.EffectPolicyDigest}
	ledger := &completionLedger{records: map[string]agenttools.EffectRecord{}, lose: true}
	goBinary, _ := exec.LookPath("go")
	gateway := agenttools.BuildGateway{Bindings: binding, Host: agenttools.Host{GoBinary: goBinary, Timeout: 30 * time.Second}, Ledger: ledger, Policy: brief.EffectPolicyDigest}
	head := git("rev-parse", "HEAD")
	if _, err := gateway.Verify(ctx, brief.InvocationID, digest, head); !errors.Is(err, agenttools.ErrEffectUncertain) {
		t.Fatalf("lost receipt accepted: %v", err)
	}
	ledger.lose = false
	if _, err := gateway.Verify(ctx, brief.InvocationID, digest, head); !errors.Is(err, agenttools.ErrEffectUncertain) || ledger.reserves != 1 {
		t.Fatalf("unknown build replayed: %v", err)
	}
}
