package gitprovider

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/tekroo-ai/teams/kernel"
)

var (
	ErrInvalidConfiguration = errors.New("invalid Git release-provider configuration")
	ErrInvalidRequest       = errors.New("invalid Git release-provider request")
	ErrRepositoryBoundary   = errors.New("repository is outside the authorized Git provider root")
	ErrGitUnavailable       = errors.New("authoritative Git state is unavailable")
	ErrIdempotencyConflict  = errors.New("provider idempotency key was reused for a different release request")
)

type Config struct {
	GitBinary        string
	AllowedRoot      string
	OperationTimeout time.Duration
}

type commandRunner interface {
	Run(context.Context, string, ...string) ([]byte, error)
}

type execRunner struct {
	binary string
}

func (runner execRunner) Run(ctx context.Context, repository string, arguments ...string) ([]byte, error) {
	base := []string{"-c", "core.hooksPath=/dev/null", "-c", "credential.interactive=never", "--git-dir", repository}
	command := exec.CommandContext(ctx, runner.binary, append(base, arguments...)...)
	command.Env = append(os.Environ(), "LC_ALL=C", "TZ=UTC", "GIT_TERMINAL_PROMPT=0", "GIT_NO_LAZY_FETCH=1")
	return command.CombinedOutput()
}

// Provider implements deterministic FF-only release effects against bare Git
// repositories under one explicitly authorized local root. It does not use a
// shell, invoke hooks, contact a forge API, or push to a network remote.
type Provider struct {
	mu               sync.Mutex
	allowedRoot      string
	operationTimeout time.Duration
	runner           commandRunner
	idempotency      map[string]string
}

func New(config Config) (*Provider, error) {
	gitBinary := config.GitBinary
	if gitBinary == "" {
		gitBinary = "git"
	}
	root, err := canonicalDirectory(config.AllowedRoot)
	if err != nil || config.OperationTimeout <= 0 {
		return nil, ErrInvalidConfiguration
	}
	return newWithRunner(config, root, execRunner{binary: gitBinary})
}

func newWithRunner(config Config, canonicalRoot string, runner commandRunner) (*Provider, error) {
	if canonicalRoot == "" || runner == nil || config.OperationTimeout <= 0 {
		return nil, ErrInvalidConfiguration
	}
	return &Provider{allowedRoot: canonicalRoot, operationTimeout: config.OperationTimeout, runner: runner, idempotency: make(map[string]string)}, nil
}

func (provider *Provider) Merge(ctx context.Context, request kernel.ReleaseMergeRequest) (kernel.ReleaseProviderObservation, error) {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	repository, err := provider.validateRequest(request)
	if err != nil {
		return unavailable(request, err.Error()), err
	}
	if err := provider.bindIdempotency(request); err != nil {
		return failed(request, kernel.ReleaseProviderOpen, err.Error()), nil
	}

	operationCtx, cancel := context.WithTimeout(ctx, provider.operationTimeout)
	defer cancel()
	state, err := provider.inspect(operationCtx, repository, request, kernel.ReleaseOutcomeAlreadyMerged)
	if err != nil {
		return unavailable(request, "authoritative Git state could not be read"), fmt.Errorf("%w: %v", ErrGitUnavailable, err)
	}
	if state.terminal {
		return state.observation, nil
	}

	_, updateErr := provider.runner.Run(operationCtx, repository, "update-ref", baseReference(request.BaseRef), request.HeadCommit, state.currentCommit)
	// Even a successful update is reconciled from authoritative state. If the
	// subprocess timed out after applying the ref update, the detached bounded
	// read below recovers the success without inventing an outcome.
	reconcileCtx, reconcileCancel := context.WithTimeout(context.WithoutCancel(ctx), provider.operationTimeout)
	defer reconcileCancel()
	reconciled, reconcileErr := provider.inspect(reconcileCtx, repository, request, kernel.ReleaseOutcomeMerged)
	if reconcileErr != nil {
		if updateErr != nil {
			return unavailable(request, "Git update acknowledgement and reconciliation are both unavailable"), fmt.Errorf("%w: update: %v; reconcile: %v", ErrGitUnavailable, updateErr, reconcileErr)
		}
		return unavailable(request, "Git update succeeded but authoritative reconciliation is unavailable"), fmt.Errorf("%w: %v", ErrGitUnavailable, reconcileErr)
	}
	if reconciled.terminal {
		return reconciled.observation, nil
	}
	if updateErr != nil {
		return reconciled.observation, nil
	}
	return unavailable(request, "Git ref update did not produce the planned authoritative state"), ErrGitUnavailable
}

func (provider *Provider) Reconcile(ctx context.Context, request kernel.ReleaseMergeRequest) (kernel.ReleaseProviderObservation, error) {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	repository, err := provider.validateRequest(request)
	if err != nil {
		return unavailable(request, err.Error()), err
	}
	if err := provider.bindIdempotency(request); err != nil {
		return failed(request, kernel.ReleaseProviderOpen, err.Error()), nil
	}
	operationCtx, cancel := context.WithTimeout(ctx, provider.operationTimeout)
	defer cancel()
	state, err := provider.inspect(operationCtx, repository, request, kernel.ReleaseOutcomeMerged)
	if err != nil {
		return unavailable(request, "authoritative Git state could not be read"), fmt.Errorf("%w: %v", ErrGitUnavailable, err)
	}
	return state.observation, nil
}

type inspectedState struct {
	observation   kernel.ReleaseProviderObservation
	currentCommit string
	terminal      bool
}

func (provider *Provider) inspect(ctx context.Context, repository string, request kernel.ReleaseMergeRequest, exactOutcome kernel.ReleaseOutcome) (inspectedState, error) {
	version, err := provider.value(ctx, repository, "--version")
	if err != nil {
		return inspectedState{}, err
	}
	if version != request.GitVersion {
		return inspectedState{observation: failed(request, kernel.ReleaseProviderOpen, "Git version does not match the qualified release plan"), terminal: true}, nil
	}
	bare, err := provider.value(ctx, repository, "rev-parse", "--is-bare-repository")
	if err != nil || bare != "true" {
		return inspectedState{}, errors.New("provider target is not a bare Git repository")
	}
	current, err := provider.value(ctx, repository, "rev-parse", "--verify", baseReference(request.BaseRef)+"^{commit}")
	if err != nil {
		return inspectedState{observation: failed(request, kernel.ReleaseProviderMissing, "base ref is missing"), terminal: true}, nil
	}
	qualifiedBase, err := provider.value(ctx, repository, "rev-parse", "--verify", request.BaseCommit+"^{commit}")
	if err != nil {
		return inspectedState{observation: failed(request, kernel.ReleaseProviderMissing, "qualified base commit is missing"), currentCommit: current, terminal: true}, nil
	}
	if qualifiedBase != request.BaseCommit {
		return inspectedState{observation: failed(request, kernel.ReleaseProviderOpen, "qualified base commit identity is not exact"), currentCommit: current, terminal: true}, nil
	}
	plannedHead, err := provider.value(ctx, repository, "rev-parse", "--verify", request.ChangeRef+"^{commit}")
	if err != nil {
		return inspectedState{observation: failed(request, kernel.ReleaseProviderMissing, "planned change ref is missing"), currentCommit: current, terminal: true}, nil
	}
	if plannedHead != request.HeadCommit {
		return inspectedState{observation: failed(request, kernel.ReleaseProviderOpen, "planned change ref no longer resolves to the exact head commit"), currentCommit: current, terminal: true}, nil
	}
	if current == request.HeadCommit {
		observation, err := provider.success(ctx, repository, request, exactOutcome, current, "base ref resolves to the exact planned head")
		return inspectedState{observation: observation, currentCommit: current, terminal: err == nil}, err
	}
	headContained, err := provider.isAncestor(ctx, repository, request.HeadCommit, current)
	if err != nil {
		return inspectedState{}, err
	}
	if headContained {
		observation, err := provider.success(ctx, repository, request, kernel.ReleaseOutcomeAlreadyMerged, current, "base ref already contains the exact planned head")
		return inspectedState{observation: observation, currentCommit: current, terminal: err == nil}, err
	}
	baseContained, err := provider.isAncestor(ctx, repository, request.BaseCommit, current)
	if err != nil {
		return inspectedState{}, err
	}
	if !baseContained {
		return inspectedState{observation: failed(request, kernel.ReleaseProviderOpen, "base ref diverged from the qualified base commit"), currentCommit: current, terminal: true}, nil
	}
	fastForward, err := provider.isAncestor(ctx, repository, current, request.HeadCommit)
	if err != nil {
		return inspectedState{}, err
	}
	if !fastForward {
		return inspectedState{observation: failed(request, kernel.ReleaseProviderOpen, "FF-only merge is not possible from the current base ref"), currentCommit: current, terminal: true}, nil
	}
	return inspectedState{observation: failed(request, kernel.ReleaseProviderOpen, "planned head is eligible but not merged"), currentCommit: current}, nil
}

func (provider *Provider) success(ctx context.Context, repository string, request kernel.ReleaseMergeRequest, outcome kernel.ReleaseOutcome, current, reason string) (kernel.ReleaseProviderObservation, error) {
	tree, err := provider.value(ctx, repository, "rev-parse", "--verify", current+"^{tree}")
	if err != nil {
		return kernel.ReleaseProviderObservation{}, err
	}
	return kernel.ReleaseProviderObservation{
		ReleasePlanID: request.ReleasePlanID, MergeID: request.MergeID, AttemptID: request.AttemptID,
		State: kernel.ReleaseProviderMerged, Outcome: outcome, BaseCommit: request.BaseCommit, HeadCommit: request.HeadCommit,
		TreeDigest: tree, Reasons: []string{reason},
	}, nil
}

func (provider *Provider) isAncestor(ctx context.Context, repository, ancestor, descendant string) (bool, error) {
	output, err := provider.runner.Run(ctx, repository, "merge-base", "--is-ancestor", ancestor, descendant)
	if err == nil {
		return true, nil
	}
	var exitError *exec.ExitError
	if errors.As(err, &exitError) && exitError.ExitCode() == 1 {
		return false, nil
	}
	return false, fmt.Errorf("git merge-base --is-ancestor: %w: %s", err, strings.TrimSpace(string(output)))
}

func (provider *Provider) value(ctx context.Context, repository string, arguments ...string) (string, error) {
	output, err := provider.runner.Run(ctx, repository, arguments...)
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(arguments, " "), err, strings.TrimSpace(string(output)))
	}
	return strings.TrimSpace(string(output)), nil
}

func (provider *Provider) validateRequest(request kernel.ReleaseMergeRequest) (string, error) {
	if !request.Valid() || !validObjectID(request.BaseCommit) || !validObjectID(request.HeadCommit) || !validBaseRef(request.BaseRef) || !validChangeRef(request.ChangeRef) {
		return "", ErrInvalidRequest
	}
	repository, err := canonicalRepository(request.RepositoryURL)
	if err != nil {
		if errors.Is(err, ErrRepositoryBoundary) {
			return "", ErrRepositoryBoundary
		}
		return "", ErrInvalidRequest
	}
	relative, err := filepath.Rel(provider.allowedRoot, repository)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return "", ErrRepositoryBoundary
	}
	return repository, nil
}

func (provider *Provider) bindIdempotency(request kernel.ReleaseMergeRequest) error {
	digest := requestDigest(request)
	if existing, found := provider.idempotency[request.ProviderIdempotencyKey]; found && existing != digest {
		return ErrIdempotencyConflict
	}
	provider.idempotency[request.ProviderIdempotencyKey] = digest
	return nil
}

func requestDigest(request kernel.ReleaseMergeRequest) string {
	value := strings.Join([]string{string(request.ReleasePlanID), string(request.PlanDigest), string(request.MergeID), string(request.AttemptID), fmt.Sprint(request.Round), request.RepositoryURL, request.BaseRef, request.BaseCommit, request.ChangeRef, request.HeadCommit, request.MergeStrategy, request.GitVersion, request.ConflictPolicy}, "\x00")
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}

func canonicalDirectory(path string) (string, error) {
	if path == "" || strings.Contains(path, "://") {
		return "", errors.New("local directory required")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.IsDir() {
		return "", errors.New("directory required")
	}
	return filepath.Clean(resolved), nil
}

func canonicalRepository(value string) (string, error) {
	parsed, err := url.Parse(value)
	if err != nil {
		return "", err
	}
	if parsed.Scheme == "" {
		return canonicalDirectory(value)
	}
	if parsed.Scheme != "file" || parsed.Host != "" || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Path == "" {
		return "", ErrRepositoryBoundary
	}
	return canonicalDirectory(parsed.Path)
}

func baseReference(value string) string {
	if strings.HasPrefix(value, "refs/heads/") {
		return value
	}
	return "refs/heads/" + value
}

func validBaseRef(value string) bool {
	if strings.HasPrefix(value, "refs/heads/") {
		value = strings.TrimPrefix(value, "refs/heads/")
	}
	return validRefTail(value)
}

func validChangeRef(value string) bool {
	return strings.HasPrefix(value, "refs/heads/") && validRefTail(strings.TrimPrefix(value, "refs/heads/"))
}

func validRefTail(value string) bool {
	if value == "" || value == "@" || strings.HasPrefix(value, "-") || strings.HasPrefix(value, "/") || strings.HasSuffix(value, ".") || strings.HasSuffix(value, "/") || strings.HasSuffix(value, ".lock") || strings.Contains(value, "..") || strings.Contains(value, "@{") || strings.Contains(value, "//") || strings.ContainsAny(value, " ~^:?*[\\") {
		return false
	}
	for _, character := range value {
		if character < 0x20 || character == 0x7f {
			return false
		}
	}
	return true
}

func validObjectID(value string) bool {
	if len(value) < 40 || len(value) > 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func failed(request kernel.ReleaseMergeRequest, state kernel.ReleaseProviderState, reason string) kernel.ReleaseProviderObservation {
	return kernel.ReleaseProviderObservation{ReleasePlanID: request.ReleasePlanID, MergeID: request.MergeID, AttemptID: request.AttemptID, State: state, Outcome: kernel.ReleaseOutcomeFailed, Reasons: []string{reason}}
}

func unavailable(request kernel.ReleaseMergeRequest, reason string) kernel.ReleaseProviderObservation {
	return kernel.ReleaseProviderObservation{ReleasePlanID: request.ReleasePlanID, MergeID: request.MergeID, AttemptID: request.AttemptID, State: kernel.ReleaseProviderUnavailable, Outcome: kernel.ReleaseOutcomeUnknown, Reasons: []string{reason}}
}

var _ kernel.ReleaseProvider = (*Provider)(nil)
