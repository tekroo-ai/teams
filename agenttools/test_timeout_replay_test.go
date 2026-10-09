package agenttools

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/tekroo-ai/teams/kernel"
)

// Seed a completed timeout receipt to exercise the real gateway's replay and
// reconciliation, without starting another process for an already-known call.
func TestTestGatewayReplaysGoInternalTimeoutAsKnownExecutionFailure(t *testing.T) {
	root := t.TempDir()
	writeSnapshotFixture(t, root, "go.mod", "module example.test/timeout-replay\n\ngo 1.26\n")
	root, err := canonicalRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	digest := kernel.Digest(strings.Repeat("a", 64))
	ledger := &memoryEffectLedger{}
	gateway := TestGateway{Bindings: effectBinding{authority: Authority{WorkspaceRoot: root,
		Permissions: []string{"repository.read", "test.execute"}, Purpose: kernel.PurposeValidation, EffectPolicyDigest: digest}},
		Host: Host{Timeout: time.Second}, Ledger: ledger, Policy: digest}
	request := Request{InvocationID: "00000000-0000-7000-8000-000000000993", RequestDigest: digest,
		ToolCallID: "internal-timeout", Call: Call{Name: "run_go_tests", Arguments: json.RawMessage(`{"package":"./..."}`)}}
	argsHash := sha256.Sum256(request.Call.Arguments)
	intent := EffectRecord{InvocationID: request.InvocationID, RequestDigest: digest, ToolCallID: request.ToolCallID,
		Name: request.Call.Name, ArgumentsSHA256: hex.EncodeToString(argsHash[:]), WorkspaceRoot: root, EffectPolicyHash: digest}
	if _, _, err := ledger.Reserve(t.Context(), intent); err != nil {
		t.Fatal(err)
	}
	actualExit := 1
	result := Result{Name: request.Call.Name, ExitCode: -1, ProcessExitCode: &actualExit,
		CommitSHA: strings.Repeat("b", 40), Output: "panic: test timed out after 10m0s\n",
		ExecutionError:  "Go test execution timed out; candidate verdict INCONCLUSIVE",
		TimeoutEvidence: "panic: test timed out after 10m0s"}
	result.SHA256 = contentSHA([]byte(result.Output))
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if err := ledger.Complete(t.Context(), intent, encoded); err != nil {
		t.Fatal(err)
	}
	replayed, err := gateway.Execute(t.Context(), request)
	if err != nil || replayed.Result.ExitCode != -1 || replayed.Result.ProcessExitCode == nil || *replayed.Result.ProcessExitCode != 1 || replayed.Result.TimeoutEvidence != result.TimeoutEvidence {
		t.Fatalf("known timeout became uncertain or lost evidence: %+v %v", replayed, err)
	}
	reconciled, found, err := gateway.Reconcile(t.Context(), request)
	if err != nil || !found || reconciled.ResultHash != replayed.ResultHash {
		t.Fatalf("timeout reconciliation: %+v %t %v", reconciled, found, err)
	}
}
