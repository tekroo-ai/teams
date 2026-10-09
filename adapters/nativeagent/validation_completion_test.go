package nativeagent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tekroo-ai/teams/agentruntime"
	"github.com/tekroo-ai/teams/agenttools"
	"github.com/tekroo-ai/teams/application"
	"github.com/tekroo-ai/teams/kernel"
)

func TestValidationCompletionRejectsR55UnsupportedPass(t *testing.T) {
	stored, err := os.ReadFile("testdata/r55-tester1-unsupported-pass-excerpt.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Verdict json.RawMessage `json:"verdict"`
	}
	if err := json.Unmarshal(stored, &fixture); err != nil {
		t.Fatal(err)
	}
	brief, digest, _ := testBriefAndProfile()
	journal := &testJournal{}
	ledger := &completionLedger{records: map[string]agenttools.EffectRecord{}}
	check := validationCompletionCheck(brief, digest, agenttools.Gateway{}, journal, ledger, nil)
	output := append([]byte(application.ValidationResultMarker+"\n"), fixture.Verdict...)
	if err := check(t.Context(), output); err == nil || !strings.Contains(err.Error(), "completed run_go_tests receipt") {
		t.Fatalf("unsupported PASS accepted: %v", err)
	}
	for _, verdict := range []string{"FAIL", "BLOCKED", "INCONCLUSIVE"} {
		output := []byte(application.OrganizationalResultMarker + `{"work_product":{"outcome":"` + verdict + `"}}`)
		if err := check(t.Context(), output); err != nil {
			t.Fatalf("%s cannot be reported: %v", verdict, err)
		}
	}
	sentinel := errors.New("existing workflow check")
	check = validationCompletionCheck(brief, digest, agenttools.Gateway{}, journal, ledger, func(context.Context, []byte) error { return sentinel })
	if err := check(t.Context(), output); !errors.Is(err, sentinel) {
		t.Fatalf("existing check lost: %v", err)
	}
}

func TestValidationCompletionRequiresDurableBoundCandidateTests(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	root := t.TempDir()
	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	git := func(args ...string) string {
		t.Helper()
		command := exec.CommandContext(ctx, "git", args...)
		command.Dir = root
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v %s", args, err, output)
		}
		return strings.TrimSpace(string(output))
	}
	write("go.mod", "module example.test/validation\n\ngo 1.24\n")
	write("value_test.go", "package validation\nimport \"testing\"\nfunc TestValue(t *testing.T){}\n")
	git("init", "-q")
	git("add", ".")
	git("-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-qm", "candidate")
	brief, digest, _ := testBriefAndProfile()
	brief.Purpose = kernel.PurposeValidation
	brief.Scope.BaselineSHA = git("rev-parse", "HEAD")
	binding := nativeTestBinding{root: root, policy: brief.EffectPolicyDigest}
	ledger := &completionLedger{records: map[string]agenttools.EffectRecord{}}
	journal := &testJournal{}
	goBinary, err := exec.LookPath("go")
	if err != nil {
		t.Fatal(err)
	}
	gateway := agenttools.Gateway{Bindings: binding, Host: agenttools.Host{GoBinary: goBinary, Timeout: time.Minute}}
	check := validationCompletionCheck(brief, digest, gateway, journal, ledger, nil)
	pass := []byte(application.OrganizationalResultMarker + `{"work_product":{"outcome":"PASS","reasons":["verified"]}}`)
	call := agentruntime.ToolCall{ID: "actual-test", Name: "run_go_tests", Arguments: json.RawMessage(`{"package":"./..."}`)}
	turn, _ := json.Marshal(agentruntime.Completion{ToolCalls: []agentruntime.ToolCall{call}})
	journal.Append(ctx, string(brief.InvocationID), 0, agentruntime.ModelTurn, turn)
	if err := check(ctx, pass); err == nil {
		t.Fatal("unexecuted request accepted")
	}
	receipt, err := (agenttools.TestGateway{Bindings: binding, Host: gateway.Host, Ledger: ledger, Policy: brief.EffectPolicyDigest}).Execute(ctx, agenttools.Request{InvocationID: brief.InvocationID, RequestDigest: digest, ToolCallID: call.ID, Call: agenttools.Call{Name: call.Name, Arguments: call.Arguments}})
	if err != nil || receipt.Result.ExitCode != 0 || receipt.Result.ExecutionError != "" {
		t.Fatalf("test execution failed: %v %+v", err, receipt.Result)
	}
	result, _ := json.Marshal(receipt.Result)
	done, _ := json.Marshal(agentruntime.ToolResult{CallID: call.ID, Name: call.Name, Output: result})
	journal.Append(ctx, string(brief.InvocationID), 1, agentruntime.ToolDone, done)
	for range 2 {
		if err := check(ctx, pass); err != nil {
			t.Fatalf("genuine durable test rejected: %v", err)
		}
	}
	if ledger.reserves != 1 {
		t.Fatalf("finalization reran tests: reserves=%d", ledger.reserves)
	}
	// A known rejected request supplies no evidence, but does not poison the
	// independently recorded successful execution that follows it.
	badCall := agentruntime.ToolCall{ID: "invalid-test-request", Name: "run_go_tests", Arguments: json.RawMessage(`{"package":"../outside"}`)}
	badTurn, _ := json.Marshal(agentruntime.Completion{ToolCalls: []agentruntime.ToolCall{badCall}})
	badDone, _ := json.Marshal(agentruntime.ToolResult{CallID: badCall.ID, Name: badCall.Name, Output: json.RawMessage("null"), Error: "invalid call"})
	originalEntries := journal.entries
	journal.entries = append([]agentruntime.Entry{{InvocationID: string(brief.InvocationID), Kind: agentruntime.ModelTurn, Payload: badTurn}, {InvocationID: string(brief.InvocationID), Kind: agentruntime.ToolDone, Payload: badDone}}, originalEntries...)
	if err := check(ctx, pass); err != nil {
		t.Fatalf("known tool error poisoned a later valid receipt: %v", err)
	}
	journal.entries = originalEntries
	for _, tc := range []struct {
		name   string
		mutate func(*agenttools.Result)
	}{
		{"failed suite", func(r *agenttools.Result) { r.ExitCode = 1 }},
		{"execution timeout", func(r *agenttools.Result) { r.ExitCode = -1; r.ExecutionError = "context deadline exceeded" }},
		{"permission denial", func(r *agenttools.Result) { r.PermissionDenial = true }},
		{"different commit", func(r *agenttools.Result) { r.CommitSHA = strings.Repeat("b", 40) }},
		{"no tests", func(r *agenttools.Result) { r.Output = "? example.test/validation [no test files]\n" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			changed := receipt.Result
			tc.mutate(&changed)
			encoded, _ := json.Marshal(changed)
			original := ledger.records[call.ID]
			altered := original
			altered.Result = encoded
			ledger.records[call.ID] = altered
			journal.entries[1].Payload, _ = json.Marshal(agentruntime.ToolResult{CallID: call.ID, Name: call.Name, Output: encoded})
			if err := check(ctx, pass); err == nil {
				t.Fatal("invalid test evidence accepted")
			}
			ledger.records[call.ID] = original
			journal.entries[1].Payload = done
		})
	}
	// Only a later execution of the same package can supersede a failed suite.
	// These counterfactual receipts test ordering without rerunning candidate code.
	for _, pkg := range []string{"./", "./..."} {
		t.Run("later package "+pkg, func(t *testing.T) {
			original := ledger.records[call.ID]
			failed := receipt.Result
			failed.ExitCode = 1
			failedRaw, _ := json.Marshal(failed)
			failedRecord := original
			failedRecord.Result = failedRaw
			ledger.records[call.ID] = failedRecord
			journal.entries[1].Payload, _ = json.Marshal(agentruntime.ToolResult{CallID: call.ID, Name: call.Name, Output: failedRaw})
			args, _ := json.Marshal(map[string]string{"package": pkg})
			laterCall := agentruntime.ToolCall{ID: "later-test", Name: "run_go_tests", Arguments: args}
			argsSum := sha256.Sum256(args)
			laterRecord := original
			laterRecord.ToolCallID, laterRecord.ArgumentsSHA256 = laterCall.ID, hex.EncodeToString(argsSum[:])
			ledger.records[laterCall.ID] = laterRecord
			laterTurn, _ := json.Marshal(agentruntime.Completion{ToolCalls: []agentruntime.ToolCall{laterCall}})
			laterDone, _ := json.Marshal(agentruntime.ToolResult{CallID: laterCall.ID, Name: laterCall.Name, Output: result})
			journal.Append(ctx, string(brief.InvocationID), 2, agentruntime.ModelTurn, laterTurn)
			journal.Append(ctx, string(brief.InvocationID), 3, agentruntime.ToolDone, laterDone)
			err := check(ctx, pass)
			if (err == nil) != (pkg == "./...") {
				t.Fatalf("failed-suite supersession for %s: %v", pkg, err)
			}
			ledger.records[call.ID] = original
			delete(ledger.records, laterCall.ID)
			journal.entries = journal.entries[:2]
			journal.entries[1].Payload = done
		})
	}
	// A forged journal observation cannot substitute for the durable ledger.
	forged := receipt.Result
	forged.Output = "ok forged"
	encoded, _ := json.Marshal(forged)
	journal.entries[1].Payload, _ = json.Marshal(agentruntime.ToolResult{CallID: call.ID, Name: call.Name, Output: encoded})
	if err := check(ctx, pass); err == nil {
		t.Fatal("forged journal result accepted")
	}
	journal.entries[1].Payload = done
	original := ledger.records[call.ID]
	delete(ledger.records, call.ID)
	if err := check(ctx, pass); err == nil {
		t.Fatal("journal-only result accepted")
	}
	original.Result = nil
	ledger.records[call.ID] = original
	if err := check(ctx, pass); !errors.Is(err, agenttools.ErrEffectUncertain) {
		t.Fatalf("uncertain effect accepted: %v", err)
	}
	original.Result = result
	ledger.records[call.ID] = original
	write("uncommitted.go", "package validation\n")
	if err := check(ctx, pass); err == nil {
		t.Fatal("changed candidate accepted")
	}
}

func TestValidationGoTestSessionHasEvidenceGuardAndRequiredTool(t *testing.T) {
	brief, _, profile := testBriefAndProfile()
	brief.Purpose = kernel.PurposeValidation
	brief.WorkProfile.RequiredDeterministicGateIDs = []string{"go-test"}
	brief.RoleGrounding.Permissions = []string{"repository.read", "test.execute"}
	profile.BaseURL = "http://127.0.0.1:1/v1"
	profile.AllowedEffectTools = []string{"run_go_tests"}
	config := Config{Journal: &testJournal{}, Effects: &nativeEffectLedger{}, HTTP: http.DefaultClient, Profile: profile}
	binding := nativeTestBinding{root: t.TempDir(), policy: brief.EffectPolicyDigest}
	config.Bindings = binding
	config.Gateway = agenttools.Gateway{Bindings: binding, Host: agenttools.Host{Timeout: time.Second}}
	encoded, _ := json.Marshal(brief)
	sum := sha256.Sum256(encoded)
	digest := kernel.Digest(hex.EncodeToString(sum[:]))
	session, err := PrepareWithEffects(t.Context(), brief, digest, config)
	if err != nil || session.Runner.ValidateFinalResult == nil {
		t.Fatalf("guard not wired: %v", err)
	}
	if err := session.Runner.ValidateFinalResult(t.Context(), []byte(application.ValidationResultMarker+`{"outcome":"PASS"}`)); err == nil {
		t.Fatal("session accepted no-test PASS")
	}
	config.Profile.AllowedEffectTools = []string{"run_go_tests_worktree"}
	if _, err := PrepareWithEffects(t.Context(), brief, digest, config); !errors.Is(err, ErrRequiredTestToolUnavailable) {
		t.Fatalf("missing test tool accepted: %v", err)
	}
}

func TestValidationNoTestPassGetsBoundedSameInvocationCorrection(t *testing.T) {
	brief, _, profile := testBriefAndProfile()
	brief.Purpose = kernel.PurposeValidation
	brief.WorkProfile.RequiredDeterministicGateIDs = []string{"go-test"}
	brief.RoleGrounding.Permissions = []string{"repository.read", "test.execute"}
	brief.ResultProtocol = &application.ExecutionResultProtocol{SchemaVersion: "1.0.0", Marker: application.ValidationResultMarker, Outcomes: []string{"PASS", "FAIL", "BLOCKED", "INCONCLUSIVE"}}
	encoded, _ := json.Marshal(brief)
	sum := sha256.Sum256(encoded)
	digest := kernel.Digest(hex.EncodeToString(sum[:]))
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		var request struct {
			Messages []agentruntime.Message `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		verdict, reason := "PASS", "go test passed"
		if requests > 1 {
			feedback := false
			for _, m := range request.Messages {
				// Structured turns serialize tool observations as user messages.
				if m.Role == "user" && strings.Contains(m.Content, "completed run_go_tests receipt") {
					feedback = true
				}
			}
			if !feedback {
				t.Error("test-receipt correction was not delivered to the model")
			}
			verdict, reason = "INCONCLUSIVE", "No tests executed; cannot claim a pass."
		}
		arguments, _ := json.Marshal(map[string]any{"schema_version": "1.0.0", "outcome": verdict, "reasons": []string{reason}})
		content, _ := json.Marshal(map[string]json.RawMessage{"submit_result": arguments})
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "fixture-" + verdict, "choices": []any{map[string]any{"finish_reason": "stop", "message": map[string]any{"content": string(content)}}}})
	}))
	defer server.Close()
	profile.BaseURL = server.URL + "/v1"
	profile.ResponseMode = ResponseModeJSONSchemaActions
	profile.AllowedEffectTools = []string{"run_go_tests"}
	profile.MaxTurns = 3
	binding := nativeTestBinding{root: t.TempDir(), policy: brief.EffectPolicyDigest}
	journal := &testJournal{}
	ledger := &completionLedger{records: map[string]agenttools.EffectRecord{}}
	config := Config{Bindings: binding, Gateway: agenttools.Gateway{Bindings: binding, Host: agenttools.Host{Timeout: time.Second}}, Journal: journal, Effects: ledger, HTTP: server.Client(), Profile: profile}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	session, err := PrepareWithEffects(ctx, brief, digest, config)
	if err != nil {
		t.Fatal(err)
	}
	output, err := session.Run(ctx)
	if err != nil || !strings.Contains(output, `"outcome":"INCONCLUSIVE"`) {
		t.Fatalf("unsupported pass advanced: %s %v", output, err)
	}
	if requests != 2 || ledger.reserves != 0 {
		t.Fatalf("unexpected retries or automatic tests: requests=%d reserves=%d", requests, ledger.reserves)
	}
	corrections := 0
	for _, entry := range journal.entries {
		if entry.Kind != agentruntime.ToolDone {
			continue
		}
		var done agentruntime.ToolResult
		if json.Unmarshal(entry.Payload, &done) != nil {
			t.Fatal("invalid observation")
		}
		if done.Name == "submit_result" && done.Error != "" {
			corrections++
		}
	}
	if corrections != 1 {
		t.Fatalf("correction not durably recorded: %d", corrections)
	}
}
