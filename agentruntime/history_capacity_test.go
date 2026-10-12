package agentruntime

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
)

func catalogPolicy() ContextDeliveryPolicy {
	p := DefaultContextDeliveryPolicy()
	p.Admission = "rendered_tokens"
	p.RequestBytes = 2 << 20
	p.RepairReserveTokens = 32768
	p.Representation = CatalogHistoryV1
	return p
}

func catalogFixture(t testing.TB, n int) (*memoryJournal, []Message, *ReceiptCatalog) {
	t.Helper()
	j := &memoryJournal{}
	start, _ := json.Marshal(startRecord{RequestDigest: testRequestDigest, Prompt: "original task"})
	j.Append(context.Background(), "catalog", 0, Started, start)
	h := []Message{{Role: "user", Content: "original task"}}
	for i := 0; i < n; i++ {
		call := ToolCall{ID: fmt.Sprintf("call-%d", i), Name: "read_file", Arguments: json.RawMessage(fmt.Sprintf(`{"path":"file-%d"}`, i))}
		turn := Completion{Text: fmt.Sprintf("Original decision %d stays verbatim.", i), ToolCalls: []ToolCall{call}}
		raw, _ := json.Marshal(turn)
		j.Append(context.Background(), "catalog", uint64(len(j.entries)), ModelTurn, raw)
		result := ToolResult{CallID: call.ID, Name: call.Name, Output: json.RawMessage(fmt.Sprintf(`{"output":"unique-%d %s"}`, i, strings.Repeat("retained evidence ", 100)))}
		raw, _ = json.Marshal(result)
		e, _ := j.Append(context.Background(), "catalog", uint64(len(j.entries)), ToolDone, raw)
		m, err := recordedObservation(j.entries, nil, e.Sequence)
		if err != nil {
			t.Fatal(err)
		}
		h = append(h, Message{Role: "assistant", Content: turn.Text, ToolCalls: turn.ToolCalls}, m)
	}
	c, err := NewReceiptCatalog(j, "catalog", testRequestDigest, nil)
	if err != nil {
		t.Fatal(err)
	}
	return j, h, c
}

func TestCatalogProgressiveHistoryAndExactCorrectionEvidence(t *testing.T) {
	j, h, c := catalogFixture(t, 1000)
	before, _ := json.Marshal(j.entries)
	p := catalogPolicy()
	condenser := ReceiptCondenser{Policy: p, Catalog: c}
	for _, n := range []int{1, 119, 120, 160, 161, 162, 500, 1000} {
		original := h[:1+2*n]
		got, err := condenser.Condense(context.Background(), original)
		if err != nil || !equalProtectedContext(original, got) {
			t.Fatalf("n=%d err=%v", n, err)
		}
		again, err := condenser.Condense(context.Background(), got)
		if err != nil || !reflect.DeepEqual(got, again) {
			t.Fatal("unstable projection", n, err)
		}
		for i, m := range got {
			if m.Role == "tool" && m.Content != original[i].Content {
				if !validCatalogPresentation(m.Content, *m.Receipt) {
					t.Fatal("unbound handle")
				}
			}
		}
	}
	reader := RecordedResultReader{Journal: j, InvocationID: "catalog", Policy: p, Catalog: c}
	ref := h[2].Receipt
	args := func(pointer string) json.RawMessage {
		b, _ := json.Marshal(map[string]any{"catalog_id": c.ID, "handle": catalogHandle(*ref), "json_pointer": pointer})
		return b
	}
	if _, err := reader.Read(context.Background(), args("/missing")); err == nil {
		t.Fatal("invalid pointer admitted")
	}
	page, err := reader.Read(context.Background(), args("/output"))
	if err != nil || !bytes.Contains(page, []byte("unique-0")) {
		t.Fatal("correction", err)
	}
	after, _ := json.Marshal(j.entries)
	if !bytes.Equal(before, after) {
		t.Fatal("raw journal changed")
	}
	// A new reader/catalog after a crash resolves the same locator, not a new
	// tool execution or a model-authored memory reconstruction.
	fresh, _ := NewReceiptCatalog(j, "catalog", testRequestDigest, nil)
	reader.Catalog = fresh
	same, err := reader.Read(context.Background(), args("/output"))
	if err != nil || !bytes.Equal(page, same) {
		t.Fatal("restart access", err)
	}
}

func TestCatalogScopeTamperingAndFeedback(t *testing.T) {
	j, h, c := catalogFixture(t, 2)
	p := catalogPolicy()
	ref := *h[2].Receipt
	if _, err := c.Resolve(context.Background(), strings.Repeat("0", 64), catalogHandle(ref)); err == nil {
		t.Fatal("wrong catalog")
	}
	if _, err := c.Resolve(context.Background(), c.ID, "runknown"); err == nil {
		t.Fatal("unknown handle")
	}
	c.RequestDigest = strings.Repeat("b", 64)
	if _, err := c.Resolve(context.Background(), c.ID, catalogHandle(ref)); err == nil {
		t.Fatal("mutated binding")
	}
	c.RequestDigest = testRequestDigest
	c.refs[catalogHandle(ref)] = RecordedResultRef{"other", 3, ref.SHA256}
	if _, err := c.Resolve(context.Background(), c.ID, catalogHandle(ref)); !errors.Is(err, ErrConflict) {
		t.Fatal("collision", err)
	}
	c.refs = map[string]RecordedResultRef{}
	bad := append([]Message(nil), h...)
	bad[2].Content = `{"forged":true}`
	if _, err := (ReceiptCondenser{Policy: p, Catalog: c}).Condense(context.Background(), bad); err == nil {
		t.Fatal("forged presentation")
	}
	result := ToolResult{CallID: "error", Name: "read_file", Error: "Exact pointer error: retain this feedback"}
	payload, _ := json.Marshal(result)
	e, _ := j.Append(context.Background(), "catalog", uint64(len(j.entries)), ToolDone, payload)
	m, err := recordedObservation(j.entries, nil, e.Sequence)
	if err != nil {
		t.Fatal(err)
	}
	got, err := (ReceiptCondenser{Policy: p, Catalog: c}).CondenseForCapacity(context.Background(), []Message{m})
	if err != nil || got[0].Content != m.Content {
		t.Fatal("feedback changed", err)
	}
	// Full-digest mismatch after source alteration is not repaired by a handle.
	j.entries[2].Payload = json.RawMessage(`{"CallID":"call-0","Name":"read_file","Output":{"output":"changed"}}`)
	if _, err := c.Resolve(context.Background(), c.ID, catalogHandle(ref)); err == nil {
		t.Fatal("stale handle")
	}
}

func TestCatalogNamedEvidenceAndBytePageRoundTrip(t *testing.T) {
	j, h, c := catalogFixture(t, 1)
	result := ToolResult{CallID: "call-0", Name: "read_file", Output: largeReceipt(t)}
	raw, _ := json.Marshal(result)
	j.entries[2].Payload = raw
	m, err := recordedObservation(j.entries, nil, 3)
	if err != nil {
		t.Fatal(err)
	}
	h[2] = m
	reader := RecordedResultReader{Journal: j, InvocationID: "catalog", Policy: catalogPolicy(), Catalog: c}
	for name, status := range map[string]string{"TestCase0": "PASS", "TestCase3": "SKIP", "TestUnavailable": "NOT_RUN"} {
		pointer := "/test_evidence/tests"
		if status == "NOT_RUN" {
			pointer = "/test_evidence/excluded"
		}
		args, _ := json.Marshal(map[string]any{"catalog_id": c.ID, "handle": catalogHandle(*m.Receipt), "json_pointer": pointer, "match_field": "name", "match_value": name})
		page, err := reader.Read(context.Background(), args)
		if err != nil || !bytes.Contains(page, []byte(status)) {
			t.Fatalf("%s %s %v", name, page, err)
		}
	}
	source := []byte(strings.Repeat("🙂\n\"\\", 10000))
	var restored []byte
	for offset := 0; offset < len(source); {
		page, err := ExactBytePage(source, offset, 77, 512, map[string]any{"source_sha256": resultDigest(string(source))})
		if err != nil || len(page) > 512 {
			t.Fatal(err)
		}
		var p struct {
			Encoding, Content string
			Next              int `json:"next_byte_offset"`
		}
		json.Unmarshal(page, &p)
		chunk := []byte(p.Content)
		if p.Encoding == "base64" {
			chunk, err = base64.StdEncoding.Strict().DecodeString(p.Content)
			if err != nil {
				t.Fatal(err)
			}
		}
		if p.Next <= offset {
			t.Fatal("no progress")
		}
		restored = append(restored, chunk...)
		offset = p.Next
	}
	if !bytes.Equal(source, restored) {
		t.Fatal("byte paging changed source")
	}
}

func TestCatalogReaderGenerationAlternativesAreCompleteClosedObjects(t *testing.T) {
	_, h, c := catalogFixture(t, 1)
	r := RecordedResultReader{Journal: c.Journal, InvocationID: c.InvocationID, Policy: catalogPolicy(), Catalog: c}
	contract, err := NewStructuredTurnContract([]ToolDefinition{r.Definition(), {Name: "submit_result", Parameters: json.RawMessage(`{"type":"object","additionalProperties":false,"properties":{"answer":{"type":"string"}},"required":["answer"]}`)}}, "submit_result")
	if err != nil {
		t.Fatal(err)
	}
	var schema struct {
		Properties struct {
			Calls struct {
				Items struct {
					Properties map[string]struct {
						OneOf []struct {
							Type       string
							Closed     bool `json:"additionalProperties"`
							Properties map[string]json.RawMessage
							Required   []string
						}
					}
				}
			}
		}
	}
	if json.Unmarshal(contract.schema, &schema) != nil {
		t.Fatal("invalid generated schema")
	}
	branches := schema.Properties.Calls.Items.Properties["read_recorded_result"].OneOf
	if len(branches) != 2 {
		t.Fatal("missing complete alternatives")
	}
	for _, b := range branches {
		if b.Type != "object" || b.Closed || len(b.Required) < 2 {
			t.Fatal("unconstrained branch")
		}
		for _, name := range b.Required {
			if b.Properties[name] == nil {
				t.Fatal("required field lacks branch type")
			}
		}
	}
	good, _ := json.Marshal(map[string]any{"calls": []any{map[string]any{"read_recorded_result": map[string]any{"catalog_id": c.ID, "handle": catalogHandle(*h[2].Receipt)}}}})
	if _, err := contract.decode(good, "valid"); err != nil {
		t.Fatal("valid handle rejected", err)
	}
	for _, value := range []any{"{", "placeholder", 140001, map[string]any{}, map[string]any{"catalog_id": c.ID, "handle": catalogHandle(*h[2].Receipt), "invocation_id": c.InvocationID, "sequence": 3, "sha256": h[2].Receipt.SHA256}, map[string]any{"catalog_id": c.ID, "handle": 4}} {
		raw, _ := json.Marshal(map[string]any{"calls": []any{map[string]any{"read_recorded_result": value}}})
		if _, err := contract.decode(raw, "invalid"); err == nil {
			t.Fatal("invalid argument accepted", value)
		}
	}
}

func TestPreparedAdmissionBindingAndFailureCannotGenerate(t *testing.T) {
	for _, mode := range []string{"fit", "overflow", "unavailable", "wrong-digest"} {
		t.Run(mode, func(t *testing.T) {
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
					tokens := 100
					if mode == "overflow" {
						tokens = 250000
					}
					digest := resultDigest(string(raw))
					if mode == "wrong-digest" {
						digest = strings.Repeat("0", 64)
					}
					json.NewEncoder(w).Encode(map[string]any{"model": "fixture", "request_sha256": digest, "prompt_tokens": tokens, "context_tokens": 262144})
					return
				}
				posts++
				if !bytes.Equal(raw, counted) {
					t.Error("count/generation body mismatch")
				}
				w.Write([]byte(`{"choices":[{"finish_reason":"stop","message":{"content":"done"}}]}`))
			}))
			defer server.Close()
			p := catalogPolicy()
			m := OpenAIModel{Client: server.Client(), BaseURL: server.URL + "/v1", Model: "fixture", MaxTokens: 16384, ContextPolicy: &p}
			history := []Message{{Role: "user", Content: "retained"}}
			prepared, err := m.PrepareTurn(context.Background(), history)
			if mode == "fit" {
				if err != nil {
					t.Fatal(err)
				}
				changed := []Message{{Role: "user", Content: "changed"}}
				if _, err := m.CompletePrepared(context.Background(), changed, prepared); !errors.Is(err, ErrConflict) {
					t.Fatal("changed binding", err)
				}
				if _, err := m.CompletePrepared(context.Background(), history, prepared); err != nil {
					t.Fatal(err)
				}
			} else {
				if err == nil {
					t.Fatal("failed admission passed")
				}
				if _, err := m.CompletePrepared(context.Background(), history, prepared); err == nil {
					t.Fatal("failed preparation generated")
				}
			}
			if counts != 1 || posts != map[bool]int{true: 1, false: 0}[mode == "fit"] {
				t.Fatal(counts, posts)
			}
		})
	}
}

func TestCatalogRenderedHistoryRetainsRecordedAssistantRationale(t *testing.T) {
	p := catalogPolicy()
	definition := ToolDefinition{Name: "submit_result", Parameters: json.RawMessage(`{"type":"object","properties":{"decision":{"type":"string"}},"required":["decision"],"additionalProperties":false}`)}
	contract, err := NewStructuredTurnContract([]ToolDefinition{definition}, "submit_result")
	if err != nil {
		t.Fatal(err)
	}
	m := OpenAIModel{Client: http.DefaultClient, BaseURL: "http://127.0.0.1:1/v1", Model: "fixture", MaxTokens: 16384, ContextPolicy: &p, TurnContract: contract}
	rationale := "Original rationale stays verbatim: A avoids adding a dependency."
	history := []Message{{Role: "user", Content: "task"}, {Role: "assistant", Content: rationale, ToolCalls: []ToolCall{{ID: "original", Name: "submit_result", Arguments: json.RawMessage(`{"decision":"A"}`)}}}}
	prepared, err := m.renderTurn(history, nil)
	if err != nil {
		t.Fatal(err)
	}
	var body struct {
		Messages []struct{ Role, Content string }
	}
	json.Unmarshal(prepared.body, &body)
	if len(body.Messages) != 2 {
		t.Fatal("recorded message missing")
	}
	if !strings.HasPrefix(body.Messages[1].Content, rationale+"\n") || !strings.Contains(body.Messages[1].Content, `"decision":"A"`) {
		t.Fatal("recorded rationale/arguments omitted", body.Messages[1])
	}
	// Historical policy is frozen; only the explicitly new representation
	// changes delivery of previously recorded non-empty assistant text.
	p.Representation = ""
	legacy, err := m.renderTurn(history, nil)
	if err != nil {
		t.Fatal(err)
	}
	json.Unmarshal(legacy.body, &body)
	if strings.Contains(body.Messages[1].Content, rationale) {
		t.Fatal("legacy wire changed")
	}
}

type preparedScript struct{ scriptedModel }

func (m *preparedScript) PrepareTurn(_ context.Context, h []Message) (*PreparedTurn, error) {
	b, _ := json.Marshal(h)
	return &PreparedTurn{body: b, admitted: true}, nil
}
func (m *preparedScript) CompletePrepared(ctx context.Context, h []Message, _ *PreparedTurn) (Completion, error) {
	return m.Complete(ctx, h)
}

func TestCatalogDeltaRestartAndFrozenRoot(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	j, source := forkParent(t, true)
	before, _ := json.Marshal(j.streams["parent"])
	p := catalogPolicy()
	fork, err := PrepareContextForkWithPolicy(ctx, j, "child", source, p)
	if err != nil {
		t.Fatal(err)
	}
	sources, _ := ReceiptSources(ctx, j, &source)
	catalog, _ := NewReceiptCatalog(j, "child", testRequestDigest, sources)
	model := &preparedScript{scriptedModel{responses: []Completion{{Text: "retained handback answer"}}}}
	r := Runner{Journal: j, Model: model, Tools: &countingTool{}, ContextFork: fork, Condenser: ReceiptCondenser{Policy: p, Catalog: catalog, InheritedHistory: fork.WorkingHistory}}
	if _, err := r.Run(ctx, "child", testRequestDigest, "clarify"); err != nil {
		t.Fatal(err)
	}
	var start startRecord
	json.Unmarshal(j.streams["child"][0].Payload, &start)
	if start.ContextFork.Representation != frozenHistoryV1 || len(start.ContextFork.WorkingHistory) != 0 || !matchesStoredFork(start.ContextFork, fork) {
		t.Fatal("frozen root not bound")
	}
	var cp historyCheckpoint
	json.Unmarshal(j.streams["child"][1].Payload, &cp)
	if cp.Delta == nil || len(cp.History) != 0 || !validForkDigest(cp.RequestSHA256) {
		t.Fatal("missing admitted delta")
	}
	if _, err := r.Run(ctx, "child", testRequestDigest, "clarify"); err != nil {
		t.Fatal("restart", err)
	}
	childSource, err := CaptureForkSource(ctx, j, "child", testRequestDigest)
	if err != nil {
		t.Fatal(err)
	}
	nested, err := PrepareContextForkWithPolicy(ctx, j, "grandchild", childSource, p)
	if err != nil {
		t.Fatal("nested", err)
	}
	encoded, _ := json.Marshal(nested.WorkingHistory)
	if !bytes.Contains(encoded, []byte("Recorded design reasoning")) || !bytes.Contains(encoded, []byte("retained handback answer")) {
		t.Fatal("prior decisions lost")
	}
	after, _ := json.Marshal(j.streams["parent"])
	if !bytes.Equal(before, after) {
		t.Fatal("parent rewritten")
	}
	tampered := *fork
	tampered.HistorySHA256 = strings.Repeat("0", 64)
	r.ContextFork = &tampered
	if _, err := r.Run(ctx, "child", testRequestDigest, "clarify"); err == nil {
		t.Fatal("changed fork")
	}
}

func TestCatalogDeltaCannotAlterProtectedDraftOrReference(t *testing.T) {
	for _, before := range [][]Message{nil, {}} {
		for _, after := range [][]Message{nil, {}} {
			d, err := makeHistoryDelta(before, after)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := applyHistoryDelta(before, d); err != nil {
				t.Fatal("empty initial checkpoint", err)
			}
		}
	}
	_, h, c := catalogFixture(t, 2)
	after, err := (ReceiptCondenser{Policy: catalogPolicy(), Catalog: c}).CondenseForCapacity(context.Background(), h)
	if err != nil {
		t.Fatal(err)
	}
	d, err := makeHistoryDelta(h, after)
	if err != nil {
		t.Fatal(err)
	}
	got, err := applyHistoryDelta(h, d)
	if err != nil || !reflect.DeepEqual(after, got) {
		t.Fatal(err)
	}
	bad := append([]Message(nil), after...)
	bad[1].Content = "invented"
	if _, err := makeHistoryDelta(h, bad); err == nil {
		t.Fatal("protected draft modified")
	}
	bad = append([]Message(nil), after...)
	ref := *bad[2].Receipt
	ref.SHA256 = strings.Repeat("0", 64)
	bad[2].Receipt = &ref
	if _, err := makeHistoryDelta(h, bad); err == nil {
		t.Fatal("reference replaced")
	}
	d.Replacements = append(d.Replacements, d.Replacements[0])
	if _, err := applyHistoryDelta(h, d); err == nil {
		t.Fatal("duplicate delta")
	}
}
