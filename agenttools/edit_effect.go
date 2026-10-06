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

// An edit's postimage cannot be reconstructed from its arguments after the
// preimage has changed. Reserve its computed hash before touching the file.
func (gateway MutationGateway) editBinding(ctx context.Context, request Request) (Authority, string, fileEdit, EffectRecord, error) {
	if gateway.Bindings == nil || gateway.Ledger == nil || gateway.Host.Timeout <= 0 || !gateway.Policy.Valid() ||
		!request.InvocationID.Valid() || !request.RequestDigest.Valid() || request.Call.Name != "edit_file" ||
		strings.TrimSpace(request.ToolCallID) == "" || len(request.ToolCallID) > 256 {
		return Authority{}, "", fileEdit{}, EffectRecord{}, ErrInvalidCall
	}
	args, err := decodeFileEdit(request.Call.Arguments)
	if err != nil {
		return Authority{}, "", fileEdit{}, EffectRecord{}, err
	}
	authority, err := gateway.Bindings.BindToolInvocation(ctx, request.InvocationID, request.RequestDigest)
	if err != nil {
		return Authority{}, "", fileEdit{}, EffectRecord{}, err
	}
	if authority.Purpose != kernel.PurposeImplementation && authority.Purpose != kernel.PurposeRepair ||
		authority.EffectPolicyDigest != gateway.Policy || !permitted(authority.Permissions, "repository.edit") {
		return Authority{}, "", fileEdit{}, EffectRecord{}, ErrForbidden
	}
	root, err := canonicalRoot(authority.WorkspaceRoot)
	if err != nil {
		return Authority{}, "", fileEdit{}, EffectRecord{}, err
	}
	hash := sha256.Sum256(request.Call.Arguments)
	intent := EffectRecord{InvocationID: request.InvocationID, RequestDigest: request.RequestDigest, ToolCallID: request.ToolCallID,
		Name: request.Call.Name, ArgumentsSHA256: hex.EncodeToString(hash[:]), WorkspaceRoot: root, EffectPolicyHash: gateway.Policy}
	return authority, root, args, intent, nil
}

func (gateway MutationGateway) executeEdit(ctx context.Context, request Request) (Receipt, error) {
	operation, cancel := context.WithTimeout(ctx, gateway.Host.Timeout)
	defer cancel()
	_, root, args, intent, err := gateway.editBinding(operation, request)
	if err != nil {
		return Receipt{}, err
	}
	unlock, err := lockEffectPath(operation, root, "")
	if err != nil {
		return Receipt{}, err
	}
	defer unlock()
	stored, found, err := gateway.Ledger.Lookup(operation, intent)
	if err != nil {
		return Receipt{}, errors.Join(ErrEffectUncertain, err)
	}
	if found {
		receipt, completed, err := gateway.reconcileEditRecord(operation, request, args, stored)
		if err != nil || completed {
			return receipt, err
		}
		// A pending intent is never permission to repeat a physical edit.
		return Receipt{}, ErrEffectUncertain
	}
	content, err := prepareFileEdit(root, args)
	if err != nil {
		return Receipt{}, err
	}
	digest := sha256.Sum256(content)
	intent.PreparedSHA256 = hex.EncodeToString(digest[:])
	stored, created, err := gateway.Ledger.Reserve(operation, intent)
	if err != nil {
		return Receipt{}, errors.Join(ErrEffectUncertain, err)
	}
	if !created {
		receipt, completed, err := gateway.reconcileEditRecord(operation, request, args, stored)
		if err != nil || completed {
			return receipt, err
		}
		return Receipt{}, ErrEffectUncertain
	}
	actual, writeErr := writeFile(root, args.Path, content, args.ExpectedSHA256)
	if writeErr == nil && actual == intent.PreparedSHA256 {
		return completeEffect(gateway.Ledger, request, intent, Result{Name: request.Call.Name, SHA256: actual})
	}
	current, exists, inspectErr := fileHash(root, args.Path)
	if inspectErr != nil {
		return Receipt{}, errors.Join(ErrEffectUncertain, writeErr, inspectErr)
	}
	if exists && current == intent.PreparedSHA256 {
		return completeEffect(gateway.Ledger, request, intent, Result{Name: request.Call.Name, SHA256: current})
	}
	if writeErr != nil && exists && current == args.ExpectedSHA256 {
		return completeEffect(gateway.Ledger, request, intent, failedEffectResult(request.Call.Name, Result{}, writeErr))
	}
	return Receipt{}, ErrEffectUncertain
}

func (gateway MutationGateway) reconcileEdit(ctx context.Context, request Request) (Receipt, bool, error) {
	operation, cancel := context.WithTimeout(ctx, gateway.Host.Timeout)
	defer cancel()
	_, root, args, intent, err := gateway.editBinding(operation, request)
	if err != nil {
		return Receipt{}, false, err
	}
	unlock, err := lockEffectPath(operation, root, "")
	if err != nil {
		return Receipt{}, false, err
	}
	defer unlock()
	stored, found, err := gateway.Ledger.Lookup(operation, intent)
	if err != nil {
		return Receipt{}, false, errors.Join(ErrEffectUncertain, err)
	}
	if !found {
		return Receipt{}, false, nil
	}
	return gateway.reconcileEditRecord(operation, request, args, stored)
}

func (gateway MutationGateway) reconcileEditRecord(ctx context.Context, request Request, args fileEdit, stored EffectRecord) (Receipt, bool, error) {
	if len(stored.PreparedSHA256) != 64 || !validExpectedSHA(stored.PreparedSHA256) {
		return Receipt{}, false, ErrEffectUncertain
	}
	if len(stored.Result) > 0 {
		var result Result
		if json.Unmarshal(stored.Result, &result) != nil || !validWriteEffectResult(request.Call.Name, stored.PreparedSHA256, result) {
			return Receipt{}, false, ErrEffectUncertain
		}
		receipt, err := effectReceipt(request, stored.ArgumentsSHA256, result)
		return receipt, err == nil, err
	}
	actual, exists, err := fileHash(stored.WorkspaceRoot, args.Path)
	if err != nil {
		return Receipt{}, false, errors.Join(ErrEffectUncertain, err)
	}
	if exists && actual == stored.PreparedSHA256 {
		receipt, err := completeEffect(gateway.Ledger, request, stored, Result{Name: request.Call.Name, SHA256: actual})
		return receipt, err == nil, err
	}
	if exists && actual == args.ExpectedSHA256 {
		return Receipt{}, false, nil
	}
	return Receipt{}, false, ErrEffectUncertain
}
