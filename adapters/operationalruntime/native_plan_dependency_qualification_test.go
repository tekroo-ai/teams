//go:build mongo_integration && native_qualification

package operationalruntime

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/tekroo-ai/teams/adapters/nativeagent"
	"github.com/tekroo-ai/teams/agentruntime"
	"github.com/tekroo-ai/teams/agenttools"
	"github.com/tekroo-ai/teams/kernel"
	"github.com/tekroo-ai/teams/organization"
)

// Scoped, opt-in live qualification with exact signed architect/PM profiles.
// Uses no production service, DB writes, run recovery or qualification rebinding.
// Two deliberate invalid drafts consume the existing three-submission budget;
// the real PM must repair the graph in the same durable invocation.
func TestQualifyNativeArchitectToPMDependencyRecovery(t *testing.T) {
	configPath, output := os.Getenv("TEKROO_NATIVE_DEPENDENCY_CONFIG"), os.Getenv("TEKROO_NATIVE_DEPENDENCY_OUTPUT")
	if configPath == "" || output == "" {
		t.Skip("explicit read-only configuration and new qualification output required")
	}
	if !filepath.IsAbs(configPath) || !filepath.IsAbs(output) {
		t.Fatal("absolute qualification paths required")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Minute)
	defer cancel()
	// A stopped supervisor is allowed; a loaded/running team is not. Do not
	// start that service merely to query it during this isolated capability test.
	if raw, err := exec.CommandContext(ctx, "pgrep", "-x", "tekrood").Output(); err == nil || len(raw) != 0 {
		t.Fatal("Teams must remain stopped")
	} else {
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() != 1 {
			t.Fatal("cannot verify stopped Teams", err)
		}
	}
	config, err := LoadProductionConfig(configPath)
	if err != nil {
		t.Fatal(err)
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
	profile := func(name kernel.RoleFQRN) nativeagent.Profile {
		i := slices.IndexFunc(config.Profiles, func(p ProductionProfile) bool { return p.RoleFQRN == name })
		if i < 0 {
			t.Fatal("missing profile", name)
		}
		p := config.Profiles[i]
		r := slices.IndexFunc(team.Roles, func(r organization.LoadedRole) bool { return r.Binding.Role == string(name) })
		if r < 0 || p.NativeSettings == nil || p.Qualification == nil || p.RoleBundleDigest != team.Roles[r].Binding.BundleDigest || p.ModelProfileDigest != team.Roles[r].Binding.ModelProfileDigest {
			t.Fatal("qualified signed identity mismatch", name)
		}
		s := p.NativeSettings
		v := nativeagent.Profile{RoleFQRN: p.RoleFQRN, RoleBundleDigest: p.RoleBundleDigest, ModelProfileDigest: p.ModelProfileDigest, RuntimeIdentityDigest: p.RuntimeIdentityDigest, ToolPolicyDigest: p.ToolPolicyDigest, EffectPolicyDigest: p.EffectPolicyDigest, BaseURL: s.BaseURL, Model: s.Model, ModelInstructions: s.ModelInstructions, ResponseMode: s.ResponseMode, MaxOutputTokens: s.MaxOutputTokens, MaxTurns: s.MaxTurns, Reasoning: s.Reasoning}
		for _, d := range agenttools.ReadOnlyDefinitions(team.Roles[r].Bundle.Permissions) {
			v.AllowedReadTools = append(v.AllowedReadTools, d.Name)
		}
		slices.Sort(v.AllowedReadTools)
		return v
	}
	architectProfile, pmProfile := profile("architect"), profile("project-manager")
	client := &http.Client{Timeout: 20 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSuffix(pmProfile.BaseURL, "/v1")+"/metrics", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	metrics, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	response.Body.Close()
	if err != nil || response.StatusCode != http.StatusOK || !strings.Contains(string(metrics), "vllm:num_requests_running 0\n") || !strings.Contains(string(metrics), "vllm:num_requests_waiting 0\n") {
		t.Fatal("model must be idle")
	}
	if err := os.Mkdir(output, 0700); err != nil {
		t.Fatal("single-use output directory required", err)
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
	write("profiles.json", []nativeagent.Profile{architectProfile, pmProfile})
	root := filepath.Join(output, "fixture")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	// The admitted proposal names existing files. Supply those actual sources;
	// legitimate read/format tools must not fail on an invented fixture path.
	for name, text := range map[string]string{
		"AGENTS.md":   "Read-only dependency boundary qualification. The supplied plan is the complete input. Do not edit or run implementation tests.\n",
		"go.mod":      "module example.test/dependencyfixture\n\ngo 1.26.0\n",
		"provider.go": "package fixture\n\ntype Payload struct{}\n",
		"consumer.go": "package fixture\n\nfunc Consume(p Payload) string { return \"\" }\n",
		"observer.go": "package fixture\n\nfunc Observe(p Payload) string { return \"\" }\n",
	} {
		if err := captureWrite(filepath.Join(root, name), []byte(text)); err != nil {
			t.Fatal(err)
		}
	}
	source, deltas, feature := dependencyRecoveryFixture(t)
	write("synthetic-source.json", string(source))
	write("synthetic-invalid-drafts.json", deltas[:2])
	architectJournal := &nativeReleaseJournal{path: filepath.Join(output, "architect-journal.json")}
	architectHTTP := &http.Client{Timeout: 10 * time.Minute, Transport: &nativeHTTPCapture{directory: output, scenario: "architect"}}
	architect := dependencyRecoverySession(t, ctx, team, architectProfile, root, architectJournal, architectHTTP, config.Native.ContextDelivery, source, feature)
	started := time.Now().UTC()
	accepted, err := architect.Run(ctx)
	if err != nil {
		t.Fatal("architect fixture failed", err)
	}
	for _, entry := range architectJournal.entries {
		if entry.Kind == agentruntime.ModelRejected || entry.Kind == agentruntime.ModelPartial || entry.Kind == agentruntime.Failed {
			t.Fatal("unexpected architect correction/continuation/failure", string(entry.Payload))
		}
		if entry.Kind == agentruntime.ToolDone {
			var done agentruntime.ToolResult
			if json.Unmarshal(entry.Payload, &done) != nil || done.Error != "" {
				t.Fatal("unexpected architect tool error", string(entry.Payload))
			}
		}
	}
	write("accepted-architect-output.json", accepted)
	// This qualification must actually reach the sparse-source boundary, not
	// silently become an explicit-empty-array scenario on a model variation.
	var envelope struct {
		Work struct {
			Tasks []map[string]json.RawMessage `json:"tasks"`
		} `json:"work_product"`
	}
	if json.Unmarshal([]byte(strings.TrimPrefix(accepted, "TEKROO_ORGANIZATIONAL_RESULT:\n")), &envelope) != nil || len(envelope.Work.Tasks) != 3 {
		t.Fatal("architect did not return the three-task fixture")
	}
	for _, i := range []int{0, 1} {
		if _, present := envelope.Work.Tasks[i]["depends_on"]; present {
			t.Fatal("architect did not exercise omitted source dependencies", i)
		}
	}
	proposed, err := parseArchitectureStageResult(source, nil)
	if err != nil {
		t.Fatal(err)
	}
	actual, err := parseArchitectureStageResult([]byte(accepted), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateFinalizedDesign(proposed, actual); err != nil {
		t.Fatal("architect changed supplied technical contract", err)
	}
	feature.Design.OutputDigest = digestBytes([]byte(accepted))
	pmJournal := &nativeReleaseJournal{path: filepath.Join(output, "project-manager-journal.json")}
	pmHTTP := &http.Client{Timeout: 10 * time.Minute, Transport: &nativeHTTPCapture{directory: output, scenario: "project-manager"}}
	pm := dependencyRecoverySession(t, ctx, team, pmProfile, root, pmJournal, pmHTTP, config.Native.ContextDelivery, []byte(accepted), feature)
	seed := pm.Runner
	seed.Model = &dependencyRecoveryModel{submissions: deltas[:2]}
	seed.MaxTurns = 2
	if _, err := seed.Run(ctx, pm.InvocationID, pm.RequestDigest, pm.Prompt); !errors.Is(err, agentruntime.ErrTurnLimit) {
		t.Fatal("deliberate bounded failure sequence did not remain recoverable", err)
	}
	seedCount := len(pmJournal.entries)
	before, _ := json.Marshal(pmJournal.entries)
	result, err := pm.Run(ctx)
	if err != nil {
		t.Fatal("PM did not correct within original finite invocation", err)
	}
	after, _ := json.Marshal(pmJournal.entries[:seedCount])
	if !bytes.Equal(before, after) {
		t.Fatal("original correction journal changed")
	}
	assertDependencyRecoveryJournal(t, pmJournal.entries)
	if err := dependencyRecoveryValidator(feature, []byte(accepted))(ctx, []byte(result)); err != nil {
		t.Fatal(err)
	}
	final, err := parsePlanFinalizationStageResult([]byte(result), nil)
	if err != nil || final.Tasks[0].DependsOn == nil || len(final.Tasks[0].DependsOn) != 0 || !slices.Equal(final.Tasks[1].DependsOn, []uint32{0}) || !slices.Equal(final.Tasks[2].DependsOn, []uint32{0}) {
		t.Fatal("missing empty confirmation, added edge or retained edge", err)
	}
	if pm.Runner.MaxFinalSubmissions != 3 || pm.Runner.MaxTurns != pmProfile.MaxTurns {
		t.Fatal("recovery budget/profile changed")
	}
	write("qualification.json", map[string]any{"status": "PASS", "scope": "synthetic architect-to-PM dependency failure/correction, not feature completion or full role qualification", "started_at": started, "finished_at": time.Now().UTC(), "architect_invocation": architect.InvocationID, "pm_invocation": pm.InvocationID, "accepted_source_digest": feature.Design.OutputDigest, "source_omitted_dependencies": []int{0, 1}, "seeded_invalid_submissions": 2, "total_pm_submissions": 3, "pm_invocations": 1, "invocation_retries": 0, "max_final_submissions": pm.Runner.MaxFinalSubmissions, "max_turns": pm.Runner.MaxTurns, "seed_journal_preserved": true, "original_edge_preserved": true, "technical_content_preserved": true, "result": result})
}
