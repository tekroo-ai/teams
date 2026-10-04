package agenttools

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"slices"

	"github.com/tekroo-ai/teams/agentruntime"
	"github.com/tekroo-ai/teams/kernel"
)

// NativeToolSurfaceDigest binds a qualification to the exact Teams-native
// model-facing tool definitions available under a signed role's permissions.
// Invocation purpose may narrow this surface; it cannot add tools to it.
func NativeToolSurfaceDigest(permissions []string) (kernel.Digest, error) {
	tools := make([]agentruntime.ToolDefinition, 0)
	tools = append(tools, ReadOnlyDefinitions(permissions)...)
	tools = append(tools, MutationDefinitions(permissions)...)
	tools = append(tools, TestDefinitions(permissions)...)
	slices.SortFunc(tools, func(left, right agentruntime.ToolDefinition) int {
		if left.Name < right.Name {
			return -1
		}
		if left.Name > right.Name {
			return 1
		}
		return 0
	})
	for index := 1; index < len(tools); index++ {
		if tools[index-1].Name == tools[index].Name {
			return "", ErrInvalidCall
		}
	}
	encoded, err := json.Marshal(struct {
		SchemaVersion string                        `json:"schema_version"`
		Tools         []agentruntime.ToolDefinition `json:"tools"`
	}{"tekroo.teams.native-tool-surface/1.0.0", tools})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(encoded)
	return kernel.Digest(hex.EncodeToString(sum[:])), nil
}
