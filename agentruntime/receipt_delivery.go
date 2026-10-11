package agentruntime

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strconv"
	"strings"
	"unicode/utf8"
)

var ErrContextBudget = errors.New("model request exceeds admitted context delivery budget")

// ContextDeliveryPolicy is one host-owned policy shared by every native role.
// Request admission is a conservative byte bound, NOT an exact token count.
// It is qualified for byte-BPE models whose chat-template overhead fits ReserveTokens.
// Other tokenizers/templates require qualification of an explicit policy.
type ContextDeliveryPolicy struct {
	ContextTokens int `json:"context_tokens"`
	ReserveTokens int `json:"reserve_tokens"`
	RequestBytes  int `json:"request_bytes"`
	ResultBytes   int `json:"result_bytes"`
	HistoryBytes  int `json:"history_bytes"`
	PageBytes     int `json:"page_bytes"`
}

func DefaultContextDeliveryPolicy() ContextDeliveryPolicy {
	return ContextDeliveryPolicy{ContextTokens: 262144, ReserveTokens: 8192, RequestBytes: 196608,
		ResultBytes: 8192, HistoryBytes: 65536, PageBytes: 4096}
}

func (p ContextDeliveryPolicy) Valid() bool {
	return p.ContextTokens > 0 && p.ContextTokens <= 1<<24 && p.ReserveTokens > 0 && p.ReserveTokens < p.ContextTokens &&
		p.RequestBytes >= 1024 && p.RequestBytes <= p.ContextTokens-p.ReserveTokens &&
		p.ResultBytes >= 1024 && p.ResultBytes <= p.HistoryBytes && p.HistoryBytes <= p.RequestBytes &&
		p.PageBytes > 0 && p.PageBytes <= 4096
}

func (p ContextDeliveryPolicy) Admit(requestBytes, outputTokens int) error {
	if !p.Valid() || requestBytes < 0 || outputTokens <= 0 {
		return ErrInvalidTurn
	}
	limit := min(p.RequestBytes, p.ContextTokens-p.ReserveTokens-outputTokens)
	if requestBytes > limit {
		return fmt.Errorf("%w: serialized request %d bytes, limit %d, output reserve %d tokens; original journal retained", ErrContextBudget, requestBytes, limit, outputTokens)
	}
	return nil
}

// RecordedResultRef addresses the exact displayed result, including an error.
// It is made only while replaying a durable ToolDone, never by the model.
type RecordedResultRef struct {
	InvocationID string `json:"invocation_id"`
	Sequence     uint64 `json:"sequence"`
	SHA256       string `json:"sha256"`
}

func resultDigest(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}

// Historical checkpoint hashes predate derived receipt metadata. Accept their
// original hash only for histories without that metadata, then restore an
// address only when the exact unchanged observation is unambiguous.
func legacyReceiptHistoryDigest(messages []Message) string {
	legacy := append([]Message(nil), messages...)
	for i := range legacy {
		legacy[i].Receipt = nil
	}
	return historyDigest(legacy)
}

func restoreRecordedReferences(history, original []Message) []Message {
	type key struct{ name, callID, digest string }
	refs := map[key]*RecordedResultRef{}
	for _, m := range original {
		if m.Role != "tool" || m.Receipt == nil {
			continue
		}
		k := key{m.Name, m.CallID, resultDigest(m.Content)}
		if _, duplicate := refs[k]; duplicate {
			refs[k] = nil
		} else {
			refs[k] = m.Receipt
		}
	}
	result := append([]Message(nil), history...)
	for i, m := range result {
		if m.Role == "tool" && m.Receipt == nil {
			result[i].Receipt = refs[key{m.Name, m.CallID, resultDigest(m.Content)}]
		}
	}
	return result
}

func legacyReceiptForkEquivalent(recorded, current *ContextFork) bool {
	if recorded == nil || current == nil || recorded.Source != current.Source || historyDigest(recorded.WorkingHistory) != recorded.HistorySHA256 || historyDigest(current.WorkingHistory) != current.HistorySHA256 {
		return false
	}
	for _, m := range recorded.WorkingHistory {
		if m.Receipt != nil {
			return false
		}
	}
	plain := append([]Message(nil), current.WorkingHistory...)
	for i := range plain {
		plain[i].Receipt = nil
	}
	return reflect.DeepEqual(recorded.WorkingHistory, plain)
}

// ReceiptCondenser changes only tool presentation. Instructions, decisions,
// arguments and raw journal records are untouched; there is no generated summary.
type ReceiptCondenser struct {
	Policy ContextDeliveryPolicy
	// Supplied only from the coordinator's verified frozen context fork. It
	// backfills derived addresses in legacy children, not evidence or decisions.
	InheritedHistory []Message
}

func (c ReceiptCondenser) Condense(ctx context.Context, history []Message) ([]Message, error) {
	return c.CondenseContext(ctx, nil, history)
}

func (c ReceiptCondenser) CondenseContext(ctx context.Context, _ []Message, history []Message) ([]Message, error) {
	if !c.Policy.Valid() {
		return nil, ErrInvalidTurn
	}
	result := restoreRecordedReferences(history, c.InheritedHistory)
	total := 0
	for i, m := range result {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if m.Role != "tool" {
			continue
		}
		if len(m.Content) > c.Policy.ResultBytes {
			if m.Receipt == nil {
				return nil, fmt.Errorf("%w: oversized tool result has no durable reference", ErrContextBudget)
			}
			content, err := receiptView(m, true)
			if err != nil {
				return nil, err
			}
			if len(content) > c.Policy.ResultBytes {
				content, err = receiptView(m, false)
			}
			if err != nil {
				return nil, err
			}
			if len(content) > c.Policy.ResultBytes {
				return nil, ErrContextBudget
			}
			result[i].Content = content
		}
		total += len(result[i].Content)
	}
	// Older receipts yield first. Even small results are addressable; a long
	// sequence of individually bounded responses cannot grow without admission.
	for i, m := range result {
		if total <= c.Policy.HistoryBytes {
			break
		}
		if m.Role != "tool" || m.Receipt == nil {
			continue
		}
		content, err := receiptView(m, false)
		if err != nil {
			return nil, err
		}
		if len(content) < len(m.Content) {
			total -= len(m.Content) - len(content)
			result[i].Content = content
		}
	}
	if total > c.Policy.HistoryBytes {
		return nil, fmt.Errorf("%w: tool-reference history exceeds %d bytes; journal retained", ErrContextBudget, c.Policy.HistoryBytes)
	}
	return result, nil
}

type receiptPresentation struct {
	Delivery  string            `json:"delivery"`
	Reference RecordedResultRef `json:"reference"`
	Tool      string            `json:"tool"`
	CallID    string            `json:"call_id"`
	Summary   map[string]any    `json:"summary,omitempty"`
	Read      string            `json:"read"`
}

func receiptView(m Message, summary bool) (string, error) {
	ref := m.Receipt
	if ref == nil || ref.InvocationID == "" || ref.Sequence == 0 || !validForkDigest(ref.SHA256) {
		return "", ErrInvalidTurn
	}
	// A previously projected checkpoint must retain the same immutable reference.
	if resultDigest(m.Content) != ref.SHA256 {
		var prior receiptPresentation
		if json.Unmarshal([]byte(m.Content), &prior) != nil || prior.Delivery != "RECORDED_RESULT_REFERENCE" || prior.Reference != *ref || prior.Tool != m.Name || prior.CallID != m.CallID {
			return "", ErrInvalidTurn
		}
		if summary {
			return m.Content, nil
		}
	}
	v := receiptPresentation{Delivery: "RECORDED_RESULT_REFERENCE", Reference: *ref, Tool: m.Name, CallID: m.CallID,
		Read: "read_recorded_result with reference invocation_id, sequence and sha256; json_pointer selects an exact field; match_field/match_value select exact array records. This view is not complete evidence."}
	if summary && resultDigest(m.Content) == ref.SHA256 {
		var object map[string]json.RawMessage
		if json.Unmarshal([]byte(m.Content), &object) == nil {
			v.Summary = make(map[string]any)
			for _, key := range []string{"name", "path", "status", "truncated", "line_count", "start_line", "end_line", "next_start_line", "match_count", "next_after", "commit_sha", "snapshot_sha256", "sha256", "exit_code", "execution_error", "permission_denial", "head", "clean"} {
				if raw := object[key]; len(raw) > 0 && len(raw) <= 512 {
					v.Summary[key] = raw
				}
			}
			var evidence struct {
				Complete bool            `json:"complete"`
				Command  json.RawMessage `json:"command"`
				Tags     json.RawMessage `json:"tags"`
				Tests    []struct {
					Status string `json:"status"`
				} `json:"tests"`
				Excluded []struct {
					Status string `json:"status"`
				} `json:"excluded"`
			}
			if raw := object["test_evidence"]; len(raw) > 0 && json.Unmarshal(raw, &evidence) == nil {
				counts := map[string]int{}
				for _, t := range evidence.Tests {
					counts[t.Status]++
				}
				for _, t := range evidence.Excluded {
					counts[t.Status]++
				}
				tests := map[string]any{"complete": evidence.Complete, "status_counts": counts, "records": len(evidence.Tests) + len(evidence.Excluded)}
				if len(evidence.Command) <= 1024 {
					tests["command"] = evidence.Command
				}
				if len(evidence.Tags) > 0 && len(evidence.Tags) <= 512 {
					tests["tags"] = evidence.Tags
				}
				v.Summary["test_evidence"] = tests
			}
		}
	}
	raw, err := json.Marshal(v)
	return string(raw), err
}

// RecordedResultReader is a context accessor, not a repository/effect tool.
// Parent sources are frozen by the coordinator and verified on every read.
type RecordedResultReader struct {
	Journal      Journal
	InvocationID string
	Sources      []ForkSource
	Policy       ContextDeliveryPolicy
}

func (r RecordedResultReader) Definition() ToolDefinition {
	ids := []string{r.InvocationID}
	for _, source := range r.Sources {
		ids = append(ids, source.InvocationID)
	}
	parameters, _ := json.Marshal(map[string]any{"type": "object", "additionalProperties": false,
		"required": []string{"invocation_id", "sequence", "sha256"}, "properties": map[string]any{
			"invocation_id": map[string]any{"type": "string", "enum": ids}, "sequence": map[string]any{"type": "integer", "minimum": 1},
			"sha256":       map[string]any{"type": "string", "minLength": 64, "maxLength": 64},
			"json_pointer": map[string]any{"type": "string", "maxLength": 1024},
			"match_field":  map[string]any{"type": "string", "maxLength": 128}, "match_value": map[string]any{"type": "string", "maxLength": 1024},
			"byte_offset": map[string]any{"type": "integer", "minimum": 0, "default": 0, "description": "Starting BYTE offset in the selected JSON, not an array index. Omit for the first page."},
			"byte_limit":  map[string]any{"type": "integer", "minimum": 1, "maximum": r.Policy.PageBytes, "default": r.Policy.PageBytes, "description": "Maximum BYTES per page, NOT the number of records. Omit to return the full bounded page, including after an exact record match. A value of 1 returns only one byte."}}})
	return ToolDefinition{Name: "read_recorded_result", Description: "Read an exact durable tool result for this invocation or its authorized ancestors. Read-only, never reruns a tool. Supply its displayed reference. Optional JSON pointer and exact array-record match avoid scanning a full suite. Omit byte_limit/byte_offset for an exact named-record lookup. Byte pagination includes digest, total_bytes and next_byte_offset; page_complete is not test completeness.", Parameters: parameters, Strict: true}
}

func (r RecordedResultReader) Read(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	var args struct {
		InvocationID string  `json:"invocation_id"`
		Sequence     uint64  `json:"sequence"`
		SHA256       string  `json:"sha256"`
		Pointer      string  `json:"json_pointer"`
		Field        string  `json:"match_field"`
		Value        *string `json:"match_value"`
		Offset       int     `json:"byte_offset"`
		Limit        *int    `json:"byte_limit"`
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&args) != nil || d.Decode(new(any)) != io.EOF || !r.Policy.Valid() || r.Journal == nil || args.Sequence == 0 || !validForkDigest(args.SHA256) || args.Offset < 0 || args.Limit != nil && (*args.Limit <= 0 || *args.Limit > r.Policy.PageBytes) || len(args.Pointer) > 1024 || len(args.Field) > 128 || (args.Field == "") != (args.Value == nil) || args.Value != nil && len(*args.Value) > 1024 {
		return nil, ErrInvalidTurn
	}
	var entries []Entry
	var err error
	if args.InvocationID == r.InvocationID {
		entries, err = r.Journal.Load(ctx, r.InvocationID)
	} else {
		found := false
		for _, source := range r.Sources {
			if source.InvocationID == args.InvocationID {
				entries, err = LoadForkEntries(ctx, r.Journal, source)
				found = true
				break
			}
		}
		if !found {
			return nil, ErrInvalidTurn
		}
	}
	if err != nil {
		return nil, err
	}
	if args.Sequence > uint64(len(entries)) {
		return nil, ErrInvalidTurn
	}
	e := entries[args.Sequence-1]
	var result ToolResult
	if e.InvocationID != args.InvocationID || e.Sequence != args.Sequence || e.Kind != ToolDone || json.Unmarshal(e.Payload, &result) != nil || !json.Valid(result.Output) {
		return nil, ErrInvalidTurn
	}
	content := string(result.Output)
	if result.Error != "" {
		content = strJSON(result.Error)
	}
	if resultDigest(content) != args.SHA256 {
		return nil, ErrConflict
	}
	selected, err := selectRecordedJSON([]byte(content), args.Pointer, args.Field, args.Value)
	if err != nil {
		return nil, err
	}
	if args.Offset > len(selected) {
		return nil, ErrInvalidTurn
	}
	limit := r.Policy.PageBytes
	if args.Limit != nil {
		limit = *args.Limit
	}
	end := args.Offset + min(limit, len(selected)-args.Offset)
	// Ordinary JSON is readable; a page splitting a UTF-8 codepoint uses base64
	// so byte pagination always round-trips exactly.
	pageValue := map[string]any{"reference": RecordedResultRef{args.InvocationID, args.Sequence, args.SHA256}, "tool": result.Name, "call_id": result.CallID,
		"json_pointer": args.Pointer, "total_bytes": len(selected), "byte_offset": args.Offset}
	// Bound the encoded envelope too: escaping control bytes can expand a page.
	// Shrink only the returned page, never its source or its exact byte offsets.
	for {
		chunk := selected[args.Offset:end]
		pageValue["next_byte_offset"], pageValue["page_complete"] = end, end == len(selected)
		if utf8.Valid(chunk) {
			pageValue["encoding"], pageValue["content"] = "utf8", string(chunk)
		} else {
			pageValue["encoding"], pageValue["content"] = "base64", chunk
		}
		page, err := json.Marshal(pageValue)
		if err != nil {
			return nil, err
		}
		if len(page) <= r.Policy.ResultBytes {
			return page, nil
		}
		if end-args.Offset <= 1 {
			return nil, ErrContextBudget
		}
		end = args.Offset + (end-args.Offset)/2
	}
}

// ReceiptSources follows only frozen coordinator-admitted ancestry. There is
// no actor/name-based access and no model-selectable conversation. Cycles and
// excessive depth fail rather than granting unverified history access.
func ReceiptSources(ctx context.Context, journal Journal, source *ForkSource) ([]ForkSource, error) {
	var sources []ForkSource
	seen := map[string]bool{}
	for source != nil {
		if seen[source.InvocationID] || len(sources) >= 64 {
			return nil, ErrInvalidTurn
		}
		entries, err := LoadForkEntries(ctx, journal, *source)
		if err != nil {
			return nil, err
		}
		seen[source.InvocationID] = true
		sources = append(sources, *source)
		var start startRecord
		if json.Unmarshal(entries[0].Payload, &start) != nil {
			return nil, ErrInvalidTurn
		}
		source = nil
		if start.ContextFork != nil {
			if !validContextFork(start.ContextFork, entries[0].InvocationID) {
				return nil, ErrInvalidTurn
			}
			source = &start.ContextFork.Source
		}
	}
	return sources, nil
}

func selectRecordedJSON(raw []byte, pointer, field string, value *string) ([]byte, error) {
	if pointer == "" && field == "" {
		return raw, nil
	}
	var node json.RawMessage = raw
	if pointer != "" {
		if !strings.HasPrefix(pointer, "/") {
			return nil, ErrInvalidTurn
		}
		for _, part := range strings.Split(pointer[1:], "/") {
			// Reject invalid escapes rather than aliasing a different path.
			for i := 0; i < len(part); i++ {
				if part[i] == '~' {
					if i+1 >= len(part) || part[i+1] != '0' && part[i+1] != '1' {
						return nil, ErrInvalidTurn
					}
					i++
				}
			}
			part = strings.ReplaceAll(strings.ReplaceAll(part, "~1", "/"), "~0", "~")
			var object map[string]json.RawMessage
			if json.Unmarshal(node, &object) == nil && object != nil {
				node = object[part]
			} else {
				var array []json.RawMessage
				i, err := strconv.Atoi(part)
				if err != nil || strconv.Itoa(i) != part || json.Unmarshal(node, &array) != nil || i < 0 || i >= len(array) {
					return nil, ErrInvalidTurn
				}
				node = array[i]
			}
			if len(node) == 0 {
				return nil, ErrInvalidTurn
			}
		}
	}
	if field != "" {
		var array []json.RawMessage
		if json.Unmarshal(node, &array) != nil || array == nil {
			return nil, ErrInvalidTurn
		}
		matched := make([]json.RawMessage, 0)
		for _, item := range array {
			var object map[string]json.RawMessage
			var s string
			if json.Unmarshal(item, &object) == nil && json.Unmarshal(object[field], &s) == nil && s == *value {
				matched = append(matched, item)
			}
		}
		return json.Marshal(matched)
	}
	return node, nil
}
