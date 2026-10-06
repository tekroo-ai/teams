package agentruntime

import (
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
	CallID    string `json:"tool_call_id"`
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
		if !duplicate {
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
		fields["content_reference"], _ = json.Marshal(readReference{previous, excerpt.Path, excerpt.SHA256, excerpt.StartLine, excerpt.EndLine})
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
