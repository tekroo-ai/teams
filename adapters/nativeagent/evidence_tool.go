package nativeagent

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"unicode/utf8"

	"github.com/tekroo-ai/teams/agentruntime"
	"github.com/tekroo-ai/teams/kernel"
)

const evidencePageMaximum = 64 << 10

// EvidenceReader supplies immutable, digest-checked evidence blobs. The model
// chooses only an evidence ID admitted in its execution brief, never a path or
// digest. The host verifies the bytes again before returning a page.
type EvidenceReader interface {
	Read(context.Context, kernel.Digest) ([]byte, error)
}

type admittedEvidenceTool struct {
	reader  EvidenceReader
	allowed map[string]kernel.Digest
	ids     []string
}

func newAdmittedEvidenceTool(reader EvidenceReader, refs []kernel.EvidenceRef) (*admittedEvidenceTool, error) {
	if reader == nil || len(refs) == 0 || len(refs) > 128 {
		return nil, ErrInvalidBinding
	}
	tool := &admittedEvidenceTool{reader: reader, allowed: make(map[string]kernel.Digest, len(refs))}
	for _, ref := range refs {
		id := string(ref.EvidenceID)
		if !ref.EvidenceID.Valid() || !ref.SHA256.Valid() {
			return nil, ErrInvalidBinding
		}
		if existing, found := tool.allowed[id]; found && existing != ref.SHA256 {
			return nil, ErrInvalidBinding
		}
		tool.allowed[id] = ref.SHA256
	}
	for id := range tool.allowed {
		tool.ids = append(tool.ids, id)
	}
	slices.Sort(tool.ids)
	return tool, nil
}

func (tool *admittedEvidenceTool) definition() agentruntime.ToolDefinition {
	encoded, _ := json.Marshal(map[string]any{
		"type": "object", "additionalProperties": false,
		"properties": map[string]any{
			"evidence_id": map[string]any{"type": "string", "enum": tool.ids},
			"offset":      map[string]any{"type": "integer", "minimum": 0},
			"limit":       map[string]any{"type": "integer", "minimum": 1, "maximum": evidencePageMaximum},
		},
		"required": []string{"evidence_id"},
	})
	return agentruntime.ToolDefinition{Name: "read_evidence", Description: "Read one admitted immutable Teams evidence artifact by evidence_id, not by filesystem path. Use read_file only for workspace files. offset and limit are byte counts; defaults are 0 and 32768. The response includes the full SHA-256 and next offset.", Parameters: encoded}
}

func (tool *admittedEvidenceTool) read(ctx context.Context, arguments json.RawMessage) (json.RawMessage, error) {
	var request struct {
		EvidenceID string `json:"evidence_id"`
		Offset     int    `json:"offset"`
		Limit      int    `json:"limit"`
	}
	if tool == nil || json.Unmarshal(arguments, &request) != nil || request.Offset < 0 || request.Limit < 0 || request.Limit > evidencePageMaximum {
		return nil, ErrInvalidBinding
	}
	digest, allowed := tool.allowed[request.EvidenceID]
	if !allowed {
		return nil, ErrInvalidBinding
	}
	if request.Limit == 0 {
		request.Limit = 32 << 10
	}
	content, err := tool.reader.Read(ctx, digest)
	if err != nil {
		return nil, err
	}
	observed := sha256.Sum256(content)
	if kernel.Digest(hex.EncodeToString(observed[:])) != digest || request.Offset > len(content) {
		return nil, ErrInvalidBinding
	}
	end := min(len(content), request.Offset+request.Limit)
	page := content[request.Offset:end]
	result := map[string]any{"evidence_id": request.EvidenceID, "sha256": digest,
		"total_bytes": len(content), "offset": request.Offset, "next_offset": end, "complete": end == len(content)}
	if utf8.Valid(page) {
		result["encoding"] = "utf8"
		result["content"] = string(page)
	} else {
		result["encoding"] = "base64"
		result["content"] = base64.StdEncoding.EncodeToString(page)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return nil, errors.Join(ErrInvalidBinding, err)
	}
	return encoded, nil
}
