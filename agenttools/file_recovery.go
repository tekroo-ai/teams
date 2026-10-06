package agenttools

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/tekroo-ai/teams/kernel"
)

type createFileArgs struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// FileChangeLookup loads a host-retained preimage. It never accepts content or
// authority from the model; the gateway checks workspace and policy ownership.
type FileChangeLookup interface {
	LoadFileChange(context.Context, string) (EffectRecord, bool, error)
}

func fileChangeID(record EffectRecord) string {
	return contentSHA([]byte(string(record.InvocationID) + "\x00" + record.ToolCallID))
}

func contentSHA(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

func captureFilePreimage(root, path string, intent *EffectRecord) error {
	resolved, err := existingPath(root, path)
	if err != nil {
		return err
	}
	info, err := os.Lstat(resolved)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 1<<20 {
		return ErrBoundary
	}
	content, err := os.ReadFile(resolved)
	if err != nil {
		return err
	}
	intent.FilePath, intent.HasPreimage, intent.BeforeExists = path, true, true
	intent.BeforeContent, intent.BeforeSHA256 = content, contentSHA(content)
	return nil
}

func validateChangePath(root, path string) error {
	if _, err := relativePath(path); err != nil {
		return err
	}
	parent, err := existingPath(root, filepath.Dir(path))
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(root, filepath.Join(parent, filepath.Base(path)))
	if err != nil {
		return ErrBoundary
	}
	for _, reserved := range []string{".git", ".openhands"} {
		if rel == reserved || strings.HasPrefix(rel, reserved+string(filepath.Separator)) {
			return ErrForbidden
		}
	}
	return nil
}

func createFile(root, path string, content []byte) (string, error) {
	if err := validateChangePath(root, path); err != nil {
		return "", err
	}
	return writeFile(root, path, content, "")
}

type fileRecoveryArgs struct {
	Path           string `json:"path,omitempty"`
	Commit         string `json:"commit,omitempty"`
	ChangeID       string `json:"change_id,omitempty"`
	ExpectedSHA256 string `json:"expected_sha256"`
}

func decodeFileRecovery(call Call) (fileRecoveryArgs, error) {
	var args fileRecoveryArgs
	if decode(call.Arguments, &args) != nil || len(args.ExpectedSHA256) != 64 || !validExpectedSHA(args.ExpectedSHA256) {
		return args, ErrInvalidCall
	}
	if call.Name == "undo_file_change" {
		if len(args.ChangeID) != 64 || !validExpectedSHA(args.ChangeID) || args.Path != "" || args.Commit != "" {
			return args, ErrInvalidCall
		}
	} else if call.Name == "restore_file_from_commit" {
		if !validFullGitSHA(args.Commit) || args.Path == "" || args.ChangeID != "" {
			return args, ErrInvalidCall
		}
	} else {
		return args, ErrForbidden
	}
	return args, nil
}

func (gateway MutationGateway) recoveryBinding(ctx context.Context, request Request) (string, fileRecoveryArgs, EffectRecord, error) {
	if gateway.Bindings == nil || gateway.Ledger == nil || gateway.Host.Timeout <= 0 || !gateway.Policy.Valid() || !request.InvocationID.Valid() || !request.RequestDigest.Valid() || strings.TrimSpace(request.ToolCallID) == "" || len(request.ToolCallID) > 256 {
		return "", fileRecoveryArgs{}, EffectRecord{}, ErrInvalidCall
	}
	args, err := decodeFileRecovery(request.Call)
	if err != nil {
		return "", args, EffectRecord{}, err
	}
	authority, err := gateway.Bindings.BindToolInvocation(ctx, request.InvocationID, request.RequestDigest)
	if err != nil {
		return "", args, EffectRecord{}, err
	}
	if (authority.Purpose != kernel.PurposeImplementation && authority.Purpose != kernel.PurposeRepair) || authority.EffectPolicyDigest != gateway.Policy || !permitted(authority.Permissions, "repository.edit") {
		return "", args, EffectRecord{}, ErrForbidden
	}
	root, err := canonicalRoot(authority.WorkspaceRoot)
	intent := EffectRecord{InvocationID: request.InvocationID, RequestDigest: request.RequestDigest, ToolCallID: request.ToolCallID, Name: request.Call.Name, ArgumentsSHA256: contentSHA(request.Call.Arguments), WorkspaceRoot: root, EffectPolicyHash: gateway.Policy}
	return root, args, intent, err
}

func (gateway MutationGateway) executeFileRecovery(ctx context.Context, request Request) (Receipt, error) {
	operation, cancel := context.WithTimeout(ctx, gateway.Host.Timeout)
	defer cancel()
	root, args, intent, err := gateway.recoveryBinding(operation, request)
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
		receipt, known, err := gateway.reconcileFileRecord(operation, request, stored)
		if err != nil || known {
			return receipt, err
		}
		return Receipt{}, ErrEffectUncertain
	}
	path := args.Path
	var content []byte
	if request.Call.Name == "undo_file_change" {
		lookup, ok := gateway.Ledger.(FileChangeLookup)
		if !ok {
			return Receipt{}, ErrForbidden
		}
		prior, exists, err := lookup.LoadFileChange(operation, args.ChangeID)
		if err != nil {
			return Receipt{}, err
		}
		if !exists || !prior.HasPreimage || prior.WorkspaceRoot != root || prior.EffectPolicyHash != gateway.Policy || fileChangeID(prior) != args.ChangeID || len(prior.Result) == 0 {
			return Receipt{}, ErrForbidden
		}
		var result Result
		if json.Unmarshal(prior.Result, &result) != nil || result.Name != prior.Name || result.ExecutionError != "" || result.SHA256 != args.ExpectedSHA256 || result.SHA256 != prior.PreparedSHA256 || prior.DesiredAbsent {
			return Receipt{}, ErrConflict
		}
		if prior.BeforeExists && contentSHA(prior.BeforeContent) != prior.BeforeSHA256 {
			return Receipt{}, ErrEffectUncertain
		}
		path, content, intent.DesiredAbsent = prior.FilePath, prior.BeforeContent, !prior.BeforeExists
	} else {
		content, err = gateway.Host.committedFile(operation, root, args.Commit, path)
		if err != nil {
			return Receipt{}, err
		}
	}
	if err := validateChangePath(root, path); err != nil {
		return Receipt{}, err
	}
	if err := captureFilePreimage(root, path, &intent); err != nil {
		return Receipt{}, err
	}
	if intent.BeforeSHA256 != args.ExpectedSHA256 {
		return Receipt{}, filePreconditionError(path, args.ExpectedSHA256, intent.BeforeSHA256, true)
	}
	intent.PreparedSHA256 = contentSHA(content)
	stored, created, err := gateway.Ledger.Reserve(operation, intent)
	if err != nil {
		return Receipt{}, errors.Join(ErrEffectUncertain, err)
	}
	if !created {
		receipt, known, err := gateway.reconcileFileRecord(operation, request, stored)
		if err != nil || known {
			return receipt, err
		}
		return Receipt{}, ErrEffectUncertain
	}
	// No new action after uncertain reservation. A created reservation admits
	// exactly this preimage-to-postimage transition under the workspace lock.
	if intent.DesiredAbsent {
		resolved, resolveErr := existingPath(root, path)
		if resolveErr != nil {
			return Receipt{}, errors.Join(ErrEffectUncertain, resolveErr)
		}
		current, exists, checkErr := fileHash(root, path)
		if checkErr != nil || !exists || current != args.ExpectedSHA256 {
			return Receipt{}, ErrEffectUncertain
		}
		err = os.Remove(resolved)
	} else {
		_, err = writeFile(root, path, content, args.ExpectedSHA256)
	}
	completion, stop := context.WithTimeout(context.Background(), gateway.Host.Timeout)
	defer stop()
	receipt, known, inspectErr := gateway.reconcileFileRecord(completion, request, intent)
	if inspectErr != nil || known {
		return receipt, inspectErr
	}
	if err != nil {
		return completeEffect(gateway.Ledger, request, intent, failedEffectResult(request.Call.Name, Result{}, err))
	}
	return Receipt{}, ErrEffectUncertain
}

func (gateway MutationGateway) reconcileFileRecovery(ctx context.Context, request Request) (Receipt, bool, error) {
	operation, cancel := context.WithTimeout(ctx, gateway.Host.Timeout)
	defer cancel()
	root, _, intent, err := gateway.recoveryBinding(operation, request)
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
	return gateway.reconcileFileRecord(operation, request, stored)
}

func (gateway MutationGateway) reconcileFileRecord(ctx context.Context, request Request, stored EffectRecord) (Receipt, bool, error) {
	if !stored.HasPreimage || stored.FilePath == "" || !validExpectedSHA(stored.PreparedSHA256) || len(stored.PreparedSHA256) != 64 {
		return Receipt{}, false, ErrEffectUncertain
	}
	if err := validateChangePath(stored.WorkspaceRoot, stored.FilePath); err != nil {
		return Receipt{}, false, err
	}
	if len(stored.Result) > 0 {
		var result Result
		if json.Unmarshal(stored.Result, &result) != nil || result.Name != request.Call.Name || (result.ExecutionError != "" && !validFailedEffectResult(request.Call.Name, result)) || (result.ExecutionError == "" && (result.SHA256 != stored.PreparedSHA256 || result.Removed != stored.DesiredAbsent || result.ChangeID != fileChangeID(stored))) {
			return Receipt{}, false, ErrEffectUncertain
		}
		r, e := effectReceipt(request, stored.ArgumentsSHA256, result)
		return r, e == nil, e
	}
	current, exists, err := fileHash(stored.WorkspaceRoot, stored.FilePath)
	if err != nil {
		return Receipt{}, false, errors.Join(ErrEffectUncertain, err)
	}
	if (stored.DesiredAbsent && !exists) || (!stored.DesiredAbsent && exists && current == stored.PreparedSHA256) {
		result := Result{Name: request.Call.Name, SHA256: stored.PreparedSHA256, Removed: stored.DesiredAbsent, ChangeID: fileChangeID(stored), Path: stored.FilePath}
		r, e := completeEffect(gateway.Ledger, request, stored, result)
		return r, e == nil, e
	}
	if exists && current == stored.BeforeSHA256 {
		return Receipt{}, false, nil
	}
	return Receipt{}, false, ErrEffectUncertain
}

func (host Host) committedFile(ctx context.Context, root, commit, path string) ([]byte, error) {
	if !validFullGitSHA(commit) {
		return nil, ErrInvalidCall
	}
	if err := validateChangePath(root, path); err != nil {
		return nil, err
	}
	if err := host.requireGitRoot(ctx, root); err != nil {
		return nil, err
	}
	ancestry, err := host.command(ctx, root, "git_ancestry", host.gitBinary(), []string{"merge-base", "--is-ancestor", commit, "HEAD"})
	if err != nil || ancestry.ExitCode != 0 {
		return nil, ErrForbidden
	}
	mode, err := host.command(ctx, root, "git_file_mode", host.gitBinary(), []string{"ls-tree", commit, "--", ":(literal)" + path})
	if err != nil || mode.ExitCode != 0 || (!strings.HasPrefix(mode.Output, "100644 ") && !strings.HasPrefix(mode.Output, "100755 ")) {
		return nil, ErrBoundary
	}
	// Blob bytes must never share a buffer with Git diagnostics. Even a
	// successful command's stderr is not part of the historical file.
	operation, cancel := context.WithTimeout(ctx, host.Timeout)
	defer cancel()
	command := exec.CommandContext(operation, host.gitBinary(), "cat-file", "blob", commit+":"+path)
	command.Dir, command.Env = root, toolEnvironment()
	content, diagnostics := &boundedOutput{limit: 1 << 20}, &boundedOutput{limit: 128 << 10}
	command.Stdout, command.Stderr = content, diagnostics
	if err := command.Run(); err != nil {
		return nil, err
	}
	if !utf8.Valid(content.data) {
		return nil, ErrTooLarge
	}
	return content.data, nil
}

func (host Host) readFileFromCommit(ctx context.Context, root string, call Call) (Result, error) {
	var args struct {
		Path      string `json:"path"`
		Commit    string `json:"commit"`
		StartLine int    `json:"start_line"`
		LineCount int    `json:"line_count"`
	}
	if decode(call.Arguments, &args) != nil || args.Path == "" {
		return Result{}, ErrInvalidCall
	}
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(call.Arguments, &fields)
	for _, name := range []string{"start_line", "line_count"} {
		if value, supplied := fields[name]; supplied && (string(value) == "null" || string(value) == "0") {
			return Result{}, ErrInvalidCall
		}
	}
	if args.StartLine == 0 {
		args.StartLine = 1
	}
	if args.LineCount == 0 {
		args.LineCount = 400
	}
	if args.StartLine < 1 || args.LineCount < 1 || args.LineCount > 400 {
		return Result{}, ErrInvalidCall
	}
	content, err := host.committedFile(ctx, root, args.Commit, args.Path)
	if err != nil {
		return Result{}, err
	}
	lines := strings.Split(string(content), "\n")
	if len(content) == 0 {
		lines = nil
	} else if content[len(content)-1] == '\n' {
		lines = lines[:len(lines)-1]
	}
	total := len(lines)
	start := args.StartLine - 1
	if start > total {
		start = total
	}
	end := start + min(args.LineCount, total-start)
	out := strings.Join(lines[start:end], "\n")
	if end > start {
		out += "\n"
	}
	complete := start == 0 && end == total
	actualStart, actualEnd := 0, 0
	if end > start {
		actualStart, actualEnd = start+1, end
	}
	result := Result{Name: call.Name, Path: args.Path, Output: out, SHA256: contentSHA(content), CommitSHA: args.Commit, LineCount: &total, ContentComplete: &complete, StartLine: &actualStart, EndLine: &actualEnd, MaxReadLines: maxReadFileLines, Truncated: !complete}
	if end < total {
		next := end + 1
		result.NextStartLine = &next
	}
	return result, nil
}
