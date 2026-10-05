package agenttools

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// snapshotWorkingTree copies Git-visible files, including untracked but not
// ignored files, into a disposable test root. Its digest identifies the exact
// bytes the test sees; neither the live workspace nor its Git index is changed.
func snapshotWorkingTree(ctx context.Context, root, destination, gitBinary string) (string, error) {
	command := exec.CommandContext(ctx, gitBinary, "-c", "core.hooksPath=/dev/null", "ls-files", "--cached", "--others", "--exclude-standard", "--full-name", "-z")
	command.Dir = root
	command.Env = toolEnvironment()
	output := &boundedOutput{limit: 4 << 20}
	command.Stdout = output
	command.Stderr = &boundedOutput{limit: 4 << 10}
	if err := command.Run(); err != nil {
		return "", fmt.Errorf("list test snapshot files: %w", err)
	}
	if len(output.data) > 0 && output.data[len(output.data)-1] != 0 {
		return "", ErrTooLarge
	}
	entries := bytes.Split(output.data, []byte{0})
	if len(entries) > maxTestArchiveFiles+1 {
		return "", ErrTooLarge
	}
	paths := make([]string, 0, len(entries))
	for _, entry := range entries {
		if len(entry) == 0 {
			continue
		}
		path := string(entry)
		if _, err := relativePath(path); err != nil || path == ".git" || strings.HasPrefix(path, ".git"+string(os.PathSeparator)) {
			return "", ErrBoundary
		}
		if path == ".openhands" || strings.HasPrefix(path, ".openhands"+string(os.PathSeparator)) {
			// The injected runtime hook is not candidate source.
			continue
		}
		paths = append(paths, path)
	}
	sort.Strings(paths)
	digest := sha256.New()
	var total int64
	for index, relative := range paths {
		if index > 0 && paths[index-1] == relative {
			return "", ErrBoundary
		}
		if err := ctx.Err(); err != nil {
			return "", err
		}
		original := filepath.Join(root, relative)
		info, err := os.Lstat(original)
		if errors.Is(err, os.ErrNotExist) {
			// A tracked deletion must remain absent in the captured working tree.
			continue
		}
		if err != nil {
			return "", err
		}
		if !info.Mode().IsRegular() {
			return "", ErrBoundary
		}
		if info.Size() > maxTestArchiveBytes-total {
			return "", fmt.Errorf("test snapshot exceeds %d-byte source limit at %q: %w", maxTestArchiveBytes, relative, ErrTooLarge)
		}
		resolved, err := filepath.EvalSymlinks(original)
		if err != nil || resolved != original {
			return "", ErrBoundary
		}
		target := filepath.Join(destination, relative)
		if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
			return "", err
		}
		if err := copySnapshotFile(original, target, relative, info, digest); err != nil {
			return "", err
		}
		total += info.Size()
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}

func copySnapshotFile(original, target, relative string, originalInfo os.FileInfo, digest hash.Hash) error {
	source, err := os.Open(original)
	if err != nil {
		return err
	}
	defer source.Close()
	openedInfo, err := source.Stat()
	if err != nil || !openedInfo.Mode().IsRegular() || !os.SameFile(originalInfo, openedInfo) || openedInfo.Size() != originalInfo.Size() {
		return ErrBoundary
	}
	copy, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(relative)))
	_, _ = digest.Write(length[:])
	_, _ = digest.Write([]byte(relative))
	binary.BigEndian.PutUint64(length[:], uint64(openedInfo.Size()))
	_, _ = digest.Write(length[:])
	_, err = io.CopyN(io.MultiWriter(copy, digest), source, openedInfo.Size())
	closeErr := copy.Close()
	if err != nil {
		return err
	}
	return closeErr
}
