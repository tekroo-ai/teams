package agentruntime

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"reflect"
	"sync"
)

// A handle is a locator, NOT authentication. The catalog binds full references
// to one admitted invocation and coordinator-verified frozen ancestry. Every
// actual read still verifies the original journal and full SHA256 reference.
type ReceiptCatalog struct {
	Journal       Journal
	InvocationID  string
	RequestDigest string
	Sources       []ForkSource
	ID            string
	mu            sync.Mutex
	refs          map[string]RecordedResultRef
	binding       string
}

type catalogPresentation struct {
	Delivery string `json:"delivery"`
	Handle   string `json:"handle"`
}

const catalogDelivery = "RECORDED_RESULT_HANDLE"

type catalogObservation struct {
	Message
	Failed bool
}

func NewReceiptCatalog(j Journal, id, requestDigest string, sources []ForkSource) (*ReceiptCatalog, error) {
	if j == nil || id == "" || !validForkDigest(requestDigest) || len(sources) > 64 {
		return nil, ErrInvalidTurn
	}
	seen := map[string]bool{}
	for _, s := range sources {
		if !s.Valid() || s.InvocationID == id {
			return nil, ErrInvalidTurn
		}
		if seen[s.InvocationID] {
			return nil, ErrInvalidTurn
		}
		seen[s.InvocationID] = true
	}
	identity := catalogIdentity(id, requestDigest, sources)
	return &ReceiptCatalog{Journal: j, InvocationID: id, RequestDigest: requestDigest, Sources: append([]ForkSource(nil), sources...), ID: identity, binding: identity, refs: map[string]RecordedResultRef{}}, nil
}

func catalogIdentity(id, requestDigest string, sources []ForkSource) string {
	encoded, _ := json.Marshal(struct {
		Invocation, Request string
		Sources             []ForkSource
	}{id, requestDigest, sources})
	return resultDigest(string(encoded))
}

func catalogHandle(ref RecordedResultRef) string {
	b, _ := json.Marshal(ref)
	sum := sha256.Sum256(b)
	// Truncation saves locator bytes only; collision checks and authentication
	// use the complete reference and original observation digest below.
	return "r" + base64.RawURLEncoding.EncodeToString(sum[:16])
}

func (c *ReceiptCatalog) remember(ref RecordedResultRef) (string, error) {
	h := catalogHandle(ref)
	c.mu.Lock()
	defer c.mu.Unlock()
	if prior, ok := c.refs[h]; ok && prior != ref {
		return "", ErrConflict
	}
	c.refs[h] = ref
	return h, nil
}

// Sources are loaded through existing frozen-source verification; current
// observations are read-only journal data. No effect is initiated by indexing.
func (c *ReceiptCatalog) observations(ctx context.Context) (map[RecordedResultRef]catalogObservation, error) {
	if c == nil || c.Journal == nil || c.ID != c.binding || c.ID != catalogIdentity(c.InvocationID, c.RequestDigest, c.Sources) {
		return nil, ErrInvalidTurn
	}
	groups := []struct {
		id     string
		source *ForkSource
	}{{c.InvocationID, nil}}
	for i := range c.Sources {
		groups = append(groups, struct {
			id     string
			source *ForkSource
		}{c.Sources[i].InvocationID, &c.Sources[i]})
	}
	result := map[RecordedResultRef]catalogObservation{}
	for _, g := range groups {
		var es []Entry
		var err error
		if g.source == nil {
			es, err = c.Journal.Load(ctx, g.id)
		} else {
			es, err = LoadForkEntries(ctx, c.Journal, *g.source)
		}
		if err != nil {
			return nil, err
		}
		for i, e := range es {
			if e.InvocationID != g.id || e.Sequence != uint64(i+1) || !json.Valid(e.Payload) {
				return nil, ErrConflict
			}
		}
		if len(es) > 0 && g.source == nil {
			var start startRecord
			if es[0].Kind != Started || json.Unmarshal(es[0].Payload, &start) != nil || start.RequestDigest != c.RequestDigest {
				return nil, ErrConflict
			}
		}
		for _, e := range es {
			if e.Kind != ToolDone && (e.Kind != Finished || g.source == nil) {
				continue
			}
			if e.Kind == Finished {
				var start startRecord
				if json.Unmarshal(es[0].Payload, &start) != nil {
					return nil, ErrInvalidTurn
				}
				if start.FinalTool == "" {
					continue
				}
			}
			m, err := recordedObservation(es, g.source, e.Sequence)
			if err != nil {
				return nil, err
			}
			failed := false
			if e.Kind == ToolDone {
				var tool ToolResult
				if json.Unmarshal(e.Payload, &tool) != nil {
					return nil, ErrInvalidTurn
				}
				failed = tool.Error != ""
			}
			result[*m.Receipt] = catalogObservation{m, failed}
		}
	}
	return result, nil
}

func (c *ReceiptCatalog) Resolve(ctx context.Context, id, handle string) (RecordedResultRef, error) {
	if c == nil || id != c.ID {
		return RecordedResultRef{}, ErrInvalidTurn
	}
	// Reconstruct on a pending-call restart too, before a new model request has
	// had an opportunity to run the condenser. Index only authenticated sources.
	observed, err := c.observations(ctx)
	if err != nil {
		return RecordedResultRef{}, err
	}
	for ref := range observed {
		if _, err = c.remember(ref); err != nil {
			return RecordedResultRef{}, err
		}
	}
	c.mu.Lock()
	ref, ok := c.refs[handle]
	c.mu.Unlock()
	if !ok {
		return RecordedResultRef{}, ErrInvalidTurn
	}
	if _, ok = observed[ref]; !ok {
		return RecordedResultRef{}, ErrConflict
	}
	return ref, nil
}

func catalogView(m Message) (string, error) {
	if m.Receipt == nil {
		return "", ErrInvalidTurn
	}
	raw, err := json.Marshal(catalogPresentation{catalogDelivery, catalogHandle(*m.Receipt)})
	return string(raw), err
}

func validCatalogPresentation(content string, ref RecordedResultRef) bool {
	expected, _ := json.Marshal(catalogPresentation{catalogDelivery, catalogHandle(ref)})
	return content == string(expected)
}

func (c ReceiptCondenser) BindingDigest() string {
	if c.Policy.Representation != CatalogHistoryV1 {
		return ""
	}
	b, _ := json.Marshal(c.Policy)
	return resultDigest(string(b))
}

func (c ReceiptCondenser) CondenseForCapacity(ctx context.Context, history []Message) ([]Message, error) {
	return c.condenseCatalog(ctx, history, true)
}

func (c ReceiptCondenser) condenseCatalog(ctx context.Context, history []Message, all bool) ([]Message, error) {
	if c.Catalog == nil || !c.Policy.Valid() || c.Policy.Representation != CatalogHistoryV1 {
		return nil, ErrInvalidTurn
	}
	observed, err := c.Catalog.observations(ctx)
	if err != nil {
		return nil, err
	}
	result := restoreRecordedReferences(history, c.InheritedHistory)
	total := 0
	protected := map[int]bool{}
	duplicates := map[string]bool{}
	compact := map[int]bool{}
	for i, m := range result {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if m.Role != "tool" {
			continue
		}
		if m.Receipt == nil {
			if len(m.Content) > c.Policy.ResultBytes {
				return nil, fmt.Errorf("%w: oversized unrecorded observation; no reference manufactured", ErrContextBudget)
			}
			protected[i] = true
			total += len(m.Content)
			continue
		}
		original, ok := observed[*m.Receipt]
		if !ok || original.Name != m.Name || original.CallID != m.CallID {
			return nil, ErrConflict
		}
		if m.Content != original.Content && !validCatalogPresentation(m.Content, *m.Receipt) {
			var legacy receiptPresentation
			if json.Unmarshal([]byte(m.Content), &legacy) != nil || legacy.Delivery != "RECORDED_RESULT_REFERENCE" || legacy.Reference != *m.Receipt || legacy.Tool != m.Name || legacy.CallID != m.CallID {
				return nil, ErrConflict
			}
			// Do not carry forward a legacy descriptor's derived summary or
			// visible-page link. Rebind its locator to the verified original.
			result[i].Content, err = catalogView(m)
			if err != nil {
				return nil, err
			}
		}
		if _, err = c.Catalog.remember(*m.Receipt); err != nil {
			return nil, err
		}
		// Keep actual tool failure feedback verbatim. Successful JSON string
		// results are evidence and may be addressed like other successful output.
		if original.Failed {
			result[i].Content = original.Content
			protected[i] = true
		}
		key := m.Name + "\x00" + resultDigest(original.Content)
		compact[i] = duplicates[key] || all || len(result[i].Content) > c.Policy.ResultBytes
		duplicates[key] = true
		if compact[i] && !protected[i] {
			result[i].Content, err = catalogView(m)
			if err != nil {
				return nil, err
			}
		}
		if len(result[i].Content) > c.Policy.ResultBytes {
			return nil, fmt.Errorf("%w: protected feedback exceeds result delivery limit; journal retained", ErrContextBudget)
		}
		total += len(result[i].Content)
	}
	for i, m := range result {
		if total <= c.Policy.HistoryBytes {
			break
		}
		if m.Role != "tool" || m.Receipt == nil || protected[i] {
			continue
		}
		view, err := catalogView(m)
		if err != nil {
			return nil, err
		}
		if len(view) < len(m.Content) {
			total -= len(m.Content) - len(view)
			result[i].Content = view
		}
	}
	// This is a watermark, not a second context window. The exact complete
	// rendered request, including protected text and all wrappers, gates use.
	return result, nil
}

func equalProtectedContext(before, after []Message) bool {
	if len(before) != len(after) {
		return false
	}
	for i, m := range before {
		if m.Role != "tool" && !reflect.DeepEqual(m, after[i]) {
			return false
		}
		if m.Role == "tool" && (m.Role != after[i].Role || m.Name != after[i].Name || m.CallID != after[i].CallID) {
			return false
		}
		if m.Role == "tool" {
			x, y := m, after[i]
			x.Content, y.Content = "", ""
			if x.Receipt == nil {
				y.Receipt = nil
			}
			if !reflect.DeepEqual(x, y) {
				return false
			}
		}
	}
	return true
}
