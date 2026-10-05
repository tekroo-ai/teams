package agenttools

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"

	"github.com/tekroo-ai/teams/kernel"
)

// TestGateway journals the exact test call before launching candidate code.
// If a process disappears before its receipt is committed, replay stops at an
// uncertain outcome instead of silently starting a second physical run.
type TestGateway struct {
	Bindings BindingSource
	Host     Host
	Ledger   EffectLedger
	Policy   kernel.Digest
}

func (gateway TestGateway) Execute(ctx context.Context, request Request) (Receipt, error) {
	if gateway.Bindings == nil || gateway.Ledger == nil || gateway.Host.Timeout <= 0 || !gateway.Policy.Valid() ||
		!request.InvocationID.Valid() || !request.RequestDigest.Valid() || strings.TrimSpace(request.ToolCallID) == "" || len(request.ToolCallID) > 256 ||
		!isGoTestTool(request.Call.Name) {
		return Receipt{}, ErrInvalidCall
	}
	var args struct {
		Package string `json:"package"`
	}
	if decode(request.Call.Arguments, &args) != nil {
		return Receipt{}, ErrInvalidCall
	}
	operation, cancel := context.WithTimeout(ctx, gateway.Host.Timeout)
	defer cancel()
	authority, err := gateway.Bindings.BindToolInvocation(operation, request.InvocationID, request.RequestDigest)
	if err != nil {
		return Receipt{}, err
	}
	if authority.EffectPolicyDigest != gateway.Policy || !permitted(authority.Permissions, "test.execute") ||
		!goTestPurposeAllowed(request.Call.Name, authority.Purpose) {
		return Receipt{}, ErrForbidden
	}
	root, err := canonicalRoot(authority.WorkspaceRoot)
	if err != nil {
		return Receipt{}, err
	}
	if !validGoPackageInModule(root, args.Package) {
		return Receipt{}, ErrInvalidCall
	}
	argumentsHash := sha256.Sum256(request.Call.Arguments)
	intent := EffectRecord{InvocationID: request.InvocationID, RequestDigest: request.RequestDigest, ToolCallID: request.ToolCallID,
		Name: request.Call.Name, ArgumentsSHA256: hex.EncodeToString(argumentsHash[:]), WorkspaceRoot: root, EffectPolicyHash: gateway.Policy}
	stored, created, err := gateway.Ledger.Reserve(operation, intent)
	if err != nil {
		return Receipt{}, errors.Join(ErrEffectUncertain, err)
	}
	if !created {
		return testEffectReceipt(request, intent.ArgumentsSHA256, stored)
	}
	// Host returns only after preparation or the bounded process has ended.
	// Known failures must be durable too; only a lost receipt is uncertain.
	result, err := gateway.Host.execute(operation, authority, request.Call)
	if err != nil {
		result = failedTestResult(request.Call.Name, result, err)
	}
	return completeEffect(gateway.Ledger, request, intent, result)
}

func (gateway TestGateway) Reconcile(ctx context.Context, request Request) (Receipt, bool, error) {
	if gateway.Bindings == nil || gateway.Ledger == nil || !gateway.Policy.Valid() || !request.InvocationID.Valid() ||
		!request.RequestDigest.Valid() || !isGoTestTool(request.Call.Name) || strings.TrimSpace(request.ToolCallID) == "" {
		return Receipt{}, false, ErrInvalidCall
	}
	var args struct {
		Package string `json:"package"`
	}
	if decode(request.Call.Arguments, &args) != nil {
		return Receipt{}, false, ErrInvalidCall
	}
	authority, err := gateway.Bindings.BindToolInvocation(ctx, request.InvocationID, request.RequestDigest)
	if err != nil {
		return Receipt{}, false, err
	}
	if authority.EffectPolicyDigest != gateway.Policy || !permitted(authority.Permissions, "test.execute") ||
		!goTestPurposeAllowed(request.Call.Name, authority.Purpose) {
		return Receipt{}, false, ErrForbidden
	}
	root, err := canonicalRoot(authority.WorkspaceRoot)
	if err != nil {
		return Receipt{}, false, err
	}
	if !validGoPackageInModule(root, args.Package) {
		return Receipt{}, false, ErrInvalidCall
	}
	argumentsHash := sha256.Sum256(request.Call.Arguments)
	intent := EffectRecord{InvocationID: request.InvocationID, RequestDigest: request.RequestDigest, ToolCallID: request.ToolCallID,
		Name: request.Call.Name, ArgumentsSHA256: hex.EncodeToString(argumentsHash[:]), WorkspaceRoot: root, EffectPolicyHash: gateway.Policy}
	stored, found, err := gateway.Ledger.Lookup(ctx, intent)
	if err != nil {
		return Receipt{}, false, errors.Join(ErrEffectUncertain, err)
	}
	if !found {
		return Receipt{}, false, nil
	}
	receipt, err := testEffectReceipt(request, intent.ArgumentsSHA256, stored)
	if err != nil {
		return Receipt{}, false, err
	}
	return receipt, true, nil
}

func testEffectReceipt(request Request, argumentsHash string, stored EffectRecord) (Receipt, error) {
	if len(stored.Result) == 0 {
		return Receipt{}, ErrEffectUncertain
	}
	var result Result
	if json.Unmarshal(stored.Result, &result) != nil || result.Name != request.Call.Name || !validExpectedSHA(result.SHA256) {
		return Receipt{}, ErrEffectUncertain
	}
	if result.ExecutionError != "" {
		if !validFailedEffectResult(request.Call.Name, result) ||
			result.CommitSHA != "" && !validFullGitSHA(result.CommitSHA) ||
			result.SnapshotSHA256 != "" && !validExpectedSHA(result.SnapshotSHA256) {
			return Receipt{}, ErrEffectUncertain
		}
	} else if !validFullGitSHA(result.CommitSHA) ||
		request.Call.Name == "run_go_tests_worktree" && !validExpectedSHA(result.SnapshotSHA256) ||
		request.Call.Name == "run_go_tests" && result.SnapshotSHA256 != "" {
		return Receipt{}, ErrEffectUncertain
	}
	return effectReceipt(request, argumentsHash, result)
}

func failedTestResult(name string, result Result, err error) Result {
	return failedEffectResult(name, result, err)
}

func isGoTestTool(name string) bool {
	return name == "run_go_tests" || name == "run_go_tests_worktree"
}

func goTestPurposeAllowed(name string, purpose kernel.WorkPurpose) bool {
	if name == "run_go_tests_worktree" {
		return purpose == kernel.PurposeImplementation || purpose == kernel.PurposeRepair
	}
	return name == "run_go_tests" && (purpose == kernel.PurposeValidation || purpose == kernel.PurposeImplementation || purpose == kernel.PurposeRepair)
}
