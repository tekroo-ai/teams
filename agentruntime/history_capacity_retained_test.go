package agentruntime

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"strconv"
	"testing"
)

// Optional read-only forensic qualification consumes the explicitly supplied
// ORIGINAL payload-byte export. No database, effect, or model request occurs.
// Ordinary CI reports NOT_RUN rather than pretending to cover this incident.
func TestQualifyRetainedHistoryCapacityR72(t *testing.T) {
	path := os.Getenv("TEKROO_RETAINED_HISTORY_R72")
	if path == "" {
		t.Skip("NOT_RUN: exact original r72 export required")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Policy  ContextDeliveryPolicy
		Entries []struct {
			ID       string `json:"invocation_id"`
			Sequence string `json:"sequence"`
			Kind     Kind   `json:"kind"`
			Payload  string `json:"payload_base64"`
			SHA      string `json:"payload_sha256"`
		}
	}
	if json.Unmarshal(raw, &fixture) != nil {
		t.Fatal("invalid original-byte export")
	}
	j := &memoryJournal{}
	var history []Message
	last := uint64(0)
	for _, entry := range fixture.Entries {
		payload, err := base64.StdEncoding.Strict().DecodeString(entry.Payload)
		if err != nil || resultDigest(string(payload)) != entry.SHA {
			t.Fatal("original payload mismatch")
		}
		seq, err := strconv.ParseUint(entry.Sequence, 10, 64)
		if err != nil {
			t.Fatal(err)
		}
		j.entries = append(j.entries, Entry{InvocationID: entry.ID, Sequence: seq, Kind: entry.Kind, Payload: payload})
		switch entry.Kind {
		case Compacted:
			var cp historyCheckpoint
			if json.Unmarshal(payload, &cp) != nil || cp.Delta != nil {
				t.Fatal("historical checkpoint mismatch")
			}
			history = cp.History
			last = seq
		case ModelTurn:
			if last > 0 {
				var turn Completion
				json.Unmarshal(payload, &turn)
				history = append(history, Message{Role: "assistant", Content: turn.Text, ToolCalls: turn.ToolCalls})
			}
		case ToolDone:
			if last > 0 {
				m, err := recordedObservation(j.entries, nil, seq)
				if err != nil {
					t.Fatal(err)
				}
				history = append(history, m)
			}
		}
	}
	if len(j.entries) != 239 || last != 233 {
		t.Fatal("wrong raw incident", len(j.entries), last)
	}
	if _, err := (ReceiptCondenser{Policy: fixture.Policy}).Condense(context.Background(), history); !errors.Is(err, ErrContextBudget) {
		t.Fatal("legacy counterexample not reproduced", err)
	}
	before, _ := json.Marshal(j.entries)
	var start startRecord
	if json.Unmarshal(j.entries[0].Payload, &start) != nil {
		t.Fatal("original start invalid")
	}
	p := fixture.Policy
	p.Representation = CatalogHistoryV1
	catalog, err := NewReceiptCatalog(j, j.entries[0].InvocationID, start.RequestDigest, nil)
	if err != nil {
		t.Fatal(err)
	}
	got, err := (ReceiptCondenser{Policy: p, Catalog: catalog}).Condense(context.Background(), history)
	if err != nil || !equalProtectedContext(history, got) {
		t.Fatal("new projection lost context", err)
	}
	total, calls := 0, 0
	for _, m := range got {
		if m.Role == "tool" {
			total += len(m.Content)
			calls++
		}
	}
	if calls != 161 || total > p.HistoryBytes {
		t.Fatal("old watermark counterexample persists", calls, total)
	}
	reader := RecordedResultReader{Journal: j, InvocationID: catalog.InvocationID, Policy: p, Catalog: catalog}
	for _, m := range got {
		if m.Receipt == nil {
			continue
		}
		ref, err := catalog.Resolve(context.Background(), catalog.ID, catalogHandle(*m.Receipt))
		if err != nil || ref != *m.Receipt {
			t.Fatal("exact original lost", err)
		}
		args, _ := json.Marshal(map[string]any{"catalog_id": catalog.ID, "handle": catalogHandle(ref)})
		if _, err := reader.Read(context.Background(), args); err != nil {
			t.Fatal("original read inaccessible", err)
		}
	}
	after, _ := json.Marshal(j.entries)
	if !bytes.Equal(before, after) {
		t.Fatal("original evidence changed")
	}
	t.Logf("COMPUTED from 239 original payloads: 161 observations; legacy fails; catalog tool presentation=%d/%d bytes; non-tool context exact; model/effect/DB calls=0. This does NOT claim the original full role request was token-counted.", total, p.HistoryBytes)
}
