package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tekroo-ai/teams/adapters/operationalruntime"
	"github.com/tekroo-ai/teams/kernel"
	"github.com/tekroo-ai/teams/organization"
)

const (
	testToken        = "0123456789abcdef0123456789abcdef"
	testInvocationID = kernel.UUIDv7("018f0000-0000-7000-8000-000000000001")
)

func TestOperatorClientUsesBearerAuthAndReturnsBoundedResponse(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1/status" || request.Method != http.MethodGet {
			t.Fatalf("unexpected request %s %s", request.Method, request.URL.Path)
		}
		if got := request.Header.Get("Authorization"); got != "Bearer "+testToken {
			t.Fatalf("authorization = %q", got)
		}
		_, _ = io.WriteString(writer, `{"state":"RUNNING"}`)
	}))
	defer server.Close()

	client, err := newOperatorClient(server.URL, testToken, time.Second, 1024)
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.request(context.Background(), http.MethodGet, "/v1/status", nil)
	if err != nil {
		t.Fatal(err)
	}
	if string(response) != `{"state":"RUNNING"}` {
		t.Fatalf("response = %s", response)
	}
}

func TestOperatorClientRejectsNonLoopbackOrMalformedEndpoint(t *testing.T) {
	t.Parallel()
	for _, endpoint := range []string{
		"https://127.0.0.1:9443",
		"http://example.com:8080",
		"http://localhost:8080@evil.example:8080",
		"http://127.0.0.1:8080/path",
		"http://127.0.0.1",
	} {
		if _, err := newOperatorClient(endpoint, testToken, time.Second, 1024); err == nil {
			t.Fatalf("accepted unsafe endpoint %q", endpoint)
		}
	}
}

func TestOperatorClientRejectsOversizedRequestAndResponse(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(writer, "12345")
	}))
	defer server.Close()
	client, err := newOperatorClient(server.URL, testToken, time.Second, 4)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.request(context.Background(), http.MethodPost, "/v1/commands", []byte("12345")); err == nil {
		t.Fatal("oversized request accepted")
	}
	if _, err := client.request(context.Background(), http.MethodGet, "/v1/status", nil); err == nil {
		t.Fatal("oversized response accepted")
	}
}

func TestLoadCancellationBuildsFocusedExactInvocationRequest(t *testing.T) {
	t.Parallel()
	request := operationalruntime.CancellationRequest{ExpectedRevision: 2, Reason: "operator requested stop", EvidenceRefs: []kernel.EvidenceRef{{EvidenceID: "018f0000-0000-7000-8000-000000000003", SHA256: kernel.Digest(strings.Repeat("a", 64))}}, IdempotencyKey: "cancel-1"}
	raw, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "cancel.json")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	method, target, loaded, err := loadCancellation([]string{string(testInvocationID), path}, bytes.NewReader(nil), 4096)
	if err != nil {
		t.Fatal(err)
	}
	if method != http.MethodPost || target != "/v1/invocations/"+string(testInvocationID)+"/cancel" || !bytes.Equal(loaded, raw) {
		t.Fatalf("cancellation request method=%s target=%s", method, target)
	}
	if _, _, _, err := loadCancellation([]string{"not-an-invocation", path}, bytes.NewReader(nil), 4096); err == nil {
		t.Fatal("invalid invocation accepted")
	}
}

func TestLoadCommandRejectsUnknownAndTrailingJSON(t *testing.T) {
	t.Parallel()
	for _, input := range []string{
		`{"command_type":"tekroo.command.story.create","unknown":true}`,
		`{"command_type":"tekroo.command.story.create"}{}`,
	} {
		if _, _, err := loadCommand([]string{"-"}, strings.NewReader(input), 4096); err == nil {
			t.Fatalf("accepted invalid command %q", input)
		}
	}
}

func TestIdentityReadRequiresUUIDv7(t *testing.T) {
	t.Parallel()
	method, path, err := identityRead("/v1/tasks/", []string{string(testInvocationID)})
	if err != nil || method != http.MethodGet || path != "/v1/tasks/"+string(testInvocationID) {
		t.Fatalf("unexpected result: %q %q %v", method, path, err)
	}
	if _, _, err := identityRead("/v1/tasks/", []string{"not-an-id"}); err == nil {
		t.Fatal("invalid identity accepted")
	}
}

func TestInvocationReadUsesExactPath(t *testing.T) {
	t.Parallel()
	method, path, err := identityRead("/v1/invocations/", []string{string(testInvocationID)})
	if err != nil || method != http.MethodGet || path != "/v1/invocations/"+string(testInvocationID) {
		t.Fatalf("unexpected result: %q %q %v", method, path, err)
	}
}

func TestLoadFederatedMessageWrapsAliasWithoutWeakeningIdentity(t *testing.T) {
	now := time.Date(2026, 9, 1, 20, 0, 0, 0, time.UTC)
	message := organization.OrganizationalMessage{SchemaVersion: organization.OrganizationalMessageSchemaVersion, ID: "00000000-0000-7000-8000-000000000620", Type: "tekroo.message.feature.request", Purpose: organization.PurposeRequest, Sender: "source::product-owner-1", SenderExecution: kernel.ExecutionTuple{ExecutionID: "00000000-0000-7000-8000-000000000611", FencingEpoch: 1}, CorrelationID: "00000000-0000-7000-8000-000000000612", Work: organization.MessageWorkLink{DAGNodeID: "00000000-0000-7000-8000-000000000613"}, Flow: organization.MessageFlow{ThreadID: "00000000-0000-7000-8000-000000000612", StepID: "00000000-0000-7000-8000-000000000613", Hop: 1, MaximumHops: 8, BudgetAccountID: "00000000-0000-7000-8000-000000000614", LifecycleEpoch: 1, ScopeRevision: 1, ProgressDigest: kernel.Digest(strings.Repeat("c", 64))}, Body: json.RawMessage(`{"request":"design"}`), CreatedAt: now, ExpiresAt: now.Add(time.Hour)}
	raw, _ := json.Marshal(message)
	wrapped, err := loadFederatedMessage([]string{"remote-architect", "-"}, bytes.NewReader(raw), 4096)
	if err != nil {
		t.Fatal(err)
	}
	var value struct {
		Alias   string                             `json:"alias"`
		Message organization.OrganizationalMessage `json:"message"`
	}
	if err := json.Unmarshal(wrapped, &value); err != nil || value.Alias != "remote-architect" || value.Message.Recipient != "" {
		t.Fatalf("wrapped=%s err=%v", wrapped, err)
	}
	if _, err := loadFederatedMessage([]string{"remote-*", "-"}, bytes.NewReader(raw), 4096); err == nil {
		t.Fatal("wildcard alias accepted")
	}
}
