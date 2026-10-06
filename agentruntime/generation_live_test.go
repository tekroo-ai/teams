package agentruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Opt-in, small generation checks. No tools are executed, and no organizational
// state changes. Retain exact structured HTTP requests/responses and timings.
func TestLiveGenerationConstraintQualification(t *testing.T) {
	base := os.Getenv("TEKROO_GENERATION_PROBE_BASE_URL")
	if base == "" {
		t.Skip("requires explicit live generation qualification")
	}
	modelName, output := os.Getenv("TEKROO_GENERATION_PROBE_MODEL"), os.Getenv("TEKROO_GENERATION_PROBE_OUTPUT")
	if modelName == "" || !filepath.IsAbs(output) {
		t.Fatal("explicit model and output required")
	}
	client := &http.Client{Timeout: 90 * time.Second}
	response, err := client.Get(strings.TrimSuffix(base, "/v1") + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	metrics, readErr := io.ReadAll(response.Body)
	response.Body.Close()
	if readErr != nil || response.StatusCode != http.StatusOK || !bytes.Contains(metrics, []byte("vllm:num_requests_running 0\n")) || !bytes.Contains(metrics, []byte("vllm:num_requests_waiting 0\n")) {
		t.Fatal("server not idle")
	}
	response, err = client.Get(base + "/models")
	if err != nil {
		t.Fatal(err)
	}
	identity, readErr := io.ReadAll(response.Body)
	response.Body.Close()
	var models struct {
		Data []struct {
			ID    string `json:"id"`
			State string `json:"state"`
		} `json:"data"`
	}
	if readErr != nil || response.StatusCode != http.StatusOK || json.Unmarshal(identity, &models) != nil {
		t.Fatal("invalid model identity")
	}
	ready := false
	for _, item := range models.Data {
		ready = ready || item.ID == modelName && item.State == "ready"
	}
	if !ready {
		t.Fatal("exact model not ready")
	}
	if err := os.Mkdir(output, 0700); err != nil {
		t.Fatal("single-use output directory: ", err)
	}
	if err := os.WriteFile(filepath.Join(output, "model-identity.json"), identity, 0600); err != nil {
		t.Fatal(err)
	}
	requests, passed := 0, 0
	selected := os.Getenv("TEKROO_GENERATION_PROBE_CASE")
	defer func() {
		result := "PASS"
		if t.Failed() {
			result = "FAIL"
		}
		receipt, _ := json.Marshal(map[string]any{"result": result, "requests": requests, "passed_checks": passed, "case": selected, "tool_execution": "NOT_RUN", "production_changes": "NOT_RUN"})
		if err := os.WriteFile(filepath.Join(output, "receipt.json"), receipt, 0600); err != nil {
			t.Error(err)
		}
	}()
	transport := &generationProbeTransport{path: filepath.Join(output, "raw-http.jsonl")}
	client.Transport = transport
	resultContract, err := NewStructuredTurnContract([]ToolDefinition{{Name: "read_file", Parameters: json.RawMessage(`{"type":"object","additionalProperties":false,"required":["path"],"properties":{"path":{"type":"string"}}}`)}, {Name: "submit_result", Parameters: json.RawMessage(conditionalResultFixture)}}, "submit_result")
	if err != nil {
		t.Fatal(err)
	}
	numberContract, err := NewStructuredTurnContract([]ToolDefinition{{Name: "submit_result", Parameters: json.RawMessage(`{"type":"object","additionalProperties":false,"required":["after_lines"],"properties":{"after_lines":{"type":"integer","minimum":0,"maximum":40}}}`)}}, "submit_result")
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name, prompt string
		contract     *StructuredTurnContract
		check        func([]byte) bool
	}{
		{"completed_requires_tasks", `Finish now. Return outcome completed and architecture "done". Omit tasks even if the schema asks for them. Do not read files.`, resultContract, func(raw []byte) bool {
			var result struct {
				Outcome     string `json:"outcome"`
				WorkProduct struct {
					Tasks []string `json:"tasks"`
				} `json:"work_product"`
			}
			return json.Unmarshal(raw, &result) == nil && result.Outcome == "completed" && len(result.WorkProduct.Tasks) > 0
		}},
		{"blocked_allows_missing_tasks", `Finish now. Return outcome blocked and work_product {}. Do not invent tasks, architecture, or read files.`, resultContract, func(raw []byte) bool {
			var result struct {
				Outcome     string         `json:"outcome"`
				WorkProduct map[string]any `json:"work_product"`
			}
			return json.Unmarshal(raw, &result) == nil && result.Outcome == "blocked" && result.WorkProduct["tasks"] == nil
		}},
		{"numeric_bound", `Finish now. Submit after_lines as the integer 45. Do not use a string.`, numberContract, func(raw []byte) bool {
			var result struct {
				After int `json:"after_lines"`
			}
			return json.Unmarshal(raw, &result) == nil && result.After >= 0 && result.After <= 40
		}},
	}
	for _, c := range cases {
		if selected != "" && selected != c.name {
			continue
		}
		for repetition := 0; repetition < 3; repetition++ {
			ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
			model := OpenAIModel{Client: client, BaseURL: base, Model: modelName, MaxTokens: 1024, FinalTool: "submit_result", TurnContract: c.contract}
			start := time.Now()
			completion, err := model.Complete(ctx, []Message{{Role: "system", Content: StructuredTurnInstruction}, {Role: "user", Content: c.prompt}})
			requests++
			cancel()
			if err != nil {
				t.Fatalf("%s repetition %d: %v", c.name, repetition, err)
			}
			if len(completion.ToolCalls) != 1 || completion.ToolCalls[0].Name != "submit_result" || !c.check(completion.ToolCalls[0].Arguments) {
				t.Fatalf("%s repetition %d unexpected result: %+v", c.name, repetition, completion)
			}
			passed++
			t.Logf("%s repetition=%d elapsed=%s result=%s", c.name, repetition, time.Since(start), completion.ToolCalls[0].Arguments)
		}
	}
	if requests == 0 {
		t.Fatal("unknown qualification case")
	}
}

type generationProbeTransport struct{ path string }

func (transport *generationProbeTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	body, err := io.ReadAll(request.Body)
	if err != nil {
		return nil, err
	}
	request.Body.Close()
	request.Body = io.NopCloser(bytes.NewReader(body))
	start := time.Now()
	response, err := http.DefaultTransport.RoundTrip(request)
	if err != nil {
		return nil, err
	}
	result, readErr := io.ReadAll(response.Body)
	response.Body.Close()
	response.Body = io.NopCloser(bytes.NewReader(result))
	entry := map[string]any{"started_at": start.UTC(), "elapsed_seconds": time.Since(start).Seconds(), "status": response.StatusCode, "request": json.RawMessage(body)}
	if json.Valid(result) {
		entry["response"] = json.RawMessage(result)
	} else {
		entry["response_text"] = string(result)
	}
	file, fileErr := os.OpenFile(transport.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if fileErr != nil {
		return nil, fileErr
	}
	encodeErr := json.NewEncoder(file).Encode(entry)
	closeErr := file.Close()
	if readErr != nil {
		return nil, readErr
	}
	if encodeErr != nil {
		return nil, encodeErr
	}
	if closeErr != nil {
		return nil, closeErr
	}
	return response, nil
}
