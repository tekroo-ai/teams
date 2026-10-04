package nativeagent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/tekroo-ai/teams/agentruntime"
	"github.com/tekroo-ai/teams/agenttools"
	"github.com/tekroo-ai/teams/kernel"
)

type evidenceReaderFunc func(context.Context, kernel.Digest) ([]byte, error)

func (reader evidenceReaderFunc) Read(ctx context.Context, digest kernel.Digest) ([]byte, error) {
	return reader(ctx, digest)
}

func TestNativeSessionAdvertisesAndExecutesOnlyAdmittedEvidence(t *testing.T) {
	brief, _, profile := testBriefAndProfile()
	profile.BaseURL = "http://127.0.0.1:8800/v1"
	content := []byte("go-test: PASS; independent validation: PASS")
	sum := sha256.Sum256(content)
	digest := kernel.Digest(hex.EncodeToString(sum[:]))
	id := kernel.UUIDv7("00000000-0000-7000-8000-000000000011")
	brief.Evidence = []kernel.EvidenceRef{{EvidenceID: id, SHA256: digest}}
	encoded, err := json.Marshal(brief)
	if err != nil {
		t.Fatal(err)
	}
	requestSum := sha256.Sum256(encoded)
	requestDigest := kernel.Digest(hex.EncodeToString(requestSum[:]))
	binding := &testBinding{root: t.TempDir()}
	config := Config{Bindings: binding, Gateway: agenttools.Gateway{Bindings: binding, Host: agenttools.Host{Timeout: time.Second}},
		Journal: &testJournal{}, HTTP: &http.Client{Timeout: time.Second}, Profile: profile,
		EvidenceReader: evidenceReaderFunc(func(context.Context, kernel.Digest) ([]byte, error) { return content, nil })}
	session, err := PrepareReadOnly(t.Context(), brief, requestDigest, config)
	if err != nil {
		t.Fatal(err)
	}
	model := session.Runner.Model.(boundModel).model.(agentruntime.OpenAIModel)
	found := false
	for _, definition := range model.Tools {
		if definition.Name == "read_evidence" {
			found = strings.Contains(string(definition.Parameters), string(id))
		}
	}
	if !found {
		t.Fatal("admitted evidence is absent from the native model tool contract")
	}
	result, err := session.Runner.Tools.ExecuteReadOnly(t.Context(), agentruntime.ToolCall{Name: "read_evidence", Arguments: json.RawMessage(`{"evidence_id":"` + string(id) + `"}`)})
	if err != nil || !strings.Contains(string(result), "independent validation: PASS") {
		t.Fatalf("admitted native evidence read: %s, %v", result, err)
	}
}

func TestAdmittedEvidenceToolBindsIDsAndPagesVerifiedContent(t *testing.T) {
	content := []byte("test pass; validator pass")
	sum := sha256.Sum256(content)
	digest := kernel.Digest(hex.EncodeToString(sum[:]))
	id := kernel.UUIDv7("00000000-0000-7000-8000-000000000001")
	reader := evidenceReaderFunc(func(_ context.Context, requested kernel.Digest) ([]byte, error) {
		if requested != digest {
			t.Fatal("reader received a model-selected digest")
		}
		return content, nil
	})
	tool, err := newAdmittedEvidenceTool(reader, []kernel.EvidenceRef{{EvidenceID: id, SHA256: digest}})
	if err != nil {
		t.Fatal(err)
	}
	definition := tool.definition()
	if definition.Name != "read_evidence" || !strings.Contains(string(definition.Parameters), string(id)) || !strings.Contains(string(definition.Parameters), `"maximum":65536`) {
		t.Fatalf("unbound evidence schema: %s", definition.Parameters)
	}
	page, err := tool.read(t.Context(), json.RawMessage(`{"evidence_id":"`+string(id)+`","offset":5,"limit":4}`))
	if err != nil || !strings.Contains(string(page), `"content":"pass"`) || !strings.Contains(string(page), `"next_offset":9`) {
		t.Fatalf("read admitted page: %s, %v", page, err)
	}
	if _, err := tool.read(t.Context(), json.RawMessage(`{"evidence_id":"00000000-0000-7000-8000-000000000002"}`)); !errors.Is(err, ErrInvalidBinding) {
		t.Fatalf("unadmitted ID accepted: %v", err)
	}
	if _, err := tool.read(t.Context(), json.RawMessage(`{"evidence_id":"`+string(id)+`","limit":65537}`)); !errors.Is(err, ErrInvalidBinding) {
		t.Fatalf("oversized page accepted: %v", err)
	}
	bad := evidenceReaderFunc(func(context.Context, kernel.Digest) ([]byte, error) { return []byte("changed"), nil })
	tool, err = newAdmittedEvidenceTool(bad, []kernel.EvidenceRef{{EvidenceID: id, SHA256: digest}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tool.read(t.Context(), json.RawMessage(`{"evidence_id":"`+string(id)+`"}`)); !errors.Is(err, ErrInvalidBinding) {
		t.Fatalf("digest mismatch accepted: %v", err)
	}
}
