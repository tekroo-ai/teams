//go:build mongo_integration && native_qualification

package operationalruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/tekroo-ai/teams/agentruntime"
)

// The seed is explicitly synthetic. Only the subsequent source/evidence
// correction uses the real signed model. Never touches a feature or daemon.
type sustainedReceiptQualificationSeed struct{ turn int }

func verifySustainedReceiptSeed(t *testing.T, entries []agentruntime.Entry) {
	t.Helper()
	successes, failures, pages, sources, searches := 0, 0, 0, 0, 0
	var selected json.RawMessage
	var sourceDigest string
	for _, e := range entries {
		if e.Kind != agentruntime.ToolDone {
			continue
		}
		var r agentruntime.ToolResult
		if json.Unmarshal(e.Payload, &r) != nil {
			t.Fatal("invalid synthetic receipt")
		}
		if r.Error != "" {
			if r.CallID == "sustained-invalid-draft" && strings.Contains(r.Error, "validates") || r.CallID == "sustained-invalid-pointer" && strings.Contains(r.Error, "/output") && strings.Contains(r.Error, "content") {
				failures++
				continue
			}
			t.Fatal("unexpected synthetic error", r.CallID, r.Error)
		}
		successes++
		switch {
		case strings.HasPrefix(r.CallID, "sustained-source-"):
			i, err := strconv.Atoi(strings.TrimPrefix(r.CallID, "sustained-source-"))
			if err != nil || i != sources || r.Name != "read_file" {
				t.Fatal("synthetic source order drift")
			}
			sources++
			if i == 0 {
				var value map[string]json.RawMessage
				if json.Unmarshal(r.Output, &value) != nil || len(r.Output) < 424384 {
					t.Fatal("synthetic original fixture missing")
				}
				selected = value["output"]
				sourceDigest = string(digestBytes(r.Output))
			}
		case strings.HasPrefix(r.CallID, "sustained-page-"):
			i, err := strconv.Atoi(strings.TrimPrefix(r.CallID, "sustained-page-"))
			if err != nil || i != pages || r.Name != "read_recorded_result" {
				t.Fatal("synthetic page order drift")
			}
			pages++
			var p struct {
				Reference         agentruntime.RecordedResultRef
				Pointer           string `json:"json_pointer"`
				Offset            int    `json:"byte_offset"`
				Next              int    `json:"next_byte_offset"`
				Content, Encoding string
			}
			if json.Unmarshal(r.Output, &p) != nil || p.Reference.InvocationID != e.InvocationID || p.Reference.Sequence != 3 || p.Reference.SHA256 != sourceDigest || p.Pointer != "/output" || p.Offset != (i%31)*4096 || p.Next != p.Offset+4096 || p.Encoding != "utf8" || !bytes.Equal([]byte(p.Content), selected[p.Offset:p.Next]) {
				t.Fatal("synthetic page source/range/bytes drift")
			}
		case strings.HasPrefix(r.CallID, "sustained-search-"):
			i, err := strconv.Atoi(strings.TrimPrefix(r.CallID, "sustained-search-"))
			if err != nil || i != searches || r.Name != "read_file" || !bytes.Contains(r.Output, []byte("synthetic empty discovery")) {
				t.Fatal("synthetic orientation order drift")
			}
			searches++
		default:
			t.Fatal("unexpected synthetic call", r.CallID)
		}
	}
	if successes != 113 || failures != 2 || sources != 70 || pages != 34 || searches != 9 {
		t.Fatalf("incomplete sustained prefix: %d/%d/%d/%d/%d", successes, failures, sources, pages, searches)
	}
}

func (s *sustainedReceiptQualificationSeed) Complete(_ context.Context, history []agentruntime.Message) (agentruntime.Completion, error) {
	s.turn++
	calls := []agentruntime.ToolCall{}
	switch s.turn {
	case 1:
		for i := 0; i < 70; i++ {
			args, _ := json.Marshal(map[string]any{"path": fmt.Sprintf("synthetic-source-%d", i)})
			calls = append(calls, agentruntime.ToolCall{ID: fmt.Sprintf("sustained-source-%d", i), Name: "read_file", Arguments: args})
		}
	case 2:
		var ref *agentruntime.RecordedResultRef
		for _, m := range history {
			if m.CallID == "sustained-source-0" {
				ref = m.Receipt
			}
		}
		if ref == nil {
			return agentruntime.Completion{}, errors.New("source reference absent")
		}
		for i := 0; i < 34; i++ {
			args, _ := json.Marshal(map[string]any{"invocation_id": ref.InvocationID, "sequence": ref.Sequence, "sha256": ref.SHA256, "json_pointer": "/output", "byte_offset": (i % 31) * 4096, "byte_limit": 4096})
			calls = append(calls, agentruntime.ToolCall{ID: fmt.Sprintf("sustained-page-%d", i), Name: "read_recorded_result", Arguments: args})
		}
	case 3:
		for i := 0; i < 9; i++ {
			args, _ := json.Marshal(map[string]any{"path": fmt.Sprintf("synthetic-search-%d", i)})
			calls = append(calls, agentruntime.ToolCall{ID: fmt.Sprintf("sustained-search-%d", i), Name: "read_file", Arguments: args})
		}
	case 4:
		args, _ := json.Marshal(map[string]any{"passing": "PASS", "skipped": "PASS", "excluded": "PASS", "validates": []int{0}, "decision": "combined-original-decision", "source_marker": strings.Repeat("0", 64), "draft": combinedQualificationDraft()})
		calls = append(calls, agentruntime.ToolCall{ID: "sustained-invalid-draft", Name: "submit_result", Arguments: args})
	case 5:
		for _, m := range history {
			if m.CallID == "sustained-page-0" && m.Receipt != nil {
				args, _ := json.Marshal(map[string]any{"invocation_id": m.Receipt.InvocationID, "sequence": m.Receipt.Sequence, "sha256": m.Receipt.SHA256, "json_pointer": "/output"})
				calls = append(calls, agentruntime.ToolCall{ID: "sustained-invalid-pointer", Name: "read_recorded_result", Arguments: args})
				break
			}
		}
		if len(calls) == 0 {
			return agentruntime.Completion{}, errors.New("page reference absent")
		}
	default:
		return agentruntime.Completion{}, errors.New("sustained fixture exceeded finite seed")
	}
	return agentruntime.Completion{Text: "Synthetic retained inspection, not executed validation; original decision remains combined-original-decision.", ToolCalls: calls}, nil
}

type sustainedReceiptQualificationTools struct {
	fixture  json.RawMessage
	delegate agentruntime.ReadOnlyTools
}

func (s sustainedReceiptQualificationTools) ExecuteReadOnly(ctx context.Context, c agentruntime.ToolCall) (json.RawMessage, error) {
	if c.Name == "read_file" {
		if c.ID == "sustained-source-0" {
			return s.fixture, nil
		}
		if strings.HasPrefix(c.ID, "sustained-search-") {
			return json.RawMessage(`{"output":"synthetic empty discovery","status":"FOUND"}`), nil
		}
		raw, _ := json.Marshal(map[string]any{"name": "read_file", "path": c.ID, "output": strings.Repeat("synthetic source inspection\n", 500), "status": "FOUND"})
		return raw, nil
	}
	return s.delegate.ExecuteReadOnly(ctx, c)
}

func TestSustainedQualificationMarkerIsNotDisclosedBySeed(t *testing.T) {
	fixture := strings.Repeat("explicit synthetic fixture output\n", 4000) + "source_marker=secret\n"
	raw, _ := json.Marshal(fixture)
	if strings.Index(string(raw), "source_marker=") < 31*4096 {
		t.Fatal("seed discloses answer")
	}
	if err := validateCombinedQualificationResult([]byte(`{"passing":"PASS","skipped":"SKIP","excluded":"NOT_RUN","source_marker":"guessed","validates":[]}`), "secret"); err == nil {
		t.Fatal("guessed evidence admitted")
	}
}
