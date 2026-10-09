//go:build mongo_integration && native_qualification

package operationalruntime

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/tekroo-ai/teams/agentruntime"
	"github.com/tekroo-ai/teams/agenttools"
	"github.com/tekroo-ai/teams/kernel"
	"github.com/tekroo-ai/teams/organization"
)

// Qualify exactly the read pagination / regex catalog delta against retained
// prior definitions. It neither edits feature work nor changes model settings.
func TestPrepareNativeReadRegexDeltaRelease(t *testing.T) {
	source, output, priorPath := os.Getenv("TEKROO_NATIVE_READ_REGEX_CONFIG"), os.Getenv("TEKROO_NATIVE_READ_REGEX_OUTPUT"), os.Getenv("TEKROO_NATIVE_READ_REGEX_PRIOR_TOOLS")
	if source == "" || output == "" || priorPath == "" {
		t.Skip("explicit source, output and prior catalog required")
	}
	var config ProductionConfig
	if err := readStrictJSONFile(source, &config); err != nil {
		t.Fatal(err)
	}
	if config.ExecutionBackend != "native" {
		t.Fatal("not native")
	}
	if err := ValidateOperationalProfileQualifications(config.Profiles, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	priorRaw, err := os.ReadFile(priorPath)
	if err != nil {
		t.Fatal(err)
	}
	var prior []agenttools.Spec
	if json.Unmarshal(priorRaw, &prior) != nil {
		t.Fatal("invalid prior catalog")
	}
	keys := map[string]ed25519.PublicKey{}
	for _, publisher := range config.Organization.Publishers {
		raw, err := os.ReadFile(publisher.PublicKeyFile)
		if err != nil {
			t.Fatal(err)
		}
		key, err := base64.StdEncoding.Strict().DecodeString(strings.TrimSpace(string(raw)))
		if err != nil || len(key) != ed25519.PublicKeySize {
			t.Fatal("invalid trusted publisher")
		}
		keys[publisher.KeyID] = key
	}
	team, err := organization.LoadTeamManifest(config.Organization.ManifestFile, config.Organization.ManifestDigest, keys)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(output, 0700); err != nil {
		t.Fatal(err)
	}
	write := func(name string, value any) {
		t.Helper()
		raw, err := json.MarshalIndent(value, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := captureWrite(filepath.Join(output, name), append(raw, '\n')); err != nil {
			t.Fatal(err)
		}
	}
	write("baseline-config.json", config)
	write("prior-tools.json", prior)
	ids, err := NewUUIDv7Source(SystemClock{})
	if err != nil {
		t.Fatal(err)
	}
	for i := range config.Profiles {
		p := &config.Profiles[i]
		var bundle organization.RoleBundle
		found := false
		for _, role := range team.Roles {
			if role.Binding.Role == string(p.RoleFQRN) {
				bundle, found = role.Bundle, true
			}
		}
		if !found {
			t.Fatal("profile has no signed role", p.RoleFQRN)
		}
		surface, err := agenttools.NativeToolSurfaceDigest(bundle.Permissions)
		if err != nil {
			t.Fatal(err)
		}
		if surface == p.ToolPolicyDigest {
			continue
		}
		tools := append(agenttools.ReadOnlyDefinitions(bundle.Permissions), agenttools.MutationDefinitions(bundle.Permissions)...)
		tools = append(tools, agenttools.TestDefinitions(bundle.Permissions)...)
		for j := range tools {
			if !slices.Contains([]string{"read_file", "read_file_from_commit", "search_regex"}, tools[j].Name) {
				continue
			}
			replaced := false
			for _, old := range prior {
				if old.Name == tools[j].Name {
					tools[j].Description, tools[j].Parameters, replaced = old.Description, old.InputSchema, true
				}
			}
			if !replaced {
				t.Fatal("missing prior definition", tools[j].Name)
			}
		}
		slices.SortFunc(tools, func(a, b agentruntime.ToolDefinition) int { return strings.Compare(a.Name, b.Name) })
		raw, err := json.Marshal(struct {
			SchemaVersion string                        `json:"schema_version"`
			Tools         []agentruntime.ToolDefinition `json:"tools"`
		}{"tekroo.teams.native-tool-surface/1.0.0", tools})
		if err != nil || digestBytes(raw) != p.ToolPolicyDigest {
			t.Fatal("unexpected catalog drift", p.RoleFQRN, err)
		}
		if p.Qualification == nil && p.QualificationCorpus == nil {
			p.ToolPolicyDigest = surface
			continue
		}
		if p.Qualification == nil || p.QualificationCorpus == nil || p.NativeSettings == nil {
			t.Fatal("missing prior qualification")
		}
		root := t.TempDir()
		files := map[string]string{"pages.txt": strings.Repeat("retained line\n", 850), "regex.txt": strings.Repeat("x", 1200) + "\nBEGIN\nEND\n"}
		for name, data := range files {
			writeText(t, filepath.Join(root, name), data, 0600)
		}
		for _, args := range [][]string{{"init", "-q"}, {"add", "."}, {"-c", "user.name=Read Regex Delta", "-c", "user.email=delta@example.test", "commit", "-qm", "baseline"}} {
			nativeQualificationGit(t, root, args...)
		}
		head := strings.TrimSpace(nativeQualificationGit(t, root, "rev-parse", "HEAD"))
		definitions := []agentruntime.ToolDefinition{}
		for _, d := range agenttools.ReadOnlyDefinitions(bundle.Permissions) {
			if slices.Contains([]string{"read_file", "read_file_from_commit", "search_regex"}, d.Name) {
				definitions = append(definitions, d)
			}
		}
		definitions = append(definitions, agentruntime.ToolDefinition{Name: "submit_result", Description: "Finish after all requested actual tool checks succeed.", Parameters: json.RawMessage(`{"type":"object","additionalProperties":false,"required":["outcome"],"properties":{"outcome":{"type":"string","enum":["completed"]}}}`)})
		contract, err := agentruntime.NewStructuredTurnContract(definitions, "submit_result")
		if err != nil {
			t.Fatal(err)
		}
		id, err := ids.Next()
		if err != nil {
			t.Fatal(err)
		}
		requestDigest := digestBytes([]byte(string(id) + ":" + string(surface)))
		journal := &nativeReleaseJournal{path: filepath.Join(output, string(p.RoleFQRN)+"-journal.json")}
		binding := nativeReleaseBinding{authority: agenttools.Authority{WorkspaceRoot: root, Permissions: bundle.Permissions, Purpose: kernel.PurposeValidation, EffectPolicyDigest: p.EffectPolicyDigest}}
		settings := p.NativeSettings
		runner := agentruntime.Runner{Journal: journal, Tools: agenttools.ReadOnlyTurnAdapter{Gateway: agenttools.Gateway{Bindings: binding, Host: agenttools.Host{Timeout: time.Minute}}, InvocationID: id, RequestDigest: requestDigest}, Model: agentruntime.OpenAIModel{Client: &http.Client{Timeout: 10 * time.Minute, Transport: &nativeHTTPCapture{directory: output, scenario: string(p.RoleFQRN)}}, BaseURL: settings.BaseURL, Model: settings.Model, MaxTokens: settings.MaxOutputTokens, Reasoning: settings.Reasoning, Tools: definitions, FinalTool: "submit_result", FinalSchema: definitions[len(definitions)-1].Parameters, TurnContract: contract}, FinalTool: "submit_result", Finalize: func(raw json.RawMessage) (string, error) { return string(raw), nil }, SystemPrompt: settings.ModelInstructions + "\n" + agentruntime.StructuredTurnInstruction, MaxTurns: settings.MaxTurns}
		prompt := "Bounded read-only capability check, not feature work. pages.txt has 850 lines. Read lines 401 through 850 with read_file, using successive pages within its per-call limit and the returned next_start_line. Then read the same lines from exact commit " + head + " using read_file_from_commit in successive pages. In regex.txt use search_regex with regex ^x{1200}$, result_mode count; expect one match. Then search regex BEGIN\\nEND with multiline true, result_mode lines; expect one match beginning on line 2 and ending on line 3. Do not change files or do unrelated work. Submit outcome completed only after all six actual tool calls succeeded."
		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Minute)
		started := time.Now().UTC()
		t.Logf("read/regex delta role=%s invocation=%s", p.RoleFQRN, id)
		result, err := runner.Run(ctx, string(id), string(requestDigest), prompt)
		finished := time.Now().UTC()
		cancel()
		if err != nil {
			t.Fatal(err)
		}
		counts := map[string]int{}
		for _, entry := range journal.entries {
			if entry.Kind == agentruntime.ModelRejected {
				t.Fatal("schema correction", string(entry.Payload))
			}
			if entry.Kind != agentruntime.ToolDone {
				continue
			}
			var done agentruntime.ToolResult
			if json.Unmarshal(entry.Payload, &done) != nil || done.Error != "" {
				t.Fatal("tool failure", string(entry.Payload))
			}
			var got agenttools.Result
			if json.Unmarshal(done.Output, &got) != nil {
				t.Fatal("invalid receipt")
			}
			switch done.Name {
			case "read_file", "read_file_from_commit":
				page := counts[done.Name]
				if got.StartLine == nil || got.EndLine == nil || page == 0 && (*got.StartLine != 401 || *got.EndLine != 800 || got.NextStartLine == nil || *got.NextStartLine != 801) || page == 1 && (*got.StartLine != 801 || *got.EndLine != 850 || got.NextStartLine != nil) || page > 1 {
					t.Fatal("wrong pagination receipt", string(done.Output))
				}
			case "search_regex":
				if counts[done.Name] == 0 && (got.MatchCount == nil || *got.MatchCount != 1) || counts[done.Name] == 1 && (len(got.Matches) != 1 || got.Matches[0].Line != 2 || got.Matches[0].EndLine != 3) || counts[done.Name] > 1 {
					t.Fatal("wrong regex receipt", string(done.Output))
				}
			default:
				t.Fatal("unexpected tool", done.Name)
			}
			counts[done.Name]++
		}
		var completion map[string]string
		if json.Unmarshal([]byte(result), &completion) != nil || len(completion) != 1 || completion["outcome"] != "completed" || len(journal.entries) == 0 || journal.entries[len(journal.entries)-1].Kind != agentruntime.Finished {
			t.Fatal("incomplete result")
		}
		for _, name := range []string{"read_file", "read_file_from_commit", "search_regex"} {
			if counts[name] != 2 {
				t.Fatal("missing checks", name, counts)
			}
		}
		for name, before := range files {
			got, err := os.ReadFile(filepath.Join(root, name))
			if err != nil || string(got) != before {
				t.Fatal("source changed")
			}
		}
		write(string(p.RoleFQRN)+"-measurement.json", map[string]any{"invocation_id": id, "model_profile_digest": p.ModelProfileDigest, "tool_surface_digest": surface, "started_at": started, "finished_at": finished, "result": json.RawMessage(result), "tool_counts": counts, "files_unchanged": true})
		bindNamedToolDeltaQualification(t, p, surface, id, "native-tool-delta-read-regex", ids)
	}
	write("tekrood.native-candidate.json", config)
	if err := ValidateOperationalProfileQualifications(config.Profiles, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadProductionConfig(filepath.Join(output, "tekrood.native-candidate.json")); err != nil {
		t.Fatal(err)
	}
}
