package nativeagent

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tekroo-ai/teams/agentruntime"
)

func TestNativeSMAContextBridgeMatchesHookBoundary(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.URL.Path != "/v1/openhands/context" {
			t.Errorf("bridge route = %s %s", request.Method, request.URL.Path)
		}
		var body struct {
			SessionID  string `json:"session_id"`
			WorkingDir string `json:"working_dir"`
			Prompt     string `json:"prompt"`
		}
		if json.NewDecoder(request.Body).Decode(&body) != nil || body.SessionID != "invocation" || body.WorkingDir != "/tmp/task" || body.Prompt != "brief" {
			t.Errorf("bridge request = %+v", body)
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"hit_count":1,"context_block":"Prior decision (untrusted).","trace_id":"ctx_valid"}`))
	}))
	defer server.Close()
	bridge := SMAContextBridge{BaseURL: server.URL, Timeout: 750 * time.Millisecond}
	memory, err := bridge.Retrieve(context.Background(), "invocation", "/tmp/task", "brief")
	if err != nil || memory.Status != "AVAILABLE" || memory.Block != "Prior decision (untrusted)." || memory.TraceID != "ctx_valid" {
		t.Fatalf("native bridge = %+v, %v", memory, err)
	}
}

func TestNativeSMAContextBridgeRejectsRemoteConfigAndOversizedRecall(t *testing.T) {
	bridge := SMAContextBridge{BaseURL: "https://example.invalid", Timeout: time.Second}
	if _, err := bridge.Retrieve(context.Background(), "i", "/tmp/task", "prompt"); !errors.Is(err, agentruntime.ErrInvalidContextConfiguration) {
		t.Fatalf("remote bridge accepted: %v", err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(writer).Encode(map[string]any{"hit_count": 1, "context_block": strings.Repeat("x", 4097), "trace_id": "ctx_valid"})
	}))
	defer server.Close()
	bridge = SMAContextBridge{BaseURL: server.URL, Timeout: time.Second}
	memory, err := bridge.Retrieve(context.Background(), "i", "/tmp/task", "prompt")
	if err != nil || memory.Status != "INVALID_RESPONSE" || memory.Block != "" {
		t.Fatalf("oversized recall entered model context: %+v, %v", memory, err)
	}
}

func TestNativeSMAContextBridgeDoesNotFollowRedirects(t *testing.T) {
	var leaked atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { leaked.Add(1) }))
	defer target.Close()
	bridgeServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		http.Redirect(writer, request, target.URL, http.StatusTemporaryRedirect)
	}))
	defer bridgeServer.Close()
	bridge := SMAContextBridge{BaseURL: bridgeServer.URL, Timeout: time.Second}
	memory, err := bridge.Retrieve(context.Background(), "i", "/tmp/task", "secret prompt")
	if err != nil || memory.Status != "UNAVAILABLE" || leaked.Load() != 0 {
		t.Fatalf("redirect leaked prompt: memory=%+v err=%v target_requests=%d", memory, err, leaked.Load())
	}
}
