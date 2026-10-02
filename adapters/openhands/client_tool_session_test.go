package openhands

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/tekroo-ai/teams/kernel"
)

type fixedToolSessionIssuer struct {
	session ReadOnlyToolSession
	err     error
	id      kernel.UUIDv7
	digest  kernel.Digest
}

func (issuer *fixedToolSessionIssuer) IssueReadOnlyToolSession(_ context.Context, id kernel.UUIDv7, digest kernel.Digest) (ReadOnlyToolSession, error) {
	issuer.id, issuer.digest = id, digest
	return issuer.session, issuer.err
}

func TestReadOnlyToolSessionPreservesOtherMCPServers(t *testing.T) {
	id := kernel.UUIDv7("00000000-0000-7000-8000-000000000951")
	digest := kernel.Digest("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	issuer := &fixedToolSessionIssuer{session: ReadOnlyToolSession{
		URL:         "http://127.0.0.1:8787/agent-tools/" + string(id) + "/" + string(digest),
		BearerToken: "scoped-test-token",
	}}
	client := &Client{toolSessions: issuer}
	other := map[string]any{"url": "http://127.0.0.1:9999/mcp", "transport": "http"}
	settings := map[string]any{"mcp_config": map[string]any{"other": other}, "system_prompt": "role"}
	got, err := client.withReadOnlyToolSession(context.Background(), settings, id, digest)
	if err != nil {
		t.Fatal(err)
	}
	if issuer.id != id || issuer.digest != digest {
		t.Fatalf("issuer received %q/%q", issuer.id, issuer.digest)
	}
	bound := got.(map[string]any)["mcp_config"].(map[string]any)
	if !reflect.DeepEqual(bound["other"], other) || got.(map[string]any)["system_prompt"] != "role" {
		t.Fatalf("existing agent settings changed: %#v", got)
	}
	want := map[string]any{"url": issuer.session.URL, "transport": "http", "headers": map[string]any{"Authorization": "Bearer scoped-test-token"}}
	if !reflect.DeepEqual(bound["tekroo_agent_tools"], want) {
		t.Fatalf("bound MCP endpoint = %#v", bound["tekroo_agent_tools"])
	}
}

func TestReadOnlyToolSessionFailsClosed(t *testing.T) {
	id := kernel.UUIDv7("00000000-0000-7000-8000-000000000951")
	digest := kernel.Digest("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	for _, test := range []struct {
		name     string
		session  ReadOnlyToolSession
		settings any
		err      error
	}{
		{"issuer failure", ReadOnlyToolSession{}, map[string]any{}, errors.New("binding failed")},
		{"missing token", ReadOnlyToolSession{URL: "http://127.0.0.1:8787/mcp"}, map[string]any{}, nil},
		{"non HTTP URL", ReadOnlyToolSession{URL: "file:///tmp/mcp", BearerToken: "token"}, map[string]any{}, nil},
		{"MCP name collision", ReadOnlyToolSession{URL: "http://127.0.0.1:8787/mcp", BearerToken: "token"}, map[string]any{"mcp_config": map[string]any{"tekroo_agent_tools": map[string]any{}}}, nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := &Client{toolSessions: &fixedToolSessionIssuer{session: test.session, err: test.err}}
			if _, err := client.withReadOnlyToolSession(context.Background(), test.settings, id, digest); err == nil {
				t.Fatal("invalid or unavailable tool session accepted")
			}
		})
	}
}
