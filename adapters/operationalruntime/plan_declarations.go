package operationalruntime

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/tekroo-ai/teams/agenttools"
	"github.com/tekroo-ai/teams/internal/validation"
	"github.com/tekroo-ai/teams/organization"
)

// The existing signed description/contract fields carry explicit declarations;
// no accepted role schema is relaxed or redefined. This guard does not infer
// declarations from prose. An omitted manifest for a new Go source is an
// actionable submission correction before publication, not a poisoned task.
func checkPlanGoDeclarations(ctx context.Context, root string, plan architectureStageResult) error {
	if len(plan.Tasks) > organization.MaximumFeatureTasks {
		return fmt.Errorf("declaration plan exceeds task bound")
	}
	var proposals []agenttools.ProposedGoSource
	var findings validation.Findings
	for index, task := range plan.Tasks {
		if err := ctx.Err(); err != nil {
			return err
		}
		if findings.Full() {
			break
		}
		if len(task.Description) > 64<<10 || len(task.WriteScope) > 32 {
			findings.Add(fmt.Errorf("task %d declaration input exceeds plan bounds", index))
			continue
		}
		scope, err := normalizeArchitectureWriteScope(task.WriteScope)
		if err != nil {
			findings.Add(fmt.Errorf("task %d declaration write_scope: %w", index, err))
			continue
		}
		blocks, err := explicitPlanGoBlocks(task.Description)
		if err != nil {
			findings.AddContext(fmt.Sprintf("task %d declaration manifest", index), err)
		}
		byPath := map[string]bool{}
		for _, block := range blocks {
			if findings.Full() {
				break
			}
			if !containsScopePath(scope, block.Path) {
				findings.Add(fmt.Errorf("task %d declaration path %q is outside its write scope", index, block.Path))
				continue
			}
			if block.Modification {
				info, statErr := os.Lstat(filepath.Join(root, block.Path))
				if statErr != nil || !info.Mode().IsRegular() {
					findings.Add(fmt.Errorf("task %d // modify: %s requires an existing regular Go file; new files require // path: and a declaration manifest", index, block.Path))
				}
				continue
			}
			byPath[block.Path] = true
			proposals = append(proposals, block.ProposedGoSource)
		}
		for _, path := range scope {
			if findings.Full() {
				break
			}
			if filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") {
				continue
			}
			// Scopes are rechecked here so independent diagnostics never trust a
			// plan that failed an earlier structural validator.
			_, err := os.Lstat(filepath.Join(root, path))
			if err != nil && !os.IsNotExist(err) {
				findings.Add(err)
				continue
			}
			if os.IsNotExist(err) && !byPath[path] {
				findings.Add(fmt.Errorf("task %d proposes new Go source %q without explicit declarations; put a ```go block beginning // path: %s, followed by package and proposed package-level declarations, in its description; existing interfaces may be referenced outside the block", index, path, path))
			}
		}
	}
	if !findings.Full() {
		findings.Add(agenttools.CheckNewGoDeclarations(ctx, root, proposals))
	}
	return findings.Err()
}

func planningDeclarationFindings(ctx context.Context, root string, stage featurePlanningStage, output []byte, workflowErr error) error {
	var plan architectureStageResult
	var err error
	switch stage {
	case stageArchitecture:
		plan, err = decodeArchitectureStageResult(output)
	case stagePlanFinalization:
		plan, err = decodePlanFinalizationStageResult(output)
	default:
		return workflowErr
	}
	if err != nil {
		// An undecodable envelope cannot safely supply independent inputs.
		if workflowErr != nil {
			return workflowErr
		}
		return err
	}
	var findings validation.Findings
	findings.Add(workflowErr)
	findings.Add(checkPlanGoDeclarations(ctx, root, plan))
	return findings.Err()
}

func containsScopePath(scope []string, path string) bool {
	for _, allowed := range scope {
		if allowed == path || strings.HasPrefix(path, strings.TrimSuffix(allowed, "/")+"/") {
			return true
		}
	}
	return false
}

type planGoBlock struct {
	agenttools.ProposedGoSource
	Modification bool
}

func explicitPlanGoBlocks(description string) ([]planGoBlock, error) {
	var proposals []planGoBlock
	var findings validation.Findings
	for {
		if findings.Full() {
			return proposals, findings.Err()
		}
		_, rest, found := strings.Cut(description, "```go\n")
		if !found {
			return proposals, findings.Err()
		}
		block, after, closed := strings.Cut(rest, "```")
		if !closed {
			findings.Add(fmt.Errorf("unterminated Go declaration block"))
			return proposals, findings.Err()
		}
		description = after
		first, source, _ := strings.Cut(block, "\n")
		modification := strings.HasPrefix(first, "// modify: ")
		if !strings.HasPrefix(first, "// path: ") && !modification {
			continue
		} // A usage example is not a declaration proposal.
		path := strings.TrimPrefix(first, "// path: ")
		if modification {
			path = strings.TrimPrefix(first, "// modify: ")
		}
		normalized, pathErr := normalizeArchitectureWriteScope([]string{path})
		if pathErr != nil || filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") || normalized[0] != path {
			findings.Add(fmt.Errorf("invalid declaration path %q: the marker must contain only an exact workspace-relative non-test .go path, without annotations; put explanation outside the block and use // modify: for existing-file examples", path))
			continue
		}
		proposals = append(proposals, planGoBlock{ProposedGoSource: agenttools.ProposedGoSource{Path: path, Source: source}, Modification: modification})
	}
}
