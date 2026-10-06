package agenttools

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

type fileEdit struct {
	Path           string `json:"path"`
	OldText        string `json:"old_text"`
	NewText        string `json:"new_text"`
	ExpectedSHA256 string `json:"expected_sha256"`
}

func decodeFileEdit(raw json.RawMessage) (fileEdit, error) {
	var args fileEdit
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || fields["new_text"] == nil || string(fields["new_text"]) == "null" {
		return fileEdit{}, ErrInvalidCall
	}
	// Unlike creation, an edit always targets an existing, hash-bound file.
	if decode(raw, &args) != nil || args.Path == "" || args.OldText == "" ||
		len(args.OldText) > 1<<20 || len(args.NewText) > 1<<20 ||
		!utf8.ValidString(args.OldText) || !utf8.ValidString(args.NewText) ||
		len(args.ExpectedSHA256) != 64 || !validExpectedSHA(args.ExpectedSHA256) {
		return fileEdit{}, ErrInvalidCall
	}
	if _, err := relativePath(args.Path); err != nil {
		return fileEdit{}, err
	}
	if args.Path == ".git" || strings.HasPrefix(args.Path, ".git/") ||
		args.Path == ".openhands" || strings.HasPrefix(args.Path, ".openhands/") {
		return fileEdit{}, ErrForbidden
	}
	return args, nil
}

func prepareFileEdit(root string, args fileEdit) ([]byte, error) {
	path, err := existingPath(root, args.Path)
	if err != nil {
		return nil, err
	}
	// A directory alias must not bypass protection of repository/runtime metadata.
	relative, err := filepath.Rel(root, path)
	if err != nil {
		return nil, ErrBoundary
	}
	if relative == ".git" || strings.HasPrefix(relative, ".git/") ||
		relative == ".openhands" || strings.HasPrefix(relative, ".openhands/") {
		return nil, ErrForbidden
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return nil, ErrBoundary
	}
	if info.Size() > 1<<20 {
		return nil, ErrTooLarge
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(content)
	actual := hex.EncodeToString(digest[:])
	if actual != args.ExpectedSHA256 {
		return nil, filePreconditionError(args.Path, args.ExpectedSHA256, actual, true)
	}
	if !utf8.Valid(content) {
		return nil, ErrInvalidCall
	}
	text := string(content)
	// Count overlapping occurrences too: replacing "aa" in "aaa" is ambiguous.
	index := strings.Index(text, args.OldText)
	if index < 0 || strings.Contains(text[index+1:], args.OldText) {
		return nil, fmt.Errorf("%w: edit_file path=%q old_text must match exactly once", ErrConflict, args.Path)
	}
	if len(content)-len(args.OldText)+len(args.NewText) > 1<<20 {
		return nil, ErrTooLarge
	}
	return []byte(text[:index] + args.NewText + text[index+len(args.OldText):]), nil
}
