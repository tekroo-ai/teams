package agenttools

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/tekroo-ai/teams/kernel"
)

// ErrEffectUncertain stops the turn rather than recording a tool error. The
// durable intent may have reached the filesystem, so the same call must be
// reconciled on restart before the model is allowed another turn.
var ErrEffectUncertain = errors.New("agent tool effect outcome is uncertain")

type EffectRecord struct {
	InvocationID     kernel.UUIDv7
	RequestDigest    kernel.Digest
	ToolCallID       string
	Name             string
	ArgumentsSHA256  string
	WorkspaceRoot    string
	EffectPolicyHash kernel.Digest
	Result           json.RawMessage
}

type EffectLedger interface {
	// Reserve returns the durable record and whether this caller created it.
	// Existing records must match every identity field exactly.
	Reserve(context.Context, EffectRecord) (EffectRecord, bool, error)
	// Lookup never creates an intent. Cancellation uses it to distinguish a
	// call that never began from a write that must first be reconciled.
	Lookup(context.Context, EffectRecord) (EffectRecord, bool, error)
	Complete(context.Context, EffectRecord, json.RawMessage) error
}

// MutationGateway is separate from the read-only gateway. Writes, staging and
// commits reserve their identities before acting and retain their outcomes.
type MutationGateway struct {
	Bindings BindingSource
	Host     Host
	Ledger   EffectLedger
	Policy   kernel.Digest
}

func (gateway MutationGateway) Execute(ctx context.Context, request Request) (Receipt, error) {
	if request.Call.Name == "git_stage_files" || request.Call.Name == "git_commit" {
		return gateway.executeGitEffect(ctx, request)
	}
	if gateway.Bindings == nil || gateway.Ledger == nil || gateway.Host.Timeout <= 0 || !gateway.Policy.Valid() || !request.InvocationID.Valid() || !request.RequestDigest.Valid() || strings.TrimSpace(request.ToolCallID) == "" || len(request.ToolCallID) > 256 {
		return Receipt{}, ErrInvalidCall
	}
	if request.Call.Name != "write_file" {
		return Receipt{}, ErrForbidden
	}
	operation, cancel := context.WithTimeout(ctx, gateway.Host.Timeout)
	defer cancel()
	authority, err := gateway.Bindings.BindToolInvocation(operation, request.InvocationID, request.RequestDigest)
	if err != nil {
		return Receipt{}, err
	}
	if authority.Purpose != kernel.PurposeImplementation && authority.Purpose != kernel.PurposeRepair || authority.EffectPolicyDigest != gateway.Policy || !permitted(authority.Permissions, "repository.edit") {
		return Receipt{}, ErrForbidden
	}
	root, err := canonicalRoot(authority.WorkspaceRoot)
	if err != nil {
		return Receipt{}, err
	}
	var args struct {
		Path           string `json:"path"`
		Content        string `json:"content"`
		ExpectedSHA256 string `json:"expected_sha256"`
	}
	if decode(request.Call.Arguments, &args) != nil || args.Path == "" || len(args.Content) > 1<<20 || !utf8.ValidString(args.Content) || !validExpectedSHA(args.ExpectedSHA256) {
		return Receipt{}, ErrInvalidCall
	}
	if _, err := relativePath(args.Path); err != nil {
		return Receipt{}, err
	}
	if args.Path == ".git" || strings.HasPrefix(args.Path, ".git/") || args.Path == ".openhands" || strings.HasPrefix(args.Path, ".openhands/") {
		return Receipt{}, ErrForbidden
	}
	if _, err := existingPath(root, filepath.Dir(args.Path)); err != nil {
		return Receipt{}, err
	}
	unlock, err := lockEffectPath(operation, root, "")
	if err != nil {
		return Receipt{}, err
	}
	defer unlock()
	argumentsHash := sha256.Sum256(request.Call.Arguments)
	intent := EffectRecord{
		InvocationID: request.InvocationID, RequestDigest: request.RequestDigest, ToolCallID: request.ToolCallID,
		Name: request.Call.Name, ArgumentsSHA256: hex.EncodeToString(argumentsHash[:]), WorkspaceRoot: root,
		EffectPolicyHash: gateway.Policy,
	}
	_, found, err := gateway.Ledger.Lookup(operation, intent)
	if err != nil {
		return Receipt{}, errors.Join(ErrEffectUncertain, err)
	}
	if !found {
		// A newly rejected precondition has no physical effect; do not leave
		// a reserved intent that would make later cancellation uncertain.
		before, exists, err := fileHash(root, args.Path)
		if err != nil {
			return Receipt{}, err
		}
		if exists && before != args.ExpectedSHA256 || !exists && args.ExpectedSHA256 != "" {
			return Receipt{}, filePreconditionError(args.Path, args.ExpectedSHA256, before, exists)
		}
	}
	stored, created, err := gateway.Ledger.Reserve(operation, intent)
	if err != nil {
		// An insert can commit even when its acknowledgment is lost. The turn
		// must stop and look up this exact intent, not feed a tool error back
		// to the model and risk a second physical action.
		return Receipt{}, errors.Join(ErrEffectUncertain, err)
	}
	// Once the durable intent exists, complete or reconcile it even if the
	// invocation is cancelled. Cancellation stops subsequent turns, not an
	// in-flight filesystem action whose outcome must be known.
	committed, stopCommitted := context.WithTimeout(context.Background(), gateway.Host.Timeout)
	defer stopCommitted()
	desired := sha256.Sum256([]byte(args.Content))
	desiredHash := hex.EncodeToString(desired[:])
	if len(stored.Result) > 0 {
		var result Result
		if json.Unmarshal(stored.Result, &result) != nil || !validWriteEffectResult(request.Call.Name, desiredHash, result) {
			return Receipt{}, ErrEffectUncertain
		}
		return effectReceipt(request, intent.ArgumentsSHA256, result)
	}
	actualHash, exists, err := fileHash(root, args.Path)
	if err != nil {
		if created {
			return completeEffect(gateway.Ledger, request, intent, failedEffectResult(request.Call.Name, Result{}, err))
		}
		return Receipt{}, errors.Join(ErrEffectUncertain, err)
	}
	if created {
		if exists && actualHash != args.ExpectedSHA256 || !exists && args.ExpectedSHA256 != "" {
			return completeEffect(gateway.Ledger, request, intent, failedEffectResult(request.Call.Name, Result{}, filePreconditionError(args.Path, args.ExpectedSHA256, actualHash, exists)))
		}
	} else if exists && actualHash == desiredHash {
		return gateway.completeWrite(committed, request, intent, desiredHash)
	} else {
		// Even an unchanged preimage does not prove an unrecorded action never
		// ran. Recovery may inspect it, but must not initiate another write.
		return Receipt{}, ErrEffectUncertain
	}
	result, writeErr := gateway.Host.execute(committed, authority, request.Call)
	if writeErr != nil {
		// The host can lose an acknowledgment after rename. Inspect before
		// deciding whether the effect is safe to report as failed.
		actualHash, exists, inspectErr := fileHash(root, args.Path)
		if inspectErr != nil {
			return Receipt{}, errors.Join(ErrEffectUncertain, writeErr, inspectErr)
		}
		if exists && actualHash == desiredHash {
			return gateway.completeWrite(committed, request, intent, desiredHash)
		}
		if exists && actualHash == args.ExpectedSHA256 || !exists && args.ExpectedSHA256 == "" {
			return completeEffect(gateway.Ledger, request, intent, failedEffectResult(request.Call.Name, Result{}, writeErr))
		}
		return Receipt{}, errors.Join(ErrEffectUncertain, writeErr)
	}
	if result.SHA256 != desiredHash {
		return Receipt{}, ErrEffectUncertain
	}
	return gateway.completeWrite(committed, request, intent, desiredHash)
}

func (gateway MutationGateway) Reconcile(ctx context.Context, request Request) (Receipt, bool, error) {
	switch request.Call.Name {
	case "write_file":
		return gateway.ReconcileWrite(ctx, request)
	case "git_stage_files", "git_commit":
		return gateway.reconcileGitEffect(ctx, request)
	default:
		return Receipt{}, false, ErrForbidden
	}
}

// ReconcileWrite inspects one already-recorded write without starting a new
// filesystem action. The caller must own the invocation lease; the per-path
// lock also waits for any prior process still finishing this effect.
// applied=false means no write from this call is known to have occurred.
func (gateway MutationGateway) ReconcileWrite(ctx context.Context, request Request) (Receipt, bool, error) {
	if gateway.Bindings == nil || gateway.Ledger == nil || gateway.Host.Timeout <= 0 || !gateway.Policy.Valid() || !request.InvocationID.Valid() || !request.RequestDigest.Valid() || request.Call.Name != "write_file" || strings.TrimSpace(request.ToolCallID) == "" {
		return Receipt{}, false, ErrInvalidCall
	}
	operation, cancel := context.WithTimeout(ctx, gateway.Host.Timeout)
	defer cancel()
	authority, err := gateway.Bindings.BindToolInvocation(operation, request.InvocationID, request.RequestDigest)
	if err != nil {
		return Receipt{}, false, err
	}
	if authority.Purpose != kernel.PurposeImplementation && authority.Purpose != kernel.PurposeRepair || authority.EffectPolicyDigest != gateway.Policy || !permitted(authority.Permissions, "repository.edit") {
		return Receipt{}, false, ErrForbidden
	}
	root, err := canonicalRoot(authority.WorkspaceRoot)
	if err != nil {
		return Receipt{}, false, err
	}
	var args struct {
		Path           string `json:"path"`
		Content        string `json:"content"`
		ExpectedSHA256 string `json:"expected_sha256"`
	}
	if decode(request.Call.Arguments, &args) != nil || args.Path == "" || len(args.Content) > 1<<20 || !utf8.ValidString(args.Content) || !validExpectedSHA(args.ExpectedSHA256) {
		return Receipt{}, false, ErrInvalidCall
	}
	if _, err := relativePath(args.Path); err != nil {
		return Receipt{}, false, err
	}
	if args.Path == ".git" || strings.HasPrefix(args.Path, ".git/") || args.Path == ".openhands" || strings.HasPrefix(args.Path, ".openhands/") {
		return Receipt{}, false, ErrForbidden
	}
	unlock, err := lockEffectPath(operation, root, "")
	if err != nil {
		return Receipt{}, false, err
	}
	defer unlock()
	argumentsHash := sha256.Sum256(request.Call.Arguments)
	intent := EffectRecord{InvocationID: request.InvocationID, RequestDigest: request.RequestDigest, ToolCallID: request.ToolCallID,
		Name: request.Call.Name, ArgumentsSHA256: hex.EncodeToString(argumentsHash[:]), WorkspaceRoot: root, EffectPolicyHash: gateway.Policy}
	stored, found, err := gateway.Ledger.Lookup(operation, intent)
	if err != nil {
		return Receipt{}, false, errors.Join(ErrEffectUncertain, err)
	}
	if !found {
		return Receipt{}, false, nil
	}
	desired := sha256.Sum256([]byte(args.Content))
	desiredHash := hex.EncodeToString(desired[:])
	if len(stored.Result) > 0 {
		var result Result
		if json.Unmarshal(stored.Result, &result) != nil || !validWriteEffectResult(request.Call.Name, desiredHash, result) {
			return Receipt{}, false, ErrEffectUncertain
		}
		receipt, err := effectReceipt(request, intent.ArgumentsSHA256, result)
		return receipt, true, err
	}
	actualHash, exists, err := fileHash(root, args.Path)
	if err != nil {
		return Receipt{}, false, errors.Join(ErrEffectUncertain, err)
	}
	if exists && actualHash == desiredHash {
		receipt, err := gateway.completeWrite(operation, request, intent, desiredHash)
		return receipt, true, err
	}
	if exists && actualHash == args.ExpectedSHA256 || !exists && args.ExpectedSHA256 == "" {
		return Receipt{}, false, nil
	}
	return Receipt{}, false, ErrEffectUncertain
}

func (gateway MutationGateway) completeWrite(ctx context.Context, request Request, intent EffectRecord, desiredHash string) (Receipt, error) {
	result := Result{Name: "write_file", SHA256: desiredHash}
	return completeEffect(gateway.Ledger, request, intent, result)
}

func validWriteEffectResult(name, desiredHash string, result Result) bool {
	if result.ExecutionError != "" {
		return validFailedEffectResult(name, result) && result.CommitSHA == "" && result.IndexTree == ""
	}
	return result.Name == name && result.SHA256 == desiredHash
}

func effectReceipt(request Request, argumentsHash string, result Result) (Receipt, error) {
	encoded, err := json.Marshal(result)
	if err != nil {
		return Receipt{}, err
	}
	sum := sha256.Sum256(encoded)
	return Receipt{InvocationID: request.InvocationID, RequestDigest: request.RequestDigest, ToolCallID: request.ToolCallID, ArgumentsHash: argumentsHash, ResultHash: hex.EncodeToString(sum[:]), Result: result}, nil
}

func fileHash(root, relative string) (string, bool, error) {
	parent, err := existingPath(root, filepath.Dir(relative))
	if err != nil {
		return "", false, err
	}
	path := filepath.Join(parent, filepath.Base(relative))
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	if !info.Mode().IsRegular() || info.Size() > 1<<20 {
		return "", false, ErrBoundary
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return "", false, err
	}
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:]), true, nil
}
