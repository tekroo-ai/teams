package openhands

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
	"sync"

	"github.com/tekroo-ai/teams/kernel"
)

// BoundWorkspaceResolver is an immutable, exact workspace/worktree binding.
// It never derives a host path from model or task text.
type BoundWorkspaceResolver struct {
	mu       sync.RWMutex
	bindings map[workspaceBindingKey]WorkspaceBinding
}

type workspaceBindingKey struct {
	workspaceID string
	worktreeID  string
}

func NewBoundWorkspaceResolver(bindings []WorkspaceBinding) (*BoundWorkspaceResolver, error) {
	resolver := &BoundWorkspaceResolver{bindings: make(map[workspaceBindingKey]WorkspaceBinding, len(bindings))}
	if len(bindings) == 0 {
		return nil, ErrInvalidConfiguration
	}
	for _, binding := range bindings {
		if err := resolver.RegisterWorkspace(binding); err != nil {
			return nil, err
		}
	}
	return resolver, nil
}

// RegisterWorkspace adds one exact workspace/worktree tuple. Re-registering
// byte-for-byte equivalent metadata is idempotent; rebinding an existing tuple
// to a different path or candidate identity is rejected.
func (resolver *BoundWorkspaceResolver) RegisterWorkspace(binding WorkspaceBinding) error {
	if resolver == nil || binding.WorkspaceID == "" || binding.WorktreeID == "" || !filepath.IsAbs(binding.WorkingDirectory) || !candidateBindingValid(binding.Candidate) {
		return ErrInvalidConfiguration
	}
	binding.WorkingDirectory = filepath.Clean(binding.WorkingDirectory)
	key := workspaceBindingKey{workspaceID: binding.WorkspaceID, worktreeID: binding.WorktreeID}
	resolver.mu.Lock()
	defer resolver.mu.Unlock()
	if prior, found := resolver.bindings[key]; found {
		if !workspaceBindingsEqual(prior, binding) {
			return ErrProtocol
		}
		return nil
	}
	resolver.bindings[key] = cloneWorkspaceBinding(binding)
	return nil
}

func (resolver *BoundWorkspaceResolver) ResolveWorkspace(ctx context.Context, scope kernel.TaskOperationalScope) (WorkspaceBinding, error) {
	if resolver == nil {
		return WorkspaceBinding{}, ErrInvalidConfiguration
	}
	resolver.mu.RLock()
	binding, found := resolver.bindings[workspaceBindingKey{workspaceID: scope.WorkspaceID, worktreeID: scope.WorktreeID}]
	resolver.mu.RUnlock()
	if !found {
		return WorkspaceBinding{}, ErrProtocol
	}
	if err := verifyCandidateWorkspace(ctx, binding); err != nil {
		return WorkspaceBinding{}, err
	}
	return cloneWorkspaceBinding(binding), nil
}

func candidateBindingValid(candidate *CandidateWorkspaceBinding) bool {
	if candidate == nil {
		return true
	}
	id := kernel.UUIDv7(candidate.CandidateID)
	return id.Valid() && candidate.ReceiptSHA256.Valid() && candidate.RepositoryDigest.Valid() && validGitObjectID(candidate.BaselineCommit) && validGitObjectID(candidate.CandidateCommit) && validGitObjectID(candidate.CandidateTree) && candidate.DiffSHA256.Valid() && candidate.ChangedFilesSHA256.Valid() && (candidate.RuntimeHookSHA256 == "" || candidate.RuntimeHookSHA256.Valid()) && candidate.AllowedReference == "refs/heads/candidate/"+candidate.CandidateID && candidate.ReadOnly
}

func validGitObjectID(value string) bool {
	if len(value) != 40 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func cloneWorkspaceBinding(binding WorkspaceBinding) WorkspaceBinding {
	copy := binding
	if binding.Candidate != nil {
		candidate := *binding.Candidate
		copy.Candidate = &candidate
	}
	return copy
}

func workspaceBindingsEqual(left, right WorkspaceBinding) bool {
	if left.WorkspaceID != right.WorkspaceID || left.WorktreeID != right.WorktreeID || left.WorkingDirectory != right.WorkingDirectory {
		return false
	}
	if left.Candidate == nil || right.Candidate == nil {
		return left.Candidate == nil && right.Candidate == nil
	}
	return *left.Candidate == *right.Candidate
}

func verifyCandidateWorkspace(ctx context.Context, binding WorkspaceBinding) error {
	candidate := binding.Candidate
	if candidate == nil {
		return nil
	}
	checks := []struct {
		arguments []string
		expected  string
	}{
		{[]string{"rev-parse", "HEAD"}, candidate.CandidateCommit},
		{[]string{"rev-parse", "HEAD^{tree}"}, candidate.CandidateTree},
		{[]string{"for-each-ref", "--format=%(refname)"}, candidate.AllowedReference},
	}
	for _, check := range checks {
		command := exec.CommandContext(ctx, "git", check.arguments...)
		command.Dir = binding.WorkingDirectory
		output, err := command.Output()
		if err != nil || strings.TrimSpace(string(output)) != check.expected {
			return errors.Join(ErrProtocol, err)
		}
	}
	command := exec.CommandContext(ctx, "git", "--no-optional-locks", "status", "--porcelain=v1", "--untracked-files=all")
	command.Dir = binding.WorkingDirectory
	status, err := command.Output()
	if err != nil || !candidateWorkspaceStatusClean(status, candidate.RuntimeHookSHA256.Valid()) {
		return errors.Join(ErrProtocol, err)
	}
	if candidate.RuntimeHookSHA256.Valid() {
		hook, err := os.ReadFile(filepath.Join(binding.WorkingDirectory, ".openhands", "hooks", "sma_context_hook.py"))
		if err != nil || digestContent(hook) != candidate.RuntimeHookSHA256 {
			return errors.Join(ErrProtocol, err)
		}
	}
	return nil
}

func candidateWorkspaceStatusClean(status []byte, runtimeHookRequired bool) bool {
	trimmed := strings.TrimSpace(string(status))
	if !runtimeHookRequired {
		return trimmed == ""
	}
	return trimmed == "?? .openhands/hooks/sma_context_hook.py"
}

func digestContent(content []byte) kernel.Digest {
	sum := sha256.Sum256(content)
	return kernel.Digest(hex.EncodeToString(sum[:]))
}

// editableCandidateCompletionReason verifies the repository postcondition that
// turns an editable OpenHands result into an immutable candidate. Prompt text
// asks the agent to commit, but the boundary verifies Git state before Teams
// records success so a forgotten commit cannot deadlock downstream validation.
func editableCandidateCompletionReason(ctx context.Context, binding WorkspaceBinding, branch, baseline string) string {
	if binding.Candidate != nil || !validGitObjectID(baseline) || strings.TrimSpace(branch) == "" {
		return "the editable workspace binding is incomplete"
	}
	git := func(arguments ...string) ([]byte, error) {
		command := exec.CommandContext(ctx, "git", arguments...)
		command.Dir = binding.WorkingDirectory
		return command.Output()
	}
	status, err := git("--no-optional-locks", "status", "--porcelain=v1", "--untracked-files=all")
	if err != nil || !editableWorkspaceStatusClean(status) {
		return "the workspace contains uncommitted or untracked candidate changes"
	}
	currentBranch, err := git("symbolic-ref", "--quiet", "--short", "HEAD")
	if err != nil || strings.TrimSpace(string(currentBranch)) != branch {
		return "the workspace is not on its assigned branch"
	}
	head, err := git("rev-parse", "HEAD")
	if err != nil {
		return "the workspace HEAD cannot be read"
	}
	headCommit := strings.TrimSpace(string(head))
	if headCommit == baseline {
		return "the task has no committed candidate beyond its baseline"
	}
	if _, err := git("merge-base", "--is-ancestor", baseline, headCommit); err != nil {
		return "the candidate commit does not descend from its authorized baseline"
	}
	command := exec.CommandContext(ctx, "git", "diff", "--quiet", baseline+".."+headCommit)
	command.Dir = binding.WorkingDirectory
	if err := command.Run(); err == nil {
		return "the candidate commit has no repository changes relative to its baseline"
	} else if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 1 {
		return "the candidate diff cannot be verified"
	}
	return ""
}

func editableWorkspaceStatusClean(status []byte) bool {
	for _, line := range strings.Split(strings.TrimSpace(string(status)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || line == "?? .openhands/hooks/sma_context_hook.py" {
			continue
		}
		return false
	}
	return true
}

type executionProfileKey struct {
	model   kernel.Digest
	runtime kernel.Digest
	tool    kernel.Digest
	effect  kernel.Digest
}

// BoundExecutionProfileResolver holds the exact qualified OpenHands settings
// for one model/runtime/tool/effect identity tuple.
type BoundExecutionProfileResolver struct {
	profiles map[executionProfileKey]ExecutionProfile
}

func NewBoundExecutionProfileResolver(profiles []ExecutionProfile) (*BoundExecutionProfileResolver, error) {
	resolver := &BoundExecutionProfileResolver{profiles: make(map[executionProfileKey]ExecutionProfile, len(profiles))}
	if len(profiles) == 0 {
		return nil, ErrInvalidConfiguration
	}
	for _, profile := range profiles {
		key := executionProfileKey{profile.ModelProfileDigest, profile.RuntimeIdentityDigest, profile.ToolPolicyDigest, profile.EffectPolicyDigest}
		if !key.model.Valid() || !key.runtime.Valid() || !key.tool.Valid() || !key.effect.Valid() || !profile.valid() {
			return nil, ErrInvalidConfiguration
		}
		if _, duplicate := resolver.profiles[key]; duplicate {
			return nil, ErrInvalidConfiguration
		}
		profile.AgentSettings = append(json.RawMessage(nil), profile.AgentSettings...)
		profile.HookConfig = append(json.RawMessage(nil), profile.HookConfig...)
		resolver.profiles[key] = profile
	}
	return resolver, nil
}

func (resolver *BoundExecutionProfileResolver) ResolveExecutionProfile(_ context.Context, model, runtime, tool, effect kernel.Digest) (ExecutionProfile, error) {
	if resolver == nil {
		return ExecutionProfile{}, ErrInvalidConfiguration
	}
	profile, found := resolver.profiles[executionProfileKey{model, runtime, tool, effect}]
	if !found {
		return ExecutionProfile{}, ErrProtocol
	}
	profile.AgentSettings = append(json.RawMessage(nil), profile.AgentSettings...)
	profile.HookConfig = append(json.RawMessage(nil), profile.HookConfig...)
	return profile, nil
}

var (
	_ WorkspaceResolver        = (*BoundWorkspaceResolver)(nil)
	_ ExecutionProfileResolver = (*BoundExecutionProfileResolver)(nil)
)
