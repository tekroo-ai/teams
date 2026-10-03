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
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	maxTestArchiveFiles   = 20000
	maxTestArchiveBytes   = 512 << 20
	maxTestWorkspaceFiles = 100000
	maxTestWorkspaceBytes = 2 << 30
)

// runIsolatedGoTests executes only committed source. The disposable checkout is
// deliberately not a Git worktree: test code cannot modify the candidate or
// gain a writable .git directory. No live tool adapter exposes this operation
// until its effect lifecycle is also bound to the turn journal.
func (host Host) runIsolatedGoTests(ctx context.Context, root, pattern string) (Result, error) {
	result := Result{Name: "run_go_tests"}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	operation, cancel := context.WithTimeout(ctx, host.Timeout)
	defer cancel()
	if runtime.GOOS != "darwin" {
		return result, fmt.Errorf("isolated Go tests require the qualified macOS sandbox")
	}
	if err := host.requireGitRoot(operation, root); err != nil {
		return result, err
	}
	head, err := host.command(operation, root, "git_head", host.gitBinary(), []string{"rev-parse", "--verify", "HEAD"})
	if err != nil || head.ExitCode != 0 || !validFullGitSHA(strings.TrimSpace(head.Output)) {
		return result, ErrInvalidCall
	}
	result.CommitSHA = strings.TrimSpace(head.Output)
	workspace, err := os.MkdirTemp("", "tekroo-go-test-")
	if err != nil {
		return result, err
	}
	defer os.RemoveAll(workspace)
	workspace, err = filepath.EvalSymlinks(workspace)
	if err != nil {
		return result, err
	}
	source := filepath.Join(workspace, "source")
	for _, path := range []string{source, filepath.Join(workspace, "home"), filepath.Join(workspace, "tmp"), filepath.Join(workspace, "cache"), filepath.Join(workspace, "gopath")} {
		if err := os.MkdirAll(path, 0700); err != nil {
			return result, err
		}
	}
	if err := archiveCommittedTree(operation, root, result.CommitSHA, source, host.gitBinary()); err != nil {
		return result, err
	}
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
	profile, err := goTestSandboxProfile(workspace, goBinary, modCache)
	if err != nil {
		return result, err
	}
	command := exec.CommandContext(operation, "/usr/bin/sandbox-exec", "-p", profile, goBinary, "test", "-count=1", pattern)
	command.Dir = source
	command.Env = append(toolEnvironment(),
		"HOME="+filepath.Join(workspace, "home"),
		"TMPDIR="+filepath.Join(workspace, "tmp"),
		"GOCACHE="+filepath.Join(workspace, "cache"),
		"GOPATH="+filepath.Join(workspace, "gopath"),
		"GOMODCACHE="+modCache,
		"XDG_CACHE_HOME="+filepath.Join(workspace, "cache"),
		"CGO_ENABLED=0",
	)
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	command.Cancel = func() error {
		if command.Process == nil {
			return os.ErrProcessDone
		}
		return syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
	}
	command.WaitDelay = 2 * time.Second
	output := &boundedOutput{limit: 128 << 10}
	command.Stdout, command.Stderr = output, output
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
			workspaceErr = boundedWorkspace(workspace)
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
	result.Output = string(output.data)
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
		return result, nil
	}
	if err != nil {
		return result, fmt.Errorf("isolated Go test process failed: %w", err)
	}
	return result, nil
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
			return ErrTooLarge
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
			return ErrTooLarge
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
	for _, path := range []string{workspace, goBinary, modCache} {
		if !filepath.IsAbs(path) || strings.ContainsAny(path, "\x00\r\n") {
			return "", ErrBoundary
		}
	}
	// The default-deny profile permits read-only OS/toolchain files, the
	// preexisting module cache, and reads/writes only in this disposable root.
	// No network or process-signal permission is granted.
	goRoot := filepath.Dir(filepath.Dir(goBinary))
	profile := `(version 1)
(deny default)
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
	return profile, nil
}
