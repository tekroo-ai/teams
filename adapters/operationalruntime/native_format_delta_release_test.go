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

// This capability delta does not replace role qualification. It retains prior
// signed roles, model settings, thresholds and evidence, adding one real-model
// receipt per qualified role for the changed read-only tool definition.
func TestPrepareNativeFormatDeltaRelease(t *testing.T) {
	source, output := os.Getenv("TEKROO_NATIVE_FORMAT_CONFIG"), os.Getenv("TEKROO_NATIVE_FORMAT_OUTPUT")
	if source == "" || output == "" {
		t.Skip("explicit qualification paths required")
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
		// Prove the delta is exactly the format definition, not arbitrary drift.
		if priorFormatSurface(t, bundle.Permissions) != p.ToolPolicyDigest {
			t.Fatal("unexpected tool surface delta", p.RoleFQRN)
		}
		if p.Qualification == nil && p.QualificationCorpus == nil {
			p.ToolPolicyDigest = surface
			continue
		}
		if p.Qualification == nil || p.QualificationCorpus == nil || p.NativeSettings == nil {
			t.Fatal("missing prior evidence/settings")
		}
		root := t.TempDir()
		files := map[string]string{"clean.go": "package fixture\n\nconst Value = 1\n", "rough.go": "package fixture\nfunc Rough(){println(1)}\n"}
		for name, content := range files {
			writeText(t, filepath.Join(root, name), content, 0600)
		}
		definitions := []agentruntime.ToolDefinition{}
		for _, d := range agenttools.ReadOnlyDefinitions(bundle.Permissions) {
			if d.Name == "check_go_format" {
				definitions = append(definitions, d)
			}
		}
		definitions = append(definitions, agentruntime.ToolDefinition{Name: "submit_result", Description: "Finish after both requested format checks succeeded.", Parameters: json.RawMessage(`{"type":"object","additionalProperties":false,"required":["outcome"],"properties":{"outcome":{"type":"string","enum":["completed"]}}}`)})
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
		prompt := "This is a bounded read-only tool capability check, not feature work. Two existing regular Go files are supplied: clean.go and rough.go. First call check_go_format with paths [clean.go, rough.go]; it should report rough.go as unformatted and format_clean false. Then call check_go_format with paths [clean.go]; it should report format_clean true and no unformatted files. Do not change files or do unrelated work. Finish with submit_result outcome completed only after both actual checks succeeded."
		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Minute)
		started := time.Now().UTC()
		t.Logf("format delta role=%s invocation=%s", p.RoleFQRN, id)
		result, err := runner.Run(ctx, string(id), string(requestDigest), prompt)
		finished := time.Now().UTC()
		cancel()
		if err != nil {
			t.Fatal(err)
		}
		var completion map[string]string
		if json.Unmarshal([]byte(result), &completion) != nil || len(completion) != 1 || completion["outcome"] != "completed" {
			t.Fatal("wrong completion", result)
		}
		checks := 0
		for _, entry := range journal.entries {
			if entry.Kind == agentruntime.ModelRejected {
				t.Fatal("schema correction", string(entry.Payload))
			}
			if entry.Kind != agentruntime.ToolDone {
				continue
			}
			var done agentruntime.ToolResult
			if json.Unmarshal(entry.Payload, &done) != nil || done.Error != "" || done.Name != "check_go_format" {
				t.Fatal("tool error/unexpected tool", string(entry.Payload))
			}
			var got agenttools.Result
			if json.Unmarshal(done.Output, &got) != nil || got.FormatClean == nil {
				t.Fatal("missing format receipt")
			}
			if checks == 0 && (*got.FormatClean || !slices.Equal(got.Files, []string{"rough.go"})) || checks == 1 && (!*got.FormatClean || len(got.Files) != 0) || checks > 1 {
				t.Fatal("wrong format receipt", string(done.Output))
			}
			checks++
		}
		if checks != 2 || len(journal.entries) == 0 || journal.entries[len(journal.entries)-1].Kind != agentruntime.Finished {
			t.Fatal("incomplete checks")
		}
		for name, before := range files {
			got, err := os.ReadFile(filepath.Join(root, name))
			if err != nil || string(got) != before {
				t.Fatal("read-only check changed source")
			}
		}
		write(string(p.RoleFQRN)+"-measurement.json", map[string]any{"invocation_id": id, "model_profile_digest": p.ModelProfileDigest, "tool_surface_digest": surface, "started_at": started, "finished_at": finished, "result": json.RawMessage(result), "tool_counts": map[string]int{"check_go_format": checks}, "journal_file": journal.path, "files_unchanged": true})
		bindNamedToolDeltaQualification(t, p, surface, id, "native-tool-delta-check-go-format", ids)
	}
	write("tekrood.native-candidate.json", config)
	if _, err := LoadProductionConfig(filepath.Join(output, "tekrood.native-candidate.json")); err != nil {
		t.Fatal(err)
	}
	if err := ValidateOperationalProfileQualifications(config.Profiles, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
}

// Frozen pre-change definition: substituting only it must reproduce the prior
// digest exactly. Any concurrent catalog change refuses this scoped qualifier.
func priorFormatSurface(t *testing.T, permissions []string) kernel.Digest {
	t.Helper()
	tools := append(agenttools.ReadOnlyDefinitions(permissions), agenttools.MutationDefinitions(permissions)...)
	tools = append(tools, agenttools.TestDefinitions(permissions)...)
	for i := range tools {
		if tools[i].Name == "check_go_format" {
			tools[i].Description = "Check whether named workspace Go files are gofmt-clean without changing them; format_clean is true only when all named files match gofmt output."
			tools[i].Parameters = json.RawMessage(`{"type":"object","additionalProperties":false,"required":["paths"],"properties":{"paths":{"type":"array","minItems":1,"maxItems":32,"uniqueItems":true,"items":{"type":"string","minLength":1}}}}`)
		}
	}
	slices.SortFunc(tools, func(a, b agentruntime.ToolDefinition) int { return strings.Compare(a.Name, b.Name) })
	raw, err := json.Marshal(struct {
		SchemaVersion string                        `json:"schema_version"`
		Tools         []agentruntime.ToolDefinition `json:"tools"`
	}{"tekroo.teams.native-tool-surface/1.0.0", tools})
	if err != nil {
		t.Fatal(err)
	}
	return digestBytes(raw)
}

func TestPriorFormatSurfaceFrozen(t *testing.T) {
	// Archived from 7feef632acbab26d2f7c92db3bd9ccfe65669fb4 with only
	// check_go_format restored; later catalogue additions are not historical input.
	raw, err := os.ReadFile(filepath.Join("testdata", "native-format-prior-read-surface.json"))
	if err != nil {
		t.Fatal(err)
	}
	frozen := digestBytes([]byte(strings.TrimSpace(string(raw))))
	if got := frozen; got != "064c49fc944e1523ecec273c3c83f2152e756cb4f16e21287818a45dcad435b3" {
		t.Fatal("frozen pre-change surface mismatch", got)
	}
	current, err := agenttools.NativeToolSurfaceDigest([]string{"repository.read"})
	if err != nil || current == frozen {
		t.Fatal("format delta not bound", err)
	}
}
