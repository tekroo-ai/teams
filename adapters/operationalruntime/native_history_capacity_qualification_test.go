//go:build mongo_integration && native_qualification

package operationalruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/tekroo-ai/teams/agentruntime"
)

// Only a finite scripted prefix is generated here. It is not a model result,
// executed test evidence, or a change to any production role/turn allowance.
type qualificationPreparedSeed struct{ agentruntime.Model }

func (s qualificationPreparedSeed) PrepareTurn(context.Context, []agentruntime.Message) (*agentruntime.PreparedTurn, error) {
	return &agentruntime.PreparedTurn{}, nil
}
func (s qualificationPreparedSeed) CompletePrepared(ctx context.Context, h []agentruntime.Message, _ *agentruntime.PreparedTurn) (agentruntime.Completion, error) {
	return s.Complete(ctx, h)
}

type historyCapacityQualificationSeed struct{ turn int }

func (s *historyCapacityQualificationSeed) Complete(ctx context.Context, h []agentruntime.Message) (agentruntime.Completion, error) {
	s.turn++
	call := agentruntime.ToolCall{Name: "read_file"}
	switch {
	case s.turn <= 240:
		i := s.turn - 1
		call.ID = fmt.Sprintf("sustained-source-%d", i)
		call.Arguments = json.RawMessage(fmt.Sprintf(`{"path":"synthetic-source-%d"}`, i))
	case s.turn <= 274:
		i := s.turn - 241
		var ref *agentruntime.RecordedResultRef
		for _, m := range h {
			if m.CallID == "sustained-source-0" {
				ref = m.Receipt
			}
		}
		if ref == nil {
			return agentruntime.Completion{}, errors.New("source absent")
		}
		call.ID = fmt.Sprintf("sustained-page-%d", i)
		call.Name = "read_recorded_result"
		call.Arguments, _ = json.Marshal(map[string]any{"invocation_id": ref.InvocationID, "sequence": ref.Sequence, "sha256": ref.SHA256, "json_pointer": "/output", "byte_offset": (i % 31) * 4096, "byte_limit": 4096})
	case s.turn <= 283:
		i := s.turn - 275
		call.ID = fmt.Sprintf("sustained-search-%d", i)
		call.Arguments = json.RawMessage(fmt.Sprintf(`{"path":"synthetic-search-%d"}`, i))
	case s.turn == 284:
		// Reuse the same deliberately invalid draft, preserving its full context.
		fixture := sustainedReceiptQualificationSeed{turn: 3}
		return fixture.Complete(ctx, h)
	case s.turn == 285:
		fixture := sustainedReceiptQualificationSeed{turn: 4}
		return fixture.Complete(ctx, h)
	default:
		return agentruntime.Completion{}, errors.New("finite capacity fixture exhausted")
	}
	return agentruntime.Completion{Text: fmt.Sprintf("Synthetic retained rationale %d: original decision remains combined-original-decision; inspections are not executed test evidence.", s.turn), ToolCalls: []agentruntime.ToolCall{call}}, nil
}

func verifyCapacityQualificationSeed(t *testing.T, entries []agentruntime.Entry) {
	t.Helper()
	sources, pages, searches, failures, turns, checkpoints := 0, 0, 0, 0, 0, 0
	for _, e := range entries {
		if e.Kind == agentruntime.ModelTurn {
			turns++
		}
		if e.Kind == agentruntime.Compacted {
			var cp struct {
				History []agentruntime.Message
				Delta   *struct{ Length int }
				Request string `json:"request_sha256"`
			}
			if json.Unmarshal(e.Payload, &cp) != nil || cp.Delta == nil || len(cp.History) != 0 || len(cp.Request) != 64 {
				t.Fatal("capacity checkpoint not a bound delta")
			}
			checkpoints++
		}
		if e.Kind != agentruntime.ToolDone {
			continue
		}
		var r agentruntime.ToolResult
		if json.Unmarshal(e.Payload, &r) != nil {
			t.Fatal("invalid seed receipt")
		}
		if r.Error != "" {
			if r.CallID == "sustained-invalid-draft" && strings.Contains(r.Error, "validates") || r.CallID == "sustained-invalid-pointer" && strings.Contains(r.Error, "/output") {
				failures++
				continue
			}
			t.Fatal("unexpected seed failure", r.CallID, r.Error)
		}
		switch {
		case strings.HasPrefix(r.CallID, "sustained-source-"):
			sources++
		case strings.HasPrefix(r.CallID, "sustained-page-"):
			pages++
		case strings.HasPrefix(r.CallID, "sustained-search-"):
			searches++
		default:
			t.Fatal("unknown seed call", r.CallID)
		}
	}
	if sources != 240 || pages != 34 || searches != 9 || failures != 2 || turns != 285 || checkpoints != 285 {
		t.Fatalf("capacity seed drift %d/%d/%d/%d/%d/%d", sources, pages, searches, failures, turns, checkpoints)
	}
}

func assertCapacityHandleRead(t *testing.T, entries []agentruntime.Entry, done agentruntime.Entry, id string) {
	t.Helper()
	var result agentruntime.ToolResult
	json.Unmarshal(done.Payload, &result)
	for _, e := range entries {
		if e.Kind != agentruntime.ModelTurn {
			continue
		}
		var turn agentruntime.Completion
		json.Unmarshal(e.Payload, &turn)
		for _, call := range turn.ToolCalls {
			if call.ID != result.CallID {
				continue
			}
			var args map[string]json.RawMessage
			json.Unmarshal(call.Arguments, &args)
			if args["catalog_id"] == nil || args["handle"] == nil || args["invocation_id"] != nil {
				t.Fatal("live model did not use compact handle", id, call.ID)
			}
			return
		}
	}
	t.Fatal("live read has no matching call", result.CallID)
}
