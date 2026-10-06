package agenttools

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// Both search tools share bounded traversal, not an overloaded model-facing
// pattern argument. Each schema accepts only its own pattern type.
func searchInputSchema(field string) json.RawMessage {
	properties := map[string]any{
		field:           map[string]any{"type": "string", "minLength": 1, "maxLength": 4096},
		"path":          map[string]any{"type": "string"},
		"filename_glob": map[string]any{"type": "string", "maxLength": 256},
		"exclude_glob":  map[string]any{"type": "string", "maxLength": 256},
		"ignore_case":   map[string]any{"type": "boolean"},
		"result_mode":   map[string]any{"type": "string", "enum": []string{"lines", "filenames", "count"}},
		"after_lines":   map[string]any{"type": "integer", "minimum": 0, "maximum": 40},
		"max_results":   map[string]any{"type": "integer", "minimum": 1, "maximum": 100},
		"summary":       map[string]any{"type": "string"},
	}
	raw, err := json.Marshal(map[string]any{
		"type": "object", "additionalProperties": false,
		"required": []string{field}, "properties": properties,
		"if":   map[string]any{"required": []string{"after_lines"}, "properties": map[string]any{"after_lines": map[string]any{"minimum": 1}}},
		"then": map[string]any{"properties": map[string]any{"result_mode": map[string]any{"const": "lines"}}},
	})
	if err != nil {
		panic(err) // Static, JSON-compatible catalog constants only.
	}
	return raw
}

// A missing target is ordinary discovery only after all existing ancestors
// have been checked. A dangling or escaping symlink is never NOT_FOUND.
func checkMissingFilePath(root, relative string) error {
	if _, err := relativePath(relative); err != nil {
		return err
	}
	current := root
	for _, segment := range strings.Split(relative, string(filepath.Separator)) {
		current = filepath.Join(current, segment)
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			resolved, err := filepath.EvalSymlinks(current)
			if err != nil || !withinRoot(root, resolved) {
				return ErrBoundary
			}
			current = resolved
		}
	}
	// The file appeared during the check; do not manufacture an absence result.
	return ErrConflict
}
