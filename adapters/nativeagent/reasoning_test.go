package nativeagent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tekroo-ai/teams/agentruntime"
	"github.com/tekroo-ai/teams/agenttools"
)

func TestNativeReasoningSettingsAreDigestBound(t *testing.T) {
	settings := ProfileSettings{SchemaVersion: ProfileSettingsVersion, BaseURL: "http://127.0.0.1:8800/v1", Model: "fixture", MaxOutputTokens: 16384}
	_, _, profile := testBriefAndProfile()
	legacy, err := ModelProfileDigest(profile.RoleFQRN, profile.RoleBundleDigest, settings)
	if err != nil {
		t.Fatal(err)
	}
	budget := -1
	settings.Reasoning = &agentruntime.ReasoningConfig{EnableThinking: true, Effort: "medium", BudgetTokens: &budget}
	enabled, err := ModelProfileDigest(profile.RoleFQRN, profile.RoleBundleDigest, settings)
	if err != nil || enabled == legacy {
		t.Fatalf("thinking not digest-bound: %v", err)
	}
	settings.Reasoning.Effort = "low"
	low, err := ModelProfileDigest(profile.RoleFQRN, profile.RoleBundleDigest, settings)
	if err != nil || low == enabled {
		t.Fatalf("effort not digest-bound: %v", err)
	}
	budget = 1024
	limited, err := ModelProfileDigest(profile.RoleFQRN, profile.RoleBundleDigest, settings)
	if err != nil || limited == low {
		t.Fatalf("budget not digest-bound: %v", err)
	}
	settings.Reasoning = &agentruntime.ReasoningConfig{EnableThinking: false, Effort: "medium"}
	if settings.Valid() {
		t.Fatal("contradictory reasoning accepted")
	}
	if _, err := ModelProfileDigest(profile.RoleFQRN, profile.RoleBundleDigest, settings); err == nil {
		t.Fatal("invalid reasoning received a profile digest")
	}
}

func TestPreparedSessionPassesReasoningToWire(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte("Use admitted tools."), 0600); err != nil {
		t.Fatal(err)
	}
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		if body["enable_thinking"] != true || body["reasoning_effort"] != "medium" || body["reasoning_budget_tokens"] != float64(-1) {
			t.Errorf("profile lost at request boundary: %+v", body)
		}
		_, _ = w.Write([]byte(`{"id":"role-turn","choices":[{"finish_reason":"stop","message":{"content":"Observed the assigned task."}}]}`))
	}))
	defer server.Close()
	brief, digest, profile := testBriefAndProfile()
	budget := -1
	profile.BaseURL = server.URL + "/v1"
	profile.Reasoning = &agentruntime.ReasoningConfig{EnableThinking: true, Effort: "medium", BudgetTokens: &budget}
	binding := &testBinding{root: root}
	session, err := PrepareReadOnly(context.Background(), brief, digest, Config{Bindings: binding, Gateway: agenttools.Gateway{Bindings: binding, Host: agenttools.Host{Timeout: time.Second}}, Journal: &testJournal{}, HTTP: server.Client(), Profile: profile})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := session.Run(ctx); err != nil || requests != 1 {
		t.Fatalf("run=%v requests=%d", err, requests)
	}
}

func TestNativeStructuredReasoningRequiresExplicitBudget(t *testing.T) {
	settings := ProfileSettings{SchemaVersion: ProfileSettingsVersion, BaseURL: "http://127.0.0.1:8800/v1", Model: "fixture", ResponseMode: ResponseModeJSONSchemaActions, MaxOutputTokens: 16384}
	settings.Reasoning = &agentruntime.ReasoningConfig{EnableThinking: true, Effort: "medium"}
	if settings.Valid() {
		t.Fatal("accepted implicit structured reasoning budget")
	}
	for _, budget := range []int{-1, 1024, 8192} {
		settings.Reasoning.BudgetTokens = &budget
		if !settings.Valid() {
			t.Fatalf("rejected supported structured reasoning budget %d", budget)
		}
	}
}
