package agentruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestCatalogHardCapacityBoundariesRemainClosed(t *testing.T) {
	p := catalogPolicy()
	output := 16384
	tokenLimit := p.ContextTokens - p.ReserveTokens - p.RepairReserveTokens - output
	for _, difference := range []int{-1, 0, 1} {
		err := p.AdmitTokens(p.RequestBytes, tokenLimit+difference, p.ContextTokens, output)
		if (err != nil) != (difference > 0) {
			t.Fatal("token boundary", difference, err)
		}
		err = p.AdmitTokens(p.RequestBytes+difference, 1, p.ContextTokens, output)
		if (err != nil) != (difference > 0) {
			t.Fatal("transport boundary", difference, err)
		}
		if difference > 0 {
			var limit *CapacityLimitError
			if !errors.As(err, &limit) || limit.Layer != "transport_bytes" {
				t.Fatal("wrong hard-limit classification", err)
			}
		}
		prefix := strings.Repeat("x", MaxStructuredResponseBytes+difference)
		partial := PartialTurnError{Prefix: prefix, Binding: testRequestDigest, ResponseID: "bound", Messages: []Message{{Role: "user", Content: "original"}}}
		if partial.valid() != (difference <= 0) {
			t.Fatal("structured-prefix boundary", difference)
		}
		j := &memoryJournal{}
		r := Runner{Journal: j}
		payload := json.RawMessage(`"` + strings.Repeat("x", MaxJournalPayloadBytes+difference-2) + `"`)
		_, err = r.append(context.Background(), "boundary", nil, ModelResponse, payload)
		if (err != nil) != (difference > 0) || difference > 0 && len(j.entries) != 0 {
			t.Fatal("journal boundary", difference, err)
		}
	}
	// Output and correction reserves are still real capacity, not free space.
	if err := p.AdmitTokens(100, tokenLimit+1, p.ContextTokens, output); err == nil {
		t.Fatal("reserve weakened")
	}
	j, h, c := catalogFixture(t, 1)
	reader := RecordedResultReader{Journal: j, InvocationID: "catalog", Policy: p, Catalog: c}
	for _, limit := range []int{p.PageBytes - 1, p.PageBytes, p.PageBytes + 1} {
		args, _ := json.Marshal(map[string]any{"catalog_id": c.ID, "handle": catalogHandle(*h[2].Receipt), "byte_limit": limit})
		got, err := reader.Read(context.Background(), args)
		if (err != nil) != (limit > p.PageBytes) || len(got) > p.ResultBytes {
			t.Fatal("page bound", limit, err)
		}
	}
	// Irreducible failure feedback cannot be silently turned into a handle.
	raw, _ := json.Marshal(ToolResult{Name: "read_file", CallID: "error", Error: strings.Repeat("feedback", p.ResultBytes/8+1)})
	e, _ := j.Append(context.Background(), "catalog", uint64(len(j.entries)), ToolDone, raw)
	m, err := recordedObservation(j.entries, nil, e.Sequence)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := (ReceiptCondenser{Policy: p, Catalog: c}).CondenseForCapacity(context.Background(), []Message{m}); !errors.Is(err, ErrContextBudget) {
		t.Fatal("protected feedback cap weakened", err)
	}
}

func TestCatalogFrozenAncestryBound(t *testing.T) {
	j := &forkJournal{streams: map[string][]Entry{}}
	var source *ForkSource
	for i := 0; i < 65; i++ {
		id := strings.Repeat("a", i+1)
		start := startRecord{RequestDigest: testRequestDigest, Prompt: "history", DeliveryDigest: (ReceiptCondenser{Policy: catalogPolicy()}).BindingDigest()}
		if source != nil {
			start.ContextFork = &ContextFork{Source: *source, HistorySHA256: testRequestDigest, Representation: frozenHistoryV1}
		}
		raw, _ := json.Marshal(start)
		j.Append(t.Context(), id, 0, Started, raw)
		j.Append(t.Context(), id, 1, ModelTurn, json.RawMessage(`{"text":"done"}`))
		j.Append(t.Context(), id, 2, Finished, json.RawMessage(`"done"`))
		ref, err := CaptureForkSource(t.Context(), j, id, testRequestDigest)
		if err != nil {
			t.Fatal(err)
		}
		source = &ref
		sources, err := ReceiptSources(t.Context(), j, source)
		if i < 64 && (err != nil || len(sources) != i+1) {
			t.Fatal("bounded chain", i, err)
		}
		if i == 64 && err == nil {
			t.Fatal("65-ancestor expansion admitted")
		}
	}
}

func BenchmarkCatalogHistoryProjection(b *testing.B) {
	for _, n := range []int{161, 1000} {
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			_, h, c := catalogFixture(b, n)
			condenser := ReceiptCondenser{Policy: catalogPolicy(), Catalog: c}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := condenser.CondenseForCapacity(context.Background(), h); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
