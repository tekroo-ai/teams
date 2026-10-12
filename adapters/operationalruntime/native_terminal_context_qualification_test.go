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
	"github.com/tekroo-ai/teams/application"
	"github.com/tekroo-ai/teams/kernel"
	"github.com/tekroo-ai/teams/organization"
)

// No historical run is resumed. The seeded parent is explicitly synthetic;
// the real child uses the current signed profile and authenticated return path.
func TestQualifyNativeCompletedSubmissionHandback(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 80*time.Minute)
	defer cancel()
	config, team, profiles, output := terminalQualificationEnvironment(t, ctx)
	for _, p := range profiles {
		t.Run(string(p.RoleFQRN), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Minute)
			defer cancel()
			root := terminalQualificationWorkspace(t, output, string(p.RoleFQRN))
			j := &terminalQualificationJournal{streams: map[string][]agentruntime.Entry{}, directory: output}
			client := &http.Client{Timeout: 10 * time.Minute, Transport: &nativeHTTPCapture{directory: output, scenario: string(p.RoleFQRN)}}
			marker := string(digestBytes([]byte(root + " original completed source marker")))
			decision := "retained-original-" + string(p.RoleFQRN)
			schema := json.RawMessage(`{"type":"object","additionalProperties":false,"required":["decision","source_marker"],"properties":{"decision":{"type":"string"},"source_marker":{"type":"string","minLength":64,"maxLength":64},"padding":{"type":"string"}}}`)
			id := deterministicOperationalUUID("terminal-qualified-parent", root)
			b := terminalQualificationBrief(t, team, p, id, root, "Explicit synthetic completed-parent fixture, not feature execution. The fixture model supplies the original decision.")
			cfg := terminalQualificationConfig(team, p, root, j, client, config.Native.ContextDelivery)
			cfg.ResultContract = terminalQualificationContract(b, schema)
			parent := terminalQualificationSession(t, ctx, b, cfg)
			args, _ := json.Marshal(map[string]any{"decision": decision, "source_marker": marker, "padding": strings.Repeat("retained completed fixture ", 800)})
			parent.Runner.Model = &terminalQualificationModel{calls: []agentruntime.ToolCall{{ID: "completed-source", Name: "submit_result", Arguments: args}}}
			accepted, err := parent.Run(ctx)
			if err != nil {
				t.Fatal(err)
			}
			before, _ := json.Marshal(j.streams[parent.InvocationID])
			childID := deterministicOperationalUUID("terminal-qualified-child", root)
			child := terminalQualificationBrief(t, team, p, childID, root, "Synthetic completed-parent handback, not feature work. Resume from the original decision context and the retained pointer-error receipt. Read BOTH /historical_result/decision and /historical_result/source_marker from the original frozen Finished reference using read_recorded_result; /output is invalid for this historical completion. Return their exact original values in decision and source_marker. The marker is not in this assignment or schema. Do not echo the optional padding, rerun tools, edit files, invent a new decision or treat historical accepted as new feature acceptance.")
			childCfg := terminalQualificationConfig(team, p, root, j, client, config.Native.ContextDelivery)
			childCfg.ResultContract = terminalQualificationContract(child, schema)
			childCfg.ValidateFinalResult = func(_ context.Context, result []byte) error {
				var value struct {
					Decision string
					Marker   string `json:"source_marker"`
				}
				if json.Unmarshal(bytes.TrimPrefix(result, []byte(application.OrganizationalResultMarker+"\n")), &value) != nil || value.Decision != decision || value.Marker != marker {
					return errors.New("original decision/marker not restored exactly")
				}
				return nil
			}
			invocation := terminalQualificationInvocation(t, b, parent, accepted)
			correction := kernel.EvidenceRef{EvidenceID: deterministicOperationalUUID("terminal-qualification-correction", root), SHA256: digestBytes([]byte("synthetic admitted correction"))}
			terminalQualificationReturn(t, ctx, j, &child, invocation, correction, &childCfg)
			session := terminalQualificationSession(t, ctx, child, childCfg)
			observation := terminalQualificationObservation(t, session.Runner.ContextFork)
			if len(observation.Content) <= config.Native.ContextDelivery.ResultBytes {
				t.Fatal("missed large terminal boundary")
			}
			// Seed the invalid pointer through the production reader. This is one
			// deliberate error, not an automatic invocation retry or model failure.
			seed := session.Runner
			seed.Model = &terminalQualificationModel{calls: []agentruntime.ToolCall{terminalQualificationRead(t, observation.Receipt, "/output", "invalid-original-pointer")}}
			seed.MaxTurns = 1
			if _, err := seed.Run(ctx, session.InvocationID, session.RequestDigest, session.Prompt); !errors.Is(err, agentruntime.ErrTurnLimit) {
				t.Fatal("invalid pointer seed", err)
			}
			seedCount := len(j.streams[session.InvocationID])
			prefix, _ := json.Marshal(j.streams[session.InvocationID])
			if _, err := session.Run(ctx); err != nil {
				t.Fatal("live handback", err)
			}
			assertTerminalQualificationReads(t, j.streams[session.InvocationID], observation.Receipt, []string{"/historical_result/decision", "/historical_result/source_marker"}, 1)
			after, _ := json.Marshal(j.streams[parent.InvocationID])
			retained, _ := json.Marshal(j.streams[session.InvocationID][:seedCount])
			if !bytes.Equal(before, after) || !bytes.Equal(prefix, retained) {
				t.Fatal("parent or correction history changed")
			}
			if session.Runner.MaxTurns != p.MaxTurns || session.Runner.MaxFinalSubmissions != 3 {
				t.Fatal("finite model/submission budget changed")
			}
			session.Runner.Model = &terminalQualificationModel{}
			if _, err := session.Run(ctx); err != nil {
				t.Fatal("restart", err)
			}
			terminalQualificationWrite(t, output, string(p.RoleFQRN)+"-result.json", map[string]any{"scope": "synthetic completed-parent / live same-profile child; not feature completion", "profile": p, "parent_id": parent.InvocationID, "child_id": session.InvocationID, "terminal_bytes": len(observation.Content), "terminal_reference": observation.Receipt, "parent_unchanged": true, "pointer_errors": 1, "invocation_retries": 0, "named_terminal_reads_verified": true, "finished_at": time.Now().UTC()})
		})
	}
}

// Real current signed PM and architect handlers exercise the completed-parent
// needs_decision return. Parent input is synthetic; no team dispatch/DB writes.
func TestQualifyNativeArchitectPMCompletedDecisionReturn(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Minute)
	defer cancel()
	config, team, profiles, output := terminalQualificationEnvironment(t, ctx)
	profile := func(role kernel.RoleFQRN) nativeagent.Profile {
		i := slices.IndexFunc(profiles, func(p nativeagent.Profile) bool { return p.RoleFQRN == role })
		if i < 0 {
			t.Fatal("qualified role missing", role)
		}
		return profiles[i]
	}
	root := terminalQualificationWorkspace(t, output, "typed-route")
	j := &terminalQualificationJournal{streams: map[string][]agentruntime.Entry{}, directory: output}
	client := &http.Client{Timeout: 10 * time.Minute, Transport: &nativeHTTPCapture{directory: output, scenario: "architect-pm-return"}}
	runTerminalArchitectPMQualification(t, ctx, team, profile("architect"), profile("project-manager"), config.Native.ContextDelivery, root, j, client, true)
	terminalQualificationWrite(t, output, "result.json", map[string]any{"scope": "synthetic accepted parent; real signed PM needs_decision, workflow revision selection and real signed architect retained-context return; not feature completion", "profiles": profiles, "parent_unchanged": true, "invocation_retries": 0, "finished_at": time.Now().UTC()})
}

func terminalQualificationContract(b application.ExecutionBrief, schema json.RawMessage) *nativeagent.ResultContract {
	return &nativeagent.ResultContract{TaskID: b.Task.TaskID, TaskSourceDigest: b.Task.SourceDigest, SchemaRef: "tekroo.qualification.completed-handback/1.0.0", SchemaSHA256: digestBytes(schema), Schema: schema}
}

func terminalQualificationEnvironment(t testing.TB, ctx context.Context) (ProductionConfig, organization.LoadedTeam, []nativeagent.Profile, string) {
	t.Helper()
	path, output := os.Getenv("TEKROO_NATIVE_TERMINAL_CONFIG"), os.Getenv("TEKROO_NATIVE_TERMINAL_OUTPUT")
	if path == "" || output == "" {
		t.Skip("explicit read-only config and new qualification directory required; not qualification PASS")
	}
	if !filepath.IsAbs(path) || !filepath.IsAbs(output) {
		t.Fatal("absolute qualification paths required")
	}
	if raw, err := exec.CommandContext(ctx, "pgrep", "-x", "tekrood").Output(); err == nil || len(raw) != 0 {
		t.Fatal("Teams must remain stopped")
	} else {
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() != 1 {
			t.Fatal("cannot verify stopped Teams", err)
		}
	}
	cfg, err := LoadProductionConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateOperationalProfileQualifications(cfg.Profiles, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if cfg.Native.ContextDelivery == nil || !cfg.Native.ContextDelivery.Valid() {
		t.Fatal("explicit existing context policy required")
	}
	keys := map[string]ed25519.PublicKey{}
	for _, p := range cfg.Organization.Publishers {
		raw, err := os.ReadFile(p.PublicKeyFile)
		if err != nil {
			t.Fatal(err)
		}
		key, err := base64.StdEncoding.Strict().DecodeString(strings.TrimSpace(string(raw)))
		if err != nil || len(key) != ed25519.PublicKeySize {
			t.Fatal("invalid trusted publisher")
		}
		keys[p.KeyID] = key
	}
	team, err := organization.LoadTeamManifest(cfg.Organization.ManifestFile, cfg.Organization.ManifestDigest, keys)
	if err != nil {
		t.Fatal(err)
	}
	var profiles []nativeagent.Profile
	for _, p := range cfg.Profiles {
		if p.Qualification == nil {
			continue
		}
		i := slices.IndexFunc(team.Roles, func(r organization.LoadedRole) bool { return r.Binding.Role == string(p.RoleFQRN) })
		if i < 0 || p.NativeSettings == nil || p.RoleBundleDigest != team.Roles[i].Binding.BundleDigest || p.ModelProfileDigest != team.Roles[i].Binding.ModelProfileDigest {
			t.Fatal("signed identity mismatch", p.RoleFQRN)
		}
		s := p.NativeSettings
		profiles = append(profiles, nativeagent.Profile{RoleFQRN: p.RoleFQRN, RoleBundleDigest: p.RoleBundleDigest, ModelProfileDigest: p.ModelProfileDigest, RuntimeIdentityDigest: p.RuntimeIdentityDigest, ToolPolicyDigest: p.ToolPolicyDigest, EffectPolicyDigest: p.EffectPolicyDigest, BaseURL: s.BaseURL, Model: s.Model, ModelInstructions: s.ModelInstructions, ResponseMode: s.ResponseMode, MaxOutputTokens: s.MaxOutputTokens, MaxTurns: s.MaxTurns, Reasoning: s.Reasoning})
		for _, d := range agenttools.ReadOnlyDefinitions(team.Roles[i].Bundle.Permissions) {
			profiles[len(profiles)-1].AllowedReadTools = append(profiles[len(profiles)-1].AllowedReadTools, d.Name)
		}
		slices.Sort(profiles[len(profiles)-1].AllowedReadTools)
	}
	if len(profiles) == 0 {
		t.Fatal("no qualified profiles")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSuffix(profiles[0].BaseURL, "/v1")+"/metrics", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := (&http.Client{Timeout: 20 * time.Second}).Do(request)
	if err != nil {
		t.Fatal(err)
	}
	metrics, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	response.Body.Close()
	if err != nil || response.StatusCode != http.StatusOK || !strings.Contains(string(metrics), "vllm:num_requests_running 0\n") || !strings.Contains(string(metrics), "vllm:num_requests_waiting 0\n") {
		t.Fatal("model must be idle before qualification")
	}
	if err := os.Mkdir(output, 0700); err != nil {
		t.Fatal("single-use output directory required", err)
	}
	terminalQualificationWrite(t, output, "baseline-config.json", cfg)
	terminalQualificationWrite(t, output, "profiles.json", profiles)
	return cfg, team, profiles, output
}

func terminalQualificationWorkspace(t testing.TB, output, name string) string {
	t.Helper()
	root := filepath.Join(output, name+"-fixture")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	for name, text := range map[string]string{"AGENTS.md": "Synthetic read-only handback qualification. The retained context and recorded-result reader are the complete evidence. No edits or test execution authorized.\n", "go.mod": "module example.test/terminalfixture\n\ngo 1.26.0\n", "provider.go": "package fixture\n\ntype Payload struct{}\n", "consumer.go": "package fixture\n\nfunc Consume(p Payload) string { return \"\" }\n", "observer.go": "package fixture\n\nfunc Observe(p Payload) string { return \"\" }\n"} {
		if err := captureWrite(filepath.Join(root, name), []byte(text)); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func terminalQualificationWrite(t testing.TB, output, name string, value any) {
	t.Helper()
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := captureWrite(filepath.Join(output, name), append(raw, '\n')); err != nil {
		t.Fatal(err)
	}
}
