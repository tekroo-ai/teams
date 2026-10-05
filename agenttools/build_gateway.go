package agenttools

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/tekroo-ai/teams/kernel"
)

// BuildGateway is a deterministic handoff check, not a model-facing tool.
// Receipts bind to committed source; a replay cannot rerun an uncertain check.
type BuildGateway struct {
	Bindings BindingSource
	Host     Host
	Ledger   EffectLedger
	Policy   kernel.Digest
}

func (gateway BuildGateway) Verify(ctx context.Context, id kernel.UUIDv7, digest kernel.Digest, commit string) (Result, error) {
	if gateway.Bindings == nil || gateway.Ledger == nil || gateway.Host.Timeout <= 0 || !gateway.Policy.Valid() || !id.Valid() || !digest.Valid() || !validFullGitSHA(commit) {
		return Result{}, ErrInvalidCall
	}
	operation, cancel := context.WithTimeout(ctx, gateway.Host.Timeout)
	defer cancel()
	authority, err := gateway.Bindings.BindToolInvocation(operation, id, digest)
	if err != nil {
		return Result{}, err
	}
	if authority.EffectPolicyDigest != gateway.Policy || !permitted(authority.Permissions, "test.execute") || authority.Purpose != kernel.PurposeImplementation && authority.Purpose != kernel.PurposeRepair {
		return Result{}, ErrForbidden
	}
	root, err := canonicalRoot(authority.WorkspaceRoot)
	if err != nil {
		return Result{}, err
	}
	head, err := gateway.Host.gitValue(operation, root, "rev-parse", "HEAD")
	if err != nil || head != commit {
		return Result{}, errors.Join(ErrConflict, err)
	}
	args, _ := json.Marshal(struct {
		Commit string `json:"commit_sha"`
	}{commit})
	sum := sha256.Sum256(args)
	request := Request{InvocationID: id, RequestDigest: digest, ToolCallID: "completion-build-" + commit, Call: Call{Name: "verify_go_build", Arguments: args}}
	intent := EffectRecord{InvocationID: id, RequestDigest: digest, ToolCallID: request.ToolCallID, Name: request.Call.Name, ArgumentsSHA256: hex.EncodeToString(sum[:]), WorkspaceRoot: root, EffectPolicyHash: gateway.Policy}
	stored, created, err := gateway.Ledger.Reserve(operation, intent)
	if err != nil {
		return Result{}, errors.Join(ErrEffectUncertain, err)
	}
	if !created {
		var result Result
		if len(stored.Result) == 0 || json.Unmarshal(stored.Result, &result) != nil || result.Name != request.Call.Name || result.CommitSHA != commit || !validExpectedSHA(result.SHA256) || result.ExecutionError != "" && !validFailedEffectResult(request.Call.Name, result) {
			return Result{}, ErrEffectUncertain
		}
		outputHash := sha256.Sum256([]byte(result.Output))
		if result.SHA256 != hex.EncodeToString(outputHash[:]) {
			return Result{}, ErrEffectUncertain
		}
		return result, nil
	}
	result, err := gateway.Host.runIsolatedGoTestsFrom(operation, root, "./...", false, true)
	if err != nil {
		result = failedEffectResult(request.Call.Name, result, err)
	}
	// A changed HEAD is not permission to attest a different candidate.
	if result.CommitSHA != "" && result.CommitSHA != commit {
		return Result{}, fmt.Errorf("%w: candidate changed during build", ErrEffectUncertain)
	}
	if result.CommitSHA == "" {
		result.CommitSHA = commit
	}
	receipt, err := completeEffect(gateway.Ledger, request, intent, result)
	return receipt.Result, err
}
