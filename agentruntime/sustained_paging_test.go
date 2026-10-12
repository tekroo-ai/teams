package agentruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func appendPagingObservation(t *testing.T, j *memoryJournal, name, call string, output json.RawMessage) Message {
	t.Helper()
	payload, _ := json.Marshal(ToolResult{CallID: call, Name: name, Output: output})
	e, err := j.Append(context.Background(), "own", uint64(len(j.entries)), ToolDone, payload)
	if err != nil {
		t.Fatal(err)
	}
	var result ToolResult
	_ = json.Unmarshal(e.Payload, &result)
	return Message{Role: "tool", Name: name, CallID: call, Content: string(result.Output), Receipt: &RecordedResultRef{"own", e.Sequence, resultDigest(string(result.Output))}}
}

func sustainedPagingHistory(t *testing.T) (*memoryJournal, []Message, RecordedResultReader, Message) {
	t.Helper()
	j := &memoryJournal{}
	history := []Message{{Role: "assistant", Content: "Original decision and rationale are immutable."}}
	var source Message
	for i := 0; i < 70; i++ {
		output, _ := json.Marshal(map[string]any{"name": "read_file", "path": fmt.Sprintf("fixture-%d.go", i), "output": strings.Repeat(fmt.Sprintf("source %d\n", i), 1600)})
		if i == 0 {
			output = largeReceipt(t)
			var fixture map[string]json.RawMessage
			_ = json.Unmarshal(output, &fixture)
			fixture["output"], _ = json.Marshal(strings.Repeat("synthetic exact source output\n", 6000))
			output, _ = json.Marshal(fixture)
		}
		m := appendPagingObservation(t, j, "read_file", fmt.Sprintf("source-%d", i), output)
		history = append(history, m)
		if i == 0 {
			source = m
		}
	}
	reader := RecordedResultReader{Journal: j, InvocationID: "own", Policy: DefaultContextDeliveryPolicy()}
	for i := 0; i < 34; i++ {
		offset := (i % 31) * 4096
		page := readReceipt(t, reader, source, map[string]any{"json_pointer": "/output", "byte_offset": offset})
		history = append(history, appendPagingObservation(t, j, "read_recorded_result", fmt.Sprintf("page-%d", i), page))
	}
	for i := 0; i < 9; i++ {
		history = append(history, appendPagingObservation(t, j, "search_text", fmt.Sprintf("search-%d", i), json.RawMessage(`{"matches":[],"match_count":0}`)))
	}
	return j, history, reader, source
}

func TestSustainedPagingPreservesHistoryUnderUnchangedBudget(t *testing.T) {
	j, history, reader, source := sustainedPagingHistory(t)
	before, _ := json.Marshal(j.entries)
	original, _ := json.Marshal(history)
	condenser := ReceiptCondenser{Policy: reader.Policy}
	got, err := condenser.Condense(context.Background(), history)
	if err != nil {
		t.Fatal(err)
	}
	total := 0
	pages := 0
	for _, m := range got {
		if m.Role == "tool" {
			total += len(m.Content)
		}
		var v receiptPresentation
		if json.Unmarshal([]byte(m.Content), &v) == nil && v.Delivery != "" {
			if v.Access != "" {
				t.Fatal("duplicated instructions")
			}
			if v.Page != nil {
				pages++
				if v.Page.Source.RecordedResultRef != *source.Receipt || v.Reference == v.Page.Source.RecordedResultRef {
					t.Fatal("page/source identity conflated")
				}
			}
		}
	}
	if len(got) != len(history) || got[0].Content != history[0].Content || pages == 0 || total > reader.Policy.HistoryBytes {
		t.Fatalf("history lost or oversized: %d", total)
	}
	again, err := condenser.Condense(context.Background(), got)
	if err != nil || !reflect.DeepEqual(got, again) {
		t.Fatal("restart changed checkpoint", err)
	}
	after, _ := json.Marshal(j.entries)
	unchanged, _ := json.Marshal(history)
	if !bytes.Equal(before, after) || !bytes.Equal(original, unchanged) {
		t.Fatal("journal or input mutated")
	}
	for _, tc := range []struct{ pointer, name, status string }{{"/test_evidence/tests", "TestCase3", "SKIP"}, {"/test_evidence/tests", "TestCase4", "PASS"}, {"/test_evidence/excluded", "TestUnavailable", "NOT_RUN"}} {
		page := readReceipt(t, reader, source, map[string]any{"json_pointer": tc.pointer, "match_field": "name", "match_value": tc.name})
		if !bytes.Contains(page, []byte(tc.status)) {
			t.Fatal("named evidence lost")
		}
	}
}

func TestPageSourceSelectionAndWrongPointerCorrection(t *testing.T) {
	j, source, reader := recordedFixture(t, largeReceipt(t))
	page := readReceipt(t, reader, source, map[string]any{"json_pointer": "/test_evidence/tests", "match_field": "name", "match_value": "TestCase3"})
	m := appendPagingObservation(t, j, "read_recorded_result", "named-page", page)
	view, err := receiptView(m, false)
	if err != nil {
		t.Fatal(err)
	}
	var v receiptPresentation
	_ = json.Unmarshal([]byte(view), &v)
	if v.Page == nil || v.Page.Source.Field != "name" || v.Page.Source.Value == nil || *v.Page.Source.Value != "TestCase3" || v.Page.Source.RecordedResultRef != *source.Receipt {
		t.Fatal("exact selection lost")
	}
	args, _ := json.Marshal(map[string]any{"invocation_id": "own", "sequence": m.Receipt.Sequence, "sha256": m.Receipt.SHA256, "json_pointer": "/output"})
	if _, err = reader.Read(context.Background(), args); !errors.Is(err, ErrInvalidTurn) {
		t.Fatal("page incorrectly accepted as source")
	}
	corrected, _ := json.Marshal(v.Page.Source)
	answer, err := reader.Read(context.Background(), corrected)
	if err != nil || !bytes.Contains(answer, []byte("SKIP")) {
		t.Fatal("correction changed evidence", err)
	}
	// The display metadata never changes authorization or digest validation.
	v.Page.Source.InvocationID = "unrelated"
	args, _ = json.Marshal(v.Page.Source)
	if _, err = reader.Read(context.Background(), args); err == nil {
		t.Fatal("unbound source allowed")
	}
}

func TestDuplicatePageReferenceNeverPointsToEvictedContent(t *testing.T) {
	j, source, reader := recordedFixture(t, json.RawMessage(`{"output":"`+strings.Repeat("source ", 2000)+`"}`))
	page := readReceipt(t, reader, source, map[string]any{"json_pointer": "/output"})
	first := appendPagingObservation(t, j, "read_recorded_result", "page-first", page)
	second := appendPagingObservation(t, j, "read_recorded_result", "page-second", page)
	history := []Message{first, second}
	got, err := (ReceiptCondenser{Policy: reader.Policy}).Condense(context.Background(), history)
	if err != nil {
		t.Fatal(err)
	}
	var v receiptPresentation
	_ = json.Unmarshal([]byte(got[0].Content), &v)
	if v.Page == nil || v.Page.InContextCallID != second.CallID || got[1].Content != second.Content {
		t.Fatal("duplicate bytes not retained exactly once")
	}
	policy := reader.Policy
	policy.ResultBytes = 1024
	policy.HistoryBytes = 2048
	got, err = (ReceiptCondenser{Policy: policy}).Condense(context.Background(), history)
	if err != nil {
		t.Fatal(err)
	}
	v = receiptPresentation{}
	_ = json.Unmarshal([]byte(got[0].Content), &v)
	if v.Page.InContextCallID != "" {
		t.Fatal("dangling in-context page reference")
	}
}

func TestLegacyReceiptProseCanBeRemovedWithoutChangingIdentity(t *testing.T) {
	_, m, _ := recordedFixture(t, largeReceipt(t))
	compact, err := receiptView(m, false)
	if err != nil {
		t.Fatal(err)
	}
	var legacy receiptPresentation
	_ = json.Unmarshal([]byte(compact), &legacy)
	legacy.Access = recordedResultAccessInstructions
	legacy.Read = "legacy accessor text"
	raw, _ := json.Marshal(legacy)
	m.Content = string(raw)
	got, err := receiptView(m, false)
	if err != nil || got != compact {
		t.Fatal("legacy reference was invalidated", err)
	}
	legacy.Reference.SHA256 = strings.Repeat("0", 64)
	raw, _ = json.Marshal(legacy)
	m.Content = string(raw)
	if _, err = receiptView(m, false); !errors.Is(err, ErrInvalidTurn) {
		t.Fatal("tampered legacy identity admitted")
	}
}

func TestSustainedPagingFailureAndCorrection(t *testing.T) {
	j, history, reader, source := sustainedPagingHistory(t)
	condenser := ReceiptCondenser{Policy: reader.Policy}
	history, err := condenser.Condense(context.Background(), history)
	if err != nil {
		t.Fatal(err)
	}
	prefix, _ := json.Marshal(j.entries)
	prefixLen := len(j.entries)
	var outer *RecordedResultRef
	var selection *recordedSelection
	for _, m := range history {
		var v receiptPresentation
		if json.Unmarshal([]byte(m.Content), &v) == nil && v.Page != nil {
			outer = m.Receipt
			selected := v.Page.Source
			selection = &selected
			break
		}
	}
	if outer == nil || selection == nil {
		t.Fatal("page provenance absent after sustained compaction")
	}
	bad, _ := json.Marshal(map[string]any{"invocation_id": outer.InvocationID, "sequence": outer.Sequence, "sha256": outer.SHA256, "json_pointer": "/output"})
	_, err = reader.Read(context.Background(), bad)
	if !errors.Is(err, ErrInvalidTurn) {
		t.Fatal("page/source confusion was silently accepted")
	}
	failed, _ := json.Marshal(ToolResult{CallID: "invalid-page-source", Name: "read_recorded_result", Output: json.RawMessage("null"), Error: err.Error()})
	e, err := j.Append(context.Background(), "own", uint64(len(j.entries)), ToolDone, failed)
	if err != nil {
		t.Fatal(err)
	}
	var failure ToolResult
	_ = json.Unmarshal(e.Payload, &failure)
	content := strJSON(failure.Error)
	history = append(history, Message{Role: "tool", Name: failure.Name, CallID: failure.CallID, Content: content, Receipt: &RecordedResultRef{"own", e.Sequence, resultDigest(content)}})
	// Resume from the same delivered source selection, not a guessed new source.
	corrected, _ := json.Marshal(selection)
	page, err := reader.Read(context.Background(), corrected)
	if err != nil {
		t.Fatal(err)
	}
	history = append(history, appendPagingObservation(t, j, "read_recorded_result", "corrected-page-source", page))
	for _, tc := range []struct{ pointer, name, status string }{{"/test_evidence/tests", "TestCase4", "PASS"}, {"/test_evidence/tests", "TestCase3", "SKIP"}, {"/test_evidence/excluded", "TestUnavailable", "NOT_RUN"}} {
		page = readReceipt(t, reader, source, map[string]any{"json_pointer": tc.pointer, "match_field": "name", "match_value": tc.name})
		var p struct{ Content string }
		_ = json.Unmarshal(page, &p)
		var records []struct{ Status string }
		_ = json.Unmarshal([]byte(p.Content), &records)
		if len(records) != 1 || records[0].Status != tc.status {
			t.Fatal("correction weakened exact validation")
		}
		history = append(history, appendPagingObservation(t, j, "read_recorded_result", "observed-"+tc.name, page))
	}
	got, err := condenser.Condense(context.Background(), history)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := condenser.Condense(context.Background(), got)
	if err != nil || !reflect.DeepEqual(got, restored) {
		t.Fatal("correction context changed on restart")
	}
	after, _ := json.Marshal(j.entries[:prefixLen])
	if !bytes.Equal(prefix, after) || got[0].Content != "Original decision and rationale are immutable." || len(got) != len(history) {
		t.Fatal("prior evidence/decision was replaced")
	}
	if len(j.entries) != prefixLen+5 {
		t.Fatal("unbounded or repeated correction")
	}
}

func TestPageProjectionNeverRewritesDecisionJSON(t *testing.T) {
	decision := `{"delivery":"RECORDED_RESULT_REFERENCE","page":{"content_in_call":"missing"}}`
	history := []Message{{Role: "assistant", Name: "read_recorded_result", Content: decision}, {Role: "user", Name: "read_recorded_result", Content: decision}}
	got, err := (ReceiptCondenser{Policy: DefaultContextDeliveryPolicy()}).Condense(context.Background(), history)
	if err != nil || !reflect.DeepEqual(history, got) {
		t.Fatal("decision or task content rewritten", err)
	}
}
