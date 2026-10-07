package agenttools

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Only compile-only go test may write the persistent cache. Test binaries never
// receive that path: seed files are copied, not linked, into their private cache.
// Go's action IDs bind source, imports, compiler flags and toolchain content.
func (host Host) seedCompilationCache(ctx context.Context, workspace, source, goBinary, profile string, environment []string, pattern string) error {
	if host.CompilationCache == "" {
		return nil
	}
	cache := host.CompilationCache
	if !filepath.IsAbs(cache) || strings.ContainsAny(cache, "\x00\r\n") {
		return ErrBoundary
	}
	if err := os.MkdirAll(cache, 0700); err != nil {
		return err
	}
	resolved, err := filepath.EvalSymlinks(cache)
	if err != nil || resolved != filepath.Clean(cache) || withinRoot(source, resolved) || withinRoot(resolved, source) {
		return ErrBoundary
	}
	binary, err := os.Open(goBinary)
	if err != nil {
		return err
	}
	h := sha256.New()
	_, err = io.Copy(h, binary)
	_ = binary.Close()
	if err != nil {
		return err
	}
	cache = filepath.Join(cache, hex.EncodeToString(h.Sum(nil)))
	if err := os.MkdirAll(cache, 0700); err != nil {
		return err
	}
	unlock, err := lockEffectPath(ctx, cache, "")
	if err != nil {
		return err
	}
	defer unlock()
	if err := boundedWorkspace(cache); err != nil {
		return fmt.Errorf("compilation cache quota: %w", err)
	}
	// The compiler can execute its installed tools, never a generated binary,
	// custom tool, repository script or shell. No build-time network is needed.
	compilerProfile := strings.ReplaceAll(profile, "(subpath "+strconv.Quote(workspace)+"))\n(allow process-fork)", ")\n(allow process-fork)")
	compilerProfile = strings.ReplaceAll(compilerProfile, "(allow process-exec (literal \"/bin/sh\") (literal \"/bin/bash\"))", "")
	for _, line := range []string{`(allow network-bind (local ip "localhost:*"))`, `(allow network-inbound (local ip "localhost:*"))`, `(allow network-outbound (remote ip "localhost:*"))`} {
		compilerProfile = strings.ReplaceAll(compilerProfile, line, "")
	}
	compilerProfile += "\n(allow file-read* (subpath " + strconv.Quote(cache) + "))\n(allow file-write* (subpath " + strconv.Quote(cache) + "))"
	for parent := filepath.Dir(cache); ; parent = filepath.Dir(parent) {
		compilerProfile += "\n(allow file-read-metadata (literal " + strconv.Quote(parent) + "))"
		if parent == string(filepath.Separator) {
			break
		}
	}
	compiled := filepath.Join(workspace, "compiled")
	if err := os.Mkdir(compiled, 0700); err != nil {
		return err
	}
	command := exec.CommandContext(ctx, "/usr/bin/sandbox-exec", "-p", compilerProfile, goBinary, "test", "-c", "-o", compiled+string(filepath.Separator), pattern)
	command.Dir = source
	command.Env = replaceEnvironment(environment, "GOCACHE", cache)
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
	// A compile failure is a normal test observation, not a cache failure.
	// The test phase still runs and records its own exact output and exit code.
	err = command.Run()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil {
		if _, ok := err.(*exec.ExitError); !ok {
			return fmt.Errorf("compilation cache process: %w", err)
		}
	}
	if err := boundedWorkspace(cache); err != nil {
		return fmt.Errorf("compilation cache quota: %w", err)
	}
	return copyCompilationCache(ctx, cache, filepath.Join(workspace, "cache"))
}

func replaceEnvironment(environment []string, name, value string) []string {
	result := make([]string, 0, len(environment)+1)
	for _, entry := range environment {
		if !strings.HasPrefix(entry, name+"=") {
			result = append(result, entry)
		}
	}
	return append(result, name+"="+value)
}

func copyCompilationCache(ctx context.Context, source, destination string) error {
	return filepath.WalkDir(source, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		rel, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		target := filepath.Join(destination, rel)
		if entry.Type()&os.ModeSymlink != 0 {
			return ErrBoundary
		}
		if entry.IsDir() {
			return os.MkdirAll(target, 0700)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return ErrBoundary
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			_ = in.Close()
			return err
		}
		_, err = io.Copy(out, in)
		_ = in.Close()
		closeErr := out.Close()
		if err != nil {
			return err
		}
		return closeErr
	})
}
