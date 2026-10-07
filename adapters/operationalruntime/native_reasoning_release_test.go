//go:build mongo_integration && native_qualification

package operationalruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tekroo-ai/teams/adapters/nativeagent"
	"github.com/tekroo-ai/teams/agentruntime"
	"github.com/tekroo-ai/teams/agenttools"
	"github.com/tekroo-ai/teams/application"
	"github.com/tekroo-ai/teams/kernel"
	"github.com/tekroo-ai/teams/organization"
)

// This is a reasoning-transport delta qualification, not another feature run
// or an assertion of improved role quality. Preserve the accepted role package,
// tool permissions, baseline qualification and exact HTTP receipts. Exercise
// every new profile through an ordinary tool turn and structured completion.
func TestPrepareNativeReasoningRelease(t *testing.T) {
	source, output := os.Getenv("TEKROO_NATIVE_REASONING_CONFIG"), os.Getenv("TEKROO_NATIVE_REASONING_OUTPUT")
	if source == "" || output == "" {
		t.Skip("explicit reasoning release paths required")
	}
	config, err := LoadProductionConfig(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateOperationalProfileQualifications(config.Profiles, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(output, 0700); err != nil && !(os.IsExist(err) && os.Getenv("TEKROO_NATIVE_REASONING_RESUME") == "1") {
		t.Fatal(err)
	}
	write := func(name string, value any) {
		t.Helper()
		raw, err := json.MarshalIndent(value, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(output, name), append(raw, '\n'), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("baseline-config.json", config)
	var manifest organization.TeamManifest
	if err := readStrictJSONFile(config.Organization.ManifestFile, &manifest); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(output, "roles"), 0700); err != nil && !(os.IsExist(err) && os.Getenv("TEKROO_NATIVE_REASONING_RESUME") == "1") {
		t.Fatal(err)
	}
	bundles := map[kernel.RoleFQRN]organization.RoleBundle{}
	for i := range manifest.Roles {
		role := &manifest.Roles[i]
		path := filepath.Join(filepath.Dir(config.Organization.ManifestFile), role.BundlePath)
		var bundle organization.RoleBundle
		if err := readStrictJSONFile(path, &bundle); err != nil {
			t.Fatal(err)
		}
		bundles[kernel.RoleFQRN(role.Role)] = bundle
		link := filepath.Join(output, "roles", role.Role)
		if err := os.Symlink(filepath.Dir(path), link); err != nil {
			target, readErr := os.Readlink(link)
			if os.Getenv("TEKROO_NATIVE_REASONING_RESUME") != "1" || readErr != nil || target != filepath.Dir(path) {
				t.Fatal(err)
			}
		}
		role.BundlePath = filepath.Join("roles", role.Role, "role.json")
	}
	ids, err := NewUUIDv7Source(SystemClock{})
	if err != nil {
		t.Fatal(err)
	}
	for i := range config.Profiles {
		p := &config.Profiles[i]
		old := *p
		settings := *p.NativeSettings
		budget := 8192
		if settings.MaxOutputTokens < 16384 {
			settings.MaxOutputTokens = 16384
		}
		settings.Reasoning = &agentruntime.ReasoningConfig{EnableThinking: true, Effort: "medium", BudgetTokens: &budget}
		digest, err := nativeagent.ModelProfileDigest(p.RoleFQRN, p.RoleBundleDigest, settings)
		if err != nil {
			t.Fatal(err)
		}
		p.NativeSettings, p.ModelProfileDigest = &settings, digest
		id, err := ids.Next()
		if err != nil {
			t.Fatal(err)
		}
		root := t.TempDir()
		if err := os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte("This disposable compatibility check has verification token REASONING_DELTA_READY. Read this file and report that exact token. Do not modify files.\n"), 0600); err != nil {
			t.Fatal(err)
		}
		bundle := bundles[p.RoleFQRN]
		definitions := agenttools.ReadOnlyDefinitions(bundle.Permissions)
		canRead := false
		for _, definition := range definitions {
			canRead = canRead || definition.Name == "read_file"
		}
		final := agentruntime.ToolDefinition{Name: "submit_result", Description: "Finish the isolated reasoning-transport check with the token actually read.", Parameters: json.RawMessage(`{"type":"object","additionalProperties":false,"required":["token"],"properties":{"token":{"type":"string","const":"REASONING_DELTA_READY"}}}`)}
		definitions = append(definitions, final)
		contract, err := agentruntime.NewStructuredTurnContract(definitions, final.Name)
		if err != nil {
			t.Fatal(err)
		}
		binding := nativeReleaseBinding{authority: agenttools.Authority{WorkspaceRoot: root, Permissions: bundle.Permissions, Purpose: kernel.PurposeValidation, EffectPolicyDigest: p.EffectPolicyDigest}}
		journal := &nativeReleaseJournal{path: filepath.Join(output, string(p.RoleFQRN)+"-journal.json")}
		transport := &reasoningReleaseTransport{path: filepath.Join(output, string(p.RoleFQRN)+"-http.jsonl"), budget: budget}
		runner := agentruntime.Runner{Journal: journal, Tools: agenttools.ReadOnlyTurnAdapter{Gateway: agenttools.Gateway{Bindings: binding, Host: agenttools.Host{Timeout: time.Minute}}, InvocationID: id, RequestDigest: digest}, Model: agentruntime.OpenAIModel{Client: &http.Client{Timeout: 5 * time.Minute, Transport: transport}, BaseURL: settings.BaseURL, Model: settings.Model, MaxTokens: settings.MaxOutputTokens, Reasoning: settings.Reasoning, Tools: definitions, FinalTool: final.Name, FinalSchema: final.Parameters, TurnContract: contract}, FinalTool: final.Name, Finalize: func(raw json.RawMessage) (string, error) { return string(raw), nil }, SystemPrompt: agentruntime.StructuredTurnInstruction, MaxTurns: 8}
		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Minute)
		t.Logf("checking ordinary reasoning role=%s invocation=%s", p.RoleFQRN, id)
		prompt := "Read AGENTS.md in this disposable workspace, then return its verification token using submit_result. This is a bounded compatibility check, not feature work. No other discovery or tests are needed."
		if !canRead {
			prompt = "This is a bounded structured-output compatibility check, not feature work. The verification token is REASONING_DELTA_READY. Return it using submit_result. This profile has no repository-read permission; do not attempt file access."
		}
		var result string
		var runErr error
		measurementPath := filepath.Join(output, string(p.RoleFQRN)+"-measurement.json")
		if raw, err := os.ReadFile(measurementPath); err == nil && os.Getenv("TEKROO_NATIVE_REASONING_RESUME") == "1" {
			var retained struct {
				ID       kernel.UUIDv7   `json:"invocation_id"`
				Digest   kernel.Digest   `json:"model_profile_digest"`
				Tokens   int             `json:"reasoning_tokens"`
				Requests int             `json:"requests"`
				Result   json.RawMessage `json:"result"`
			}
			if json.Unmarshal(raw, &retained) != nil || retained.Digest != digest || !retained.ID.Valid() || retained.Tokens <= 0 || retained.Requests < 1 {
				t.Fatal("invalid retained reasoning measurement")
			}
			journalRaw, err := os.ReadFile(journal.path)
			if err != nil || json.Unmarshal(journalRaw, &journal.entries) != nil {
				t.Fatal("invalid retained journal")
			}
			id, result, transport.requests, transport.reasoningTokens = retained.ID, string(retained.Result), retained.Requests, retained.Tokens
			t.Logf("retained completed role=%s invocation=%s", p.RoleFQRN, id)
		} else {
			result, runErr = runner.Run(ctx, string(id), string(digest), prompt)
		}
		cancel()
		if runErr != nil {
			t.Fatal(runErr)
		}
		reads := 0
		for _, entry := range journal.entries {
			if entry.Kind == agentruntime.ModelRejected {
				t.Fatalf("schema rejection: %s", entry.Payload)
			}
			if entry.Kind == agentruntime.ToolDone {
				var done agentruntime.ToolResult
				if json.Unmarshal(entry.Payload, &done) != nil || done.Error != "" {
					t.Fatalf("tool error: %s", entry.Payload)
				}
				if done.Name == "read_file" {
					reads++
				}
			}
		}
		expectedReads, minimumRequests := 1, 2
		if !canRead {
			expectedReads, minimumRequests = 0, 1
		}
		if reads < expectedReads || (!canRead && reads != 0) || transport.requests < minimumRequests || transport.reasoningTokens == 0 || journal.entries[len(journal.entries)-1].Kind != agentruntime.Finished {
			t.Fatalf("reads=%d requests=%d reasoning_tokens=%d", reads, transport.requests, transport.reasoningTokens)
		}
		write(string(p.RoleFQRN)+"-measurement.json", map[string]any{"invocation_id": id, "prior_model_profile_digest": old.ModelProfileDigest, "model_profile_digest": digest, "baseline_qualification": old.Qualification, "reasoning": settings.Reasoning, "reasoning_tokens": transport.reasoningTokens, "requests": transport.requests, "read_calls": reads, "result": json.RawMessage(result), "qualification_scope": "ordinary-turn reasoning transport delta; not end-to-end role behavior or speed"})
		if old.Qualification != nil {
			corpus := *old.QualificationCorpus
			corpus.CorpusID, err = ids.Next()
			if err != nil {
				t.Fatal(err)
			}
			corpus.Revision = 1
			corpus.ScenarioIDs = []string{"accepted-role-baseline-plus-bounded-medium-reasoning-transport-delta"}
			corpus.ThresholdDigest = digestBytes([]byte(string(old.Qualification.QualificationDigest) + ":unchanged role/tool policy; ordinary thinking verified; successful authorized read where available; structured final; no tool/schema errors"))
			corpusDigest, err := corpus.Digest()
			if err != nil {
				t.Fatal(err)
			}
			qual := old.Qualification.Clone()
			qual.QualificationID, err = ids.Next()
			if err != nil {
				t.Fatal(err)
			}
			qual.ModelProfileDigest, qual.QualificationCorpusDigest = digest, corpusDigest
			qual.EvidenceIDs = []kernel.UUIDv7{id}
			qual.ObservedAt, qual.ExpiresAt, qual.RevokedAt = time.Now().UTC(), nil, nil
			qual.QualificationDigest, err = application.QualificationDigest(qual)
			if err != nil {
				t.Fatal(err)
			}
			p.Qualification, p.QualificationCorpus = &qual, &corpus
		}
		for j := range manifest.Roles {
			if manifest.Roles[j].Role == string(p.RoleFQRN) {
				manifest.Roles[j].ModelProfileDigest = digest
			}
		}
	}
	write("team.json", manifest)
	config.Organization.ManifestFile = filepath.Join(output, "team.json")
	manifestRaw, err := os.ReadFile(config.Organization.ManifestFile)
	if err != nil {
		t.Fatal(err)
	}
	config.Organization.ManifestDigest = digestBytes(manifestRaw)
	write("tekrood.native-candidate.json", config)
	if _, err := LoadProductionConfig(filepath.Join(output, "tekrood.native-candidate.json")); err != nil {
		t.Fatal(err)
	}
	if err := ValidateOperationalProfileQualifications(config.Profiles, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
}

type reasoningReleaseTransport struct {
	path                      string
	requests, reasoningTokens int
	budget                    int
}

func (transport *reasoningReleaseTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	raw, err := io.ReadAll(request.Body)
	if err != nil {
		return nil, err
	}
	request.Body = io.NopCloser(bytes.NewReader(raw))
	var payload struct {
		Thinking bool   `json:"enable_thinking"`
		Effort   string `json:"reasoning_effort"`
		Budget   int    `json:"reasoning_budget_tokens"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, err
	}
	if !payload.Thinking || payload.Effort != "medium" || payload.Budget != transport.budget {
		return nil, agentruntime.ErrInvalidTurn
	}
	response, err := http.DefaultTransport.RoundTrip(request)
	if err != nil {
		return nil, err
	}
	data, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil {
		return nil, err
	}
	response.Body = io.NopCloser(bytes.NewReader(data))
	file, err := os.OpenFile(transport.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return nil, err
	}
	err = json.NewEncoder(file).Encode(map[string]any{"request": json.RawMessage(raw), "response": json.RawMessage(data), "status": response.StatusCode})
	closeErr := file.Close()
	if err != nil {
		return nil, err
	}
	if closeErr != nil {
		return nil, closeErr
	}
	var result struct {
		Usage struct {
			Details struct {
				Tokens int `json:"reasoning_tokens"`
			} `json:"completion_tokens_details"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, err
	}
	transport.requests++
	transport.reasoningTokens += result.Usage.Details.Tokens
	return response, nil
}
