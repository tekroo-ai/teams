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
	Path string `json:"path"`
	// Single-replacement fields are decoded only for retained historical calls.
	// The model-facing contract exposes one ordered edits list instead.
	OldText        string     `json:"old_text,omitempty"`
	NewText        string     `json:"new_text,omitempty"`
	ExpectedSHA256 string     `json:"expected_sha256"`
	Edits          []textEdit `json:"edits,omitempty"`
}

type textEdit struct {
	OldText string `json:"old_text"`
	NewText string `json:"new_text"`
}

func decodeFileEdit(raw json.RawMessage) (fileEdit, error) {
	var args fileEdit
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || decode(raw, &args) != nil {
		return fileEdit{}, ErrInvalidCall
	}
	if edits, present := fields["edits"]; present {
		if fields["old_text"] != nil || fields["new_text"] != nil || string(edits) == "null" || len(args.Edits) == 0 || len(args.Edits) > 32 {
			return fileEdit{}, ErrInvalidCall
		}
		var objects []map[string]json.RawMessage
		if json.Unmarshal(edits, &objects) != nil {
			return fileEdit{}, ErrInvalidCall
		}
		for _, object := range objects {
			if object["new_text"] == nil || string(object["new_text"]) == "null" {
				return fileEdit{}, ErrInvalidCall
			}
		}
	} else {
		if fields["new_text"] == nil || string(fields["new_text"]) == "null" {
			return fileEdit{}, ErrInvalidCall
		}
		args.Edits = []textEdit{{OldText: args.OldText, NewText: args.NewText}}
	}
	// Unlike creation, an edit always targets an existing, hash-bound file.
	if args.Path == "" ||
		len(args.ExpectedSHA256) != 64 || !validExpectedSHA(args.ExpectedSHA256) {
		return fileEdit{}, ErrInvalidCall
	}
	for _, edit := range args.Edits {
		if edit.OldText == "" || len(edit.OldText) > 1<<20 || len(edit.NewText) > 1<<20 || !utf8.ValidString(edit.OldText) || !utf8.ValidString(edit.NewText) {
			return fileEdit{}, ErrInvalidCall
		}
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
	untouched := make([]bool, len(text))
	for i := range untouched {
		untouched[i] = true
	}
	// Prepare every replacement before the single hash-conditional write.
	// A later mismatch discards the whole postimage, never a partially edited file.
	for i, edit := range args.Edits {
		// Count overlapping occurrences too: "aa" in "aaa" is ambiguous.
		index := strings.Index(text, edit.OldText)
		if index < 0 || strings.Contains(text[index+1:], edit.OldText) {
			return nil, fmt.Errorf("%w: edit_file path=%q edits[%d].old_text must match exactly once", ErrConflict, args.Path, i)
		}
		next := make([]bool, 0, len(text)-len(edit.OldText)+len(edit.NewText))
		next = append(next, untouched[:index]...)
		next = append(next, make([]bool, len(edit.NewText))...)
		next = append(next, untouched[index+len(edit.OldText):]...)
		untouched = next
		if len(text)-len(edit.OldText)+len(edit.NewText) > 1<<20 {
			return nil, ErrTooLarge
		}
		text = text[:index] + edit.NewText + text[index+len(edit.OldText):]
	}
	preserved := false
	for i, original := range untouched {
		if original && text[i] != ' ' && text[i] != '\t' && text[i] != '\r' && text[i] != '\n' {
			preserved = true
			break
		}
	}
	if !preserved {
		return nil, fmt.Errorf("%w: edit_file cannot replace or delete the entire file; restore recorded content instead", ErrForbidden)
	}
	return []byte(text), nil
}
