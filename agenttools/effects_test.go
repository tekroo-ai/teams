package agenttools

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/tekroo-ai/teams/kernel"
)

type effectBinding struct{ authority Authority }

func (binding effectBinding) BindToolInvocation(context.Context, kernel.UUIDv7, kernel.Digest) (Authority, error) {
	return binding.authority, nil
}

type memoryEffectLedger struct {
	mu           sync.Mutex
	records      map[string]EffectRecord
	failComplete bool
	failReserve  bool
}

func (ledger *memoryEffectLedger) Reserve(_ context.Context, intent EffectRecord) (EffectRecord, bool, error) {
	ledger.mu.Lock()
	defer ledger.mu.Unlock()
	if ledger.records == nil {
		ledger.records = make(map[string]EffectRecord)
	}
	key := string(intent.InvocationID) + ":" + intent.ToolCallID
	if old, found := ledger.records[key]; found {
		if old.RequestDigest != intent.RequestDigest || old.Name != intent.Name || old.ArgumentsSHA256 != intent.ArgumentsSHA256 || old.WorkspaceRoot != intent.WorkspaceRoot || old.EffectPolicyHash != intent.EffectPolicyHash {
			return EffectRecord{}, false, ErrConflict
		}
		return old, false, nil
	}
	ledger.records[key] = intent
	if ledger.failReserve {
		ledger.failReserve = false
		return EffectRecord{}, false, errors.New("simulated lost reserve acknowledgment")
	}
	return intent, true, nil
}

func (ledger *memoryEffectLedger) Lookup(_ context.Context, intent EffectRecord) (EffectRecord, bool, error) {
	ledger.mu.Lock()
	defer ledger.mu.Unlock()
	old, found := ledger.records[string(intent.InvocationID)+":"+intent.ToolCallID]
	if !found {
		return intent, false, nil
	}
	if old.RequestDigest != intent.RequestDigest || old.Name != intent.Name || old.ArgumentsSHA256 != intent.ArgumentsSHA256 || old.WorkspaceRoot != intent.WorkspaceRoot || old.EffectPolicyHash != intent.EffectPolicyHash {
		return EffectRecord{}, false, ErrConflict
	}
	return old, true, nil
}

func (ledger *memoryEffectLedger) Complete(_ context.Context, intent EffectRecord, result json.RawMessage) error {
	ledger.mu.Lock()
	defer ledger.mu.Unlock()
	if ledger.failComplete {
		ledger.failComplete = false
		return errors.New("simulated lost effect acknowledgment")
	}
	key := string(intent.InvocationID) + ":" + intent.ToolCallID
	old, found := ledger.records[key]
	if !found || old.ArgumentsSHA256 != intent.ArgumentsSHA256 {
		return ErrConflict
	}
	if len(old.Result) > 0 && string(old.Result) != string(result) {
		return ErrConflict
	}
	old.Result = append(json.RawMessage(nil), result...)
	ledger.records[key] = old
	return nil
}

func effectFixture(t *testing.T) (MutationGateway, *memoryEffectLedger, string, Request) {
	t.Helper()
	root := t.TempDir()
	digest := kernel.Digest("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	ledger := &memoryEffectLedger{}
	gateway := MutationGateway{
		Bindings: effectBinding{authority: Authority{WorkspaceRoot: root, Permissions: []string{"repository.edit"}, Purpose: kernel.PurposeImplementation, EffectPolicyDigest: digest}},
		Host:     Host{Timeout: time.Second}, Ledger: ledger, Policy: digest,
	}
	request := Request{InvocationID: kernel.UUIDv7("00000000-0000-7000-8000-000000000981"), RequestDigest: digest, ToolCallID: "write-1", Call: Call{
		Name: "write_file", Arguments: json.RawMessage(`{"path":"note.txt","content":"new content","expected_sha256":""}`),
	}}
	return gateway, ledger, root, request
}

func TestMutationGatewayReconcilesWriteAfterLostReceipt(t *testing.T) {
	gateway, ledger, root, request := effectFixture(t)
	ledger.failComplete = true
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, err := gateway.Execute(ctx, request); !errors.Is(err, ErrEffectUncertain) {
		t.Fatalf("lost receipt error = %v", err)
	}
	content, err := os.ReadFile(filepath.Join(root, "note.txt"))
	if err != nil || string(content) != "new content" {
		t.Fatalf("physical effect = %q, %v", content, err)
	}
	receipt, err := gateway.Execute(ctx, request)
	if err != nil || receipt.Result.Name != "write_file" || receipt.Result.SHA256 == "" {
		t.Fatalf("reconciled receipt = %+v, %v", receipt, err)
	}
	again, err := gateway.Execute(ctx, request)
	if err != nil || again.ResultHash != receipt.ResultHash {
		t.Fatalf("duplicate receipt = %+v, %v", again, err)
	}
	request.Call.Arguments = json.RawMessage(`{"path":"other.txt","content":"new content","expected_sha256":""}`)
	if _, err := gateway.Execute(ctx, request); !errors.Is(err, ErrConflict) {
		t.Fatalf("same call id with different arguments = %v", err)
	}
}

func TestMutationGatewayRejectsDriftAndForbiddenPaths(t *testing.T) {
	gateway, ledger, root, request := effectFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	gateway.Policy = kernel.Digest("bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
	if _, err := gateway.Execute(ctx, request); !errors.Is(err, ErrForbidden) {
		t.Fatalf("policy drift = %v", err)
	}
	gateway.Policy = kernel.Digest("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	gateway.Bindings = effectBinding{authority: Authority{WorkspaceRoot: root, Permissions: []string{"repository.edit"}, Purpose: kernel.PurposeReview, EffectPolicyDigest: gateway.Policy}}
	if _, err := gateway.Execute(ctx, request); !errors.Is(err, ErrForbidden) {
		t.Fatalf("review-purpose mutation = %v", err)
	}
	gateway.Bindings = effectBinding{authority: Authority{WorkspaceRoot: root, Permissions: []string{"repository.read"}, Purpose: kernel.PurposeImplementation, EffectPolicyDigest: gateway.Policy}}
	if _, err := gateway.Execute(ctx, request); !errors.Is(err, ErrForbidden) {
		t.Fatalf("read-only role mutation = %v", err)
	}
	gateway.Bindings = effectBinding{authority: Authority{WorkspaceRoot: root, Permissions: []string{"repository.edit"}, Purpose: kernel.PurposeImplementation, EffectPolicyDigest: gateway.Policy}}
	request.Call.Arguments = json.RawMessage(`{"path":".openhands/agent.yaml","content":"malicious","expected_sha256":""}`)
	if _, err := gateway.Execute(ctx, request); !errors.Is(err, ErrForbidden) {
		t.Fatalf("runtime hook write = %v", err)
	}
	request.Call.Arguments = json.RawMessage(`{"path":"note.txt","content":"new content","expected_sha256":""}`)
	if err := os.WriteFile(filepath.Join(root, "note.txt"), []byte("prior"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := gateway.Execute(ctx, request); !errors.Is(err, ErrConflict) {
		t.Fatalf("preimage mismatch = %v", err)
	}
	if len(ledger.records) != 0 {
		t.Fatalf("precondition conflict left an effect intent: %+v", ledger.records)
	}
	prior := sha256.Sum256([]byte("prior"))
	request.ToolCallID = "write-2"
	request.Call.Arguments, _ = json.Marshal(map[string]string{"path": "note.txt", "content": "new content", "expected_sha256": hex.EncodeToString(prior[:])})
	if _, err := gateway.Execute(ctx, request); err != nil {
		t.Fatalf("matching preimage = %v", err)
	}
}

func TestReconcileWriteNeverStartsUnreservedEffect(t *testing.T) {
	gateway, ledger, root, request := effectFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, applied, err := gateway.ReconcileWrite(ctx, request); err != nil || applied {
		t.Fatalf("unreserved reconciliation = applied %t, err %v", applied, err)
	}
	if _, err := os.Stat(filepath.Join(root, "note.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unreserved write touched file: %v", err)
	}
	if len(ledger.records) != 0 {
		t.Fatalf("unreserved reconciliation created ledger intent: %+v", ledger.records)
	}
}

func TestReconcileWriteCompletesAppliedEffectWithoutRewriting(t *testing.T) {
	gateway, ledger, root, request := effectFixture(t)
	ledger.failComplete = true
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, err := gateway.Execute(ctx, request); !errors.Is(err, ErrEffectUncertain) {
		t.Fatalf("lost receipt = %v", err)
	}
	receipt, applied, err := gateway.ReconcileWrite(ctx, request)
	if err != nil || !applied || receipt.Result.SHA256 == "" {
		t.Fatalf("applied reconciliation = %+v, %t, %v", receipt, applied, err)
	}
	content, err := os.ReadFile(filepath.Join(root, "note.txt"))
	if err != nil || string(content) != "new content" {
		t.Fatalf("physical content = %q, %v", content, err)
	}
}

func TestReconcileWriteRecognizesReservedButUnapplied(t *testing.T) {
	gateway, ledger, root, request := effectFixture(t)
	canonical, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	argumentsHash := sha256.Sum256(request.Call.Arguments)
	intent := EffectRecord{InvocationID: request.InvocationID, RequestDigest: request.RequestDigest, ToolCallID: request.ToolCallID,
		Name: request.Call.Name, ArgumentsSHA256: hex.EncodeToString(argumentsHash[:]), WorkspaceRoot: canonical, EffectPolicyHash: gateway.Policy}
	if _, _, err := ledger.Reserve(context.Background(), intent); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, applied, err := gateway.ReconcileWrite(ctx, request); err != nil || applied {
		t.Fatalf("unapplied reconciliation = applied %t, err %v", applied, err)
	}
	if _, err := os.Stat(filepath.Join(root, "note.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("reconciliation wrote unstarted effect: %v", err)
	}
}

func TestLostReserveAcknowledgmentStopsTurnWithoutWriting(t *testing.T) {
	gateway, ledger, root, request := effectFixture(t)
	ledger.failReserve = true
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := gateway.Execute(ctx, request); !errors.Is(err, ErrEffectUncertain) {
		t.Fatalf("ambiguous reserve = %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "note.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("write after lost reserve acknowledgment: %v", err)
	}
	if _, applied, err := gateway.ReconcileWrite(ctx, request); err != nil || applied {
		t.Fatalf("reserve-only reconciliation = applied %t, err %v", applied, err)
	}
}
