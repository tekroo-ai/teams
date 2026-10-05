package openhands

import (
	"context"
	"encoding/json"
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

func TestReadOnlyToolSessionExposesOneReaderPerOperation(t *testing.T) {
	id := kernel.UUIDv7("00000000-0000-7000-8000-000000000952")
	digest := kernel.Digest("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	profile, err := NewOpenAICompatibleAgentSettings(AgentSettingsConfig{
		Model: "openai/local", ModelCanonicalName: "openai/gpt-4o", BaseURL: "http://127.0.0.1:8800/v1", APIKey: "fixture",
		Tools:               []string{"file_read", "list_files", "find_files", "search_file_contents"},
		MaximumOutputTokens: 8192, CondenserOutputTokens: 4096, TimeoutSeconds: 1200,
		CondenserMaximumEvents: 80, CondenserMaximumTokens: 96000,
	})
	if err != nil {
		t.Fatal(err)
	}
	var settings map[string]any
	if err := json.Unmarshal(profile, &settings); err != nil {
		t.Fatal(err)
	}
	baseline := "1111111111111111111111111111111111111111"
	settings["tools"] = append(settings["tools"].([]any), map[string]any{
		"name": "repository_diff_operations", "params": map[string]any{"baseline_commit": baseline},
	})
	client := &Client{toolSessions: &fixedToolSessionIssuer{session: ReadOnlyToolSession{
		URL: "http://127.0.0.1:8787/agent-tools/session", BearerToken: "scoped-test-token",
	}}}
	bound, err := client.withReadOnlyToolSession(context.Background(), settings, id, digest)
	if err != nil {
		t.Fatal(err)
	}
	tools := bound.(map[string]any)["tools"].([]any)
	names := make([]string, len(tools))
	for index, tool := range tools {
		names[index] = tool.(map[string]any)["name"].(string)
	}
	want := []string{"list_changed_files", "read_file_diff"}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("model-facing native tools = %q, want %q", names, want)
	}
	for _, tool := range tools {
		params := tool.(map[string]any)["params"].(map[string]any)
		if params["baseline_commit"] != baseline {
			t.Fatalf("baseline binding lost: %#v", tool)
		}
	}
	var info conversationInfo
	if err := json.Unmarshal(mustJSON(map[string]any{"agent": bound}), &info); err != nil {
		t.Fatal(err)
	}
	if !conversationAgentMatches(info, profile, true) || conversationAgentMatches(info, profile) {
		t.Fatal("deduplicated conversation was not limited to an MCP-bound session")
	}
	var legacy conversationInfo
	if err := json.Unmarshal(mustJSON(map[string]any{"agent": json.RawMessage(profile)}), &legacy); err != nil {
		t.Fatal(err)
	}
	if !conversationAgentMatches(legacy, profile, true) {
		t.Fatal("existing conversation with the qualified native tools was rejected")
	}
	// Keep the complete materialized settings from the new conversation and
	// substitute only the two native tools retained by older MCP sessions.
	var intermediate conversationInfo
	if err := json.Unmarshal(mustJSON(map[string]any{"agent": bound}), &intermediate); err != nil {
		t.Fatal(err)
	}
	intermediate.Agent.Tools = append([]struct {
		Name   string         `json:"name"`
		Params map[string]any `json:"params"`
	}{{Name: "find_files", Params: map[string]any{}}, {Name: "search_file_contents", Params: map[string]any{}}}, intermediate.Agent.Tools...)
	if !conversationAgentMatches(intermediate, profile, true) {
		t.Fatal("existing two-reader MCP conversation was rejected")
	}
	info.Agent.Tools = info.Agent.Tools[1:]
	if conversationAgentMatches(info, profile, true) {
		t.Fatal("missing non-overlapping tool was accepted")
	}
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
