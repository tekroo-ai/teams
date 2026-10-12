package operationalruntime

import (
	"encoding/json"
	"github.com/tekroo-ai/teams/agentruntime"
	"strings"
	"testing"
)

func TestTerminalQualificationEvidenceChecksActualFrozenSource(t *testing.T) {
	ref := &agentruntime.RecordedResultRef{InvocationID: "frozen-parent", Sequence: 4, SHA256: strings.Repeat("a", 64)}
	page := map[string]any{"reference": ref, "json_pointer": "/historical_result/decision", "byte_offset": 0, "next_byte_offset": 3, "total_bytes": 3, "page_complete": true, "source_entry_kind": "finished", "content": "\"A\"", "encoding": "utf8"}
	raw, _ := json.Marshal(page)
	if !exactTerminalRead(raw, ref, "/historical_result/decision") {
		t.Fatal("full exact page rejected")
	}
	partial := map[string]any{}
	for k, v := range page {
		partial[k] = v
	}
	partial["page_complete"] = false
	partial["total_bytes"] = 5
	raw, _ = json.Marshal(partial)
	if !exactTerminalRead(raw, ref, "/historical_result/decision") {
		t.Fatal("authenticated bounded source page rejected")
	}
	for key, value := range map[string]any{"reference": agentruntime.RecordedResultRef{InvocationID: "other", Sequence: 4, SHA256: ref.SHA256}, "page_complete": false, "source_entry_kind": "tool_done", "byte_offset": 1, "next_byte_offset": 2, "json_pointer": "/other"} {
		copy := map[string]any{}
		for k, v := range page {
			copy[k] = v
		}
		copy[key] = value
		raw, _ := json.Marshal(copy)
		if exactTerminalRead(raw, ref, "/historical_result/decision") {
			t.Fatal("incomplete/forged source accepted", key)
		}
	}
}
