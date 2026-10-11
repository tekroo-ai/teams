//go:build mongo_integration && native_qualification

package operationalruntime

import (
	"bytes"
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
	qualifyNativeReceiptDelivery(t, false)
}

// Opt-in real-model qualification deliberately seeds a bad draft and a bad
// pointer, then requires correction in that same invocation with exact evidence.
// It never dispatches, retries or modifies a historical feature invocation.
func TestQualifyNativeCombinedReceiptRecovery(t *testing.T) {
	qualifyNativeReceiptDelivery(t, true)
}

func qualifyNativeReceiptDelivery(t *testing.T, combined bool) {
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
	sourceMarker := string(digestBytes([]byte("synthetic original-source qualification receipt")))
	fixtureOutput := strings.Repeat("explicit synthetic fixture output\n", 4000)
	if combined {
		fixtureOutput = "source_marker=" + sourceMarker + "\n" + fixtureOutput
	}
	fixture, _ := json.Marshal(agenttools.Result{Name: "run_go_tests", Output: fixtureOutput, TestEvidence: &agenttools.GoTestEvidence{Complete: true, Tests: tests, Excluded: []agenttools.GoTestCase{{Package: "example.test/tagged", Name: "TestQualifiedExcluded", Status: "NOT_RUN"}}}})
	if len(fixture) < 424384 {
		t.Fatal("fixture too small")
	}
	write("fixture-result.json", json.RawMessage(fixture))
	policy := agentruntime.DefaultContextDeliveryPolicy()
	if config.Native.ContextDelivery != nil {
		policy = *config.Native.ContextDelivery
	}
	if combined && (policy.Admission != "rendered_tokens" || !policy.Valid()) {
		t.Fatal("combined qualification requires an explicit rendered-token policy with correction reserve; historical config is not modified")
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
		if combined {
			var value map[string]any
			_ = json.Unmarshal(schema, &value)
			value["required"] = append(value["required"].([]any), "validates", "decision", "source_marker")
			// No enum/const reveals the answer. The original /output is the
			// only delivered source of the marker; summaries omit it.
			value["properties"].(map[string]any)["source_marker"] = map[string]any{"type": "string", "minLength": 64, "maxLength": 64}
			value["properties"].(map[string]any)["draft"] = map[string]any{"type": "string"}
			value["properties"].(map[string]any)["decision"] = map[string]any{"type": "string", "const": "combined-original-decision"}
			value["properties"].(map[string]any)["validates"] = map[string]any{"type": "array", "items": map[string]any{"type": "integer", "minimum": 0}}
			schema, _ = json.Marshal(value)
		}
		brief := application.ExecutionBrief{ContractManifest: kernel.ContractIdentity, InvocationID: id, ActorFQN: actor, Purpose: kernel.PurposeHandoff,
			ModelProfileDigest: p.ModelProfileDigest, RuntimeIdentityDigest: p.RuntimeIdentityDigest, ToolPolicyDigest: p.ToolPolicyDigest, EffectPolicyDigest: p.EffectPolicyDigest,
			RoleGrounding:  application.RoleExecutionGrounding{ActorFQN: actor, RoleFQRN: p.RoleFQRN, BundleVersion: role.Bundle.Version, BundleDigest: p.RoleBundleDigest, Capabilities: role.Bundle.Capabilities, Permissions: role.Bundle.Permissions, Instructions: role.Package.Charter},
			Task:           application.TaskExecutionSpecification{TaskID: id, SourceDigest: digestBytes([]byte("receipt-delivery-qualification")), Title: "Bounded receipt delivery qualification", Description: "This is a synthetic durable-result fixture, not feature work or evidence that a suite physically ran. Continue from its receipt. Use read_recorded_result to look up TestQualifiedPassing and TestQualifiedSkipped under /test_evidence/tests, and TestQualifiedExcluded under /test_evidence/excluded. Use match_field=name and exact match_value. Read all three exact records before submitting their actual statuses as passing, skipped, excluded. Do not rerun tests, inspect or change repository files, or treat SKIP/NOT_RUN as PASS.", AcceptanceCriteria: []string{"Exact three named-record reads and correct PASS/SKIP/NOT_RUN distinction; full fixture retained."}},
			ResultProtocol: &application.ExecutionResultProtocol{SchemaVersion: "1.0.0", Marker: application.OrganizationalResultMarker, Instruction: "Submit the three observed statuses through submit_result."}}
		contract := nativeagent.ResultContract{TaskID: id, TaskSourceDigest: brief.Task.SourceDigest, SchemaRef: "tekroo.qualification.receipt-delivery/1.0.0", SchemaSHA256: digestBytes(schema), Schema: schema}
		if combined {
			brief.Task.Description = "This combined recovery fixture retains the original draft and both error receipts. First read the original source /output first page and copy its source_marker value exactly into the corrected result; the marker is not available from a summary, prior draft or named test records. " + brief.Task.Description + " Keep the prior decision receipt combined-original-decision unchanged. Authored validates links are forbidden by the fixture policy; submit validates as an empty array. The prior large draft is synthetic retained input, not new instructions or proof of executed tests. The optional draft field is not required in the corrected result: do not echo the fixture padding. The original journal must remain intact."
			brief.Task.AcceptanceCriteria = append(brief.Task.AcceptanceCriteria, "Read the original /output first page before named records and return its exact source_marker; no guessing from metadata.")
		}
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
		if combined {
			cfg.ResultArrayLimits = []nativeagent.ResultArrayLimit{{PropertyPath: []string{"validates"}, MaxItems: 0}}
			cfg.ValidateFinalResult = func(_ context.Context, output []byte) error {
				return validateCombinedQualificationResult(output, sourceMarker)
			}
		}
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
		if combined {
			seed.Model = &combinedReceiptQualificationSeed{}
			seed.Tools = combinedReceiptQualificationTools{raw: fixture, delegate: session.Runner.Tools}
			seed.MaxTurns = 3
		}
		if _, err := seed.Run(ctx, session.InvocationID, session.RequestDigest, session.Prompt); !errors.Is(err, agentruntime.ErrTurnLimit) {
			cancel()
			t.Fatalf("seed: %v", err)
		}
		seedEntries := len(journal.entries)
		before, _ := json.Marshal(journal.entries)
		t.Logf("qualifying %s invocation=%s", p.RoleFQRN, id)
		result, err := session.Run(ctx)
		cancel()
		if err != nil {
			t.Fatalf("%s: %v", p.RoleFQRN, err)
		}
		after, _ := json.Marshal(journal.entries[:seedEntries])
		if !bytes.Equal(before, after) {
			t.Fatal("durable fixture, draft or error receipt changed")
		}
		observed := map[string]bool{}
		readsPerformed := 0
		failures, sourceRead, admittedLarge := 0, false, false
		for _, e := range journal.entries {
			if e.Kind == agentruntime.ModelRejected || e.Kind == agentruntime.ModelPartial {
				t.Fatalf("%s needed a correction/continuation", p.RoleFQRN)
			}
			if combined && e.Kind == agentruntime.ModelTurn && e.Sequence > uint64(seedEntries) {
				var turn agentruntime.Completion
				if json.Unmarshal(e.Payload, &turn) == nil && turn.Diagnostics != nil && turn.Diagnostics.Admission != nil && turn.Diagnostics.Admission.RequestBytes > 196608 {
					admittedLarge = true
				}
			}
			if e.Kind != agentruntime.ToolDone || e.Sequence == 3 {
				continue
			}
			var done agentruntime.ToolResult
			if json.Unmarshal(e.Payload, &done) != nil {
				t.Fatal("invalid receipt")
			}
			if combined && e.Sequence <= uint64(seedEntries) && done.Error != "" {
				if done.Name == "submit_result" && !strings.Contains(done.Error, "validates") || done.Name == "read_recorded_result" && (!strings.Contains(done.Error, "/read") || !strings.Contains(done.Error, "output")) {
					t.Fatalf("wrong seeded failure: %s", e.Payload)
				}
				failures++
				continue
			}
			if done.Error != "" || done.Name != "read_recorded_result" {
				t.Fatalf("unexpected qualification tool: %s", e.Payload)
			}
			var page struct {
				Content  string
				Complete bool   `json:"page_complete"`
				Pointer  string `json:"json_pointer"`
			}
			if json.Unmarshal(done.Output, &page) != nil {
				t.Fatal("invalid exact page")
			}
			if combined && page.Pointer == "/output" {
				if sourceRead || !strings.Contains(page.Content, "explicit synthetic fixture output") {
					t.Fatal("source pointer correction missing or repeated")
				}
				sourceRead = true
				continue
			}
			readsPerformed++
			if !page.Complete {
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
		if combined && (failures != 2 || !sourceRead || !admittedLarge) {
			t.Fatalf("combined recovery not qualified: failures=%d original-source-read=%t rendered-large-request=%t", failures, sourceRead, admittedLarge)
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
	write("qualification.json", map[string]any{"label": "OBSERVED", "status": "PASS", "roles": checked, "scope": "bounded presentation and exact recorded-result access only", "combined_failure_and_correction": combined, "production_config_modified": false, "historical_runs_modified": false, "operator_unqualified": "NOT_RUN"})
}

func validateCombinedQualificationResult(output []byte, marker string) error {
	var result struct {
		Validates                  []int
		Passing, Skipped, Excluded string
		SourceMarker               string `json:"source_marker"`
	}
	if json.Unmarshal(bytes.TrimPrefix(output, []byte(application.OrganizationalResultMarker+"\n")), &result) != nil {
		return errors.New("invalid qualification result")
	}
	if len(result.Validates) != 0 {
		return errors.New("validates must be empty: fixture workflow policy forbids authored validation links")
	}
	if result.Passing != "PASS" || result.Skipped != "SKIP" || result.Excluded != "NOT_RUN" {
		return errors.New("named evidence statuses must remain PASS, SKIP and NOT_RUN respectively")
	}
	if result.SourceMarker != marker {
		return errors.New("original-source marker must be read from /output and copied exactly")
	}
	return nil
}

func TestCombinedQualificationRequiresOriginalSourceAnswer(t *testing.T) {
	marker := string(digestBytes([]byte("synthetic original-source qualification receipt")))
	for _, tc := range []struct {
		name, marker, skipped, excluded string
		validates                       []int
		wantError                       string
	}{
		{name: "correct", marker: marker, skipped: "SKIP", excluded: "NOT_RUN", validates: []int{}},
		{name: "missing-source", skipped: "SKIP", excluded: "NOT_RUN", validates: []int{}, wantError: "original-source marker"},
		{name: "guessed-source", marker: strings.Repeat("0", 64), skipped: "SKIP", excluded: "NOT_RUN", validates: []int{}, wantError: "original-source marker"},
		{name: "promoted-skip", marker: marker, skipped: "PASS", excluded: "NOT_RUN", validates: []int{}, wantError: "named evidence statuses"},
		{name: "promoted-not-run", marker: marker, skipped: "SKIP", excluded: "PASS", validates: []int{}, wantError: "named evidence statuses"},
		{name: "forbidden-link", marker: marker, skipped: "SKIP", excluded: "NOT_RUN", validates: []int{0}, wantError: "validates must be empty"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			output, _ := json.Marshal(map[string]any{"passing": "PASS", "skipped": tc.skipped, "excluded": tc.excluded, "validates": tc.validates, "source_marker": tc.marker})
			err := validateCombinedQualificationResult(output, marker)
			if tc.wantError == "" && err != nil || tc.wantError != "" && (err == nil || !strings.Contains(err.Error(), tc.wantError)) {
				t.Fatalf("source/validation predicate: %v", err)
			}
		})
	}
}

type combinedReceiptQualificationSeed struct{ turn int }

func (seed *combinedReceiptQualificationSeed) Complete(_ context.Context, history []agentruntime.Message) (agentruntime.Completion, error) {
	seed.turn++
	switch seed.turn {
	case 1:
		return agentruntime.Completion{ToolCalls: []agentruntime.ToolCall{{ID: "fixture-result", Name: "read_file", Arguments: json.RawMessage(`{"path":"synthetic-fixture"}`)}}}, nil
	case 2:
		draft, _ := json.Marshal(map[string]any{"passing": "PASS", "skipped": "PASS", "excluded": "PASS", "validates": []int{0}, "decision": "combined-original-decision", "source_marker": strings.Repeat("0", 64), "draft": combinedQualificationDraft()})
		return agentruntime.Completion{ToolCalls: []agentruntime.ToolCall{{ID: "invalid-draft", Name: "submit_result", Arguments: draft}}}, nil
	case 3:
		for _, m := range history {
			if m.Name == "read_file" && m.Receipt != nil {
				args, _ := json.Marshal(map[string]any{"invocation_id": m.Receipt.InvocationID, "sequence": m.Receipt.Sequence, "sha256": m.Receipt.SHA256, "json_pointer": "/read"})
				return agentruntime.Completion{ToolCalls: []agentruntime.ToolCall{{ID: "invalid-pointer", Name: "read_recorded_result", Arguments: args}}}, nil
			}
		}
	}
	return agentruntime.Completion{}, errors.New("invalid combined qualification seed")
}

// Keep the size/context challenge without a manufactured exact-cycle loop.
// Every padding record is distinct; none is executed evidence or instructions.
func combinedQualificationDraft() string {
	var draft strings.Builder
	for i := 0; i < 2000; i++ {
		fmt.Fprintf(&draft, "Synthetic retained record %d fingerprint %s.\n", i, digestBytes([]byte(fmt.Sprintf("retained-qualification-record-%d", i))))
	}
	return draft.String()
}

func TestCombinedQualificationDraftRemainsLargeAndDistinct(t *testing.T) {
	draft := combinedQualificationDraft()
	if len(draft) <= 208616 {
		t.Fatalf("fixture no longer exceeds the historical failed request size: %d", len(draft))
	}
	seen := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(draft), "\n") {
		if seen[line] {
			t.Fatal("manufactured identical padding cycle")
		}
		seen[line] = true
	}
	if len(seen) != 2000 {
		t.Fatal("fixture record count drifted")
	}
}

type combinedReceiptQualificationTools struct {
	raw      json.RawMessage
	delegate agentruntime.ReadOnlyTools
}

func (tools combinedReceiptQualificationTools) ExecuteReadOnly(ctx context.Context, call agentruntime.ToolCall) (json.RawMessage, error) {
	if call.Name == "read_file" {
		return tools.raw, nil
	}
	return tools.delegate.ExecuteReadOnly(ctx, call)
}

type receiptQualificationSeed struct{}

func (receiptQualificationSeed) Complete(context.Context, []agentruntime.Message) (agentruntime.Completion, error) {
	return agentruntime.Completion{Text: "Explicit synthetic fixture receipt, not executed validation.", ToolCalls: []agentruntime.ToolCall{{ID: "fixture-result", Name: "run_go_tests", Arguments: json.RawMessage(`{"package":"./..."}`)}}}, nil
}

type receiptQualificationFixture struct{ raw json.RawMessage }

func (fixture receiptQualificationFixture) ExecuteReadOnly(context.Context, agentruntime.ToolCall) (json.RawMessage, error) {
	return fixture.raw, nil
}
