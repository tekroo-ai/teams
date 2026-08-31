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

	"github.com/tekroo-ai/teams/kernel"
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

func TestLoadCancellationRequiresExactInvocationCommand(t *testing.T) {
	t.Parallel()
	command := kernel.KernelCommand{
		CommandType: "tekroo.command.work-invocation.request-cancellation",
		Target: kernel.AggregateRef{
			Kind: kernel.AggregateWorkInvocation,
			ID:   testInvocationID,
		},
	}
	raw, err := json.Marshal(command)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "cancel.json")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := loadCancellation([]string{string(testInvocationID), path}, bytes.NewReader(nil), 4096)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(loaded, raw) {
		t.Fatal("cancellation command bytes changed")
	}
	if _, err := loadCancellation([]string{"018f0000-0000-7000-8000-000000000002", path}, bytes.NewReader(nil), 4096); err == nil {
		t.Fatal("mismatched invocation accepted")
	}
}

func TestLoadCommandRejectsUnknownAndTrailingJSON(t *testing.T) {
	t.Parallel()
	for _, input := range []string{
		`{"CommandType":"tekroo.command.story.create","unknown":true}`,
		`{"CommandType":"tekroo.command.story.create"}{}`,
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
