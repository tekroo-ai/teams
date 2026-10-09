package nativeagent

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"

	"github.com/tekroo-ai/teams/agentruntime"
	"github.com/tekroo-ai/teams/agenttools"
	"github.com/tekroo-ai/teams/application"
	"github.com/tekroo-ai/teams/kernel"
)

// A model verdict is not a test receipt. Require a completed, durable test
// effect on the bound candidate before accepting PASS for a go-test validator.
// This is a minimum execution-evidence check, not proof of every prose claim or
// of acceptance-criterion coverage. Review and product acceptance are separate.
func validationCompletionCheck(brief application.ExecutionBrief, digest kernel.Digest, gateway agenttools.Gateway, journal agentruntime.Journal, ledger agenttools.EffectLedger, previous func(context.Context, []byte) error) func(context.Context, []byte) error {
	return func(ctx context.Context, output []byte) error {
		if previous != nil {
			if err := previous(ctx, output); err != nil {
				return err
			}
		}
		raw := strings.TrimSpace(strings.TrimPrefix(string(output), application.OrganizationalResultMarker))
		var envelope struct {
			WorkProduct json.RawMessage `json:"work_product"`
		}
		if strings.HasPrefix(raw, application.ValidationResultMarker) {
			raw = strings.TrimSpace(strings.TrimPrefix(raw, application.ValidationResultMarker))
		} else {
			if json.Unmarshal([]byte(raw), &envelope) != nil {
				return ErrInvalidBinding
			}
			raw = string(envelope.WorkProduct)
		}
		var verdict struct {
			Outcome string `json:"outcome"`
		}
		if json.Unmarshal([]byte(raw), &verdict) != nil || verdict.Outcome == "" {
			return ErrInvalidBinding
		}
		if verdict.Outcome != "PASS" {
			return nil // Failures, blockers and inconclusive results remain reportable.
		}
		if journal == nil || ledger == nil {
			return fmt.Errorf("validation PASS requires a durable run_go_tests receipt for this invocation; no test ledger is available")
		}
		entries, err := journal.Load(ctx, string(brief.InvocationID))
		if err != nil {
			return err
		}
		calls := make(map[string]agentruntime.ToolCall)
		latest := make(map[string]agenttools.Result)
		tests := agenttools.TestGateway{Bindings: gateway.Bindings, Host: gateway.Host, Ledger: ledger, Policy: brief.EffectPolicyDigest}
		for _, entry := range entries {
			if entry.InvocationID != string(brief.InvocationID) {
				return ErrInvalidBinding
			}
			switch entry.Kind {
			case agentruntime.ModelTurn:
				var turn agentruntime.Completion
				if json.Unmarshal(entry.Payload, &turn) != nil {
					return ErrInvalidBinding
				}
				for _, call := range turn.ToolCalls {
					if call.Name == "run_go_tests" {
						calls[call.ID] = call
					}
				}
			case agentruntime.ToolDone:
				var done agentruntime.ToolResult
				if json.Unmarshal(entry.Payload, &done) != nil {
					return ErrInvalidBinding
				}
				call, found := calls[done.CallID]
				if !found {
					if done.Name == "run_go_tests" {
						return ErrInvalidBinding
					}
					continue
				}
				if done.Name != call.Name {
					return ErrInvalidBinding
				}
				if done.Error != "" {
					// A rejected/malformed request is not an executed suite. It
					// supplies no proof, but a later valid request can recover.
					delete(calls, call.ID)
					continue
				}
				// Lookup only: finalization and restart must never execute tests again.
				receipt, found, err := tests.Reconcile(ctx, agenttools.Request{InvocationID: brief.InvocationID, RequestDigest: digest, ToolCallID: call.ID, Call: agenttools.Call{Name: call.Name, Arguments: call.Arguments}})
				if err != nil {
					return fmt.Errorf("validation test receipt %s unavailable: %w", call.ID, err)
				}
				var recorded agenttools.Result
				if !found || json.Unmarshal(done.Output, &recorded) != nil || !reflect.DeepEqual(recorded, receipt.Result) {
					return fmt.Errorf("validation test receipt %s does not match the durable effect", call.ID)
				}
				var args struct {
					Package string `json:"package"`
				}
				if json.Unmarshal(call.Arguments, &args) != nil || args.Package == "" {
					return ErrInvalidBinding
				}
				latest[args.Package] = recorded
				delete(calls, call.ID)
			}
		}
		if len(calls) != 0 || len(latest) == 0 {
			return fmt.Errorf("validation PASS requires a completed run_go_tests receipt for this invocation; run the scoped tests or report INCONCLUSIVE/BLOCKED")
		}
		status, err := gateway.ExecuteReadOnly(ctx, agenttools.Request{InvocationID: brief.InvocationID, RequestDigest: digest, ToolCallID: "validation-completion-status", Call: agenttools.Call{Name: "git_status", Arguments: json.RawMessage(`{}`)}})
		if err != nil {
			return err
		}
		if status.Result.Clean == nil || !*status.Result.Clean || status.Result.HEAD != brief.Scope.BaselineSHA {
			return fmt.Errorf("validation candidate changed: expected clean HEAD %s, observed %s", brief.Scope.BaselineSHA, status.Result.HEAD)
		}
		// The latest result for every tested package must pass. A failed suite
		// cannot be hidden by subsequently testing an unrelated package.
		packages := make([]string, 0, len(latest))
		for pkg := range latest {
			packages = append(packages, pkg)
		}
		slices.Sort(packages)
		for _, pkg := range packages {
			result := latest[pkg]
			if result.CommitSHA != brief.Scope.BaselineSHA || result.ExitCode != 0 || result.ExecutionError != "" || result.PermissionDenial || !strings.HasPrefix(result.Output, "ok") && !strings.Contains(result.Output, "\nok") {
				return fmt.Errorf("validation PASS lacks a passing test receipt on candidate %s for %s (call result commit %s, exit %d, error %q)", brief.Scope.BaselineSHA, pkg, result.CommitSHA, result.ExitCode, result.ExecutionError)
			}
		}
		return nil
	}
}
