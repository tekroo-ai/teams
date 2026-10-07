package nativeagent

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/tekroo-ai/teams/agentruntime"
	"github.com/tekroo-ai/teams/agenttools"
	"github.com/tekroo-ai/teams/application"
	"github.com/tekroo-ai/teams/kernel"
)

// This opt-in diagnostic reads an existing checkpoint and runs only its
// architect. It never writes MongoDB, advances a workflow, enables effects,
// restarts services, or replaces the retained invocation. Its fresh output
// directory is single-use and holds the full raw journal, not a PASS attestation.
func TestArchitectReadEfficiencyDiagnostic(t *testing.T) {
	configPath := os.Getenv("TEKROO_ARCHITECT_DIAGNOSTIC_CONFIG")
	if configPath == "" {
		t.Skip("requires explicit architect-only diagnostic authorization")
	}
	sourceID, outputDir := os.Getenv("TEKROO_ARCHITECT_DIAGNOSTIC_SOURCE"), os.Getenv("TEKROO_ARCHITECT_DIAGNOSTIC_OUTPUT")
	if !kernel.UUIDv7(sourceID).Valid() || !filepath.IsAbs(outputDir) {
		t.Fatal("invalid explicit diagnostic identity or output")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Minute)
	defer cancel()
	configData, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	var deployment struct {
		Profiles []struct {
			Role     kernel.RoleFQRN `json:"role_fqrn"`
			Settings ProfileSettings `json:"native_settings"`
		} `json:"profiles"`
		Workspaces []struct {
			Root     string `json:"working_directory"`
			Baseline string `json:"baseline_sha"`
		} `json:"workspaces"`
	}
	if err := json.Unmarshal(configData, &deployment); err != nil {
		t.Fatal(err)
	}
	script := `const fs=require("fs");const c=JSON.parse(fs.readFileSync(process.env.TEKROO_ARCHITECT_DIAGNOSTIC_CONFIG,"utf8"));const d=new Mongo(fs.readFileSync(c.mongo.uri_file,"utf8").trim()).getDB(c.mongo.database);if(d.work_invocations.countDocuments({state:{$in:["AUTHORIZED","CLAIMED","STARTED"]}})>0)throw new Error("active invocation");let active=0;for(const r of d.work_invocations.find({}).toArray()){const j=JSON.parse(r.data.toString("utf8")).invocation;if(["AUTHORIZED","CLAIMED","STARTED"].includes(j.State))active++;}if(active)throw new Error("active work");const rows=d.agent_turn_entries.find({invocation_id:process.env.TEKROO_ARCHITECT_DIAGNOSTIC_SOURCE}).sort({sequence:1}).toArray();if(!rows.length||rows[0].kind!=="started"||rows.at(-1).kind!=="failed")throw new Error("source is not retained failed architecture");print(JSON.stringify({start:JSON.parse(rows[0].payload.toString("utf8")),evidence:rows.filter(r=>r.kind==="tool_done").map(r=>JSON.parse(r.payload.toString("utf8"))).filter(r=>r.name==="read_evidence"&&!r.error).map(r=>r.output)}));void 0;`
	raw, err := exec.CommandContext(ctx, "mongosh", "--nodb", "--quiet", "--eval", script).Output()
	if err != nil {
		t.Fatalf("read checkpoint: %v", err)
	}
	var retained struct {
		Start struct {
			Prompt    string                       `json:"prompt"`
			System    string                       `json:"system_prompt"`
			Digest    string                       `json:"request_digest"`
			FinalTool string                       `json:"final_tool"`
			Semantic  agentruntime.SemanticContext `json:"semantic_context"`
		} `json:"start"`
		Evidence []struct {
			SHA      kernel.Digest `json:"sha256"`
			Content  string        `json:"content"`
			Complete bool          `json:"complete"`
			Encoding string        `json:"encoding"`
			Offset   int           `json:"offset"`
		} `json:"evidence"`
	}
	if err := json.Unmarshal(raw, &retained); err != nil {
		t.Fatal(err)
	}
	var prompt struct {
		Brief application.ExecutionBrief `json:"execution_brief"`
	}
	if err := json.Unmarshal([]byte(retained.Start.Prompt), &prompt); err != nil {
		t.Fatal(err)
	}
	brief := prompt.Brief
	if brief.InvocationID != kernel.UUIDv7(sourceID) || brief.RoleGrounding.RoleFQRN != "architect" || retained.Start.FinalTool != "submit_result" || brief.Purpose != kernel.PurposeHandoff {
		t.Fatal("checkpoint role/purpose mismatch")
	}
	var settings ProfileSettings
	for _, profile := range deployment.Profiles {
		if profile.Role == "architect" {
			settings = profile.Settings
		}
	}
	if !settings.Valid() {
		t.Fatal("invalid current model settings")
	}
	profileDigest, digestErr := ModelProfileDigest(brief.RoleGrounding.RoleFQRN, brief.RoleGrounding.BundleDigest, settings)
	if digestErr != nil || profileDigest != brief.ModelProfileDigest {
		t.Fatal("current model settings differ from the retained checkpoint")
	}
	var workspace struct{ Root, Baseline string }
	for _, w := range deployment.Workspaces {
		if strings.HasSuffix(w.Root, "/architect-1") {
			workspace.Root, workspace.Baseline = w.Root, w.Baseline
		}
	}
	if workspace.Root == "" {
		t.Fatal("missing preserved architect workspace")
	}
	git := exec.CommandContext(ctx, "git", "rev-parse", "HEAD")
	git.Dir = workspace.Root
	head, err := git.Output()
	if err != nil || strings.TrimSpace(string(head)) != workspace.Baseline {
		t.Fatal("workspace baseline changed")
	}
	git = exec.CommandContext(ctx, "git", "status", "--porcelain")
	git.Dir = workspace.Root
	status, err := git.Output()
	if err != nil || len(status) != 0 {
		t.Fatalf("workspace is not clean: %v", err)
	}
	preflightClient := &http.Client{Timeout: 10 * time.Second}
	response, err := preflightClient.Get(strings.TrimSuffix(settings.BaseURL, "/v1") + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	var metrics strings.Builder
	_, err = io.Copy(&metrics, response.Body)
	response.Body.Close()
	if err != nil || !strings.Contains(metrics.String(), "vllm:num_requests_running 0\n") || !strings.Contains(metrics.String(), "vllm:num_requests_waiting 0\n") {
		t.Fatal("model server is not idle")
	}
	if err := os.Mkdir(outputDir, 0700); err != nil {
		t.Fatal("single-use diagnostic directory: ", err)
	}
	journal := &diagnosticJournal{path: filepath.Join(outputDir, "raw-journal.jsonl")}
	settingsData, _ := json.Marshal(settings)
	checkpointHash := sha256.Sum256(raw)
	if err := os.WriteFile(filepath.Join(outputDir, "source-checkpoint.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outputDir, "model-settings.json"), settingsData, 0600); err != nil {
		t.Fatal(err)
	}
	reader := evidenceReaderFunc(func(_ context.Context, digest kernel.Digest) ([]byte, error) {
		for _, e := range retained.Evidence {
			if e.SHA == digest && e.Complete && e.Offset == 0 && e.Encoding == "utf8" {
				data := []byte(e.Content)
				if kernel.Digest(fmt.Sprintf("%x", sha256.Sum256(data))) == digest {
					return data, nil
				}
			}
		}
		return nil, fmt.Errorf("complete retained evidence unavailable: %s", digest)
	})
	binding := &testBinding{root: workspace.Root}
	profile := Profile{RoleFQRN: brief.RoleGrounding.RoleFQRN, RoleBundleDigest: brief.RoleGrounding.BundleDigest, ModelProfileDigest: brief.ModelProfileDigest, RuntimeIdentityDigest: brief.RuntimeIdentityDigest, ToolPolicyDigest: brief.ToolPolicyDigest, EffectPolicyDigest: brief.EffectPolicyDigest, BaseURL: settings.BaseURL, Model: settings.Model, ModelInstructions: settings.ModelInstructions, ResponseMode: settings.ResponseMode, MaxOutputTokens: settings.MaxOutputTokens, MaxTurns: settings.MaxTurns}
	profile.Reasoning = settings.Reasoning
	for _, definition := range agenttools.ReadOnlyDefinitions(brief.RoleGrounding.Permissions) {
		profile.AllowedReadTools = append(profile.AllowedReadTools, definition.Name)
	}
	slices.Sort(profile.AllowedReadTools)
	session, err := PrepareReadOnly(ctx, brief, kernel.Digest(retained.Start.Digest), Config{Bindings: binding, Gateway: agenttools.Gateway{Bindings: binding, Host: agenttools.Host{Timeout: 10 * time.Second}}, Journal: journal, HTTP: &http.Client{Timeout: 3 * time.Minute}, Profile: profile, EvidenceReader: reader})
	if err != nil {
		t.Fatal(err)
	}
	if session.Prompt != retained.Start.Prompt {
		t.Fatal("admitted task prompt changed")
	}
	session.Runner.SystemPrompt = retained.Start.System
	// Reuse only the recorded initial state, including its exact SMA recall.
	// Do not retrieve new context or replay any of the failed architect's work.
	startData, _ := json.Marshal(retained.Start)
	if _, err := journal.Append(ctx, sourceID, 0, agentruntime.Started, startData); err != nil {
		t.Fatal(err)
	}
	observer := &diagnosticModel{inner: session.Runner.Model, t: t, timingsPath: filepath.Join(outputDir, "request-timings.jsonl")}
	session.Runner.Model = observer
	startTime := time.Now()
	result, runErr := session.Run(ctx)
	receipt := map[string]any{"source_invocation": sourceID, "source_checkpoint_sha256": fmt.Sprintf("%x", checkpointHash), "workspace_baseline": workspace.Baseline, "started_at": startTime.UTC(), "elapsed_seconds": time.Since(startTime).Seconds(), "model_requests": observer.calls, "maximum_message_bytes": observer.maxBytes, "read_references_last_request": observer.references, "compaction": "NOT_RUN", "workflow_advancement": "NOT_RUN", "production_deployment": "NOT_RUN", "result": "COMPLETED"}
	toolCounts := map[string]int{}
	var toolErrors, schemaRejections int
	for _, entry := range journal.entries {
		switch entry.Kind {
		case agentruntime.ToolDone:
			var tool agentruntime.ToolResult
			if json.Unmarshal(entry.Payload, &tool) == nil {
				toolCounts[tool.Name]++
				if tool.Error != "" {
					toolErrors++
				}
			}
		case agentruntime.ModelRejected:
			schemaRejections++
		}
	}
	receipt["executed_read_tools"], receipt["tool_errors"], receipt["schema_rejections"] = toolCounts, toolErrors, schemaRejections
	if runErr != nil {
		receipt["result"] = "FAILED"
		receipt["error"] = runErr.Error()
	}
	data, _ := json.MarshalIndent(receipt, "", "  ")
	if err := os.WriteFile(filepath.Join(outputDir, "receipt.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	if result != "" {
		if err := os.WriteFile(filepath.Join(outputDir, "architecture-result.txt"), []byte(result), 0600); err != nil {
			t.Fatal(err)
		}
	}
	t.Logf("diagnostic %s", data)
	if runErr != nil {
		t.Fatal(runErr)
	}
}

type diagnosticJournal struct {
	testJournal
	path string
}

func (journal *diagnosticJournal) Append(ctx context.Context, id string, expected uint64, kind agentruntime.Kind, payload json.RawMessage) (agentruntime.Entry, error) {
	entry, err := journal.testJournal.Append(ctx, id, expected, kind, payload)
	if err != nil {
		return entry, err
	}
	file, err := os.OpenFile(journal.path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0600)
	if err != nil {
		return entry, err
	}
	defer file.Close()
	if err := json.NewEncoder(file).Encode(entry); err != nil {
		return entry, err
	}
	return entry, file.Sync()
}

type diagnosticModel struct {
	inner                       agentruntime.Model
	t                           *testing.T
	timingsPath                 string
	calls, maxBytes, references int
}

func (model *diagnosticModel) Complete(ctx context.Context, messages []agentruntime.Message) (agentruntime.Completion, error) {
	model.calls++
	size, refs := 0, 0
	for _, message := range messages {
		size += len(message.Content)
		if strings.Contains(message.Content, `"content_delivery":"IN_CONTEXT_REFERENCE"`) {
			refs++
		}
	}
	model.maxBytes = max(model.maxBytes, size)
	model.references = refs
	start := time.Now()
	result, err := model.inner.Complete(ctx, messages)
	names := make([]string, 0, len(result.ToolCalls))
	for _, call := range result.ToolCalls {
		names = append(names, call.Name)
	}
	elapsed := time.Since(start)
	model.t.Logf("turn=%d input_bytes=%d references=%d elapsed=%s tools=%v error=%v", model.calls, size, refs, elapsed.Round(time.Millisecond), names, err)
	receipt := map[string]any{"turn": model.calls, "started_at": start.UTC(), "elapsed_seconds": elapsed.Seconds(), "input_bytes": size, "references": refs, "tools": names}
	if err != nil {
		receipt["error"] = err.Error()
	}
	if len(result.ToolCalls) > 0 {
		receipt["first_call_id"] = result.ToolCalls[0].ID
	}
	file, writeErr := os.OpenFile(model.timingsPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if writeErr != nil {
		return agentruntime.Completion{}, writeErr
	}
	writeErr = json.NewEncoder(file).Encode(receipt)
	closeErr := file.Close()
	if writeErr != nil {
		return agentruntime.Completion{}, writeErr
	}
	if closeErr != nil {
		return agentruntime.Completion{}, closeErr
	}
	return result, err
}

func (model *diagnosticModel) Continue(ctx context.Context, partial agentruntime.PartialTurnError) (agentruntime.Completion, error) {
	continuing, ok := model.inner.(agentruntime.ContinuingModel)
	if !ok {
		return agentruntime.Completion{}, fmt.Errorf("diagnostic model has no continuation")
	}
	return continuing.Continue(ctx, partial)
}
