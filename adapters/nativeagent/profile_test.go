package nativeagent

import (
	"testing"

	"github.com/tekroo-ai/teams/kernel"
)

func TestNativeProfileSettingsRequireExactLoopbackEndpoint(t *testing.T) {
	valid := ProfileSettings{SchemaVersion: ProfileSettingsVersion, BaseURL: "http://127.0.0.1:8800/v1", Model: "local-model", MaxOutputTokens: 8192}
	if !valid.Valid() {
		t.Fatal("valid native profile was rejected")
	}
	for _, endpoint := range []string{"http://0.0.0.0:8800/v1", "http://example.com:8800/v1", "https://127.0.0.1:8800/v1", "http://127.0.0.1:8800", "http://127.0.0.1:8800/v1/", "http://127.0.0.1:8800/v1?x=1"} {
		modified := valid
		modified.BaseURL = endpoint
		if modified.Valid() {
			t.Fatalf("accepted non-exact endpoint %q", endpoint)
		}
	}
}

func TestNativeProfileDigestChangesWithExecutionSettings(t *testing.T) {
	settings := ProfileSettings{SchemaVersion: ProfileSettingsVersion, BaseURL: "http://127.0.0.1:8800/v1", Model: "local-model", MaxOutputTokens: 8192}
	bundle := kernel.Digest("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	first, err := ModelProfileDigest("coder", bundle, settings)
	if err != nil || !first.Valid() {
		t.Fatalf("digest: %s %v", first, err)
	}
	settings.MaxOutputTokens++
	second, err := ModelProfileDigest("coder", bundle, settings)
	if err != nil || first == second {
		t.Fatalf("settings change did not change digest: %s %s %v", first, second, err)
	}
	settings.ModelInstructions = "Use native structured tool calls."
	third, err := ModelProfileDigest("coder", bundle, settings)
	if err != nil || second == third {
		t.Fatalf("model instructions did not change digest: %s %s %v", second, third, err)
	}
	settings.ResponseMode = ResponseModeJSONSchemaActions
	fourth, err := ModelProfileDigest("coder", bundle, settings)
	if err != nil || third == fourth || !settings.Valid() {
		t.Fatalf("response transport did not rebind profile: %s %s %v", third, fourth, err)
	}
	settings.ResponseMode = "unknown"
	if settings.Valid() {
		t.Fatal("accepted unknown response mode")
	}
}
