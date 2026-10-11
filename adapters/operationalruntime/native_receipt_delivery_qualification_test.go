//go:build mongo_integration && native_qualification

package operationalruntime

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
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

// This qualifies only receipt delivery/access with the exact signed profiles.
// The large journal result is an explicit synthetic fixture, NOT a feature test
// execution. No service/run/role lock/qualification definition is modified.
func TestQualifyNativeReceiptDelivery(t *testing.T) {
	source, output := os.Getenv("TEKROO_NATIVE_RECEIPT_CONFIG"), os.Getenv("TEKROO_NATIVE_RECEIPT_OUTPUT")
	if source == "" || output == "" {
		t.Skip("explicit read-only config and new receipt directory required")
	}
	config, err := LoadProductionConfig(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateOperationalProfileQualifications(config.Profiles, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	keys := map[string]ed25519.PublicKey{}
	for _, p := range config.Organization.Publishers {
		raw, err := os.ReadFile(p.PublicKeyFile)
		if err != nil {
			t.Fatal(err)
		}
		key, err := base64.StdEncoding.Strict().DecodeString(strings.TrimSpace(string(raw)))
		if err != nil || len(key) != ed25519.PublicKeySize {
			t.Fatal("invalid trusted key")
		}
		keys[p.KeyID] = key
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
	tests := make([]agenttools.GoTestCase, 2500)
	for i := range tests {
		tests[i] = agenttools.GoTestCase{Package: "example.test/" + strings.Repeat("package/", 20), Name: fmt.Sprintf("TestFixture%d", i), Status: "PASS"}
	}
	tests[42].Name = "TestQualifiedPassing"
	tests[1739].Name, tests[1739].Status = "TestQualifiedSkipped", "SKIP"
	fixture, _ := json.Marshal(agenttools.Result{Name: "run_go_tests", Output: strings.Repeat("explicit synthetic fixture output\n", 4000), TestEvidence: &agenttools.GoTestEvidence{Complete: true, Tests: tests, Excluded: []agenttools.GoTestCase{{Package: "example.test/tagged", Name: "TestQualifiedExcluded", Status: "NOT_RUN"}}}})
	if len(fixture) < 424384 {
		t.Fatal("fixture too small")
	}
	write("fixture-result.json", json.RawMessage(fixture))
	policy := agentruntime.DefaultContextDeliveryPolicy()
	if config.Native.ContextDelivery != nil {
		policy = *config.Native.ContextDelivery
	}
	write("delivery-policy.json", policy)
	ids, err := NewUUIDv7Source(SystemClock{})
	if err != nil {
		t.Fatal(err)
	}
	checked := []string{}
	for _, p := range config.Profiles {
		if p.Qualification == nil {
			continue
		}
		i := slices.IndexFunc(team.Roles, func(r organization.LoadedRole) bool { return r.Binding.Role == string(p.RoleFQRN) })
		if i < 0 {
			t.Fatal("missing signed role")
		}
		role := team.Roles[i]
		if p.NativeSettings == nil || p.RoleBundleDigest != role.Binding.BundleDigest || p.ModelProfileDigest != role.Binding.ModelProfileDigest {
			t.Fatal("profile differs from signed binding")
		}
		id, err := ids.Next()
		if err != nil {
			t.Fatal(err)
		}
		actor := kernel.ActorFQN("teams::" + string(p.RoleFQRN) + "-1")
		schema := json.RawMessage(`{"type":"object","additionalProperties":false,"required":["passing","skipped","excluded"],"properties":{"passing":{"type":"string","enum":["PASS","SKIP","FAIL","NOT_RUN"]},"skipped":{"type":"string","enum":["PASS","SKIP","FAIL","NOT_RUN"]},"excluded":{"type":"string","enum":["PASS","SKIP","FAIL","NOT_RUN"]}}}`)
		brief := application.ExecutionBrief{ContractManifest: kernel.ContractIdentity, InvocationID: id, ActorFQN: actor, Purpose: kernel.PurposeHandoff,
			ModelProfileDigest: p.ModelProfileDigest, RuntimeIdentityDigest: p.RuntimeIdentityDigest, ToolPolicyDigest: p.ToolPolicyDigest, EffectPolicyDigest: p.EffectPolicyDigest,
			RoleGrounding:  application.RoleExecutionGrounding{ActorFQN: actor, RoleFQRN: p.RoleFQRN, BundleVersion: role.Bundle.Version, BundleDigest: p.RoleBundleDigest, Capabilities: role.Bundle.Capabilities, Permissions: role.Bundle.Permissions, Instructions: role.Package.Charter},
			Task:           application.TaskExecutionSpecification{TaskID: id, SourceDigest: digestBytes([]byte("receipt-delivery-qualification")), Title: "Bounded receipt delivery qualification", Description: "This is a synthetic durable-result fixture, not feature work or evidence that a suite physically ran. Continue from its receipt. Use read_recorded_result to look up TestQualifiedPassing and TestQualifiedSkipped under /test_evidence/tests, and TestQualifiedExcluded under /test_evidence/excluded. Use match_field=name and exact match_value. Read all three exact records before submitting their actual statuses as passing, skipped, excluded. Do not rerun tests, inspect or change repository files, or treat SKIP/NOT_RUN as PASS.", AcceptanceCriteria: []string{"Exact three named-record reads and correct PASS/SKIP/NOT_RUN distinction; full fixture retained."}},
			ResultProtocol: &application.ExecutionResultProtocol{SchemaVersion: "1.0.0", Marker: application.OrganizationalResultMarker, Instruction: "Submit the three observed statuses through submit_result."}}
		contract := nativeagent.ResultContract{TaskID: id, TaskSourceDigest: brief.Task.SourceDigest, SchemaRef: "tekroo.qualification.receipt-delivery/1.0.0", SchemaSHA256: digestBytes(schema), Schema: schema}
		settings := p.NativeSettings
		binding := nativeReleaseBinding{authority: agenttools.Authority{WorkspaceRoot: t.TempDir(), Permissions: role.Bundle.Permissions, Purpose: brief.Purpose, EffectPolicyDigest: p.EffectPolicyDigest}}
		reads := []string{}
		for _, d := range agenttools.ReadOnlyDefinitions(role.Bundle.Permissions) {
			reads = append(reads, d.Name)
		}
		slices.Sort(reads)
		journal := &nativeReleaseJournal{path: filepath.Join(output, string(p.RoleFQRN)+"-journal.json")}
		cfg := nativeagent.Config{ContextDelivery: &policy, Bindings: binding, Gateway: agenttools.Gateway{Bindings: binding, Host: agenttools.Host{Timeout: time.Minute}}, Journal: journal, ResultContract: &contract,
			HTTP:    &http.Client{Timeout: 10 * time.Minute, Transport: &nativeHTTPCapture{directory: output, scenario: string(p.RoleFQRN)}},
			Profile: nativeagent.Profile{RoleFQRN: p.RoleFQRN, RoleBundleDigest: p.RoleBundleDigest, ModelProfileDigest: p.ModelProfileDigest, RuntimeIdentityDigest: p.RuntimeIdentityDigest, ToolPolicyDigest: p.ToolPolicyDigest, EffectPolicyDigest: p.EffectPolicyDigest, BaseURL: settings.BaseURL, Model: settings.Model, ModelInstructions: settings.ModelInstructions, ResponseMode: settings.ResponseMode, MaxOutputTokens: settings.MaxOutputTokens, MaxTurns: settings.MaxTurns, Reasoning: settings.Reasoning, AllowedReadTools: reads}}
		encoded, _ := json.Marshal(brief)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		session, err := nativeagent.PrepareReadOnly(ctx, brief, digestBytes(encoded), cfg)
		if err != nil {
			cancel()
			t.Fatal(err)
		}
		// Seed one durable tool receipt through the real runner. No model/effect
		// executes in this explicitly marked fixture phase. The real prepared
		// session resumes at ToolDone and must project before its first HTTP call.
		seed := session.Runner
		seed.Model = receiptQualificationSeed{}
		seed.Tools = receiptQualificationFixture{fixture}
		seed.MaxTurns = 1
		if _, err := seed.Run(ctx, session.InvocationID, session.RequestDigest, session.Prompt); !errors.Is(err, agentruntime.ErrTurnLimit) {
			cancel()
			t.Fatalf("seed: %v", err)
		}
		before := slices.Clone(journal.entries[len(journal.entries)-1].Payload)
		t.Logf("qualifying %s invocation=%s", p.RoleFQRN, id)
		result, err := session.Run(ctx)
		cancel()
		if err != nil {
			t.Fatalf("%s: %v", p.RoleFQRN, err)
		}
		if !slices.Equal(before, journal.entries[2].Payload) {
			t.Fatal("durable fixture changed")
		}
		observed := map[string]bool{}
		readsPerformed := 0
		for _, e := range journal.entries {
			if e.Kind == agentruntime.ModelRejected || e.Kind == agentruntime.ModelPartial {
				t.Fatalf("%s needed a correction/continuation", p.RoleFQRN)
			}
			if e.Kind != agentruntime.ToolDone || e.Sequence == 3 {
				continue
			}
			var done agentruntime.ToolResult
			readsPerformed++
			if json.Unmarshal(e.Payload, &done) != nil || done.Error != "" || done.Name != "read_recorded_result" {
				t.Fatalf("unexpected qualification tool: %s", e.Payload)
			}
			var page struct {
				Content  string
				Complete bool `json:"page_complete"`
			}
			if json.Unmarshal(done.Output, &page) != nil || !page.Complete {
				t.Fatal("lookup did not return complete exact record")
			}
			var records []agenttools.GoTestCase
			if json.Unmarshal([]byte(page.Content), &records) != nil || len(records) != 1 {
				t.Fatal("missing exact named record")
			}
			observed[records[0].Name] = true
		}
		for _, name := range []string{"TestQualifiedPassing", "TestQualifiedSkipped", "TestQualifiedExcluded"} {
			if !observed[name] {
				t.Fatalf("model guessed without reading %s", name)
			}
		}
		if readsPerformed != 3 {
			t.Fatalf("unnecessary lookup repetitions: %d", readsPerformed)
		}
		var final struct{ Passing, Skipped, Excluded string }
		if json.Unmarshal([]byte(strings.TrimPrefix(result, application.OrganizationalResultMarker+"\n")), &final) != nil || final.Passing != "PASS" || final.Skipped != "SKIP" || final.Excluded != "NOT_RUN" {
			t.Fatalf("statuses wrong: %s", result)
		}
		write(string(p.RoleFQRN)+"-measurement.json", map[string]any{"label": "OBSERVED", "scope": "synthetic receipt-delivery delta only; not feature/work-kind qualification", "invocation_id": id, "profile": p, "fixture_bytes": len(fixture), "fixture_sha256": digestBytes(fixture), "result": result, "exact_records_read": observed, "journal": journal.entries})
		checked = append(checked, string(p.RoleFQRN))
	}
	if len(checked) == 0 {
		t.Fatal("no qualified roles checked")
	}
	write("qualification.json", map[string]any{"label": "OBSERVED", "status": "PASS", "roles": checked, "scope": "bounded presentation and exact recorded-result access only", "production_config_modified": false, "historical_runs_modified": false, "operator_unqualified": "NOT_RUN"})
}

type receiptQualificationSeed struct{}

func (receiptQualificationSeed) Complete(context.Context, []agentruntime.Message) (agentruntime.Completion, error) {
	return agentruntime.Completion{Text: "Explicit synthetic fixture receipt, not executed validation.", ToolCalls: []agentruntime.ToolCall{{ID: "fixture-result", Name: "run_go_tests", Arguments: json.RawMessage(`{"package":"./..."}`)}}}, nil
}

type receiptQualificationFixture struct{ raw json.RawMessage }

func (fixture receiptQualificationFixture) ExecuteReadOnly(context.Context, agentruntime.ToolCall) (json.RawMessage, error) {
	return fixture.raw, nil
}
