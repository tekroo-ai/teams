package agentruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestCatalogRunnerBoundedProjectionBeforeGeneration(t *testing.T) {
	for _, mode := range []string{"reduce", "irreducible", "unavailable", "invalid-count"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			j, h, c := catalogFixture(t, 2)
			// The first batch has already had a successor model turn; the second
			// remains fresh. Only consumed evidence may satisfy capacity reduction.
			p := catalogPolicy()
			counts, posts := 0, 0
			var counted []byte
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				raw, _ := io.ReadAll(r.Body)
				if strings.HasSuffix(r.URL.Path, "token-count") {
					counts++
					counted = raw
					if mode == "unavailable" {
						w.WriteHeader(503)
						return
					}
					tokens := 250000
					if mode == "reduce" && bytes.Contains(raw, []byte(catalogDelivery)) {
						tokens = 100
					}
					digest := resultDigest(string(raw))
					if mode == "invalid-count" {
						digest = strings.Repeat("0", 64)
					}
					json.NewEncoder(w).Encode(map[string]any{"model": "fixture", "request_sha256": digest, "prompt_tokens": tokens, "context_tokens": 262144})
					return
				}
				posts++
				if !bytes.Equal(raw, counted) {
					t.Error("wire changed after count")
				}
				if j.entries[len(j.entries)-1].Kind != Compacted {
					t.Error("generation before durable checkpoint")
				}
				w.Write([]byte(`{"choices":[{"finish_reason":"stop","message":{"content":"done"}}]}`))
			}))
			defer server.Close()
			m := OpenAIModel{Client: server.Client(), BaseURL: server.URL + "/v1", Model: "fixture", MaxTokens: 16384, ContextPolicy: &p}
			// Mark the already-recorded synthetic prefix with the new policy; this
			// fixture isn't an upgrade of any historical run or deployed journal.
			start, _ := json.Marshal(startRecord{RequestDigest: testRequestDigest, Prompt: "original task", DeliveryDigest: (ReceiptCondenser{Policy: p}).BindingDigest()})
			j.entries[0].Payload = start
			r := Runner{Journal: j, Model: m, Tools: &countingTool{}, Condenser: ReceiptCondenser{Policy: p, Catalog: c}}
			before, _ := json.Marshal(j.entries)
			prefixCount := len(j.entries)
			result, err := r.Run(ctx, "catalog", testRequestDigest, "original task")
			if mode == "reduce" {
				if err != nil || result != "done" || counts != 2 || posts != 1 {
					t.Fatal(result, err, counts, posts)
				}
				var cp historyCheckpoint
				json.Unmarshal(j.entries[prefixCount].Payload, &cp)
				if cp.RequestSHA256 != resultDigest(string(counted)) {
					t.Fatal("checkpoint not bound to admitted wire")
				}
				prefix, _ := json.Marshal(j.entries[:prefixCount])
				if !bytes.Equal(prefix, before) {
					t.Fatal("original prefix changed")
				}
				if _, err := r.Run(ctx, "catalog", testRequestDigest, "original task"); err != nil || posts != 1 {
					t.Fatal("restart generated again", err)
				}
			} else {
				if err == nil || posts != 0 {
					t.Fatal("failure dispatched", err, posts)
				}
				if mode == "irreducible" && (!errors.Is(err, ErrContextBudget) || counts != 2) {
					t.Fatal("true exhaustion", err, counts)
				}
				if mode != "irreducible" && counts != 1 {
					t.Fatal("count error retried", counts)
				}
				after, _ := json.Marshal(j.entries)
				if !bytes.Equal(before, after) {
					t.Fatal("failed preparation changed journal")
				}
			}
			_ = h
		})
	}
}

func TestCatalogForkAboveLegacyCopyLimitUsesVerifiedRoot(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	j, source := forkParent(t, false)
	var result ToolResult
	json.Unmarshal(j.streams["parent"][2].Payload, &result)
	result.Output = json.RawMessage(`{"output":"` + strings.Repeat("large exact source ", 300000) + `"}`)
	raw, _ := json.Marshal(result)
	j.streams["parent"][2].Payload = raw
	source, err := CaptureForkSource(ctx, j, "parent", testRequestDigest)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := PrepareContextFork(ctx, j, "child", source); err == nil {
		t.Fatal("legacy copy guard weakened")
	}
	p := catalogPolicy()
	fork, err := PrepareContextForkWithPolicy(ctx, j, "child", source, p)
	if err != nil {
		t.Fatal("compact root preparation", err)
	}
	sources, _ := ReceiptSources(ctx, j, &source)
	c, _ := NewReceiptCatalog(j, "child", testRequestDigest, sources)
	model := &preparedScript{scriptedModel{responses: []Completion{{Text: "done"}}}}
	r := Runner{Journal: j, Model: model, Tools: &countingTool{}, ContextFork: fork, Condenser: ReceiptCondenser{Policy: p, Catalog: c, InheritedHistory: fork.WorkingHistory}}
	if _, err := r.Run(ctx, "child", testRequestDigest, "clarify"); err != nil {
		t.Fatal(err)
	}
	if len(j.streams["child"][0].Payload) > 4096 {
		t.Fatal("copied large parent into child")
	}
	if _, err := r.Run(ctx, "child", testRequestDigest, "clarify"); err != nil {
		t.Fatal("large restart", err)
	}
}

func TestCatalogPendingEffectRestartDoesNotRepeatPhysicalWork(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	// This fixture interrupts its second append attempt; start that counter at
	// one so the first physical effect's acknowledgement is the lost one.
	j := &interruptedReadJournal{fail: true, doneCount: 1}
	p := catalogPolicy()
	c, err := NewReceiptCatalog(j, "pending", testRequestDigest, nil)
	if err != nil {
		t.Fatal(err)
	}
	effects := &concurrentEffects{}
	model := &preparedScript{scriptedModel{responses: []Completion{{ToolCalls: []ToolCall{{ID: "edit", Name: "edit_file", Arguments: json.RawMessage(`{"retained":"original mutation intent"}`)}}}, {Text: "done"}}}}
	r := Runner{Journal: j, Model: model, Tools: &countingTool{}, Effects: effects, Condenser: ReceiptCondenser{Policy: p, Catalog: c}, SystemPrompt: "original authority"}
	if _, err := r.Run(ctx, "pending", testRequestDigest, "original task"); err == nil {
		t.Fatal("lost effect acknowledgement hidden")
	}
	if len(effects.reads.calls) != 1 {
		t.Fatal("effect not physically run once")
	}
	before, _ := json.Marshal(j.entries)
	prefix := len(j.entries)
	j.fail = false
	if result, err := r.Run(ctx, "pending", testRequestDigest, "original task"); err != nil || result != "done" {
		t.Fatal("pending restart", err)
	}
	if len(effects.reads.calls) != 1 {
		t.Fatal("physical mutation repeated")
	}
	after, _ := json.Marshal(j.entries[:prefix])
	if !bytes.Equal(before, after) {
		t.Fatal("pending context rewritten")
	}
	if len(model.seen) != 2 || model.seen[1][0].Content != "original authority" || model.seen[1][1].Content != "original task" {
		t.Fatal("context lost")
	}
}
