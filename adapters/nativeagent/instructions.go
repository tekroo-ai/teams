package nativeagent

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/tekroo-ai/teams/agentruntime"
	"github.com/tekroo-ai/teams/application"
	"github.com/tekroo-ai/teams/promptpolicy"
)

// The five layers are pinned as one system message. The turn journal records
// that exact message; history condensation must never summarize or replace it.
const maxProjectInstructionsBytes = 1 << 17

func projectInstructions(workspaceRoot string) (string, error) {
	if strings.TrimSpace(workspaceRoot) == "" {
		return "", ErrInvalidBinding
	}
	path := filepath.Join(workspaceRoot, "AGENTS.md")
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxProjectInstructionsBytes {
		return "", ErrInvalidBinding
	}
	raw, err := os.ReadFile(path)
	if err != nil || len(raw) > maxProjectInstructionsBytes || !utf8.Valid(raw) {
		return "", ErrInvalidBinding
	}
	return string(raw), nil
}

func assembleInstructions(brief application.ExecutionBrief, profile Profile, workspaceRoot string) (string, error) {
	project, err := projectInstructions(workspaceRoot)
	if err != nil || len(profile.ModelInstructions) > 1<<16 {
		return "", ErrInvalidBinding
	}
	modelInstructions := profile.ModelInstructions
	if profile.ResponseMode == ResponseModeJSONSchemaActions {
		modelInstructions += "\n[Native response transport]\n" + agentruntime.StructuredTurnInstruction
	}
	layers := []struct{ name, source, content string }{
		{"ROLE", "signed role bundle", brief.RoleGrounding.Instructions},
		{"PROJECT", "workspace AGENTS.md", project},
		{"TEAM", "bound team manifest", brief.RoleGrounding.TeamInstructions},
		{"MODEL", "bound model profile", modelInstructions},
		{"TEKROO", promptpolicy.Version, promptpolicy.Universal},
	}
	var prompt strings.Builder
	prompt.WriteString("Pinned Teams instruction layers, ordered role → project → team → model → Tekroo. Preserve every layer verbatim across condensation.\n")
	for _, layer := range layers {
		content := layer.content
		if strings.TrimSpace(content) == "" {
			content = "No additional instructions configured."
		}
		digest := sha256.Sum256([]byte(content))
		fmt.Fprintf(&prompt, "\n[%s | source=%s | sha256=%s]\n%s\n[/ %s]\n", layer.name, layer.source, hex.EncodeToString(digest[:]), content, layer.name)
	}
	return prompt.String(), nil
}
