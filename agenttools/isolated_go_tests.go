package agenttools

import (
	"archive/tar"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/tekroo-ai/teams/internal/verification"
)

const (
	maxTestArchiveFiles = 20000
	// Capture and execution share a source ceiling; fixtures/receipts count too.
	// Execution still bounds source plus generated files at the sandbox quota.
	maxTestArchiveBytes   = maxTestWorkspaceBytes
	maxTestWorkspaceFiles = 100000
	maxTestWorkspaceBytes = 2 << 30
)

// runIsolatedGoTests executes only committed source. The disposable checkout is
// never a linked Git worktree. Local test deployments receive independent Git
// metadata so repository self-tests work without sharing candidate objects.
func (host Host) runIsolatedGoTests(ctx context.Context, root, pattern string) (Result, error) {
	return host.runIsolatedGoTestsFrom(ctx, root, pattern, false, false)
}

// runIsolatedGoTestsWorktree copies admitted workspace files into the same
// sandbox, so implementation can observe red/green tests before one commit.
// The committed-HEAD validation tool remains a separate operation.
func (host Host) runIsolatedGoTestsWorktree(ctx context.Context, root, pattern string) (Result, error) {
	return host.runIsolatedGoTestsFrom(ctx, root, pattern, true, false)
}

func (host Host) runIsolatedGoTestsFrom(ctx context.Context, root, pattern string, worktree, compileOnly bool, options ...goTestArguments) (result Result, returnedErr error) {
	started := time.Now()
	timings := &VerificationTimings{}
	result = Result{Name: "run_go_tests", VerificationTimings: timings}
	defer func() { timings.TotalNS = time.Since(started).Nanoseconds() }()
	if compileOnly {
		result.Name = "verify_go_build"
	}
	if worktree {
		result.Name = "run_go_tests_worktree"
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	operation, cancel := context.WithTimeout(ctx, host.Timeout)
	defer cancel()
	phaseStarted := time.Now()
	if err := host.requireGitRoot(operation, root); err != nil {
		return result, err
	}
	head, err := host.command(operation, root, "git_head", host.gitBinary(), []string{"rev-parse", "--verify", "HEAD"})
	if err != nil || head.ExitCode != 0 || !validFullGitSHA(strings.TrimSpace(head.Output)) {
		return result, ErrInvalidCall
	}
	result.CommitSHA = strings.TrimSpace(head.Output)
	argv := []string{host.goBinary(), "test", pattern}
	class := verification.CommandClass(argv)
	if compileOnly {
		class = verification.Focused
	} else if class == verification.WholeModule && pattern != "./..." && strings.HasPrefix(pattern, "./") && strings.HasSuffix(pattern, "/...") {
		if worktree {
			class = verification.CommandClassAt(operation, root, argv)
		} else {
			// Classify the pinned commit, not dirty live files. NUL-separated
			// Git names preserve whitespace; a clipped/failed inventory stays broad.
			inventory, inventoryErr := host.command(operation, root, "verification_inventory", host.gitBinary(), []string{"ls-tree", "-r", "-z", "--name-only", result.CommitSHA})
			if inventoryErr == nil && inventory.ExitCode == 0 {
				class = verification.CommandClassWithInventory(argv, strings.Split(inventory.Output, "\x00"))
			}
		}
	}
	timings.PreparationNS = time.Since(phaseStarted).Nanoseconds()
	phaseStarted = time.Now()
	slot, release, err := verification.AcquireSlot(operation, class)
	timings.QueueNS = time.Since(phaseStarted).Nanoseconds()
	if err != nil {
		return result, fmt.Errorf("verification queue: %w", err)
	}
	defer release()
	phaseStarted = time.Now()
	preparationStarted := phaseStarted
	preparationComplete := false
	defer func() {
		if !preparationComplete {
			timings.PreparationNS += time.Since(preparationStarted).Nanoseconds() - timings.CleanupNS
		}
	}()
	if runtime.GOOS != "darwin" {
		return result, fmt.Errorf("isolated Go tests require the qualified macOS sandbox")
	}
	workspace, err := host.newVerificationWorkspaceForSlot(slot)
	if err != nil {
		return result, err
	}
	createdWorkspace := workspace
	defer func() {
		cleanupStarted := time.Now()
		if err := os.RemoveAll(createdWorkspace); err != nil {
			returnedErr = errors.Join(returnedErr, fmt.Errorf("verification workspace cleanup: %w", err))
		}
		timings.CleanupNS = time.Since(cleanupStarted).Nanoseconds()
	}()
	workspace, err = resolveVerificationWorkspace(createdWorkspace)
	if err != nil {
		return result, err
	}
	source := filepath.Join(workspace, "source")
	for _, path := range []string{source, filepath.Join(workspace, "home"), filepath.Join(workspace, "tmp"), filepath.Join(workspace, "cache"), filepath.Join(workspace, "gopath")} {
		if err := os.MkdirAll(path, 0700); err != nil {
			return result, err
		}
	}
	snapshotStarted := time.Now()
	if worktree {
		result.SnapshotSHA256, err = snapshotWorkingTree(operation, root, source, host.gitBinary())
	} else {
		err = archiveCommittedTree(operation, root, result.CommitSHA, source, host.gitBinary())
	}
	timings.SnapshotNS = time.Since(snapshotStarted).Nanoseconds()
	if err != nil {
		return result, err
	}
	if host.LocalTestExecution && !compileOnly {
		repositoryStarted := time.Now()
		err := host.prepareLocalTestRepository(operation, root, source, result.CommitSHA)
		timings.RepositoryNS = time.Since(repositoryStarted).Nanoseconds()
		if err != nil {
			return result, err
		}
		if err := boundedWorkspace(workspace); err != nil {
			return result, err
		}
	}
	toolchainStarted := time.Now()
	goBinary, err := exec.LookPath(host.goBinary())
	if err != nil {
		return result, err
	}
	goBinary, err = filepath.EvalSymlinks(goBinary)
	if err != nil {
		return result, err
	}
	modCache := filepath.Join(os.Getenv("HOME"), "go", "pkg", "mod")
	if configured := os.Getenv("GOMODCACHE"); configured != "" {
		modCache = configured
	} else if configured := os.Getenv("GOPATH"); configured != "" {
		modCache = filepath.Join(strings.Split(configured, string(os.PathListSeparator))[0], "pkg", "mod")
	}
	if resolved, resolveErr := filepath.EvalSymlinks(modCache); resolveErr == nil {
		modCache = resolved
	}
	gitBinary, err := exec.LookPath(host.gitBinary())
	if err != nil {
		return result, err
	}
	gitBinary, err = filepath.EvalSymlinks(gitBinary)
	if err != nil {
		return result, err
	}
	// /usr/bin/git is an SDK launcher and tries to create a host xcrun cache.
	// Resolve that configured launcher outside the sandbox, then use the real
	// executable inside it. Neither fixture execution nor caching escapes.
	if gitBinary == "/usr/bin/git" {
		lookup := exec.CommandContext(operation, "/usr/bin/xcrun", "--find", "git")
		lookup.Env = toolEnvironment()
		located, lookupErr := lookup.Output()
		if lookupErr != nil {
			return result, fmt.Errorf("resolve isolated Git toolchain: %w", lookupErr)
		}
		gitBinary, err = filepath.EvalSymlinks(strings.TrimSpace(string(located)))
		if err != nil || gitBinary == "/usr/bin/git" || !filepath.IsAbs(gitBinary) {
			return result, ErrBoundary
		}
	}
	profile, err := goTestSandboxProfileWithGit(workspace, goBinary, modCache, gitBinary)
	timings.ToolchainNS = time.Since(toolchainStarted).Nanoseconds()
	if err != nil {
		return result, err
	}
	// Both build checks and executing tests use the same path-sensitive compiler
	// artifacts. The stable workspace pathname permits reuse without -trimpath;
	// executed fixtures retain their absolute runtime.Caller source paths.
	var selected goTestArguments
	if len(options) > 0 {
		selected = options[0]
	}
	arguments := []string{"test", "-count=1"}
	if !compileOnly {
		arguments = append(arguments, "-json")
	}
	if len(selected.Tags) > 0 {
		arguments = append(arguments, "-tags="+strings.Join(selected.Tags, ","))
	}
	if compileOnly {
		// -run '^$' still executes init/TestMain. Compile-only means -c: no
		// candidate executable or test initializer is run, even with side effects.
		compiled := filepath.Join(workspace, "compiled-tests")
		if err := os.Mkdir(compiled, 0700); err != nil {
			return result, err
		}
		arguments = append(arguments, "-c", "-o", compiled+string(filepath.Separator))
	}
	arguments = append(arguments, pattern)
	boundedCommand, _ := verification.GoCommand(append([]string{goBinary}, arguments...))
	arguments = boundedCommand[1:]
	command := exec.CommandContext(operation, goBinary, arguments...)
	if !host.LocalTestExecution || compileOnly {
		command = exec.CommandContext(operation, "/usr/bin/sandbox-exec", append([]string{"-p", profile, goBinary}, arguments...)...)
	}
	command.Dir = source
	command.Env = goTestEnvironment(workspace, gitBinary, modCache)
	timings.PreparationNS += time.Since(phaseStarted).Nanoseconds()
	preparationComplete = true
	sourceIdentity := "commit:" + result.CommitSHA
	if worktree {
		sourceIdentity = "snapshot:" + result.SnapshotSHA256
	}
	if err := host.seedCompilationCache(operation, workspace, source, goBinary, profile, command.Env, pattern, sourceIdentity, timings, selected.Tags); err != nil {
		return result, err
	}
	phaseStarted = time.Now()
	if !compileOnly {
		inventoryArgs := []string{"list", "-e", "-json"}
		if len(selected.Tags) > 0 {
			inventoryArgs = append(inventoryArgs, "-tags="+strings.Join(selected.Tags, ","))
		}
		inventoryArgs = append(inventoryArgs, pattern)
		inventory := exec.CommandContext(operation, goBinary, inventoryArgs...)
		inventory.Dir, inventory.Env = source, command.Env
		inventoryOutput := &boundedOutput{limit: 4 << 20}
		inventory.Stdout = inventoryOutput
		if err := inventory.Run(); err != nil {
			timings.PreparationNS += time.Since(phaseStarted).Nanoseconds()
			if operation.Err() != nil {
				return result, operation.Err()
			}
			return result, fmt.Errorf("Go test selection inventory: %w", err)
		}
		excluded, err := excludedGoTests(inventoryOutput.data)
		if err != nil {
			return result, err
		}
		result.TestEvidence = &GoTestEvidence{Command: append([]string{goBinary}, arguments...), Tags: slices.Clone(selected.Tags), Tests: []GoTestCase{}, Excluded: excluded}
	}
	timings.PreparationNS += time.Since(phaseStarted).Nanoseconds()
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	command.Cancel = func() error {
		if command.Process == nil {
			return os.ErrProcessDone
		}
		return syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
	}
	command.WaitDelay = 2 * time.Second
	output := &boundedOutput{limit: 128 << 10}
	var timeout verification.TimeoutDetector
	// Use one shared writer, so os/exec serializes stdout/stderr observations.
	var observations io.Writer = io.MultiWriter(output, &timeout)
	var events *goTestObservations
	if !compileOnly {
		events = &goTestObservations{output: observations, tests: map[string]GoTestCase{}, complete: true}
		observations = events
	}
	command.Stdout, command.Stderr = observations, observations
	phaseStarted = time.Now()
	if err := command.Start(); err != nil {
		return result, fmt.Errorf("isolated Go test process failed: %w", err)
	}
	waited := make(chan error, 1)
	go func() { waited <- command.Wait() }()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	var workspaceErr error
	for {
		select {
		case err = <-waited:
			goto finished
		case <-ticker.C:
			scanStarted := time.Now()
			workspaceErr = boundedWorkspace(workspace)
			timings.QuotaScanNS += time.Since(scanStarted).Nanoseconds()
			if workspaceErr != nil {
				_ = syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
				err = <-waited
				goto finished
			}
		case <-operation.Done():
			err = <-waited
			goto finished
		}
	}
finished:
	if events != nil {
		events.finish(result.TestEvidence)
	}
	timings.ExecutionNS = time.Since(phaseStarted).Nanoseconds()
	result.Output = string(output.data)
	foldedOutput := strings.ToLower(result.Output)
	result.PermissionDenial = strings.Contains(foldedOutput, "operation not permitted") || strings.Contains(foldedOutput, "permission denied")
	digest := sha256.Sum256(output.data)
	result.SHA256 = hex.EncodeToString(digest[:])
	if workspaceErr != nil {
		return result, workspaceErr
	}
	if operation.Err() != nil {
		return result, operation.Err()
	}
	if errors.Is(err, ErrTooLarge) {
		return result, ErrTooLarge
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		result.ExitCode = exit.ExitCode()
		if timeout.TimedOut() {
			actualExit := result.ExitCode
			result.ProcessExitCode = &actualExit
			result.ExitCode = -1 // Incomplete suite; preserve the process exit separately.
			result.ExecutionError = "Go test execution timed out; candidate verdict INCONCLUSIVE"
			result.TimeoutEvidence = timeout.Evidence()
		}
		return result, nil
	}
	if err != nil {
		return result, fmt.Errorf("isolated Go test process failed: %w", err)
	}
	return result, nil
}

// Acquire must be held by the caller. One fresh directory at a stable pathname
// per process/cache/lane lets Go reuse local-package action IDs without -trimpath.
// Contents are never reused: snapshots/Git metadata are rebuilt and removed
// after every call. Separate processes cannot share a writable checkout; a
// leftover directory fails closed rather than deleting an unknown prior run.
func (host Host) newVerificationWorkspace() (string, error) {
	return host.newVerificationWorkspaceForSlot(0)
}

func (host Host) newVerificationWorkspaceForSlot(slot int) (string, error) {
	if slot < 0 || slot >= verification.MaxConcurrent {
		return "", ErrInvalidCall
	}
	if host.CompilationCache == "" {
		return os.MkdirTemp("", "tekroo-go-test-")
	}
	key := sha256.Sum256([]byte(host.CompilationCache))
	parent, err := filepath.EvalSymlinks(os.TempDir())
	if err != nil {
		return "", err
	}
	workspace := filepath.Join(parent, "tekroo-go-test-"+strconv.Itoa(os.Getpid())+"-"+hex.EncodeToString(key[:])+"-lane"+strconv.Itoa(slot))
	if err := os.Mkdir(workspace, 0700); err != nil {
		return "", fmt.Errorf("fresh verification workspace required at %s (prior contents preserved): %w", workspace, err)
	}
	return workspace, nil
}

func resolveVerificationWorkspace(created string) (string, error) {
	parent, err := filepath.EvalSymlinks(filepath.Dir(created))
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(created)
	if err != nil {
		return "", err
	}
	// Resolve platform temp-directory aliases, but never follow a replaced leaf
	// into another directory. Cleanup keeps the original path for the same reason.
	if resolved != filepath.Join(parent, filepath.Base(created)) {
		return "", ErrBoundary
	}
	return resolved, nil
}

func goTestEnvironment(workspace, gitBinary, modCache string) []string {
	environment := append(toolEnvironment(),
		// Fixture subprocesses must select the same concrete Git admitted by
		// policy, not a different launcher inherited from the daemon's PATH.
		"PATH="+filepath.Dir(gitBinary)+string(os.PathListSeparator)+os.Getenv("PATH"),
		"HOME="+filepath.Join(workspace, "home"),
		"TMPDIR="+filepath.Join(workspace, "tmp"),
		"GOCACHE="+filepath.Join(workspace, "cache"),
		"GOPATH="+filepath.Join(workspace, "gopath"),
		"GOMODCACHE="+modCache,
		"XDG_CACHE_HOME="+filepath.Join(workspace, "cache"),
		"CGO_ENABLED=0",
		"GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_TERMINAL_PROMPT=0", "GIT_ATTR_NOSYSTEM=1",
	)
	return verification.Environment(environment)
}

func (host Host) prepareLocalTestRepository(ctx context.Context, root, source, commit string) error {
	// Fetch one commit into a new object store, not a linked worktree, shared
	// repository or hard-linked clone. This preserves the exact HEAD/tree for
	// nested repository tools while keeping their writes in the disposable copy.
	for _, arguments := range [][]string{
		{"init", "--quiet"},
		{"-c", "core.hooksPath=/dev/null", "fetch", "--quiet", "--no-tags", "--depth=1", "--", root, commit},
		{"update-ref", "HEAD", commit},
		{"read-tree", commit},
	} {
		result, err := host.command(ctx, source, "prepare_local_test_repository", host.gitBinary(), arguments)
		if err != nil || result.ExitCode != 0 {
			return fmt.Errorf("prepare disposable test checkout: %s: %w", result.Stderr, errors.Join(ErrInvalidCall, err))
		}
	}
	return nil
}

func boundedWorkspace(root string) error {
	var count, bytes int64
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if errors.Is(walkErr, os.ErrNotExist) {
			return nil
		}
		if walkErr != nil {
			return walkErr
		}
		count++
		if count > maxTestWorkspaceFiles {
			return fmt.Errorf("workspace entry quota: %d exceeds %d: %w", count, maxTestWorkspaceFiles, ErrTooLarge)
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if info.Size() > maxTestWorkspaceBytes-bytes {
			return fmt.Errorf("workspace byte quota: %d exceeds %d: %w", bytes+info.Size(), int64(maxTestWorkspaceBytes), ErrTooLarge)
		}
		bytes += info.Size()
		return nil
	})
	return err
}

func archiveCommittedTree(ctx context.Context, root, commit, destination, gitBinary string) error {
	command := exec.CommandContext(ctx, gitBinary, "-c", "core.hooksPath=/dev/null", "archive", "--format=tar", commit)
	command.Dir = root
	command.Env = toolEnvironment()
	stream, err := command.StdoutPipe()
	if err != nil {
		return err
	}
	if err := command.Start(); err != nil {
		return err
	}
	reader := tar.NewReader(stream)
	var count, total int64
	var extractionErr error
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			extractionErr = err
			break
		}
		count++
		if count > maxTestArchiveFiles || header.Size < 0 || header.Size > maxTestArchiveBytes-total {
			extractionErr = ErrTooLarge
			break
		}
		total += header.Size
		name := filepath.Clean(header.Name)
		if name == "." || filepath.IsAbs(name) || name == ".." || strings.HasPrefix(name, ".."+string(os.PathSeparator)) {
			extractionErr = fmt.Errorf("archive path %q: %w", header.Name, ErrBoundary)
			break
		}
		target := filepath.Join(destination, name)
		switch header.Typeflag {
		case tar.TypeXGlobalHeader, tar.TypeXHeader:
			// Git's tar stream may carry PAX metadata before tree entries.
		case tar.TypeDir:
			extractionErr = os.MkdirAll(target, 0700)
		case tar.TypeReg, tar.TypeRegA:
			if extractionErr = os.MkdirAll(filepath.Dir(target), 0700); extractionErr == nil {
				var file *os.File
				file, extractionErr = os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
				if extractionErr == nil {
					_, extractionErr = io.CopyN(file, reader, header.Size)
					if closeErr := file.Close(); extractionErr == nil {
						extractionErr = closeErr
					}
				}
			}
		default:
			// Links, devices, and FIFOs cannot appear in an executable test snapshot.
			extractionErr = fmt.Errorf("archive entry %q type %d: %w", header.Name, header.Typeflag, ErrBoundary)
		}
		if extractionErr != nil {
			break
		}
	}
	if extractionErr != nil {
		_ = command.Process.Kill()
		_ = command.Wait()
		return extractionErr
	}
	if err := command.Wait(); err != nil {
		return fmt.Errorf("archive committed tree: %w", err)
	}
	return nil
}

func goTestSandboxProfile(workspace, goBinary, modCache string) (string, error) {
	return goTestSandboxProfileWithGit(workspace, goBinary, modCache, "")
}

func goTestSandboxProfileWithGit(workspace, goBinary, modCache, gitBinary string) (string, error) {
	for _, path := range []string{workspace, goBinary, modCache} {
		if !filepath.IsAbs(path) || strings.ContainsAny(path, "\x00\r\n") {
			return "", ErrBoundary
		}
	}
	if gitBinary != "" && (!filepath.IsAbs(gitBinary) || strings.ContainsAny(gitBinary, "\x00\r\n")) {
		return "", ErrBoundary
	}
	// The default-deny profile permits read-only OS/toolchain files, the
	// preexisting module cache, and reads/writes only in this disposable root.
	// Native development tests may bind/connect on loopback for HTTP fixtures.
	// This intentionally permits existing localhost services too; filesystem,
	// non-loopback network and process-signal restrictions remain unchanged.
	goRoot := filepath.Dir(filepath.Dir(goBinary))
	// EvalSymlinks lstat's each ancestor even when the target is inside the
	// admitted snapshot. Permit metadata on these exact directories, not their
	// contents or descendants; outside-file reads/writes remain denied.
	ancestors := make(map[string]bool)
	metadataTargets := []string{workspace, goRoot, modCache}
	if gitBinary != "" {
		metadataTargets = append(metadataTargets, gitBinary)
	}
	for _, target := range metadataTargets {
		for parent := filepath.Dir(target); ; parent = filepath.Dir(parent) {
			ancestors[parent] = true
			if parent == string(filepath.Separator) {
				break
			}
		}
	}
	metadata := make([]string, 0, len(ancestors))
	for parent := range ancestors {
		metadata = append(metadata, "(literal "+strconv.Quote(parent)+")")
	}
	sort.Strings(metadata)
	profile := `(version 1)
(deny default)
(allow network-bind (local ip "localhost:*"))
(allow network-inbound (local ip "localhost:*"))
(allow network-outbound (remote ip "localhost:*"))
(allow file-read-metadata ` + strings.Join(metadata, " ") + `)
(allow process-exec (subpath ` + strconv.Quote(goRoot) + `) (subpath ` + strconv.Quote(workspace) + `))
(allow process-fork)
(allow process-info*)
(allow sysctl*)
(allow mach-lookup)
(allow ipc-posix*)
(allow file-read* (literal "/")
  (subpath "/usr") (subpath "/System") (subpath "/Library")
  (subpath "/private/etc") (subpath "/private/var/db") (subpath "/dev")
  (subpath "/opt/homebrew")
  (subpath ` + strconv.Quote(filepath.Dir(goBinary)) + `)
  (subpath ` + strconv.Quote(modCache) + `)
  (subpath ` + strconv.Quote(workspace) + `))
(allow file-write* (subpath ` + strconv.Quote(workspace) + `) (literal "/dev/null"))`
	if gitBinary != "" {
		profile += "\n(allow process-exec (literal " + strconv.Quote(gitBinary) + "))"
		gitRoot := filepath.Dir(filepath.Dir(gitBinary))
		profile += "\n(allow file-read* (literal " + strconv.Quote(gitBinary) + ") (subpath " + strconv.Quote(filepath.Join(gitRoot, "lib")) + ") (subpath " + strconv.Quote(filepath.Join(gitRoot, "libexec", "git-core")) + ") (subpath " + strconv.Quote(filepath.Join(gitRoot, "share", "git-core")) + "))"
		// Local clone/worktree fixtures need Git's own helper executables;
		// Git launches upload-pack through sh. Both remain in this sandbox:
		// no external network, host writes, or agent-facing shell tool is added.
		profile += "\n(allow process-exec (subpath " + strconv.Quote(filepath.Join(gitRoot, "libexec", "git-core")) + "))"
		// On current macOS sh is a launcher for bash; its selection file is
		// system metadata, not a fixture-controlled or user configuration path.
		profile += "\n(allow process-exec (literal \"/bin/sh\") (literal \"/bin/bash\"))\n(allow file-read* (literal \"/bin/sh\") (literal \"/bin/bash\") (literal \"/private/var/select/sh\"))\n(allow file-read-metadata (literal \"/bin\") (literal \"/private/var/select\"))"
	}
	return profile, nil
}
