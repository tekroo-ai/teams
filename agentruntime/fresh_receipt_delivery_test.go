package agentruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
)

func appendFreshReceipt(t *testing.T, j *memoryJournal, h []Message, calls []ToolCall, outputs []json.RawMessage) []Message {
	t.Helper()
	turn := Completion{Text: "Retained question and decision", ToolCalls: calls}
	raw, _ := json.Marshal(turn)
	if _, err := j.Append(t.Context(), "catalog", uint64(len(j.entries)), ModelTurn, raw); err != nil {
		t.Fatal(err)
	}
	h = append(h, Message{Role: "assistant", Content: turn.Text, ToolCalls: calls})
	for i, call := range calls {
		raw, _ = json.Marshal(ToolResult{CallID: call.ID, Name: call.Name, Output: outputs[i]})
		e, err := j.Append(t.Context(), "catalog", uint64(len(j.entries)), ToolDone, raw)
		if err != nil {
			t.Fatal(err)
		}
		m, err := recordedObservation(j.entries, nil, e.Sequence)
		if err != nil {
			t.Fatal(err)
		}
		h = append(h, m)
	}
	return h
}

func TestCatalogExactPageRereadReachesAdmittedHTTPBody(t *testing.T) {
	j, h, c := catalogFixture(t, 60)
	p := catalogPolicy()
	reader := RecordedResultReader{Journal: j, InvocationID: "catalog", Policy: p, Catalog: c}
	ref := *h[2].Receipt
	args, _ := json.Marshal(map[string]any{"catalog_id": c.ID, "handle": catalogHandle(ref), "json_pointer": "/output", "byte_limit": 4096})
	page, err := reader.Read(t.Context(), args)
	if err != nil {
		t.Fatal(err)
	}
	h = appendFreshReceipt(t, j, h, []ToolCall{{ID: "prior-page", Name: "read_recorded_result", Arguments: args}}, []json.RawMessage{page})
	h[len(h)-1].Content, _ = catalogView(h[len(h)-1])
	h = appendFreshReceipt(t, j, h, []ToolCall{{ID: "reread-page", Name: "read_recorded_result", Arguments: args}}, []json.RawMessage{page})
	before, _ := json.Marshal(j.entries)
	got, err := (ReceiptCondenser{Policy: p, Catalog: c}).CondenseForCapacity(t.Context(), h)
	if err != nil {
		t.Fatal(err)
	}
	counts, posts := 0, 0
	var admitted []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var wire struct{ Messages []struct{ Content string } }
		if json.Unmarshal(raw, &wire) != nil || wire.Messages[len(wire.Messages)-1].Content != string(page) {
			t.Error("fresh exact page absent from provider HTTP body")
		}
		if strings.HasSuffix(r.URL.Path, "token-count") {
			counts++
			admitted = raw
			json.NewEncoder(w).Encode(map[string]any{"model": "fixture", "request_sha256": resultDigest(string(raw)), "prompt_tokens": 100, "context_tokens": 262144})
			return
		}
		posts++
		if !bytes.Equal(raw, admitted) {
			t.Error("admitted evidence changed before generation")
		}
		w.Write([]byte(`{"choices":[{"finish_reason":"stop","message":{"content":"done"}}]}`))
	}))
	defer server.Close()
	model := OpenAIModel{Client: server.Client(), BaseURL: server.URL + "/v1", Model: "fixture", MaxTokens: 16384, ContextPolicy: &p}
	prepared, err := model.PrepareTurn(t.Context(), got)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := model.CompletePrepared(t.Context(), got, prepared); err != nil || counts != 1 || posts != 1 {
		t.Fatal("admitted generation", err, counts, posts)
	}
	after, _ := json.Marshal(j.entries)
	if !bytes.Equal(before, after) {
		t.Fatal("source or read journals changed")
	}
}

func TestCatalogFreshOnlyCapacityExhaustionCannotHideEvidence(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	j, _, c := catalogFixture(t, 1)
	p := catalogPolicy()
	start, _ := json.Marshal(startRecord{RequestDigest: testRequestDigest, Prompt: "original task", DeliveryDigest: (ReceiptCondenser{Policy: p}).BindingDigest()})
	j.entries[0].Payload = start
	before, _ := json.Marshal(j.entries)
	counts, posts := 0, 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		if strings.HasSuffix(r.URL.Path, "token-count") {
			counts++
			if !bytes.Contains(raw, []byte("unique-0")) {
				t.Error("fresh evidence hidden to fit provider limit")
			}
			json.NewEncoder(w).Encode(map[string]any{"model": "fixture", "request_sha256": resultDigest(string(raw)), "prompt_tokens": 250000, "context_tokens": 262144})
			return
		}
		posts++
	}))
	defer server.Close()
	model := OpenAIModel{Client: server.Client(), BaseURL: server.URL + "/v1", Model: "fixture", MaxTokens: 16384, ContextPolicy: &p}
	runner := Runner{Journal: j, Model: model, Tools: &countingTool{}, Condenser: ReceiptCondenser{Policy: p, Catalog: c}}
	if _, err := runner.Run(ctx, "catalog", testRequestDigest, "original task"); !errors.Is(err, ErrContextBudget) || counts != 1 || posts != 0 {
		t.Fatal("fresh-only exhaustion did not fail closed", err, counts, posts)
	}
	after, _ := json.Marshal(j.entries)
	if !bytes.Equal(before, after) {
		t.Fatal("failed admission changed retained history")
	}
}

func TestCatalogFreshBatchSurvivesDedupWatermarkAndCapacity(t *testing.T) {
	for _, tool := range []string{"read_file", "read_recorded_result", "search_text"} {
		t.Run(tool, func(t *testing.T) {
			j, h, c := catalogFixture(t, 60)
			p := catalogPolicy()
			condenser := ReceiptCondenser{Policy: p, Catalog: c}
			output := json.RawMessage(`{"content":"exact bounded fresh evidence"}`)
			if tool == "search_text" {
				output = json.RawMessage(`{"name":"search_text"}`)
			}
			calls := []ToolCall{{ID: "old-copy", Name: tool, Arguments: json.RawMessage(`{}`)}}
			h = appendFreshReceipt(t, j, h, calls, []json.RawMessage{output})
			old := len(h) - 1
			// Reproduce an already compacted identical old observation. The raw
			// source is retained; it is not an inline witness for the fresh read.
			h[old].Content, _ = catalogView(h[old])
			calls = []ToolCall{{ID: "fresh-1", Name: tool, Arguments: json.RawMessage(`{}`)}, {ID: "fresh-2", Name: tool, Arguments: json.RawMessage(`{}`)}}
			h = appendFreshReceipt(t, j, h, calls, []json.RawMessage{output, output})
			before, _ := json.Marshal(j.entries)
			for _, capacity := range []bool{false, true} {
				got, err := condenser.condenseCatalog(t.Context(), h, capacity)
				if err != nil || !equalProtectedContext(h, got) {
					t.Fatal("projection", err)
				}
				for _, i := range []int{len(h) - 2, len(h) - 1} {
					if got[i].Content != string(output) {
						t.Fatalf("fresh %s result replaced before consumption: %s", tool, got[i].Content)
					}
				}
				again, err := condenser.condenseCatalog(t.Context(), got, capacity)
				if err != nil || !reflect.DeepEqual(got, again) {
					t.Fatal("unstable fresh projection", err)
				}
				// Check the serialized provider body, not merely the reader result.
				model := OpenAIModel{Client: http.DefaultClient, BaseURL: "http://127.0.0.1:1/v1", Model: "fixture", MaxTokens: 16384, ContextPolicy: &p}
				prepared, err := model.renderTurn(got, nil)
				if err != nil {
					t.Fatal(err)
				}
				var wire struct {
					Messages []struct{ Role, Content string }
				}
				if json.Unmarshal(prepared.body, &wire) != nil {
					t.Fatal("invalid provider body")
				}
				for _, i := range []int{len(h) - 2, len(h) - 1} {
					if wire.Messages[i].Content != string(output) {
						t.Fatal("fresh result absent from actual rendered request")
					}
				}
			}
			after, _ := json.Marshal(j.entries)
			if !bytes.Equal(before, after) {
				t.Fatal("raw evidence rewritten")
			}
		})
	}
}

func TestCatalogDedupKeepsNewestAndDoesNotInflateEmptyResults(t *testing.T) {
	j, h, c := catalogFixture(t, 1)
	var original ToolResult
	json.Unmarshal(j.entries[2].Payload, &original)
	h = appendFreshReceipt(t, j, h, []ToolCall{{ID: "newest", Name: "read_file", Arguments: json.RawMessage(`{}`)}}, []json.RawMessage{original.Output})
	h = appendFreshReceipt(t, j, h, []ToolCall{{ID: "empty", Name: "search_text", Arguments: json.RawMessage(`{}`)}}, []json.RawMessage{json.RawMessage(`{"name":"search_text"}`)})
	got, err := (ReceiptCondenser{Policy: catalogPolicy(), Catalog: c}).Condense(t.Context(), h)
	if err != nil || !validCatalogPresentation(got[2].Content, *h[2].Receipt) || got[4].Content != h[4].Content || got[6].Content != h[6].Content {
		t.Fatal("dedup discarded newest or inflated empty search", err)
	}
	// Oversized raw output still requires exact paging; fresh protection does
	// not enlarge the individual result limit or invent a truncated summary.
	h = appendFreshReceipt(t, j, h, []ToolCall{{ID: "large", Name: "read_file", Arguments: json.RawMessage(`{}`)}}, []json.RawMessage{json.RawMessage(`{"output":"` + strings.Repeat("x", 10000) + `"}`)})
	got, err = (ReceiptCondenser{Policy: catalogPolicy(), Catalog: c}).Condense(context.Background(), h)
	if err != nil || !validCatalogPresentation(got[len(got)-1].Content, *h[len(h)-1].Receipt) {
		t.Fatal("oversized output escaped the paging boundary", err)
	}
}

func TestCatalogRestoresFreshHandleOnlyFromVerifiedJournal(t *testing.T) {
	j, h, _ := catalogFixture(t, 1)
	original := h[2].Content
	h[2].Content, _ = catalogView(h[2])
	// A restart rebuilds the catalog from the same raw journal, not from a
	// handle's model-authored summary or a historical tool re-execution.
	c, err := NewReceiptCatalog(j, "catalog", testRequestDigest, nil)
	if err != nil {
		t.Fatal(err)
	}
	condenser := ReceiptCondenser{Policy: catalogPolicy(), Catalog: c}
	got, err := condenser.CondenseForCapacity(t.Context(), h)
	if err != nil || got[2].Content != original {
		t.Fatal("fresh checkpoint was not restored exactly", err)
	}
	delta, err := makeHistoryDelta(h, got)
	if err != nil {
		t.Fatal(err)
	}
	if replayed, err := applyHistoryDelta(h, delta); err != nil || !reflect.DeepEqual(replayed, got) {
		t.Fatal("fresh restoration is not replayable", err)
	}
	j.entries[2].Payload = json.RawMessage(`{"CallID":"call-0","Name":"read_file","Output":{"output":"changed"}}`)
	if _, err := condenser.Condense(t.Context(), h); err == nil {
		t.Fatal("altered raw source accepted during restoration")
	}
}
