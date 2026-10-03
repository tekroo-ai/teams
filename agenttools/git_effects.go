package agenttools

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"strings"

	"github.com/tekroo-ai/teams/kernel"
)

type gitEffectArguments struct {
	Paths             []string `json:"paths"`
	ExpectedHead      string   `json:"expected_head"`
	ExpectedIndexTree string   `json:"expected_index_tree"`
	Subject           string   `json:"subject"`
	Body              string   `json:"body"`
}

func (gateway MutationGateway) bindGitEffect(ctx context.Context, request Request) (Authority, string, EffectRecord, gitEffectArguments, error) {
	if gateway.Bindings == nil || gateway.Ledger == nil || gateway.Host.Timeout <= 0 || !gateway.Policy.Valid() || !request.InvocationID.Valid() || !request.RequestDigest.Valid() || strings.TrimSpace(request.ToolCallID) == "" || len(request.ToolCallID) > 256 {
		return Authority{}, "", EffectRecord{}, gitEffectArguments{}, ErrInvalidCall
	}
	var args gitEffectArguments
	if decode(request.Call.Arguments, &args) != nil || !validFullGitSHA(args.ExpectedHead) {
		return Authority{}, "", EffectRecord{}, args, ErrInvalidCall
	}
	switch request.Call.Name {
	case "git_stage_files":
		if len(args.Paths) == 0 || len(args.Paths) > 32 || args.ExpectedIndexTree != "" || args.Subject != "" || args.Body != "" {
			return Authority{}, "", EffectRecord{}, args, ErrInvalidCall
		}
		seen := make(map[string]bool, len(args.Paths))
		for _, path := range args.Paths {
			if _, err := relativePath(path); err != nil || path == ".git" || strings.HasPrefix(path, ".git/") || path == ".openhands" || strings.HasPrefix(path, ".openhands/") || seen[path] {
				return Authority{}, "", EffectRecord{}, args, ErrInvalidCall
			}
			seen[path] = true
		}
	case "git_commit":
		if len(args.Paths) != 0 || !validFullGitSHA(args.ExpectedIndexTree) || !validCommitMessage(args.Subject, args.Body) {
			return Authority{}, "", EffectRecord{}, args, ErrInvalidCall
		}
	default:
		return Authority{}, "", EffectRecord{}, args, ErrForbidden
	}
	authority, err := gateway.Bindings.BindToolInvocation(ctx, request.InvocationID, request.RequestDigest)
	if err != nil {
		return Authority{}, "", EffectRecord{}, args, err
	}
	if authority.Purpose != kernel.PurposeImplementation && authority.Purpose != kernel.PurposeRepair || authority.EffectPolicyDigest != gateway.Policy || !permitted(authority.Permissions, "repository.edit") {
		return Authority{}, "", EffectRecord{}, args, ErrForbidden
	}
	root, err := canonicalRoot(authority.WorkspaceRoot)
	if err != nil {
		return Authority{}, "", EffectRecord{}, args, err
	}
	if err := gateway.Host.requireGitRoot(ctx, root); err != nil {
		return Authority{}, "", EffectRecord{}, args, err
	}
	sum := sha256.Sum256(request.Call.Arguments)
	intent := EffectRecord{InvocationID: request.InvocationID, RequestDigest: request.RequestDigest, ToolCallID: request.ToolCallID,
		Name: request.Call.Name, ArgumentsSHA256: hex.EncodeToString(sum[:]), WorkspaceRoot: root, EffectPolicyHash: gateway.Policy}
	return authority, root, intent, args, nil
}

func validGitEffectResult(name string, args gitEffectArguments, result Result) bool {
	if result.Name != name {
		return false
	}
	if name == "git_stage_files" {
		return validFullGitSHA(result.IndexTree) && result.CommitSHA == ""
	}
	return validFullGitSHA(result.CommitSHA) && result.IndexTree == args.ExpectedIndexTree
}

func (gateway MutationGateway) completeGitEffect(ctx context.Context, request Request, intent EffectRecord, result Result) (Receipt, error) {
	encoded, _ := json.Marshal(result)
	if err := gateway.Ledger.Complete(ctx, intent, encoded); err != nil {
		return Receipt{}, errors.Join(ErrEffectUncertain, err)
	}
	return effectReceipt(request, intent.ArgumentsSHA256, result)
}

func (gateway MutationGateway) executeGitEffect(ctx context.Context, request Request) (Receipt, error) {
	operation, cancel := context.WithTimeout(ctx, gateway.Host.Timeout)
	defer cancel()
	authority, root, intent, args, err := gateway.bindGitEffect(operation, request)
	if err != nil {
		return Receipt{}, err
	}
	unlock, err := lockEffectPath(operation, root, "")
	if err != nil {
		return Receipt{}, err
	}
	defer unlock()
	stored, _, err := gateway.Ledger.Reserve(operation, intent)
	if err != nil {
		return Receipt{}, errors.Join(ErrEffectUncertain, err)
	}
	if len(stored.Result) > 0 {
		var result Result
		if json.Unmarshal(stored.Result, &result) != nil || !validGitEffectResult(request.Call.Name, args, result) {
			return Receipt{}, ErrEffectUncertain
		}
		return effectReceipt(request, intent.ArgumentsSHA256, result)
	}
	committed, stopCommitted := context.WithTimeout(context.Background(), gateway.Host.Timeout)
	defer stopCommitted()
	result, err := gateway.Host.execute(committed, authority, request.Call)
	if err != nil || !validGitEffectResult(request.Call.Name, args, result) {
		return Receipt{}, errors.Join(ErrEffectUncertain, err)
	}
	return gateway.completeGitEffect(committed, request, intent, result)
}

// This path reads Git state only. The workspace lock ensures that an older
// daemon's synchronous Git operation has finished before state is judged.
func (gateway MutationGateway) reconcileGitEffect(ctx context.Context, request Request) (Receipt, bool, error) {
	operation, cancel := context.WithTimeout(ctx, gateway.Host.Timeout)
	defer cancel()
	_, root, intent, args, err := gateway.bindGitEffect(operation, request)
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
	if len(stored.Result) > 0 {
		var result Result
		if json.Unmarshal(stored.Result, &result) != nil || !validGitEffectResult(request.Call.Name, args, result) {
			return Receipt{}, false, ErrEffectUncertain
		}
		receipt, err := effectReceipt(request, intent.ArgumentsSHA256, result)
		return receipt, true, err
	}
	result, applied, err := gateway.inspectGitEffect(operation, root, request.Call.Name, args)
	if err != nil || !applied {
		return Receipt{}, false, err
	}
	receipt, err := gateway.completeGitEffect(operation, request, intent, result)
	return receipt, true, err
}

func (gateway MutationGateway) inspectGitEffect(ctx context.Context, root, name string, args gitEffectArguments) (Result, bool, error) {
	head, err := gateway.Host.gitValue(ctx, root, "rev-parse", "HEAD")
	if err != nil {
		return Result{}, false, errors.Join(ErrEffectUncertain, err)
	}
	if name == "git_commit" {
		if head == args.ExpectedHead {
			index, err := gateway.Host.gitValue(ctx, root, "write-tree")
			if err != nil || index != args.ExpectedIndexTree {
				return Result{}, false, errors.Join(ErrEffectUncertain, err)
			}
			return Result{}, false, nil
		}
		parent, parentErr := gateway.Host.gitValue(ctx, root, "rev-parse", "HEAD^")
		tree, treeErr := gateway.Host.gitValue(ctx, root, "rev-parse", "HEAD^{tree}")
		message, messageErr := gateway.Host.gitCommitMessage(ctx, root)
		if parentErr != nil || treeErr != nil || messageErr != nil || parent != args.ExpectedHead || tree != args.ExpectedIndexTree || message != commitMessage(args.Subject, args.Body) {
			return Result{}, false, ErrEffectUncertain
		}
		return Result{Name: name, CommitSHA: head, IndexTree: tree}, true, nil
	}
	if head != args.ExpectedHead {
		return Result{}, false, ErrEffectUncertain
	}
	staged, err := gateway.gitNameSet(ctx, root, []string{"diff", "--cached", "--name-only", "-z", "--"})
	if err != nil {
		return Result{}, false, err
	}
	for path := range staged {
		if !matchesSelectedPath(path, args.Paths) {
			return Result{}, false, ErrEffectUncertain
		}
	}
	pathspecs := make([]string, 0, len(args.Paths))
	for _, path := range args.Paths {
		pathspecs = append(pathspecs, ":(literal)"+path)
	}
	unstaged, err := gateway.gitNameSet(ctx, root, append([]string{"diff", "--name-only", "-z", "--"}, pathspecs...))
	if err != nil {
		return Result{}, false, err
	}
	untracked, err := gateway.gitNameSet(ctx, root, append([]string{"ls-files", "--others", "--exclude-standard", "-z", "--"}, pathspecs...))
	if err != nil {
		return Result{}, false, err
	}
	if len(unstaged) != 0 || len(untracked) != 0 {
		if len(staged) != 0 {
			return Result{}, false, ErrEffectUncertain
		}
		return Result{}, false, nil
	}
	tree, err := gateway.Host.gitValue(ctx, root, "write-tree")
	if err != nil {
		return Result{}, false, errors.Join(ErrEffectUncertain, err)
	}
	return Result{Name: name, IndexTree: tree}, true, nil
}

func (gateway MutationGateway) gitNameSet(ctx context.Context, root string, args []string) (map[string]bool, error) {
	result, err := gateway.Host.command(ctx, root, "git_effect_inspect", gateway.Host.gitBinary(), args)
	if err != nil || result.ExitCode != 0 {
		return nil, errors.Join(ErrEffectUncertain, err)
	}
	paths := make(map[string]bool)
	for _, path := range strings.Split(result.Output, "\x00") {
		if path != "" {
			paths[path] = true
		}
	}
	return paths, nil
}

func matchesSelectedPath(path string, selected []string) bool {
	return slices.Contains(selected, path) || slices.ContainsFunc(selected, func(prefix string) bool { return strings.HasPrefix(path, prefix+"/") })
}
