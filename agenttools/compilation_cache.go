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
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/tekroo-ai/teams/internal/verification"
)

// This is only a bounded compiler-priming hint, never a test-result cache.
// The consumer still runs go test -count=1 and Go checks its action IDs itself;
// missing/evicted artifacts are compiled in the fresh private cache. A changed
// snapshot, environment, selector or mode requires a new persistent primer.
var compilationPrimers = struct {
	sync.Mutex
	last map[string][32]byte
}{last: make(map[string][32]byte)}

func hasCompilationPrimer(namespace string, key [32]byte) bool {
	compilationPrimers.Lock()
	defer compilationPrimers.Unlock()
	previous, found := compilationPrimers.last[namespace]
	return found && previous == key
}

func rememberCompilationPrimer(namespace string, key [32]byte) {
	compilationPrimers.Lock()
	defer compilationPrimers.Unlock()
	// Cache configurations are normally fixed for the process lifetime. Keep
	// test fixtures/dynamic configurations bounded; clearing only causes misses.
	if len(compilationPrimers.last) >= 32 {
		clear(compilationPrimers.last)
	}
	compilationPrimers.last[namespace] = key
}

// Only compile-only go test may write the persistent cache. Test binaries never
// receive that path: seed files are copied, not linked, into their private cache.
// Go's action IDs bind source, imports, compiler flags and toolchain content.
// Precompilation must match the consumer's compiler flags: -trimpath artifacts
// cannot seed an untrimmed execution. Retention runs under the same lock
// as compilation and copying; Go treats evicted index/data files as misses.
func (host Host) seedCompilationCache(ctx context.Context, workspace, source, goBinary, profile string, environment []string, pattern, sourceIdentity string, compileOnly bool, timings *VerificationTimings) error {
	if host.CompilationCache == "" {
		return nil
	}
	cacheStarted := time.Now()
	defer func() { timings.CacheNS += time.Since(cacheStarted).Nanoseconds() }()
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
	started := time.Now()
	unlock, err := lockEffectPath(ctx, cache, "")
	timings.CacheLockNS += time.Since(started).Nanoseconds()
	if err != nil {
		return err
	}
	defer unlock()
	started = time.Now()
	err = retainCompilationCache(ctx, cache, 512<<20, maxTestWorkspaceFiles/2)
	timings.CacheRetentionNS += time.Since(started).Nanoseconds()
	if err != nil {
		return fmt.Errorf("compilation cache retention: %w", err)
	}
	key := sha256.Sum256([]byte(strings.Join(append([]string{sourceIdentity, source, pattern, strconv.FormatBool(compileOnly), profile}, environment...), "\x00")))
	if sourceIdentity != "" && hasCompilationPrimer(cache, key) {
		started = time.Now()
		err = copyCompilationCache(ctx, cache, filepath.Join(workspace, "cache"))
		timings.CacheCopyNS += time.Since(started).Nanoseconds()
		return err
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
	args := append([]string{goBinary, "test"}, goTestCompileFlags(compileOnly)...)
	args, _ = verification.GoCommand(append(args, "-c", "-o", compiled+string(filepath.Separator), pattern))
	command := exec.CommandContext(ctx, "/usr/bin/sandbox-exec", append([]string{"-p", compilerProfile}, args...)...)
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
	started = time.Now()
	err = command.Run()
	compiledSuccessfully := err == nil
	timings.CacheCompileNS += time.Since(started).Nanoseconds()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil {
		if _, ok := err.(*exec.ExitError); !ok {
			return fmt.Errorf("compilation cache process: %w", err)
		}
	}
	started = time.Now()
	err = retainCompilationCache(ctx, cache, 512<<20, maxTestWorkspaceFiles/2)
	timings.CacheRetentionNS += time.Since(started).Nanoseconds()
	if err != nil {
		return fmt.Errorf("compilation cache retention: %w", err)
	}
	started = time.Now()
	err = copyCompilationCache(ctx, cache, filepath.Join(workspace, "cache"))
	timings.CacheCopyNS += time.Since(started).Nanoseconds()
	if err == nil && compiledSuccessfully && sourceIdentity != "" {
		rememberCompilationPrimer(cache, key)
	}
	return err
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

// retainCompilationCache bounds reusable compiler data independently of the
// execution workspace. Leave room for source, temporary files and binaries.
// Validate the whole namespace before removal so a malformed cache fails closed.
// mtime is Go's approximate last-use clock; ties use paths for stable eviction.
// The caller must hold the toolchain namespace's effect lock.
func retainCompilationCache(ctx context.Context, root string, maximumBytes int64, maximumEntries int) error {
	type entry struct {
		path string
		info os.FileInfo
	}
	var files []entry
	var bytes int64
	entries := 0
	err := filepath.WalkDir(root, func(path string, item os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		entries++
		if entries > maxTestWorkspaceFiles {
			return fmt.Errorf("compilation cache scan exceeds %d entries: %w", maxTestWorkspaceFiles, ErrTooLarge)
		}
		if item.Type()&os.ModeSymlink != 0 {
			return ErrBoundary
		}
		if item.IsDir() {
			return nil
		}
		info, err := item.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return ErrBoundary
		}
		bytes += info.Size()
		files = append(files, entry{path, info})
		return nil
	})
	if err != nil {
		return err
	}
	// Validate every entry, but sorting is useful only when eviction is needed.
	if bytes <= maximumBytes && entries <= maximumEntries {
		return nil
	}
	sort.Slice(files, func(i, j int) bool {
		if files[i].info.ModTime().Equal(files[j].info.ModTime()) {
			return files[i].path < files[j].path
		}
		return files[i].info.ModTime().Before(files[j].info.ModTime())
	})
	for _, file := range files {
		if bytes <= maximumBytes && entries <= maximumEntries {
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := os.Remove(file.path); err != nil {
			return err
		}
		bytes -= file.info.Size()
		entries--
	}
	if bytes > maximumBytes || entries > maximumEntries {
		return fmt.Errorf("compilation cache quota: %d bytes, %d entries: %w", bytes, entries, ErrTooLarge)
	}
	return nil
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
		if cloned, err := cloneCacheFile(path, target); cloned || err != nil {
			return err
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
