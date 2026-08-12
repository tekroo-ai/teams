package gitprovider

import (
	"context"
	"errors"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/tekroo-ai/teams/kernel"
)

func TestProviderFastForwardsOnlyExactPlannedHeadAndReplaysAlreadyMerged(t *testing.T) {
	repository := newTestRepository(t)
	provider := newTestProvider(t, repository.root, execRunner{binary: "git"})
	request := repository.request(repository.head1, "refs/heads/story-1", "merge-story-1")

	merged, err := provider.Merge(context.Background(), request)
	if err != nil || merged.Outcome != kernel.ReleaseOutcomeMerged || merged.State != kernel.ReleaseProviderMerged || merged.BaseCommit != repository.base || merged.HeadCommit != repository.head1 || merged.TreeDigest != repository.tree1 {
		t.Fatalf("merged observation=%#v err=%v", merged, err)
	}
	if current := repository.git(t, "--git-dir", repository.bare, "rev-parse", "refs/heads/main"); current != repository.head1 {
		t.Fatalf("base ref=%s, want %s", current, repository.head1)
	}
	replayed, err := provider.Merge(context.Background(), request)
	if err != nil || replayed.Outcome != kernel.ReleaseOutcomeAlreadyMerged || replayed.TreeDigest != repository.tree1 {
		t.Fatalf("replayed observation=%#v err=%v", replayed, err)
	}
}

func TestProviderExecutesCumulativeOrderedHeadsAndRetainsQualifiedBase(t *testing.T) {
	repository := newTestRepository(t)
	provider := newTestProvider(t, repository.root, execRunner{binary: "git"})
	first := repository.request(repository.head1, "refs/heads/story-1", "merge-story-1")
	second := repository.request(repository.head2, "refs/heads/story-2", "merge-story-2")
	second.MergeID = "00000000-0000-7000-8000-000000000765"
	second.AttemptID = "00000000-0000-7000-8000-000000000766"

	if result, err := provider.Merge(context.Background(), first); err != nil || result.Outcome != kernel.ReleaseOutcomeMerged {
		t.Fatalf("first merge=%#v err=%v", result, err)
	}
	result, err := provider.Merge(context.Background(), second)
	if err != nil || result.Outcome != kernel.ReleaseOutcomeMerged || result.BaseCommit != repository.base || result.HeadCommit != repository.head2 || result.TreeDigest != repository.tree2 {
		t.Fatalf("second merge=%#v err=%v", result, err)
	}
}

func TestProviderRejectsChangedHeadDivergedBaseAndIdempotencyReuseWithoutMutation(t *testing.T) {
	t.Run("changed-head", func(t *testing.T) {
		repository := newTestRepository(t)
		provider := newTestProvider(t, repository.root, execRunner{binary: "git"})
		repository.git(t, "--git-dir", repository.bare, "update-ref", "refs/heads/story-1", repository.head2, repository.head1)
		result, err := provider.Merge(context.Background(), repository.request(repository.head1, "refs/heads/story-1", "changed-head"))
		if err != nil || result.Outcome != kernel.ReleaseOutcomeFailed || result.State != kernel.ReleaseProviderOpen || repository.main(t) != repository.base {
			t.Fatalf("changed-head result=%#v err=%v main=%s", result, err, repository.main(t))
		}
	})
	t.Run("diverged-base", func(t *testing.T) {
		repository := newTestRepository(t)
		provider := newTestProvider(t, repository.root, execRunner{binary: "git"})
		repository.git(t, "--git-dir", repository.bare, "update-ref", "refs/heads/main", repository.other, repository.base)
		result, err := provider.Merge(context.Background(), repository.request(repository.head1, "refs/heads/story-1", "diverged-base"))
		if err != nil || result.Outcome != kernel.ReleaseOutcomeFailed || result.State != kernel.ReleaseProviderOpen || repository.main(t) != repository.other {
			t.Fatalf("diverged-base result=%#v err=%v main=%s", result, err, repository.main(t))
		}
	})
	t.Run("idempotency-reuse", func(t *testing.T) {
		repository := newTestRepository(t)
		provider := newTestProvider(t, repository.root, execRunner{binary: "git"})
		first := repository.request(repository.head1, "refs/heads/story-1", "same-key")
		if _, err := provider.Merge(context.Background(), first); err != nil {
			t.Fatal(err)
		}
		conflict := repository.request(repository.head2, "refs/heads/story-2", "same-key")
		conflict.AttemptID = "00000000-0000-7000-8000-000000000766"
		result, err := provider.Merge(context.Background(), conflict)
		if err != nil || result.Outcome != kernel.ReleaseOutcomeFailed || result.State != kernel.ReleaseProviderOpen || repository.main(t) != repository.head1 {
			t.Fatalf("idempotency result=%#v err=%v main=%s", result, err, repository.main(t))
		}
	})
	t.Run("qualified-git-version", func(t *testing.T) {
		repository := newTestRepository(t)
		provider := newTestProvider(t, repository.root, execRunner{binary: "git"})
		request := repository.request(repository.head1, "refs/heads/story-1", "version-mismatch")
		request.GitVersion = "git version 0.0.0"
		result, err := provider.Merge(context.Background(), request)
		if err != nil || result.Outcome != kernel.ReleaseOutcomeFailed || repository.main(t) != repository.base {
			t.Fatalf("version result=%#v err=%v main=%s", result, err, repository.main(t))
		}
	})
	t.Run("missing-change-ref", func(t *testing.T) {
		repository := newTestRepository(t)
		provider := newTestProvider(t, repository.root, execRunner{binary: "git"})
		request := repository.request(repository.head1, "refs/heads/missing", "missing-ref")
		result, err := provider.Merge(context.Background(), request)
		if err != nil || result.Outcome != kernel.ReleaseOutcomeFailed || result.State != kernel.ReleaseProviderMissing || repository.main(t) != repository.base {
			t.Fatalf("missing-ref result=%#v err=%v main=%s", result, err, repository.main(t))
		}
	})
	t.Run("missing-base-ref", func(t *testing.T) {
		repository := newTestRepository(t)
		provider := newTestProvider(t, repository.root, execRunner{binary: "git"})
		request := repository.request(repository.head1, "refs/heads/story-1", "missing-base")
		request.BaseRef = "missing"
		result, err := provider.Merge(context.Background(), request)
		if err != nil || result.Outcome != kernel.ReleaseOutcomeFailed || result.State != kernel.ReleaseProviderMissing || repository.main(t) != repository.base {
			t.Fatalf("missing-base result=%#v err=%v main=%s", result, err, repository.main(t))
		}
	})
}

func TestProviderReconcilesAmbiguousSuccessfulUpdateAuthoritatively(t *testing.T) {
	repository := newTestRepository(t)
	runner := &ambiguousUpdateRunner{delegate: execRunner{binary: "git"}}
	provider := newTestProvider(t, repository.root, runner)
	request := repository.request(repository.head1, "refs/heads/story-1", "ambiguous-success")

	result, err := provider.Merge(context.Background(), request)
	if err != nil || result.Outcome != kernel.ReleaseOutcomeMerged || repository.main(t) != repository.head1 || runner.updates != 1 {
		t.Fatalf("ambiguous result=%#v err=%v main=%s updates=%d", result, err, repository.main(t), runner.updates)
	}
}

func TestProviderClassifiesRejectedOrTimedOutUnappliedUpdateFromAuthoritativeState(t *testing.T) {
	for _, test := range []struct {
		name string
		err  error
	}{
		{name: "permission-rejected", err: os.ErrPermission},
		{name: "deadline-before-effect", err: context.DeadlineExceeded},
	} {
		t.Run(test.name, func(t *testing.T) {
			repository := newTestRepository(t)
			runner := &rejectedUpdateRunner{delegate: execRunner{binary: "git"}, err: test.err}
			provider := newTestProvider(t, repository.root, runner)
			result, err := provider.Merge(context.Background(), repository.request(repository.head1, "refs/heads/story-1", test.name))
			if err != nil || result.Outcome != kernel.ReleaseOutcomeFailed || result.State != kernel.ReleaseProviderOpen || repository.main(t) != repository.base || runner.updates != 1 {
				t.Fatalf("result=%#v err=%v main=%s updates=%d", result, err, repository.main(t), runner.updates)
			}
		})
	}
}

func TestProviderConcurrentDuplicatesConvergeOnOneHead(t *testing.T) {
	repository := newTestRepository(t)
	provider := newTestProvider(t, repository.root, execRunner{binary: "git"})
	request := repository.request(repository.head1, "refs/heads/story-1", "concurrent-merge")
	const workers = 24
	results := make(chan kernel.ReleaseProviderObservation, workers)
	errorsFound := make(chan error, workers)
	var wait sync.WaitGroup
	for index := 0; index < workers; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			result, err := provider.Merge(context.Background(), request)
			results <- result
			errorsFound <- err
		}()
	}
	wait.Wait()
	close(results)
	close(errorsFound)
	for err := range errorsFound {
		if err != nil {
			t.Fatalf("concurrent merge error: %v", err)
		}
	}
	for result := range results {
		if (result.Outcome != kernel.ReleaseOutcomeMerged && result.Outcome != kernel.ReleaseOutcomeAlreadyMerged) || result.TreeDigest != repository.tree1 {
			t.Fatalf("concurrent result=%#v", result)
		}
	}
	if repository.main(t) != repository.head1 {
		t.Fatalf("concurrent main=%s", repository.main(t))
	}
}

func TestProviderReconcileIsReadOnlyAndClassifiesUnmergedAndMergedState(t *testing.T) {
	repository := newTestRepository(t)
	provider := newTestProvider(t, repository.root, execRunner{binary: "git"})
	request := repository.request(repository.head1, "refs/heads/story-1", "read-only-reconcile")

	unmerged, err := provider.Reconcile(context.Background(), request)
	if err != nil || unmerged.Outcome != kernel.ReleaseOutcomeFailed || unmerged.State != kernel.ReleaseProviderOpen || repository.main(t) != repository.base {
		t.Fatalf("unmerged reconciliation=%#v err=%v main=%s", unmerged, err, repository.main(t))
	}
	if _, err := provider.Merge(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	merged, err := provider.Reconcile(context.Background(), request)
	if err != nil || merged.Outcome != kernel.ReleaseOutcomeMerged || merged.TreeDigest != repository.tree1 {
		t.Fatalf("merged reconciliation=%#v err=%v", merged, err)
	}
}

func TestProviderFailsClosedOutsideAllowedRootAndWhenGitIsUnavailable(t *testing.T) {
	inside := newTestRepository(t)
	outside := newTestRepository(t)
	provider := newTestProvider(t, inside.root, execRunner{binary: "git"})
	outsideRequest := outside.request(outside.head1, "refs/heads/story-1", "outside-root")
	result, err := provider.Merge(context.Background(), outsideRequest)
	if !errors.Is(err, ErrRepositoryBoundary) || result.Outcome != kernel.ReleaseOutcomeUnknown || outside.main(t) != outside.base {
		t.Fatalf("boundary result=%#v err=%v", result, err)
	}
	networkRequest := inside.request(inside.head1, "refs/heads/story-1", "network-url")
	networkRequest.RepositoryURL = "https://example.invalid/tekroo/teams.git"
	result, err = provider.Merge(context.Background(), networkRequest)
	if !errors.Is(err, ErrRepositoryBoundary) || result.Outcome != kernel.ReleaseOutcomeUnknown || inside.main(t) != inside.base {
		t.Fatalf("network boundary result=%#v err=%v", result, err)
	}

	unavailable := newTestProvider(t, inside.root, errorRunner{err: errors.New("git unavailable")})
	result, err = unavailable.Merge(context.Background(), inside.request(inside.head1, "refs/heads/story-1", "unavailable"))
	if !errors.Is(err, ErrGitUnavailable) || result.Outcome != kernel.ReleaseOutcomeUnknown || result.State != kernel.ReleaseProviderUnavailable || inside.main(t) != inside.base {
		t.Fatalf("unavailable result=%#v err=%v main=%s", result, err, inside.main(t))
	}
}

type testRepository struct {
	root, source, bare        string
	base, head1, head2, other string
	tree1, tree2              string
	gitVersion                string
}

func newTestRepository(t *testing.T) testRepository {
	t.Helper()
	root := t.TempDir()
	result := testRepository{root: root, source: filepath.Join(root, "source"), bare: filepath.Join(root, "target.git")}
	result.gitVersion = result.git(t, "--version")
	result.git(t, "init", "-b", "main", result.source)
	result.git(t, "-C", result.source, "config", "user.name", "Tekroo Test")
	result.git(t, "-C", result.source, "config", "user.email", "tekroo@example.invalid")
	writeTestFile(t, filepath.Join(result.source, "artifact.txt"), "base\n")
	result.git(t, "-C", result.source, "add", "artifact.txt")
	result.git(t, "-C", result.source, "commit", "-m", "base")
	result.base = result.git(t, "-C", result.source, "rev-parse", "HEAD")
	result.git(t, "-C", result.source, "switch", "-c", "story-1")
	writeTestFile(t, filepath.Join(result.source, "artifact.txt"), "base\nstory-1\n")
	result.git(t, "-C", result.source, "commit", "-am", "story one")
	result.head1 = result.git(t, "-C", result.source, "rev-parse", "HEAD")
	result.tree1 = result.git(t, "-C", result.source, "rev-parse", "HEAD^{tree}")
	result.git(t, "-C", result.source, "switch", "-c", "story-2")
	writeTestFile(t, filepath.Join(result.source, "artifact.txt"), "base\nstory-1\nstory-2\n")
	result.git(t, "-C", result.source, "commit", "-am", "story two")
	result.head2 = result.git(t, "-C", result.source, "rev-parse", "HEAD")
	result.tree2 = result.git(t, "-C", result.source, "rev-parse", "HEAD^{tree}")
	result.git(t, "-C", result.source, "switch", "main")
	result.git(t, "-C", result.source, "switch", "-c", "other")
	writeTestFile(t, filepath.Join(result.source, "other.txt"), "divergent\n")
	result.git(t, "-C", result.source, "add", "other.txt")
	result.git(t, "-C", result.source, "commit", "-m", "other")
	result.other = result.git(t, "-C", result.source, "rev-parse", "HEAD")
	result.git(t, "clone", "--bare", result.source, result.bare)
	return result
}

func (repository testRepository) request(head, changeRef, key string) kernel.ReleaseMergeRequest {
	return kernel.ReleaseMergeRequest{
		ReleasePlanID: "00000000-0000-7000-8000-000000000751", PlanDigest: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		MergeID: "00000000-0000-7000-8000-000000000752", AttemptID: "00000000-0000-7000-8000-000000000755", Round: 1,
		ProviderIdempotencyKey: key, RepositoryURL: (&url.URL{Scheme: "file", Path: repository.bare}).String(), BaseRef: "main", BaseCommit: repository.base,
		ChangeRef: changeRef, HeadCommit: head, MergeStrategy: "FF_ONLY_ORDERED",
		GitVersion: repository.gitVersion, ConflictPolicy: "FAIL_NO_IMPROVISATION",
	}
}

func (repository testRepository) main(t *testing.T) string {
	t.Helper()
	return repository.git(t, "--git-dir", repository.bare, "rev-parse", "refs/heads/main")
}

func (repository testRepository) git(t *testing.T, arguments ...string) string {
	t.Helper()
	command := exec.Command("git", arguments...)
	command.Env = append(os.Environ(), "LC_ALL=C", "TZ=UTC", "GIT_TERMINAL_PROMPT=0", "GIT_NO_LAZY_FETCH=1")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", arguments, err, output)
	}
	return stringTrimSpace(string(output))
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func newTestProvider(t *testing.T, root string, runner commandRunner) *Provider {
	t.Helper()
	canonical, err := canonicalDirectory(root)
	if err != nil {
		t.Fatal(err)
	}
	provider, err := newWithRunner(Config{AllowedRoot: root, OperationTimeout: 2 * time.Second}, canonical, runner)
	if err != nil {
		t.Fatal(err)
	}
	return provider
}

type ambiguousUpdateRunner struct {
	delegate commandRunner
	updates  int
}

func (runner *ambiguousUpdateRunner) Run(ctx context.Context, repository string, arguments ...string) ([]byte, error) {
	if len(arguments) > 0 && arguments[0] == "update-ref" {
		runner.updates++
		output, err := runner.delegate.Run(context.WithoutCancel(ctx), repository, arguments...)
		if err != nil {
			return output, err
		}
		return output, context.DeadlineExceeded
	}
	return runner.delegate.Run(ctx, repository, arguments...)
}

type errorRunner struct{ err error }

func (runner errorRunner) Run(context.Context, string, ...string) ([]byte, error) {
	return nil, runner.err
}

type rejectedUpdateRunner struct {
	delegate commandRunner
	err      error
	updates  int
}

func (runner *rejectedUpdateRunner) Run(ctx context.Context, repository string, arguments ...string) ([]byte, error) {
	if len(arguments) > 0 && arguments[0] == "update-ref" {
		runner.updates++
		return nil, runner.err
	}
	return runner.delegate.Run(ctx, repository, arguments...)
}

func stringTrimSpace(value string) string {
	for len(value) > 0 && (value[len(value)-1] == '\n' || value[len(value)-1] == '\r' || value[len(value)-1] == ' ' || value[len(value)-1] == '\t') {
		value = value[:len(value)-1]
	}
	return value
}
