package nativeagent

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/tekroo-ai/teams/agentruntime"
	"github.com/tekroo-ai/teams/agenttools"
)

func TestPriorTurnsLargeOriginalEntryExactBytePaging(t *testing.T) {
	j, _, _, old := contextFixture(t)
	entries := j.streams[old.InvocationID]
	// A synthetic large original model-turn payload preserves whitespace and
	// escape spelling. Paging must return THESE bytes, not a JSON remarshal.
	payload := json.RawMessage(` { "text" : "` + strings.Repeat(`original 🙂 \n `, 100000) + `" } `)
	if !json.Valid(payload) || len(payload) <= 1<<20 {
		t.Fatal("bad large fixture")
	}
	entries[1].Payload = payload
	source, err := agentruntime.CaptureForkSource(t.Context(), j, old.InvocationID, old.RequestDigest)
	if err != nil {
		t.Fatal(err)
	}
	p := agentruntime.DefaultContextDeliveryPolicy()
	p.Admission = "rendered_tokens"
	p.RepairReserveTokens = 32768
	p.Representation = agentruntime.CatalogHistoryV1
	tool := priorTurnsTool{journal: j, source: source, policy: p, checkAuthority: func(context.Context) error { return nil }}
	if _, err := tool.read(t.Context(), json.RawMessage(`{"start_sequence":2,"entry_count":1}`)); err == nil {
		t.Fatal("whole-entry cap weakened")
	}
	var assembled []byte
	for offset := 0; offset < len(payload); {
		args, _ := json.Marshal(map[string]any{"start_sequence": 2, "entry_count": 1, "byte_offset": offset, "byte_limit": p.PageBytes})
		raw, err := tool.read(t.Context(), args)
		if err != nil || len(raw) > p.ResultBytes {
			t.Fatal(err)
		}
		var page struct {
			Encoding, Content string
			Next              int    `json:"next_byte_offset"`
			SHA               string `json:"payload_sha256"`
			Source            agentruntime.ForkSource
		}
		if json.Unmarshal(raw, &page) != nil || page.Source != source || page.Next <= offset {
			t.Fatal("wrong page provenance")
		}
		chunk := []byte(page.Content)
		if page.Encoding == "base64" {
			chunk, err = base64.StdEncoding.Strict().DecodeString(page.Content)
			if err != nil {
				t.Fatal(err)
			}
		}
		assembled = append(assembled, chunk...)
		offset = page.Next
	}
	if !bytes.Equal(assembled, payload) {
		t.Fatal("original payload bytes changed")
	}
	for _, bad := range []string{`{"start_sequence":2,"entry_count":2,"byte_offset":0}`, `{"start_sequence":2,"entry_count":1,"byte_limit":999999}`, `{"start_sequence":2,"entry_count":1,"byte_offset":-1}`, `{"start_sequence":2,"entry_count":1,"byte_limit":null,"invocation_id":"other"}`} {
		if _, err := tool.read(t.Context(), json.RawMessage(bad)); err == nil {
			t.Fatal("invalid page admitted", bad)
		}
	}
	tool.checkAuthority = func(context.Context) error { return agenttools.ErrForbidden }
	if _, err := tool.read(t.Context(), json.RawMessage(`{"start_sequence":2,"entry_count":1,"byte_offset":0}`)); !errors.Is(err, agenttools.ErrForbidden) {
		t.Fatal("revoked history read", err)
	}
	tool.policy.Representation = ""
	tool.checkAuthority = func(context.Context) error { return nil }
	if _, err := tool.read(t.Context(), json.RawMessage(`{"start_sequence":2,"entry_count":1,"byte_offset":0}`)); err == nil {
		t.Fatal("legacy behavior changed")
	}
}
