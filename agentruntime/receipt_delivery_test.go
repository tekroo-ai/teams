package agentruntime

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
)

func largeReceipt(t *testing.T) json.RawMessage {
	t.Helper()
	tests := make([]map[string]string, 2500)
	for i := range tests {
		tests[i] = map[string]string{"package": "example.test/" + strings.Repeat("pkg/", 30), "name": fmt.Sprintf("TestCase%d", i), "status": "PASS"}
	}
	tests[3]["status"] = "SKIP"
	raw, err := json.Marshal(map[string]any{"exit_code": 0, "output": strings.Repeat("successful package output\n", 4000), "test_evidence": map[string]any{
		"complete": true, "command": []string{"go", "test", "./..."}, "tests": tests,
		"excluded": []map[string]string{{"package": "example.test/tagged", "name": "TestUnavailable", "status": "NOT_RUN"}},
	}})
	if err != nil || len(raw) < 424384 {
		t.Fatalf("fixture must cover observed large-response size: bytes=%d err=%v", len(raw), err)
	}
	return raw
}

func recordedFixture(t *testing.T, raw json.RawMessage) (*memoryJournal, Message, RecordedResultReader) {
	t.Helper()
	result := ToolResult{CallID: "call", Name: "run_go_tests", Output: raw}
	payload, _ := json.Marshal(result)
	// References authenticate the durable JSON bytes, after journal encoding,
	// not a pre-serialization value (which may have whitespace/HTML escapes).
	_ = json.Unmarshal(payload, &result)
	raw = result.Output
	j := &memoryJournal{entries: []Entry{{InvocationID: "own", Sequence: 1, Kind: ToolDone, Payload: payload}}}
	m := Message{Role: "tool", Name: result.Name, CallID: result.CallID, Content: string(raw), Receipt: &RecordedResultRef{"own", 1, resultDigest(string(raw))}}
	return j, m, RecordedResultReader{Journal: j, InvocationID: "own", Policy: DefaultContextDeliveryPolicy()}
}

func TestReceiptProjectionPreservesRawEvidenceAndDecisions(t *testing.T) {
	raw := largeReceipt(t)
	_, m, _ := recordedFixture(t, raw)
	for _, name := range []string{"run_go_tests", "run_go_tests_worktree", "read_file", "search_text", "git_log", "read_evidence", "protocol_send"} {
		t.Run(name, func(t *testing.T) {
			m.Name = name
			history := []Message{{Role: "system", Content: "exact authority"}, {Role: "user", Content: "original task"}, {Role: "assistant", Content: "original reasoning", ToolCalls: []ToolCall{{ID: m.CallID, Name: name, Arguments: json.RawMessage(`{"package":"./..."}`)}}}, m}
			before, _ := json.Marshal(history)
			condenser := ReceiptCondenser{Policy: DefaultContextDeliveryPolicy()}
			got, err := condenser.Condense(context.Background(), history)
			if err != nil || len(got[3].Content) > condenser.Policy.ResultBytes || !reflect.DeepEqual(history[:3], got[:3]) {
				t.Fatalf("projection=%+v err=%v", got[:3], err)
			}
			var view receiptPresentation
			if json.Unmarshal([]byte(got[3].Content), &view) != nil || view.Reference != *m.Receipt {
				t.Fatal("lost immutable reference")
			}
			encoded, _ := json.Marshal(view.Summary)
			if !strings.Contains(string(encoded), `"NOT_RUN":1`) || !strings.Contains(string(encoded), `"SKIP":1`) || !strings.Contains(string(encoded), `"PASS":2499`) {
				t.Fatalf("status distinction lost: %s", encoded)
			}
			again, err := condenser.Condense(context.Background(), got)
			if err != nil || !reflect.DeepEqual(got, again) {
				t.Fatal("checkpoint projection not stable")
			}
			after, _ := json.Marshal(history)
			if !bytes.Equal(before, after) || m.Content != string(raw) {
				t.Fatal("raw source or history changed")
			}
		})
	}
}

func TestReceiptHistoryBudgetAndMissingReferenceFailClosed(t *testing.T) {
	policy := DefaultContextDeliveryPolicy()
	policy.HistoryBytes = 4096
	policy.ResultBytes = 2048
	_, m, _ := recordedFixture(t, json.RawMessage(`{"output":"`+strings.Repeat("x", 1700)+`"}`))
	history := []Message{m, m, m, m}
	got, err := (ReceiptCondenser{Policy: policy}).Condense(context.Background(), history)
	total := 0
	for _, m := range got {
		total += len(m.Content)
	}
	if err != nil || total > policy.HistoryBytes || len(got) != len(history) {
		t.Fatalf("total=%d err=%v", total, err)
	}
	history = make([]Message, 100)
	for i := range history {
		history[i] = m
	}
	if _, err := (ReceiptCondenser{Policy: policy}).Condense(context.Background(), history); !errors.Is(err, ErrContextBudget) {
		t.Fatalf("reference floor overflow admitted: %v", err)
	}
	m.Content, m.Receipt = strings.Repeat("x", 3000), nil
	if _, err := (ReceiptCondenser{Policy: policy}).Condense(context.Background(), []Message{m}); !errors.Is(err, ErrContextBudget) {
		t.Fatal(err)
	}
	_, m, _ = recordedFixture(t, json.RawMessage(`{"output":"`+strings.Repeat("x", 3000)+`"}`))
	m.Content += " "
	if _, err := (ReceiptCondenser{Policy: policy}).Condense(context.Background(), []Message{m}); !errors.Is(err, ErrInvalidTurn) {
		t.Fatalf("tamper admitted: %v", err)
	}
}

func TestSourcePointerMetadataFitsMinimumPresentationBudget(t *testing.T) {
	value := map[string]any{"output": strings.Repeat("exact source ", 1000)}
	for i := 0; i < 30; i++ {
		value[fmt.Sprintf("%02d-%s", i, strings.Repeat("long-key", 15))] = "x"
	}
	raw, _ := json.Marshal(value)
	_, message, _ := recordedFixture(t, raw)
	policy := DefaultContextDeliveryPolicy()
	policy.ResultBytes = 1024
	projected, err := (ReceiptCondenser{Policy: policy}).Condense(context.Background(), []Message{message})
	if err != nil || len(projected[0].Content) > policy.ResultBytes || !strings.Contains(projected[0].Content, `"/output"`) {
		t.Fatalf("oversized pointer metadata: %v", err)
	}
}

func TestRecordedArrayMatchGuidanceAndWrongContainerReceipt(t *testing.T) {
	_, message, reader := recordedFixture(t, largeReceipt(t))
	view, err := receiptView(message, true)
	if err != nil || strings.Contains(view, "access_instructions") {
		t.Fatalf("receipt repeats shared access instructions: %s %v", view, err)
	}
	definition, _ := json.Marshal(reader.Definition())
	if !strings.Contains(string(definition), "ARRAY") || !strings.Contains(string(definition), "/test_evidence/tests") || !strings.Contains(string(definition), "/test_evidence/excluded") {
		t.Fatal("reader schema does not explain matching container")
	}
	name := "TestCase3"
	if _, err := selectRecordedJSON([]byte(message.Content), "/test_evidence", "name", &name); !errors.Is(err, ErrInvalidTurn) || !strings.Contains(err.Error(), "/test_evidence") || !strings.Contains(err.Error(), "array") || !strings.Contains(err.Error(), "excluded") || !strings.Contains(err.Error(), "tests") {
		t.Fatalf("wrong-container error is not actionable: %v", err)
	}
	got, err := selectRecordedJSON([]byte(message.Content), "/test_evidence/tests", "name", &name)
	if err != nil || !bytes.Contains(got, []byte(`"status":"SKIP"`)) {
		t.Fatalf("array correction lost exact status: %s %v", got, err)
	}
	missing := "NoSuchTest"
	got, err = selectRecordedJSON([]byte(message.Content), "/test_evidence/tests", "name", &missing)
	if err != nil || string(got) != "[]" {
		t.Fatalf("legitimate empty match became an error: %s %v", got, err)
	}
}

func readReceipt(t *testing.T, reader RecordedResultReader, m Message, extra map[string]any) json.RawMessage {
	t.Helper()
	args := map[string]any{"invocation_id": m.Receipt.InvocationID, "sequence": m.Receipt.Sequence, "sha256": m.Receipt.SHA256}
	for k, v := range extra {
		args[k] = v
	}
	raw, _ := json.Marshal(args)
	page, err := reader.Read(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(page) > reader.Policy.ResultBytes {
		t.Fatal("encoded page exceeded policy")
	}
	return page
}

func TestRecordedResultNamedLookupAndExactPagination(t *testing.T) {
	for _, raw := range []json.RawMessage{largeReceipt(t), json.RawMessage(`"` + strings.Repeat("é<>&", 10000) + `"`), json.RawMessage("{\n" + strings.Repeat("\t", 16000) + `"value":1}`)} {
		j, m, reader := recordedFixture(t, raw)
		raw = json.RawMessage(m.Content)
		before, _ := json.Marshal(j.entries)
		var restored []byte
		for offset := 0; ; {
			page := readReceipt(t, reader, m, map[string]any{"byte_offset": offset, "byte_limit": 4093})
			var decoded struct {
				Encoding, Content string
				Next              int  `json:"next_byte_offset"`
				Complete          bool `json:"page_complete"`
				Total             int  `json:"total_bytes"`
			}
			if json.Unmarshal(page, &decoded) != nil || decoded.Total != len(raw) {
				t.Fatal("invalid pagination")
			}
			chunk := []byte(decoded.Content)
			if decoded.Encoding == "base64" {
				var err error
				chunk, err = base64.StdEncoding.DecodeString(decoded.Content)
				if err != nil {
					t.Fatal(err)
				}
			}
			if len(chunk) != decoded.Next-offset || decoded.Next <= offset && !decoded.Complete {
				t.Fatal("lost byte offsets")
			}
			restored = append(restored, chunk...)
			if decoded.Complete {
				break
			}
			offset = decoded.Next
		}
		if !bytes.Equal(raw, restored) {
			t.Fatal("pages did not round-trip raw bytes")
		}
		after, _ := json.Marshal(j.entries)
		if !bytes.Equal(before, after) {
			t.Fatal("reading mutated journal")
		}
	}
	_, m, reader := recordedFixture(t, largeReceipt(t))
	for _, tc := range []struct{ pointer, name, status string }{{"/test_evidence/tests", "TestCase3", "SKIP"}, {"/test_evidence/tests", "TestCase4", "PASS"}, {"/test_evidence/excluded", "TestUnavailable", "NOT_RUN"}, {"/test_evidence/tests", "missing", ""}} {
		page := readReceipt(t, reader, m, map[string]any{"json_pointer": tc.pointer, "match_field": "name", "match_value": tc.name})
		var decoded struct{ Content string }
		_ = json.Unmarshal(page, &decoded)
		if tc.status == "" && decoded.Content != "[]" || tc.status != "" && !strings.Contains(decoded.Content, `"status":"`+tc.status+`"`) {
			t.Fatalf("lookup: %s", page)
		}
	}
}

func TestRecordedResultReaderRejectsUnboundOrMalformedAccess(t *testing.T) {
	j, m, reader := recordedFixture(t, json.RawMessage(`{"value":1}`))
	for _, extra := range []map[string]any{{"invocation_id": "other"}, {"sequence": 2}, {"sha256": strings.Repeat("b", 64)}, {"byte_offset": -1}, {"byte_offset": 1000}, {"byte_limit": 0}, {"byte_limit": -1}, {"byte_limit": 4097}, {"offset": 0}, {"limit": 1}, {"json_pointer": "/missing"}, {"json_pointer": "/bad~2"}, {"match_field": "name"}, {"match_value": "x"}, {"unknown": true}} {
		args := map[string]any{"invocation_id": "own", "sequence": 1, "sha256": m.Receipt.SHA256}
		for k, v := range extra {
			args[k] = v
		}
		raw, _ := json.Marshal(args)
		if _, err := reader.Read(context.Background(), raw); err == nil {
			t.Fatalf("unbound/malformed access admitted: %s", raw)
		}
	}
	j.entries[0].Kind = ModelTurn
	args, _ := json.Marshal(m.Receipt)
	if _, err := reader.Read(context.Background(), args); err == nil {
		t.Fatal("non-result entry exposed")
	}
}

type largeReceiptTool struct {
	output json.RawMessage
	calls  int
}

func (tool *largeReceiptTool) ExecuteReadOnly(context.Context, ToolCall) (json.RawMessage, error) {
	tool.calls++
	return tool.output, nil
}

func TestReceiptRunnerRestartAndNestedForkRetainReasoningWithoutReexecution(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	j := &forkJournal{streams: map[string][]Entry{}}
	tool := &largeReceiptTool{output: largeReceipt(t)}
	model := &scriptedModel{responses: []Completion{{Text: "Original design reason must survive", ToolCalls: []ToolCall{{ID: "large", Name: "run_go_tests", Arguments: json.RawMessage(`{"package":"./..."}`)}}}, {Text: "Original conclusion"}}}
	r := Runner{Journal: j, Model: model, Tools: tool, Condenser: ReceiptCondenser{Policy: DefaultContextDeliveryPolicy()}, SystemPrompt: "Original authority"}
	if _, err := r.Run(ctx, "parent", testRequestDigest, "original task"); err != nil {
		kinds := []Kind{}
		for _, e := range j.streams["parent"] {
			kinds = append(kinds, e.Kind)
		}
		t.Fatalf("%v kinds=%v", err, kinds)
	}
	before, _ := json.Marshal(j.streams["parent"])
	if _, err := r.Run(ctx, "parent", testRequestDigest, "original task"); err != nil || tool.calls != 1 || len(model.seen) != 2 {
		t.Fatalf("reexecution: %v", err)
	}
	source, err := CaptureForkSource(ctx, j, "parent", testRequestDigest)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"child", "grandchild"} {
		fork, err := PrepareContextFork(ctx, j, id, source)
		if err != nil {
			t.Fatal(err)
		}
		model := &scriptedModel{responses: []Completion{{Text: "retained answer"}}}
		r.ContextFork, r.Model, r.SystemPrompt = fork, model, "Current authority"
		if _, err := r.Run(ctx, id, testRequestDigest, "clarification"); err != nil {
			t.Fatal(err)
		}
		encoded, _ := json.Marshal(model.seen[0])
		if !bytes.Contains(encoded, []byte("Original design reason must survive")) || !bytes.Contains(encoded, []byte("Original conclusion")) || bytes.Contains(encoded, []byte("TestCase2499")) {
			t.Fatal("reasoning lost or raw inventory re-inlined")
		}
		sources, err := ReceiptSources(ctx, j, &source)
		if err != nil {
			t.Fatal(err)
		}
		reader := RecordedResultReader{Journal: j, InvocationID: id, Sources: sources, Policy: DefaultContextDeliveryPolicy()}
		var receipt *RecordedResultRef
		for _, m := range fork.WorkingHistory {
			if m.Receipt != nil && m.Name == "run_go_tests" {
				receipt = m.Receipt
			}
		}
		if receipt == nil {
			t.Fatal("lost source reference")
		}
		readReceipt(t, reader, Message{Receipt: receipt}, map[string]any{"json_pointer": "/test_evidence/tests", "match_field": "name", "match_value": "TestCase3"})
		source, err = CaptureForkSource(ctx, j, id, testRequestDigest)
		if err != nil {
			t.Fatal(err)
		}
	}
	after, _ := json.Marshal(j.streams["parent"])
	if !bytes.Equal(before, after) || tool.calls != 1 {
		t.Fatal("source changed or tool replayed")
	}
	sources, err := ReceiptSources(ctx, j, &source)
	if err != nil {
		t.Fatal(err)
	}
	reader := RecordedResultReader{Journal: j, InvocationID: "grandchild", Sources: sources, Policy: DefaultContextDeliveryPolicy()}
	j.streams["child"][1].Payload = json.RawMessage(`{}`)
	args, _ := json.Marshal(map[string]any{"invocation_id": "child", "sequence": 1, "sha256": strings.Repeat("a", 64)})
	if _, err := reader.Read(ctx, args); err == nil {
		t.Fatal("changed ancestor admitted")
	}
}

func TestRequestBudgetIncludesSchemasAndNeverSendsOverflow(t *testing.T) {
	posts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		posts++
		_, _ = w.Write([]byte(`{"choices":[{"finish_reason":"stop","message":{"content":"done"}}]}`))
	}))
	defer server.Close()
	policy := DefaultContextDeliveryPolicy()
	model := OpenAIModel{Client: server.Client(), BaseURL: server.URL + "/v1", Model: "qualified", MaxTokens: 128, ContextPolicy: &policy}
	if _, err := model.Complete(context.Background(), []Message{{Role: "user", Content: "small"}}); err != nil {
		t.Fatal(err)
	}
	for _, part := range []string{"instructions", "tool schema", "output reserve"} {
		t.Run(part, func(t *testing.T) {
			m := model
			messages := []Message{{Role: "system", Content: "authority"}, {Role: "user", Content: "task"}}
			if part == "instructions" {
				messages[0].Content = strings.Repeat("x", policy.RequestBytes)
			}
			if part == "tool schema" {
				parameters, _ := json.Marshal(map[string]string{"description": strings.Repeat("x", policy.RequestBytes)})
				m.Tools = []ToolDefinition{{Name: "read_file", Parameters: parameters}}
			}
			if part == "output reserve" {
				m.MaxTokens = policy.ContextTokens
			}
			if _, err := m.Complete(context.Background(), messages); !errors.Is(err, ErrContextBudget) {
				t.Fatalf("overflow not rejected: %v", err)
			}
		})
	}
	if posts != 1 {
		t.Fatalf("overflow was sent: posts=%d", posts)
	}
}

func TestContextAdmissionAlsoGuardsContinuationAndFinalRepair(t *testing.T) {
	posts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		posts++
		_, _ = w.Write([]byte(`{"id":"partial","choices":[{"finish_reason":"length","message":{"content":"{\"submit_result\":"}}]}`))
	}))
	defer server.Close()
	policy := DefaultContextDeliveryPolicy()
	model := OpenAIModel{Client: server.Client(), BaseURL: server.URL + "/v1", Model: "qualified", MaxTokens: 128, TurnContract: structuredTestContract(t), ContextPolicy: &policy}
	_, err := model.Complete(context.Background(), []Message{{Role: "user", Content: "finish"}})
	var partial *PartialTurnError
	if !errors.As(err, &partial) {
		t.Fatalf("missing partial: %v", err)
	}
	copyPartial := *partial
	copyPartial.Prefix += strings.Repeat(" ", policy.RequestBytes)
	if _, err := model.Continue(context.Background(), copyPartial); !errors.Is(err, ErrContextBudget) {
		t.Fatalf("continuation overflow admitted: %v", err)
	}
	changed := policy
	changed.RequestBytes--
	model.ContextPolicy = &changed
	if _, err := model.Continue(context.Background(), *partial); !errors.Is(err, ErrInvalidTurn) {
		t.Fatalf("changed delivery policy resumed continuation: %v", err)
	}
	model.ContextPolicy = &policy
	model.TurnContract = nil
	model.FinalTool = "submit_result"
	model.FinalSchema = json.RawMessage(`{"type":"object"}`)
	messages := []Message{{Role: "user", Content: "task"}, {Role: "assistant", ToolCalls: []ToolCall{{ID: "final", Name: "submit_result", Arguments: json.RawMessage(`{}`)}}}, {Role: "tool", Name: "submit_result", CallID: "final", Content: strJSON(strings.Repeat("x", policy.RequestBytes))}}
	if _, err := model.Complete(context.Background(), messages); !errors.Is(err, ErrContextBudget) {
		t.Fatalf("final repair overflow admitted: %v", err)
	}
	if posts != 1 {
		t.Fatalf("overflow reached server: posts=%d", posts)
	}
}

func TestRenderedAdmissionReservesCorrectionAndFailsClosed(t *testing.T) {
	p := DefaultContextDeliveryPolicy()
	p.Admission = "rendered_tokens"
	p.RequestBytes = 2 << 20
	p.RepairReserveTokens = 32768
	if err := p.AdmitTokens(208616, 53701, 262144, 32768); err != nil {
		t.Fatal(err)
	}
	for _, tc := range [][4]int{{208616, 220000, 262144, 32768}, {208616, 53701, 65536, 32768}, {208616, -1, 262144, 32768}, {3 << 20, 1, 262144, 32768}} {
		if err := p.AdmitTokens(tc[0], tc[1], tc[2], tc[3]); err == nil {
			t.Fatalf("invalid/overflow admission: %v", tc)
		}
	}
}

func TestRecordedFilePointerContractDoesNotAliasPresentation(t *testing.T) {
	_, m, reader := recordedFixture(t, json.RawMessage(`{"name":"read_file","output":"exact source\n","padding":"`+strings.Repeat("x", 9000)+`"}`))
	got, err := (ReceiptCondenser{Policy: reader.Policy}).Condense(context.Background(), []Message{m})
	if err != nil || !strings.Contains(got[0].Content, `"/output"`) || strings.Contains(got[0].Content, `"read":`) {
		t.Fatalf("ambiguous projection: %v %s", err, got[0].Content)
	}
	args, _ := json.Marshal(map[string]any{"invocation_id": "own", "sequence": 1, "sha256": m.Receipt.SHA256, "json_pointer": "/read"})
	if _, err := reader.Read(context.Background(), args); !errors.Is(err, ErrInvalidTurn) || !strings.Contains(err.Error(), "not found") || !strings.Contains(err.Error(), "output") {
		t.Fatalf("missing actionable pointer error: %v", err)
	}
	page := readReceipt(t, reader, m, map[string]any{"json_pointer": "/output"})
	if !strings.Contains(string(page), "exact source") {
		t.Fatal(string(page))
	}
}

func TestLegacyCheckpointsAndChildRestartKeepOriginalBytes(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	j := &forkJournal{streams: map[string][]Entry{}}
	tool := &largeReceiptTool{output: largeReceipt(t)}
	parent := Runner{Journal: j, Tools: tool, Model: &scriptedModel{responses: []Completion{{Text: "Original reasoning", ToolCalls: []ToolCall{{ID: "large", Name: "run_go_tests", Arguments: json.RawMessage(`{"package":"./..."}`)}}}}}, SystemPrompt: "Original authority", MaxTurns: 1}
	if _, err := parent.Run(ctx, "parent", testRequestDigest, "original task"); !errors.Is(err, ErrTurnLimit) {
		t.Fatal(err)
	}
	messages, _, _, _, err := replay("parent", testRequestDigest, "original task", parent.SystemPrompt, "", nil, nil, j.streams["parent"])
	if err != nil {
		t.Fatal(err)
	}
	legacy := append([]Message(nil), messages[2:]...)
	for i := range legacy {
		legacy[i].Receipt = nil
	}
	payload, _ := json.Marshal(historyCheckpoint{SourceDigest: legacyReceiptHistoryDigest(messages), History: legacy})
	_, _ = j.Append(ctx, "parent", 3, Compacted, payload)
	turn, _ := json.Marshal(Completion{Text: "Original conclusion"})
	_, _ = j.Append(ctx, "parent", 4, ModelTurn, turn)
	_, _ = j.Append(ctx, "parent", 5, Finished, json.RawMessage(`"Original conclusion"`))
	before, _ := json.Marshal(j.streams["parent"])
	source, err := CaptureForkSource(ctx, j, "parent", testRequestDigest)
	if err != nil {
		t.Fatal(err)
	}
	current, err := PrepareContextFork(ctx, j, "child", source)
	if err != nil {
		t.Fatal(err)
	}
	old := *current
	old.WorkingHistory = append([]Message(nil), current.WorkingHistory...)
	for i := range old.WorkingHistory {
		old.WorkingHistory[i].Receipt = nil
	}
	old.HistorySHA256 = historyDigest(old.WorkingHistory)
	start, _ := json.Marshal(startRecord{RequestDigest: testRequestDigest, Prompt: "clarification", SystemPrompt: "Current authority", ContextFork: &old})
	_, _ = j.Append(ctx, "child", 0, Started, start)
	model := &scriptedModel{responses: []Completion{{Text: "retained answer"}}}
	child := Runner{Journal: j, Model: model, Tools: tool, SystemPrompt: "Current authority", ContextFork: current, Condenser: ReceiptCondenser{Policy: DefaultContextDeliveryPolicy(), InheritedHistory: current.WorkingHistory}}
	if _, err := child.Run(ctx, "child", testRequestDigest, "clarification"); err != nil {
		t.Fatal(err)
	}
	if _, err := child.Run(ctx, "child", testRequestDigest, "clarification"); err != nil || len(model.seen) != 1 || tool.calls != 1 {
		t.Fatalf("restart: %v", err)
	}
	after, _ := json.Marshal(j.streams["parent"])
	if !bytes.Equal(before, after) || !bytes.Equal(start, j.streams["child"][0].Payload) {
		t.Fatal("legacy source rewritten")
	}
	seen, _ := json.Marshal(model.seen[0])
	if !bytes.Contains(seen, []byte("Original reasoning")) || !bytes.Contains(seen, []byte("Original conclusion")) || bytes.Contains(seen, []byte("TestCase2499")) {
		t.Fatal("historical reasoning lost or large evidence inlined")
	}
	var checkpoint historyCheckpoint
	_ = json.Unmarshal(j.streams["parent"][3].Payload, &checkpoint)
	checkpoint.SourceDigest = strings.Repeat("b", 64)
	j.streams["parent"][3].Payload, _ = json.Marshal(checkpoint)
	if _, _, _, _, err := replay("parent", testRequestDigest, "original task", parent.SystemPrompt, "", nil, nil, j.streams["parent"]); !errors.Is(err, ErrInvalidTurn) {
		t.Fatal("wrong legacy source hash accepted")
	}
}

type largeParallelReceiptTools struct {
	concurrentReads
	raw json.RawMessage
}

func (tools *largeParallelReceiptTools) ExecuteReadOnly(ctx context.Context, call ToolCall) (json.RawMessage, error) {
	_, err := tools.concurrentReads.ExecuteReadOnly(ctx, call)
	return tools.raw, err
}

func TestParallelReceiptDeliveryKeepsOrderReferencesAndOverlap(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	tools := &largeParallelReceiptTools{concurrentReads: concurrentReads{entered: make(chan string, 2), release: make(chan struct{})}, raw: largeReceipt(t)}
	model := &scriptedModel{responses: []Completion{{ToolCalls: []ToolCall{{ID: "first", Name: "read_file", Arguments: json.RawMessage(`{"path":"a"}`)}, {ID: "second", Name: "read_file", Arguments: json.RawMessage(`{"path":"b"}`)}}}, {Text: "done"}}}
	j := &memoryJournal{}
	r := Runner{Journal: j, Tools: tools, Model: model, MaxParallelReads: 2, Condenser: ReceiptCondenser{Policy: DefaultContextDeliveryPolicy()}}
	done := make(chan error, 1)
	go func() { _, err := r.Run(ctx, "parallel", testRequestDigest, "inspect"); done <- err }()
	for range 2 {
		select {
		case <-tools.entered:
		case <-ctx.Done():
			t.Fatal("no actual overlap")
		}
	}
	close(tools.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if tools.maximum != 2 {
		t.Fatalf("overlap=%d", tools.maximum)
	}
	last := model.seen[1]
	for i, id := range []string{"first", "second"} {
		m := last[2+i]
		if m.Role != "tool" || m.CallID != id || m.Receipt == nil || m.Receipt.Sequence != uint64(3+i) || len(m.Content) > 8192 {
			t.Fatalf("ordered reference lost: %+v", m)
		}
	}
	if _, err := r.Run(ctx, "parallel", testRequestDigest, "inspect"); err != nil || len(tools.calls) != 2 {
		t.Fatalf("restart reexecuted: %v", err)
	}
}
