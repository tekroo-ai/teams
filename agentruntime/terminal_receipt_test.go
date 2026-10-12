package agentruntime

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

func terminalReceiptParent(t *testing.T, observationBytes int) (*forkJournal, ForkSource, *countingTool) {
	t.Helper()
	// Equal byte boundaries include the complete historical acknowledgement,
	// not just its submitted arguments. Padding is ordinary ASCII fixture data.
	value := map[string]any{"decision": "original-A", "padding": ""}
	args, _ := json.Marshal(value)
	observation, _ := json.Marshal(map[string]any{"historical_result": json.RawMessage(args), "accepted": true, "executed_in_child": false})
	if observationBytes < len(observation) {
		t.Fatal("fixture too small")
	}
	value["padding"] = strings.Repeat("x", observationBytes-len(observation))
	args, _ = json.Marshal(value)
	j := &forkJournal{streams: map[string][]Entry{}}
	tool := &countingTool{}
	model := &scriptedModel{responses: []Completion{
		{Text: "Original reasoning: retain original-A and its evidence.", ToolCalls: []ToolCall{{ID: "original-read", Name: "read_file", Arguments: json.RawMessage(`{"path":"prior.go"}`)}}},
		{ToolCalls: []ToolCall{{ID: "original-submission", Name: "submit_result", Arguments: args}}},
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	runner := Runner{Journal: j, Tools: tool, Model: model, SystemPrompt: "Original authority", FinalTool: "submit_result", Finalize: func(raw json.RawMessage) (string, error) { return "RESULT:" + string(raw), nil }, Condenser: ReceiptCondenser{Policy: DefaultContextDeliveryPolicy()}}
	if _, err := runner.Run(ctx, "parent", testRequestDigest, "Original assignment"); err != nil {
		t.Fatal(err)
	}
	source, err := CaptureForkSource(ctx, j, "parent", testRequestDigest)
	if err != nil {
		t.Fatal(err)
	}
	return j, source, tool
}

func terminalMessage(t *testing.T, f *ContextFork) Message {
	t.Helper()
	for i := len(f.WorkingHistory) - 1; i >= 0; i-- {
		m := f.WorkingHistory[i]
		if m.Role == "tool" && m.Name == "submit_result" {
			return m
		}
	}
	t.Fatal("terminal acknowledgement missing")
	return Message{}
}

func TestCompletedSubmissionReceiptHandbackBoundaries(t *testing.T) {
	policy := DefaultContextDeliveryPolicy()
	for _, size := range []int{policy.ResultBytes - 1, policy.ResultBytes, policy.ResultBytes + 1, 17992} {
		t.Run(strconv.Itoa(size), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			j, source, tool := terminalReceiptParent(t, size)
			before, _ := json.Marshal(j.streams["parent"])
			fork, err := PrepareContextFork(ctx, j, "child", source)
			if err != nil {
				t.Fatal(err)
			}
			terminal := terminalMessage(t, fork)
			if len(terminal.Content) != size || terminal.Receipt == nil || terminal.Receipt.Sequence != source.ThroughSequence {
				t.Fatal("terminal observation not addressed by actual Finished entry")
			}
			model := &scriptedModel{responses: []Completion{{Text: "original-A retained"}}}
			r := Runner{Journal: j, Tools: tool, Model: model, ContextFork: fork, SystemPrompt: "Current authority", Condenser: ReceiptCondenser{Policy: policy, InheritedHistory: fork.WorkingHistory}}
			if _, err := r.Run(ctx, "child", testRequestDigest, "Review the original decision"); err != nil {
				t.Fatal(err)
			}
			if tool.calls != 1 || len(model.seen) != 1 {
				t.Fatal("historical work repeated")
			}
			foundArgs := false
			for _, m := range model.seen[0] {
				if m.Role == "assistant" {
					for _, call := range m.ToolCalls {
						if call.ID == "original-submission" {
							foundArgs = bytes.Equal(call.Arguments, jTerminalArgs(t, j, "parent"))
						}
					}
				}
				if m.Role == "tool" && len(m.Content) > policy.ResultBytes {
					t.Fatal("oversized presentation")
				}
			}
			if !foundArgs {
				t.Fatal("original decision arguments changed or dropped")
			}
			sources, err := ReceiptSources(ctx, j, &source)
			if err != nil {
				t.Fatal(err)
			}
			reader := RecordedResultReader{Journal: j, InvocationID: "child", Sources: sources, Policy: policy}
			exact := readAllTerminalPages(t, ctx, reader, *terminal.Receipt)
			if string(exact) != terminal.Content {
				t.Fatal("reader and restoration disagree")
			}
			after, _ := json.Marshal(j.streams["parent"])
			if !bytes.Equal(before, after) {
				t.Fatal("parent journal mutated")
			}
			r.Model = &scriptedModel{}
			if got, err := r.Run(ctx, "child", testRequestDigest, "Review the original decision"); err != nil || got != "original-A retained" || tool.calls != 1 {
				t.Fatal("restart reexecuted original work", err)
			}
		})
	}
}

func jTerminalArgs(t *testing.T, j *forkJournal, id string) json.RawMessage {
	t.Helper()
	for i := len(j.streams[id]) - 1; i >= 0; i-- {
		e := j.streams[id][i]
		if e.Kind == ModelTurn {
			var c Completion
			if json.Unmarshal(e.Payload, &c) != nil || len(c.ToolCalls) != 1 {
				t.Fatal("bad final fixture")
			}
			return c.ToolCalls[0].Arguments
		}
	}
	t.Fatal("missing submission")
	return nil
}

func readAllTerminalPages(t *testing.T, ctx context.Context, r RecordedResultReader, ref RecordedResultRef) []byte {
	t.Helper()
	var exact []byte
	offset := 0
	for {
		args, _ := json.Marshal(map[string]any{"invocation_id": ref.InvocationID, "sequence": ref.Sequence, "sha256": ref.SHA256, "byte_offset": offset})
		raw, err := r.Read(ctx, args)
		if err != nil {
			t.Fatal(err)
		}
		var page struct {
			Content, Encoding string
			Next              int  `json:"next_byte_offset"`
			Complete          bool `json:"page_complete"`
			Kind              Kind `json:"source_entry_kind"`
		}
		if json.Unmarshal(raw, &page) != nil || page.Kind != Finished || len(raw) > r.Policy.ResultBytes {
			t.Fatal("page provenance/boundary invalid")
		}
		chunk := []byte(page.Content)
		if page.Encoding == "base64" {
			chunk, err = base64.StdEncoding.DecodeString(page.Content)
			if err != nil {
				t.Fatal(err)
			}
		}
		exact = append(exact, chunk...)
		if page.Complete {
			return exact
		}
		if page.Next <= offset {
			t.Fatal("page did not progress")
		}
		offset = page.Next
	}
}

func TestCompletedSubmissionReceiptLegacyMixedRestart(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	j, source, tool := terminalReceiptParent(t, 17992)
	fork, err := PrepareContextFork(ctx, j, "child", source)
	if err != nil {
		t.Fatal(err)
	}
	old := *fork
	old.WorkingHistory = append([]Message(nil), fork.WorkingHistory...)
	for i, m := range old.WorkingHistory {
		if m.Role == "tool" && m.Name == "submit_result" {
			old.WorkingHistory[i].Receipt = nil
		}
	}
	old.HistorySHA256 = historyDigest(old.WorkingHistory)
	if !legacyReceiptForkEquivalent(&old, fork) {
		t.Fatal("verified missing terminal metadata not compatible with mixed old references")
	}
	start, _ := json.Marshal(startRecord{ContextFork: &old, RequestDigest: testRequestDigest, Prompt: "review", SystemPrompt: "Current authority"})
	if _, err := j.Append(ctx, "child", 0, Started, start); err != nil {
		t.Fatal(err)
	}
	before, _ := json.Marshal(j.streams["parent"])
	r := Runner{Journal: j, Tools: tool, Model: &scriptedModel{responses: []Completion{{Text: "original-A"}}}, ContextFork: fork, SystemPrompt: "Current authority", Condenser: ReceiptCondenser{Policy: DefaultContextDeliveryPolicy(), InheritedHistory: fork.WorkingHistory}}
	if _, err := r.Run(ctx, "child", testRequestDigest, "review"); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(start, j.streams["child"][0].Payload) {
		t.Fatal("stored legacy checkpoint rewritten")
	}
	r.Model = &scriptedModel{}
	if _, err := r.Run(ctx, "child", testRequestDigest, "review"); err != nil {
		t.Fatal(err)
	}
	after, _ := json.Marshal(j.streams["parent"])
	if !bytes.Equal(before, after) || tool.calls != 1 {
		t.Fatal("historical work modified/reexecuted")
	}
	for _, change := range []string{"content", "existing reference", "source"} {
		t.Run(change, func(t *testing.T) {
			bad := old
			bad.WorkingHistory = append([]Message(nil), old.WorkingHistory...)
			switch change {
			case "content":
				bad.WorkingHistory[0].Content += " changed"
			case "source":
				bad.Source.JournalSHA256 = strings.Repeat("b", 64)
			case "existing reference":
				for i, m := range bad.WorkingHistory {
					if m.Receipt != nil {
						ref := *m.Receipt
						ref.SHA256 = strings.Repeat("b", 64)
						bad.WorkingHistory[i].Receipt = &ref
						break
					}
				}
			}
			bad.HistorySHA256 = historyDigest(bad.WorkingHistory)
			if legacyReceiptForkEquivalent(&bad, fork) {
				t.Fatal("semantic/reference mutation accepted")
			}
		})
	}
}

func TestCompletedSubmissionReceiptReaderRejectsInvalidSources(t *testing.T) {
	ctx := context.Background()
	j, source, _ := terminalReceiptParent(t, 17992)
	fork, err := PrepareContextFork(ctx, j, "child", source)
	if err != nil {
		t.Fatal(err)
	}
	m := terminalMessage(t, fork)
	reader := RecordedResultReader{Journal: j, InvocationID: "child", Sources: []ForkSource{source}, Policy: DefaultContextDeliveryPolicy()}
	for _, kind := range []string{"wrong digest", "wrong sequence", "unauthorized", "own terminal", "wrong pointer", "tampered source"} {
		t.Run(kind, func(t *testing.T) {
			r := reader
			ref := *m.Receipt
			pointer := ""
			switch kind {
			case "wrong digest":
				ref.SHA256 = strings.Repeat("b", 64)
			case "wrong sequence":
				ref.Sequence--
			case "unauthorized":
				r.Sources = nil
			case "own terminal":
				r.InvocationID = "parent"
			case "wrong pointer":
				pointer = "/output"
			case "tampered source":
				r.Sources = append([]ForkSource(nil), r.Sources...)
				r.Sources[0].JournalSHA256 = strings.Repeat("b", 64)
			}
			args, _ := json.Marshal(map[string]any{"invocation_id": ref.InvocationID, "sequence": ref.Sequence, "sha256": ref.SHA256, "json_pointer": pointer})
			if _, err := r.Read(ctx, args); err == nil {
				t.Fatal("invalid terminal access admitted")
			}
		})
	}
	unknown := Message{Role: "tool", Name: "submit_result", CallID: "fake", Content: strings.Repeat("x", 17992)}
	if _, err := (ReceiptCondenser{Policy: DefaultContextDeliveryPolicy()}).Condense(ctx, []Message{unknown}); !errors.Is(err, ErrContextBudget) {
		t.Fatal("unbacked result budget guard weakened")
	}
	if !reflect.DeepEqual(j.streams["parent"][len(j.streams["parent"])-1].Kind, Finished) {
		t.Fatal("fixture unexpectedly modified")
	}
}

type terminalReaderTool struct{ reader RecordedResultReader }

func (r terminalReaderTool) ExecuteReadOnly(ctx context.Context, call ToolCall) (json.RawMessage, error) {
	if call.Name != "read_recorded_result" {
		return nil, errors.New("unexpected tool execution")
	}
	raw, err := r.reader.Read(ctx, call.Arguments)
	if err != nil {
		return json.RawMessage(`null`), err
	}
	return raw, nil
}

func TestCompletedSubmissionReceiptInterruptedCorrectionAndNestedHandback(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	j, source, originalTool := terminalReceiptParent(t, 17992)
	original, _ := json.Marshal(j.streams["parent"])
	fork, err := PrepareContextFork(ctx, j, "child", source)
	if err != nil {
		t.Fatal(err)
	}
	ref := terminalMessage(t, fork).Receipt
	arguments := func(pointer string) json.RawMessage {
		raw, _ := json.Marshal(map[string]any{"invocation_id": ref.InvocationID, "sequence": ref.Sequence, "sha256": ref.SHA256, "json_pointer": pointer})
		return raw
	}
	reader := RecordedResultReader{Journal: j, InvocationID: "child", Sources: []ForkSource{source}, Policy: DefaultContextDeliveryPolicy()}
	r := Runner{Journal: j, Tools: terminalReaderTool{reader}, ContextFork: fork, SystemPrompt: "Current authority", FinalTool: "submit_result", Finalize: func(raw json.RawMessage) (string, error) { return string(raw), nil }, Condenser: ReceiptCondenser{Policy: DefaultContextDeliveryPolicy(), InheritedHistory: fork.WorkingHistory}, MaxTurns: 1,
		Model: &scriptedModel{responses: []Completion{{ToolCalls: []ToolCall{{ID: "bad-pointer", Name: "read_recorded_result", Arguments: arguments("/output")}}}}}}
	if _, err := r.Run(ctx, "child", testRequestDigest, "Correct the pointer and retain the original decision"); !errors.Is(err, ErrTurnLimit) {
		t.Fatal("fixture must stop after the recorded pointer failure", err)
	}
	checkpoint, _ := json.Marshal(j.streams["child"])
	finalArgs, _ := json.Marshal(map[string]any{"decision": "original-A", "padding": strings.Repeat("λ", 9000)})
	model := &scriptedModel{responses: []Completion{
		{ToolCalls: []ToolCall{{ID: "correct-pointer", Name: "read_recorded_result", Arguments: arguments("/historical_result/decision")}}},
		{ToolCalls: []ToolCall{{ID: "child-submit", Name: "submit_result", Arguments: finalArgs}}},
	}}
	r.Model, r.MaxTurns = model, 4
	if _, err := r.Run(ctx, "child", testRequestDigest, "Correct the pointer and retain the original decision"); err != nil {
		t.Fatal(err)
	}
	prefix, _ := json.Marshal(j.streams["child"][:lenMustEntries(t, checkpoint)])
	if !bytes.Equal(prefix, checkpoint) || originalTool.calls != 1 {
		t.Fatal("restart changed prior receipts or executed historical effects")
	}
	foundFailure := false
	for _, m := range model.seen[0] {
		foundFailure = foundFailure || m.Role == "tool" && m.CallID == "bad-pointer" && strings.Contains(m.Content, "pointer")
	}
	if !foundFailure {
		t.Fatal("same-invocation correction lost the original error")
	}
	nestedSource, err := CaptureForkSource(ctx, j, "child", testRequestDigest)
	if err != nil {
		t.Fatal(err)
	}
	nested, err := PrepareContextFork(ctx, j, "grandchild", nestedSource)
	if err != nil {
		t.Fatal(err)
	}
	sources, err := ReceiptSources(ctx, j, &nestedSource)
	if err != nil || len(sources) != 2 {
		t.Fatal("frozen ancestry not retained", err)
	}
	nestedReader := RecordedResultReader{Journal: j, InvocationID: "grandchild", Sources: sources, Policy: DefaultContextDeliveryPolicy()}
	last := terminalMessage(t, nested)
	if string(readAllTerminalPages(t, ctx, nestedReader, *last.Receipt)) != last.Content || string(readAllTerminalPages(t, ctx, nestedReader, *ref)) != terminalMessage(t, fork).Content {
		t.Fatal("nested or original Unicode terminal observation not exactly retrievable")
	}
	grand := Runner{Journal: j, Tools: originalTool, Model: &scriptedModel{responses: []Completion{{Text: "original-A remains"}}}, ContextFork: nested, Condenser: ReceiptCondenser{Policy: DefaultContextDeliveryPolicy(), InheritedHistory: nested.WorkingHistory}}
	if _, err := grand.Run(ctx, "grandchild", testRequestDigest, "Continue from the retained decisions"); err != nil {
		t.Fatal(err)
	}
	after, _ := json.Marshal(j.streams["parent"])
	if !bytes.Equal(original, after) || originalTool.calls != 1 {
		t.Fatal("nested handback changed or reexecuted original work")
	}
}

func lenMustEntries(t *testing.T, raw []byte) int {
	t.Helper()
	var entries []Entry
	if json.Unmarshal(raw, &entries) != nil {
		t.Fatal("invalid checkpoint fixture")
	}
	return len(entries)
}

func TestTerminalReceiptNeverCertifiesFailedOrUnrecordedEffects(t *testing.T) {
	for _, kind := range []Kind{Failed, Cancelled, TimedOut} {
		t.Run(string(kind), func(t *testing.T) {
			ctx := context.Background()
			j := &forkJournal{streams: map[string][]Entry{}}
			start, _ := json.Marshal(startRecord{RequestDigest: testRequestDigest, Prompt: "original", FinalTool: "submit_result"})
			turn, _ := json.Marshal(Completion{ToolCalls: []ToolCall{{ID: "possibly-executed", Name: "write_file", Arguments: json.RawMessage(`{"path":"unknown.go"}`)}}})
			j.streams["parent"] = []Entry{{"parent", 1, Started, start}, {"parent", 2, ModelTurn, turn}, {"parent", 3, kind, json.RawMessage(`"terminal fixture"`)}}
			source, err := CaptureRetryForkSource(ctx, j, "parent", testRequestDigest, kind)
			if err != nil {
				t.Fatal(err)
			}
			fork, err := PrepareContextFork(ctx, j, "child", source)
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, m := range fork.WorkingHistory {
				if m.Role == "tool" {
					found = true
					if m.Receipt != nil || !strings.Contains(m.Content, `"result_recorded":false`) || strings.Contains(m.Content, `"accepted":true`) {
						t.Fatal("unrecorded effect certified or made addressable")
					}
				}
			}
			if !found {
				t.Fatal("pending effect not truthfully closed")
			}
			if _, err := (ReceiptCondenser{Policy: DefaultContextDeliveryPolicy()}).Condense(ctx, fork.WorkingHistory); err != nil {
				t.Fatal(err)
			}
			reader := RecordedResultReader{Journal: j, InvocationID: "child", Sources: []ForkSource{source}, Policy: DefaultContextDeliveryPolicy()}
			for _, seq := range []int{2, 3} {
				args, _ := json.Marshal(map[string]any{"invocation_id": "parent", "sequence": seq, "sha256": strings.Repeat("a", 64)})
				if _, err := reader.Read(ctx, args); err == nil {
					t.Fatal("non-result terminal or unrecorded effect made readable as success")
				}
			}
		})
	}
}
