package operationalruntime

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/tekroo-ai/teams/agenttools"
)

// The existing signed description/contract fields carry explicit declarations;
// no accepted role schema is relaxed or redefined. This guard does not infer
// declarations from prose. An omitted manifest for a new Go source is an
// actionable submission correction before publication, not a poisoned task.
func checkPlanGoDeclarations(ctx context.Context, root string, plan architectureStageResult) error {
	var proposals []agenttools.ProposedGoSource
	for index, task := range plan.Tasks {
		blocks, err := explicitNewGoSources(task.Description)
		if err != nil {
			return fmt.Errorf("task %d declaration manifest: %w", index, err)
		}
		byPath := map[string]bool{}
		for _, block := range blocks {
			if !containsScopePath(task.WriteScope, block.Path) {
				return fmt.Errorf("task %d declaration path %q is outside its write scope", index, block.Path)
			}
			byPath[block.Path] = true
		}
		for _, path := range task.WriteScope {
			if filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") {
				continue
			}
			// Write scopes have already passed the plan's canonical path checks.
			_, err := os.Lstat(filepath.Join(root, path))
			if err != nil && !os.IsNotExist(err) {
				return err
			}
			if os.IsNotExist(err) && !byPath[path] {
				return fmt.Errorf("task %d proposes new Go source %q without explicit declarations; put a ```go block beginning // path: %s, followed by package and proposed package-level declarations, in its description; existing interfaces may be referenced outside the block", index, path, path)
			}
		}
		proposals = append(proposals, blocks...)
	}
	return agenttools.CheckNewGoDeclarations(ctx, root, proposals)
}

func containsScopePath(scope []string, path string) bool {
	for _, allowed := range scope {
		if allowed == path || strings.HasPrefix(path, strings.TrimSuffix(allowed, "/")+"/") {
			return true
		}
	}
	return false
}

func explicitNewGoSources(description string) ([]agenttools.ProposedGoSource, error) {
	var proposals []agenttools.ProposedGoSource
	for {
		_, rest, found := strings.Cut(description, "```go\n")
		if !found {
			return proposals, nil
		}
		block, after, closed := strings.Cut(rest, "```")
		if !closed {
			return nil, fmt.Errorf("unterminated Go declaration block")
		}
		description = after
		first, source, _ := strings.Cut(block, "\n")
		if !strings.HasPrefix(first, "// path: ") {
			continue
		} // A usage example is not a declaration proposal.
		path := strings.TrimPrefix(first, "// path: ")
		if strings.TrimSpace(path) != path || path == "" {
			return nil, fmt.Errorf("invalid declaration path")
		}
		proposals = append(proposals, agenttools.ProposedGoSource{Path: path, Source: source})
	}
}
