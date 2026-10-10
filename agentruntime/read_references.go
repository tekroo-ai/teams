package agentruntime

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
)

type readExcerpt struct {
	Path      string `json:"path"`
	SHA256    string `json:"sha256"`
	StartLine int    `json:"start_line"`
	EndLine   int    `json:"end_line"`
	Output    string `json:"output"`
}

type readReference struct {
	CallID    string `json:"tool_call_id,omitempty"`
	Source    string `json:"source,omitempty"`
	Path      string `json:"path"`
	SHA256    string `json:"sha256"`
	StartLine int    `json:"start_line"`
	EndLine   int    `json:"end_line"`
}

// referenceRepeatedReads changes only model delivery, never the raw journal or
// execution of a read. Its index is rebuilt from the current visible transcript
// on every request: a removed excerpt cannot leave a dangling cache reference.
// Distinct sections and changed file versions always retain their full output.
func referenceRepeatedReads(messages []Message) []Message {
	var result []Message
	seen := make(map[readExcerpt]string)
	project, projectHash := pinnedProjectInstructions(messages)
	for index, message := range messages {
		if message.Role != "tool" || message.Name != "read_file" || message.CallID == "" {
			continue
		}
		var excerpt readExcerpt
		if json.Unmarshal([]byte(message.Content), &excerpt) != nil || excerpt.Path == "" || excerpt.StartLine < 1 || excerpt.EndLine < excerpt.StartLine || excerpt.Output == "" {
			continue
		}
		digest, err := hex.DecodeString(excerpt.SHA256)
		if err != nil || len(digest) != 32 || excerpt.SHA256 != strings.ToLower(excerpt.SHA256) {
			continue
		}
		previous, duplicate := seen[excerpt]
		pinned := excerpt.Path == "AGENTS.md" && excerpt.SHA256 == projectHash && exactProjectExcerpt(project, excerpt)
		if !duplicate && !pinned {
			seen[excerpt] = message.CallID
			continue
		}
		// Preserve all discovery, EOF and file-version metadata. Replace only
		// the repeated content with an explicit, structured in-context reference.
		var fields map[string]json.RawMessage
		if json.Unmarshal([]byte(message.Content), &fields) != nil {
			continue
		}
		delete(fields, "output")
		fields["content_delivery"] = json.RawMessage(`"IN_CONTEXT_REFERENCE"`)
		reference := readReference{CallID: previous, Path: excerpt.Path, SHA256: excerpt.SHA256, StartLine: excerpt.StartLine, EndLine: excerpt.EndLine}
		if pinned {
			reference.CallID, reference.Source = "", "PINNED_PROJECT_LAYER"
		}
		fields["content_reference"], _ = json.Marshal(reference)
		encoded, err := json.Marshal(fields)
		if err != nil || len(encoded) >= len(message.Content) {
			continue
		}
		if result == nil {
			result = append([]Message(nil), messages...)
		}
		result[index].Content = string(encoded)
	}
	if result == nil {
		return messages
	}
	return result
}

// Only a digest-verified, currently visible pinned layer can replace duplicate
// instruction bytes. A changed file, modified excerpt or absent layer stays raw.
func pinnedProjectInstructions(messages []Message) (string, string) {
	const prefix = "[PROJECT | source=workspace AGENTS.md | sha256="
	for _, message := range messages {
		if message.Role != "system" {
			continue
		}
		_, rest, found := strings.Cut(message.Content, prefix)
		if !found {
			continue
		}
		hash, rest, found := strings.Cut(rest, "]\n")
		if !found {
			continue
		}
		content, _, found := strings.Cut(rest, "\n[/ PROJECT]\n")
		if !found {
			continue
		}
		sum := sha256.Sum256([]byte(content))
		if hash == hex.EncodeToString(sum[:]) {
			return content, hash
		}
	}
	return "", ""
}

func exactProjectExcerpt(project string, excerpt readExcerpt) bool {
	if project == "" {
		return false
	}
	lines := strings.Split(strings.TrimSuffix(project, "\n"), "\n")
	return excerpt.EndLine <= len(lines) && strings.Join(lines[excerpt.StartLine-1:excerpt.EndLine], "\n") == excerpt.Output
}
